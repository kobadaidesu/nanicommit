"""テスト共通の準備。

DB を使うテストは、TEST_DATABASE_URL があればその Postgres を、無ければ
dev 依存の pgserver で一時的な Postgres を起動して使う。
テスト用データベース nanicommit_test を毎回作り直し、Supabase の auth.users と
anon / authenticated ロールの代わりを用意してからマイグレーションを適用する。
"""

import os
import tempfile
from collections.abc import Iterator
from pathlib import Path
from uuid import UUID, uuid4

import psycopg
import pytest
from fastapi.testclient import TestClient

MIGRATIONS = Path(__file__).resolve().parents[2] / "supabase" / "migrations"
TEST_DB = "nanicommit_test"

SUPABASE_STUB = """
create schema if not exists auth;
create table if not exists auth.users (id uuid primary key);
do $$ begin
    if not exists (select from pg_roles where rolname = 'anon') then create role anon; end if;
    if not exists (select from pg_roles where rolname = 'authenticated') then create role authenticated; end if;
end $$;
"""


def _with_dbname(uri: str, dbname: str) -> str:
    return psycopg.conninfo.make_conninfo(uri, dbname=dbname)


@pytest.fixture(scope="session")
def database_url() -> Iterator[str]:
    server = None
    admin_uri = os.environ.get("TEST_DATABASE_URL")
    if admin_uri is None:
        pgserver = pytest.importorskip("pgserver")
        server = pgserver.get_server(tempfile.mkdtemp(prefix="nanicommit-pg-"), cleanup_mode="delete")
        admin_uri = server.get_uri()

    with psycopg.connect(admin_uri, autocommit=True) as conn:
        conn.execute(f"drop database if exists {TEST_DB} with (force)")
        conn.execute(f"create database {TEST_DB}")

    url = _with_dbname(admin_uri, TEST_DB)
    with psycopg.connect(url, autocommit=True) as conn:
        conn.execute(SUPABASE_STUB)
        for migration in sorted(MIGRATIONS.glob("*.sql")):
            conn.execute(migration.read_text())
    yield url

    if server is not None:
        server.cleanup()


@pytest.fixture
def db(database_url: str) -> Iterator[psycopg.Connection]:
    with psycopg.connect(database_url, autocommit=True) as conn:
        conn.execute("truncate public.repositories, public.commits, public.questions, public.answers, auth.users cascade")
        yield conn


@pytest.fixture
def app_env(database_url: str, monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    from app.config import get_settings

    monkeypatch.setenv("DATABASE_URL", database_url)
    monkeypatch.setenv("WEB_BASE_URL", "https://app.example.com")
    monkeypatch.setenv("QUIZ_GENERATOR", "fake")
    monkeypatch.setenv("SUPABASE_URL", "http://supabase.invalid")
    get_settings.cache_clear()
    yield
    get_settings.cache_clear()


@pytest.fixture
def client(app_env: None, db: psycopg.Connection) -> Iterator[TestClient]:
    from app.main import create_app

    app = create_app()
    with TestClient(app) as c:
        yield c


def make_user(db: psycopg.Connection) -> UUID:
    user_id = uuid4()
    db.execute("insert into auth.users (id) values (%s)", (user_id,))
    return user_id


@pytest.fixture
def user(db: psycopg.Connection) -> UUID:
    return make_user(db)


def cli_headers(user_id: UUID) -> dict[str, str]:
    return {"X-User-Id": str(user_id)}


def register_repo(client: TestClient, user_id: UUID, **body) -> str:
    r = client.post(
        "/api/v1/repositories",
        json={"name": "demo-repo", "learning_base_sha": None, **body},
        headers=cli_headers(user_id),
    )
    assert r.status_code == 201, r.text
    return r.json()["repository_id"]


SHA1 = "0226edda3ed64eb7821ee827df5a63c8f9d21012"
SHA2 = "2222222222222222222222222222222222222222"


def commit_body(repository_id: str, sha: str = SHA1, **overrides) -> dict:
    return {
        "repository_id": repository_id,
        "commit_sha": sha,
        "branch": "feature/cache",
        "message": "キャッシュ削除処理を追加\n",
        "files": ["src/user_service.py"],
        "diff": "diff --git a/src/user_service.py b/src/user_service.py\n+x\n",
        **overrides,
    }
