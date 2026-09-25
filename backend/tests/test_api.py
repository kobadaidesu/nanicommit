"""API を DB 付きで通して確かめる（design.md 8.6 の確認項目）。"""

import asyncio
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient

from app.learning.errors import InsufficientContext
from app.learning.generator import fake_generate_quiz, get_quiz_generator
from tests.conftest import SHA1, SHA2, cli_headers, commit_body, make_user, register_repo


def test_health(client):
    assert client.get("/health").json() == {"status": "ok"}


# ---- X-User-Id -----------------------------------------------------------------


@pytest.mark.parametrize("headers", [{}, {"X-User-Id": "not-a-uuid"}, {"X-User-Id": str(uuid4())}])
def test_cli_api_requires_known_user(client, headers):
    r = client.post("/api/v1/repositories", json={"name": "r", "learning_base_sha": None}, headers=headers)
    assert r.status_code == 401
    assert r.json() == {"detail": "Authentication required"}


# ---- R0 -----------------------------------------------------------------------


def test_register_and_recover_repository(client, db, user):
    repo_id = register_repo(client, user, learning_base_sha="1" * 40)

    r = client.post(
        "/api/v1/repositories",
        json={"name": "renamed", "learning_base_sha": "2" * 40, "repository_id": repo_id},
        headers=cli_headers(user),
    )
    assert r.status_code == 200
    assert r.json() == {"repository_id": repo_id}
    # 復旧で開始点を上書きしない。
    row = db.execute("select name, learning_base_sha from public.repositories where id = %s", (repo_id,)).fetchone()
    assert row == ("demo-repo", "1" * 40)


def test_recover_other_users_repository_is_404(client, db, user):
    repo_id = register_repo(client, user)
    other = make_user(db)
    r = client.post(
        "/api/v1/repositories",
        json={"name": "x", "learning_base_sha": None, "repository_id": repo_id},
        headers=cli_headers(other),
    )
    assert r.status_code == 404


# ---- D1 -----------------------------------------------------------------------


def test_commit_creates_quiz_and_resend_returns_same(client, db, user):
    repo_id = register_repo(client, user)

    r = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    assert r.status_code == 201, r.text
    body = r.json()
    assert body["status"] == "ready"
    assert body["question_count"] == 3
    assert body["quiz_url"] == f"https://app.example.com/quizzes/{body['quiz_id']}"
    count = db.execute("select count(*) from public.questions where commit_id = %s", (body["quiz_id"],)).fetchone()
    assert count == (3,)

    # 再送は再生成しない。branch の違いは無視する。
    r2 = client.post("/api/v1/commits", json=commit_body(repo_id, branch=None), headers=cli_headers(user))
    assert r2.status_code == 200
    assert r2.json() == body


def test_resend_after_pass_reports_passed(client, db, user):
    repo_id = register_repo(client, user)
    quiz_id = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user)).json()["quiz_id"]
    db.execute("update public.commits set status = 'passed', passed_at = now() where id = %s", (quiz_id,))

    r = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    assert r.status_code == 200
    assert r.json()["status"] == "passed"


@pytest.mark.parametrize("field,value", [("message", "changed\n"), ("files", ["other.py"]), ("diff", "changed")])
def test_resend_with_different_content_is_409(client, user, field, value):
    repo_id = register_repo(client, user)
    client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    r = client.post("/api/v1/commits", json=commit_body(repo_id, **{field: value}), headers=cli_headers(user))
    assert r.status_code == 409


def test_commit_to_other_users_repository_is_404(client, db, user):
    repo_id = register_repo(client, user)
    other = make_user(db)
    r = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(other))
    assert r.status_code == 404


def test_commit_size_limits_are_413(client, user):
    repo_id = register_repo(client, user)
    too_many = commit_body(repo_id, files=[f"f{i}" for i in range(101)])
    assert client.post("/api/v1/commits", json=too_many, headers=cli_headers(user)).status_code == 413
    too_big = commit_body(repo_id, diff="x" * ((512 << 10) + 1))
    assert client.post("/api/v1/commits", json=too_big, headers=cli_headers(user)).status_code == 413


def test_invalid_commit_is_422(client, user):
    repo_id = register_repo(client, user)
    r = client.post("/api/v1/commits", json=commit_body(repo_id, commit_sha="abc"), headers=cli_headers(user))
    assert r.status_code == 422


def test_generation_failure_saves_nothing(client, db, user):
    async def failing(**_):
        raise InsufficientContext("empty commit")

    client.app.dependency_overrides[get_quiz_generator] = lambda: failing
    repo_id = register_repo(client, user)
    r = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    assert r.status_code == 422
    assert r.json() == {"detail": "Not enough context to generate a quiz"}
    assert db.execute("select count(*) from public.commits").fetchone() == (0,)


def test_invalid_generated_quiz_is_502(client, db, user):
    async def broken(**_):
        return {"questions": []}

    client.app.dependency_overrides[get_quiz_generator] = lambda: broken
    repo_id = register_repo(client, user)
    r = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    assert r.status_code == 502
    assert db.execute("select count(*) from public.commits").fetchone() == (0,)


def test_concurrent_sends_store_one_quiz(app_env, db, user):
    from app.main import create_app, lifespan

    started = asyncio.Event()
    release = asyncio.Event()
    calls = 0

    async def slow(**kwargs):
        nonlocal calls
        calls += 1
        if calls == 2:
            started.set()
        await release.wait()
        return await fake_generate_quiz(**kwargs)

    async def scenario():
        app = create_app()
        app.dependency_overrides[get_quiz_generator] = lambda: slow
        async with lifespan(app):
            transport = httpx.ASGITransport(app=app)
            async with httpx.AsyncClient(transport=transport, base_url="http://test") as ac:
                r = await ac.post(
                    "/api/v1/repositories", json={"name": "r", "learning_base_sha": None}, headers=cli_headers(user)
                )
                repo_id = r.json()["repository_id"]
                send = lambda: ac.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
                tasks = [asyncio.create_task(send()), asyncio.create_task(send())]
                await asyncio.wait_for(started.wait(), 5)  # 両方が生成中になるまで待つ
                release.set()
                return await asyncio.gather(*tasks)

    r1, r2 = asyncio.run(scenario())
    assert sorted([r1.status_code, r2.status_code]) == [200, 201]
    assert r1.json()["quiz_id"] == r2.json()["quiz_id"]
    assert db.execute("select count(*) from public.commits").fetchone() == (1,)
    assert db.execute("select count(*) from public.questions").fetchone() == (3,)


# ---- D5 -----------------------------------------------------------------------


def test_push_check(client, db, user):
    repo_id = register_repo(client, user)
    quiz_id = client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user)).json()["quiz_id"]

    def check(shas):
        r = client.post(
            "/api/v1/push/check", json={"repository_id": repo_id, "commit_shas": shas}, headers=cli_headers(user)
        )
        assert r.status_code == 200, r.text
        return r.json()

    assert check([SHA1, SHA2, SHA2]) == {
        "allowed": False,
        "pending_commits": [
            {"commit_sha": SHA1, "reason": "not_passed", "quiz_url": f"https://app.example.com/quizzes/{quiz_id}"},
            {"commit_sha": SHA2, "reason": "missing", "quiz_url": None},
        ],
    }

    db.execute("update public.commits set status = 'passed', passed_at = now() where id = %s", (quiz_id,))
    assert check([SHA1]) == {"allowed": True, "pending_commits": []}
    # 1つでも未登録があれば止める。
    assert check([SHA1, SHA2])["allowed"] is False


def test_push_check_does_not_see_other_users_commits(client, db, user):
    repo_id = register_repo(client, user)
    client.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
    other = make_user(db)
    r = client.post(
        "/api/v1/push/check", json={"repository_id": repo_id, "commit_shas": [SHA1]}, headers=cli_headers(other)
    )
    assert r.status_code == 404


def test_push_check_rejects_empty_list(client, user):
    repo_id = register_repo(client, user)
    r = client.post("/api/v1/push/check", json={"repository_id": repo_id, "commit_shas": []}, headers=cli_headers(user))
    assert r.status_code == 422


# ---- Web 認証（Bさんの quizzes.py で使う依存関数） -----------------------------


def test_web_user_requires_valid_bearer(client, user):
    from fastapi import APIRouter

    from app.auth import WebUserId

    class FakeAuth:
        async def get_user(self, token):
            from supabase_auth.errors import AuthApiError

            if token != "good":
                raise AuthApiError("invalid JWT", 401, "bad_jwt")
            return type("R", (), {"user": type("U", (), {"id": str(user)})()})()

        async def close(self):
            pass

    router = APIRouter()

    @router.get("/whoami")
    async def whoami(user_id: WebUserId):
        return {"user_id": str(user_id)}

    client.app.include_router(router)
    client.app.state.auth_client = FakeAuth()

    assert client.get("/whoami").status_code == 401
    assert client.get("/whoami", headers=cli_headers(user)).status_code == 401  # X-User-Id だけでは通らない
    assert client.get("/whoami", headers={"Authorization": "Bearer bad"}).status_code == 401
    r = client.get("/whoami", headers={"Authorization": "Bearer good"})
    assert r.json() == {"user_id": str(user)}
