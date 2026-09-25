"""D1: commit 受信と問題生成（CLI の post-commit / send から呼ばれる）。

流れ（design.md 4.2）:
1. repository が本人のものか確認する。
2. 同じ user・repository・SHA が登録済みなら、再生成せず既存の quiz を返す（200）。
   message・files・diff が保存済みと違えば 409（branch は比較しない）。
3. 新規なら問題を生成する。生成は数十秒かかり得るので、DB トランザクションの外で行う。
4. commit と3問を1トランザクションで保存する（201）。同時送信で先に保存されていたら、
   一意制約で挿入をやめ、2 と同じ判定をする。保存されるのは常に1セットだけ。
"""

import logging
from typing import Annotated, Any

from fastapi import APIRouter, Depends, HTTPException, Response, status
from psycopg import AsyncConnection
from psycopg.types.json import Jsonb

from app.auth import CliUserId
from app.config import Settings, get_settings
from app.db import Conn
from app.learning.errors import GenerationError
from app.learning.runner import QuizGenerator, get_quiz_generator, run_generation
from app.routers.common import ensure_repository_owner, quiz_url
from app.schemas import MAX_DIFF_BYTES, MAX_FILES, QUESTION_COUNT, CommitIn, CommitOut

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/v1", tags=["commits"])


def check_size_limits(body: CommitIn) -> None:
    if len(body.files) > MAX_FILES or len(body.diff.encode()) > MAX_DIFF_BYTES:
        raise HTTPException(status.HTTP_413_CONTENT_TOO_LARGE, "Commit exceeds size limits")


async def _find_commit(conn: AsyncConnection, user_id: Any, body: CommitIn) -> dict | None:
    cur = await conn.execute(
        "select id, status, message, files, diff from public.commits"
        " where user_id = %s and repository_id = %s and commit_sha = %s",
        (user_id, body.repository_id, body.commit_sha),
    )
    return await cur.fetchone()


def _existing_response(row: dict, body: CommitIn) -> CommitOut:
    if (row["message"], row["files"], row["diff"]) != (body.message, body.files, body.diff):
        raise HTTPException(status.HTTP_409_CONFLICT, "Commit already registered with different content")
    return CommitOut(
        quiz_id=row["id"], quiz_url=quiz_url(row["id"]), status=row["status"], question_count=QUESTION_COUNT
    )


@router.post("/commits", status_code=status.HTTP_201_CREATED, response_model=CommitOut)
async def receive_commit(
    body: CommitIn,
    user_id: CliUserId,
    conn: Conn,
    response: Response,
    generator: Annotated[QuizGenerator, Depends(get_quiz_generator)],
    settings: Annotated[Settings, Depends(get_settings)],
) -> CommitOut:
    check_size_limits(body)
    await ensure_repository_owner(conn, body.repository_id, user_id)

    existing = await _find_commit(conn, user_id, body)
    if existing is not None:
        response.status_code = status.HTTP_200_OK
        return _existing_response(existing, body)
    # 生成を待つ間、読み取りのトランザクションを開いたままにしない。
    await conn.commit()

    try:
        quiz = await run_generation(
            generator,
            message=body.message,
            files=body.files,
            diff=body.diff,
            timeout=settings.generation_timeout_seconds,
        )
    except GenerationError as e:
        logger.warning("quiz generation failed (%s): %s", type(e).__name__, e)
        raise HTTPException(e.status_code, e.public_detail) from e

    async with conn.transaction():
        cur = await conn.execute(
            "insert into public.commits"
            " (user_id, repository_id, commit_sha, branch, message, files, diff)"
            " values (%s, %s, %s, %s, %s, %s, %s)"
            " on conflict (user_id, repository_id, commit_sha) do nothing"
            " returning id",
            (user_id, body.repository_id, body.commit_sha, body.branch, body.message, Jsonb(body.files), body.diff),
        )
        inserted = await cur.fetchone()
        if inserted is not None:
            async with conn.cursor() as qcur:
                await qcur.executemany(
                    "insert into public.questions"
                    " (commit_id, position, question, choices, correct_index, hint, explanation)"
                    " values (%s, %s, %s, %s, %s, %s, %s)",
                    [
                        (
                            inserted["id"],
                            position,
                            q.question,
                            Jsonb(q.choices),
                            q.correct_index,
                            q.hint,
                            q.explanation,
                        )
                        for position, q in enumerate(quiz.questions, start=1)
                    ],
                )

    if inserted is None:
        # 生成中に同じ commit が別リクエストで保存された。
        existing = await _find_commit(conn, user_id, body)
        assert existing is not None
        response.status_code = status.HTTP_200_OK
        return _existing_response(existing, body)

    return CommitOut(
        quiz_id=inserted["id"], quiz_url=quiz_url(inserted["id"]), status="ready", question_count=QUESTION_COUNT
    )
