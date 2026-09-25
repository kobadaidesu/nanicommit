"""PostgreSQL 接続プール。

Bさんへ：ルーターでは `conn: Conn` を引数に書くだけで接続を受け取れます。
リクエストの終わりに接続はプールへ返され、例外がなければ commit、
例外が出れば rollback されます（psycopg の `pool.connection()` の動作）。

    from app.db import Conn

    @router.get("/api/v1/quizzes/{quiz_id}")
    async def get_quiz(quiz_id: UUID, conn: Conn, user_id: WebUserId):
        async with conn.transaction():          # 明示的なトランザクション
            cur = await conn.execute(
                "select id, status from public.commits where id = %s and user_id = %s for update",
                (quiz_id, user_id),
            )
            row = await cur.fetchone()          # 行は dict で返る（row["status"]）

- 行は dict（`dict_row`）で返ります。
- 同じ行を並行して更新する処理（回答保存など）は `select ... for update` で
  commit 行をロックして直列化してください（design.md D4）。
"""

from collections.abc import AsyncIterator
from typing import Annotated

from fastapi import Depends, Request
from psycopg import AsyncConnection
from psycopg.rows import DictRow, dict_row
from psycopg_pool import AsyncConnectionPool

from app.config import Settings


def create_pool(settings: Settings) -> AsyncConnectionPool:
    return AsyncConnectionPool(
        settings.database_url,
        min_size=settings.db_pool_min_size,
        max_size=settings.db_pool_max_size,
        # Supabase の transaction pooler (6543) でも動くよう prepared statement を使わない。
        kwargs={"row_factory": dict_row, "prepare_threshold": None},
        open=False,
        # DB に繋がらないとき、この秒数で諦めて 503 を返す。
        timeout=5,
    )


async def get_conn(request: Request) -> AsyncIterator[AsyncConnection[DictRow]]:
    """リクエスト1回分の DB 接続を貸し出す FastAPI 依存関数。"""
    pool: AsyncConnectionPool = request.app.state.pool
    async with pool.connection() as conn:
        yield conn


Conn = Annotated[AsyncConnection[DictRow], Depends(get_conn)]
