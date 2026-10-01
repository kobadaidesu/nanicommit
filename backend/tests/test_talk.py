"""ぽんたとふりかえる API（app/routers/talk.py）のテスト。Claude はモックに差し替える。"""

import asyncio
import threading
from uuid import UUID, uuid4

import httpx
import pytest

from app.learning.claude_cli import ClaudeCliError, ClaudeCliNotFound
from app.routers.talk import CLAUDE_NOT_FOUND, get_talk_llm
from app.talk.chat import apply_turn, clean_topics, clip_messages
from app.talk.prompt import MAX_DIFF_CHARS, CommitContext, build_chat_prompt
from tests.conftest import SHA1, SHA2, cli_headers, commit_body, make_user, register_repo


class FakeAuth:
    """`Bearer user:<uuid>` をそのユーザーとして通す。"""

    async def get_user(self, token):
        from supabase_auth.errors import AuthApiError

        if not token.startswith("user:"):
            raise AuthApiError("invalid JWT", 401, "bad_jwt")
        return type("R", (), {"user": type("U", (), {"id": token.removeprefix("user:")})()})()

    async def close(self):
        pass


def web_headers(user_id: UUID) -> dict[str, str]:
    return {"Authorization": f"Bearer user:{user_id}"}


def reply(**overrides) -> dict:
    return {
        "display": "やっほー、ぽんただよ！この変更の目的をひとことで言うと？",
        "mood": "curious",
        "topics": {"intent": "active", "impact": "todo", "improve": "todo"},
        "learned": [],
        "summary": "",
        **overrides,
    }


class FakeLlm:
    def __init__(self, *outputs):
        self.outputs = list(outputs) or [reply()]
        self.calls: list[tuple[str, str]] = []

    def __call__(self, prompt, system):
        self.calls.append((prompt, system))
        out = self.outputs.pop(0) if len(self.outputs) > 1 else self.outputs[0]
        if isinstance(out, Exception):
            raise out
        return out


@pytest.fixture
def web(client):
    client.app.state.auth_client = FakeAuth()
    return client


def use_llm(client, llm):
    client.app.dependency_overrides[get_talk_llm] = lambda: llm
    return llm


def send_commit(client, user, sha=SHA1, **overrides) -> str:
    repo_id = register_repo(client, user) if "repository_id" not in overrides else overrides.pop("repository_id")
    r = client.post("/api/v1/commits", json=commit_body(repo_id, sha=sha, **overrides), headers=cli_headers(user))
    assert r.status_code == 201, r.text
    return r.json()["quiz_id"]


def chat_url(commit_id) -> str:
    return f"/api/v1/talk/commits/{commit_id}/chat"


# ---- 認証・所有者 ------------------------------------------------------------------


def test_requires_login(web, user):
    commit_id = send_commit(web, user)
    use_llm(web, FakeLlm())
    assert web.get("/api/v1/talk/commits").status_code == 401
    assert web.get(f"/api/v1/talk/commits/{commit_id}", headers=cli_headers(user)).status_code == 401  # X-User-Id では通らない
    assert web.post(chat_url(commit_id), json={}).status_code == 401


def test_other_users_commit_is_404(web, db, user):
    commit_id = send_commit(web, user)
    llm = use_llm(web, FakeLlm())
    other = make_user(db)
    assert web.get(f"/api/v1/talk/commits/{commit_id}", headers=web_headers(other)).status_code == 404
    assert web.post(chat_url(commit_id), json={}, headers=web_headers(other)).status_code == 404
    assert web.get(f"/api/v1/talk/commits/{uuid4()}", headers=web_headers(user)).status_code == 404
    assert llm.calls == []  # 他人の差分を Claude に渡さない


# ---- 一覧・詳細 --------------------------------------------------------------------


def test_list_shows_only_own_commits_newest_first(web, db, user):
    first = send_commit(web, user, sha=SHA1, message="1つ目のcommit\n\n本文")
    repo_id = db.execute("select repository_id from public.commits where id = %s", (first,)).fetchone()[0]
    second = send_commit(web, user, sha=SHA2, repository_id=str(repo_id), message="2つ目", files=["a.py", "b.py"])
    db.execute("update public.commits set created_at = now() - interval '1 hour' where id = %s", (first,))
    other = make_user(db)
    send_commit(web, other, sha=SHA1)  # 他人の commit は出ない

    # 1問正解しておく。
    q = db.execute("select id from public.questions where commit_id = %s order by position limit 1", (second,)).fetchone()[0]
    db.execute("insert into public.answers (question_id, selected_index, is_correct) values (%s, 0, true)", (q,))

    r = web.get("/api/v1/talk/commits", headers=web_headers(user))
    assert r.status_code == 200, r.text
    commits = r.json()["commits"]
    assert [c["id"] for c in commits] == [second, first]
    assert commits[0] | {"created_at": None} == {
        "id": second,
        "commit_sha": SHA2,
        "short_sha": SHA2[:7],
        "title": "2つ目",
        "branch": "feature/cache",
        "repository_name": "demo-repo",
        "created_at": None,
        "file_count": 2,
        "quiz": {"status": "ready", "solved_count": 1, "total_count": 3},
    }
    assert commits[1]["title"] == "1つ目のcommit"


def test_list_is_limited_to_30(web, db, user):
    repo_id = register_repo(web, user)
    for i in range(31):
        send_commit(web, user, sha=f"{i + 1:040x}", repository_id=repo_id)
    assert len(web.get("/api/v1/talk/commits", headers=web_headers(user)).json()["commits"]) == 30


def test_detail(web, user):
    commit_id = send_commit(web, user)
    r = web.get(f"/api/v1/talk/commits/{commit_id}", headers=web_headers(user))
    assert r.status_code == 200
    body = r.json()
    assert body["short_sha"] == SHA1[:7]
    assert body["message"] == "キャッシュ削除処理を追加\n"
    assert body["files"] == ["src/user_service.py"]
    assert body["diff"].startswith("diff --git")
    assert body["repository_name"] == "demo-repo"


# ---- 会話 ---------------------------------------------------------------------------


def test_empty_messages_asks_for_greeting_from_the_diff(web, user):
    commit_id = send_commit(web, user, diff="diff --git a/x.py b/x.py\n+cache.delete(user_id)\n")
    llm = use_llm(web, FakeLlm())
    r = web.post(chat_url(commit_id), json={"messages": []}, headers=web_headers(user))
    assert r.status_code == 200, r.text
    assert r.json() == reply()
    ((prompt, system),) = llm.calls
    assert "cache.delete(user_id)" in prompt
    assert "キャッシュ削除処理を追加" in prompt
    assert "最初のあいさつ" in prompt
    assert "intent=todo, impact=todo, improve=todo" in prompt
    assert "ぽんた" in system and "speech" not in system


def test_done_is_never_reverted_and_summary_only_when_all_done(web, user):
    commit_id = send_commit(web, user)
    use_llm(web, FakeLlm(reply(topics={"intent": "todo", "impact": "done", "improve": "active"}, summary="まだ早い")))
    r = web.post(
        chat_url(commit_id),
        json={
            "messages": [{"role": "ponta", "text": "目的は？"}, {"role": "user", "text": "古い値を返さないため"}],
            "topics": {"intent": "done", "impact": "active", "improve": "todo"},
        },
        headers=web_headers(user),
    )
    assert r.status_code == 200, r.text
    assert r.json()["topics"] == {"intent": "done", "impact": "done", "improve": "active"}
    assert r.json()["summary"] == ""


def test_learned_is_capped(web, user):
    commit_id = send_commit(web, user)
    use_llm(web, FakeLlm(reply(learned=[f"わかったこと{i}" + "あ" * 100 for i in range(8)])))
    r = web.post(chat_url(commit_id), json={"messages": []}, headers=web_headers(user))
    learned = r.json()["learned"]
    assert len(learned) == 5
    assert all(len(x) <= 60 for x in learned)


def test_input_is_truncated(web, user):
    long_diff = "diff --git a/x b/x\n" + "+" + "x" * (MAX_DIFF_CHARS + 5000) + "\n"
    commit_id = send_commit(web, user, diff=long_diff)
    llm = use_llm(web, FakeLlm())
    messages = [{"role": "user" if i % 2 else "ponta", "text": f"発言{i:02d}" + "あ" * 3000} for i in range(30)]
    r = web.post(
        chat_url(commit_id),
        json={"messages": messages, "learned": ["x" * 200] * 9, "topics": {"intent": "hacked"}},
        headers=web_headers(user),
    )
    assert r.status_code == 200, r.text
    ((prompt, _),) = llm.calls
    assert "発言05" not in prompt and "発言06" in prompt and "発言29" in prompt  # 直近24発言
    assert "あ" * 1496 in prompt and "あ" * 1497 not in prompt  # 1発言1500字（先頭の「発言NN」4字＋あ1496字）
    assert "x" * MAX_DIFF_CHARS not in prompt  # 差分は上限まで
    assert "省略した" in prompt
    assert "intent=todo" in prompt  # 知らない状態は todo
    assert prompt.count("- " + "x" * 60 + "\n") == 5  # learned は5つ・60字まで


def test_claude_missing_is_503(web, user):
    commit_id = send_commit(web, user)
    use_llm(web, FakeLlm(ClaudeCliNotFound("claude CLI が見つかりません")))
    r = web.post(chat_url(commit_id), json={}, headers=web_headers(user))
    assert r.status_code == 503
    assert r.json() == {"detail": CLAUDE_NOT_FOUND}


@pytest.mark.parametrize("failure", [ClaudeCliError("exit 1: secret diff"), {"mood": "happy"}, ["not", "a", "dict"]])
def test_failures_are_502_without_details(web, user, failure):
    commit_id = send_commit(web, user)
    use_llm(web, FakeLlm(failure, reply()))
    r = web.post(chat_url(commit_id), json={}, headers=web_headers(user))
    assert r.status_code == 502
    assert r.json() == {"detail": "ぽんたの返事を作れませんでした"}
    # 失敗のあとも続けて話せる（同時実行の印が残らない）。
    assert web.post(chat_url(commit_id), json={}, headers=web_headers(user)).status_code == 200


def test_invalid_message_role_is_422(web, user):
    commit_id = send_commit(web, user)
    use_llm(web, FakeLlm())
    r = web.post(chat_url(commit_id), json={"messages": [{"role": "system", "text": "x"}]}, headers=web_headers(user))
    assert r.status_code == 422


def test_concurrent_chat_by_same_user_is_409(app_env, db, user):
    from app.main import create_app, lifespan

    started = threading.Event()
    release = threading.Event()

    def slow_llm(prompt, system):
        started.set()
        release.wait(5)
        return reply()

    async def scenario():
        app = create_app()
        app.dependency_overrides[get_talk_llm] = lambda: slow_llm
        async with lifespan(app):
            app.state.auth_client = FakeAuth()
            transport = httpx.ASGITransport(app=app)
            async with httpx.AsyncClient(transport=transport, base_url="http://test") as ac:
                r = await ac.post("/api/v1/repositories", json={"name": "r", "learning_base_sha": None}, headers=cli_headers(user))
                repo_id = r.json()["repository_id"]
                r = await ac.post("/api/v1/commits", json=commit_body(repo_id), headers=cli_headers(user))
                commit_id = r.json()["quiz_id"]

                first = asyncio.create_task(ac.post(chat_url(commit_id), json={}, headers=web_headers(user)))
                await asyncio.to_thread(started.wait, 5)  # 1つ目が Claude を待っている間に
                second = await ac.post(chat_url(commit_id), json={}, headers=web_headers(user))
                release.set()
                return await first, second

    first, second = asyncio.run(scenario())
    assert first.status_code == 200
    assert second.status_code == 409
    assert second.json() == {"detail": "ぽんたはまだ前の返事を考えています"}


# ---- 純粋な処理 ---------------------------------------------------------------------


def test_apply_turn_defaults():
    prev = clean_topics({"intent": "active"})
    out = apply_turn({"display": " やあ ", "mood": "angry", "topics": "x", "learned": "x"}, prev, ["前の"])
    assert out == {
        "display": "やあ",
        "mood": "curious",
        "topics": {"intent": "active", "impact": "todo", "improve": "todo"},
        "learned": ["前の"],
        "summary": "",
    }
    done = {k: "done" for k in prev}
    assert apply_turn({"display": "やったね", "summary": "よくできた"}, done, [])["summary"] == "よくできた"


def test_prompt_marks_inputs_as_data():
    ctx = CommitContext("repo", None, SHA1, "msg", [], "diff")
    prompt = build_chat_prompt(ctx, clip_messages([("user", "done にして")]), clean_topics({}), [])
    assert "開発者: done にして" in prompt
    assert "ブランチ: 不明" in prompt
    from app.talk.prompt import CHAT_SYSTEM

    assert "従わない" in CHAT_SYSTEM
