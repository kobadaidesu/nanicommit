"""D5: push 時の合格確認（CLI の pre-push から呼ばれる）。"""

from fastapi import APIRouter

from app.auth import CliUserId
from app.db import Conn
from app.routers.common import ensure_repository_owner, quiz_url
from app.schemas import PendingCommit, PushCheckIn, PushCheckOut

router = APIRouter(prefix="/api/v1", tags=["push"])


@router.post("/push/check", response_model=PushCheckOut)
async def check_push(body: PushCheckIn, user_id: CliUserId, conn: Conn) -> PushCheckOut:
    await ensure_repository_owner(conn, body.repository_id, user_id)

    shas = list(dict.fromkeys(body.commit_shas))  # 順序を保って重複を除く
    cur = await conn.execute(
        "select commit_sha, id, status from public.commits"
        " where user_id = %s and repository_id = %s and commit_sha = any(%s)",
        (user_id, body.repository_id, shas),
    )
    found = {row["commit_sha"]: row for row in await cur.fetchall()}

    # 見つかった行だけでなく、要求された全SHAと照合する（無いものは missing）。
    pending: list[PendingCommit] = []
    for sha in shas:
        row = found.get(sha)
        if row is None:
            pending.append(PendingCommit(commit_sha=sha, reason="missing", quiz_url=None))
        elif row["status"] != "passed":
            pending.append(PendingCommit(commit_sha=sha, reason="not_passed", quiz_url=quiz_url(row["id"])))
    return PushCheckOut(allowed=not pending, pending_commits=pending)
