"""呼び出し元の識別（design.md 5.2）。

- CLI 向け API（リポジトリ登録・commit 送信・push 確認）: `CliUserId`
  ヘッダ `X-User-Id` の UUID が Supabase Auth（auth.users）に存在するかを確認する。
- Web 向け API（問題取得・回答）: `WebUserId`
  `Authorization: Bearer <Supabase access token>` を Auth サーバーで検証する。
  JWT を手元でデコードするだけの確認はしない。

Bさんへ：quizzes.py では Web 向けの `WebUserId` を使ってください。

    from app.auth import WebUserId

    @router.get("/api/v1/quizzes/{quiz_id}")
    async def get_quiz(quiz_id: UUID, user_id: WebUserId, conn: Conn): ...

`user_id` はログイン中ユーザーの UUID です。トークンが無い・無効なら、
ルーター本体が呼ばれる前に 401 が返ります。`X-User-Id` だけでは通りません。
取得したデータが `user_id` のものか（commits.user_id = user_id）の確認は
ルーター側で必ず行い、別ユーザーのものなら 404 を返してください。
"""

from typing import Annotated
from uuid import UUID

import httpx
from fastapi import Depends, Header, HTTPException, Request, status
from supabase_auth import AsyncGoTrueClient
from supabase_auth.errors import (
    AuthApiError,
    AuthError,
    AuthRetryableError,
    AuthUnknownError,
)

from app.config import Settings
from app.db import Conn

AUTH_REQUIRED = "Authentication required"


def _unauthorized() -> HTTPException:
    return HTTPException(
        status.HTTP_401_UNAUTHORIZED,
        AUTH_REQUIRED,
        headers={"WWW-Authenticate": "Bearer"},
    )


def create_auth_client(settings: Settings) -> AsyncGoTrueClient:
    return AsyncGoTrueClient(
        url=f"{settings.supabase_url.rstrip('/')}/auth/v1",
        headers={"apikey": settings.supabase_anon_key},
        auto_refresh_token=False,
        persist_session=False,
    )


def parse_user_id(value: str | None) -> UUID | None:
    """`X-User-Id` を UUID として読む。形式が違えば None。"""
    if not value:
        return None
    try:
        user_id = UUID(value)
    except ValueError:
        return None
    # 大文字・波括弧・ハイフン無し等の別表記は受け付けない。
    return user_id if str(user_id) == value else None


async def get_cli_user_id(
    conn: Conn,
    x_user_id: Annotated[str | None, Header()] = None,
) -> UUID:
    user_id = parse_user_id(x_user_id)
    if user_id is None:
        raise _unauthorized()
    cur = await conn.execute("select 1 from auth.users where id = %s", (user_id,))
    if await cur.fetchone() is None:
        raise _unauthorized()
    return user_id


def parse_bearer(value: str | None) -> str | None:
    if not value:
        return None
    scheme, _, token = value.partition(" ")
    token = token.strip()
    if scheme.lower() != "bearer" or not token:
        return None
    return token


async def get_web_user_id(
    request: Request,
    authorization: Annotated[str | None, Header()] = None,
) -> UUID:
    token = parse_bearer(authorization)
    if token is None:
        raise _unauthorized()
    client: AsyncGoTrueClient = request.app.state.auth_client
    try:
        response = await client.get_user(token)
    except (AuthRetryableError, AuthUnknownError, httpx.HTTPError) as e:
        raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, "Auth service unavailable") from e
    except AuthApiError as e:
        if e.status >= 500:
            raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, "Auth service unavailable") from e
        raise _unauthorized() from e
    except AuthError as e:
        raise _unauthorized() from e
    if response is None or response.user is None:
        raise _unauthorized()
    return UUID(response.user.id)


CliUserId = Annotated[UUID, Depends(get_cli_user_id)]
WebUserId = Annotated[UUID, Depends(get_web_user_id)]
