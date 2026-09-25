"""R0: リポジトリ登録（CLI の init から呼ばれる）。"""

from fastapi import APIRouter, Response, status

from app.auth import CliUserId
from app.db import Conn
from app.routers.common import ensure_repository_owner
from app.schemas import RepositoryIn, RepositoryOut

router = APIRouter(prefix="/api/v1", tags=["repositories"])


@router.post("/repositories", status_code=status.HTTP_201_CREATED, response_model=RepositoryOut)
async def register_repository(
    body: RepositoryIn, user_id: CliUserId, conn: Conn, response: Response
) -> RepositoryOut:
    if body.repository_id is not None:
        # 復旧：既存の登録をそのまま返す。name や学習開始点は上書きしない。
        await ensure_repository_owner(conn, body.repository_id, user_id)
        response.status_code = status.HTTP_200_OK
        return RepositoryOut(repository_id=body.repository_id)

    cur = await conn.execute(
        "insert into public.repositories (user_id, name, learning_base_sha)"
        " values (%s, %s, %s) returning id",
        (user_id, body.name, body.learning_base_sha),
    )
    row = await cur.fetchone()
    assert row is not None
    return RepositoryOut(repository_id=row["id"])
