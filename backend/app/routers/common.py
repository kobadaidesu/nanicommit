"""ルーター間で共有する小さな処理（Aさん担当）。

Bさんの quizzes.py でも not_found() と quiz_url() を使えます。
"""

from uuid import UUID

from fastapi import HTTPException, status
from psycopg import AsyncConnection

from app.config import get_settings


def not_found() -> HTTPException:
    return HTTPException(status.HTTP_404_NOT_FOUND, "Not found")


def quiz_url(quiz_id: UUID) -> str:
    """問題ページの URL（quiz_id は commits.id と同じ値）。"""
    return f"{get_settings().web_base_url.rstrip('/')}/quizzes/{quiz_id}"


async def ensure_repository_owner(conn: AsyncConnection, repository_id: UUID, user_id: UUID) -> None:
    """repository が user のものでなければ 404。別ユーザーのものか無いかは区別しない。"""
    cur = await conn.execute(
        "select 1 from public.repositories where id = %s and user_id = %s",
        (repository_id, user_id),
    )
    if await cur.fetchone() is None:
        raise not_found()
