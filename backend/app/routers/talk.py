# app/routers/talk.py
"""ぽんたと commit をふりかえる画面（/talk、テスト環境用）の API。

- 既存の D1〜D5 とは独立。DB は読むだけ（スキーマ変更なし）、会話はサーバーに保存しない（ブラウザが持つ）。
- ぽんたの返事は、バックエンドを動かしている人の Claude Code（`claude -p`、ツール無効）で作る。
- 認証は Web 用の WebUserId。他人の commit は 404。
"""

import asyncio
import logging
from collections.abc import Callable
from datetime import datetime
from typing import Annotated, Any, Literal
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Request, status
from pydantic import BaseModel, Field

from app.auth import WebUserId
from app.db import Conn
from app.learning.claude_cli import ClaudeCliNotFound, run_claude_json
from app.routers.common import not_found
from app.talk.chat import apply_turn, clean_learned, clean_topics, clip_messages
from app.talk.prompt import CHAT_SYSTEM, CommitContext, build_chat_prompt

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/api/v1/talk", tags=["talk"])

COMMIT_LIST_LIMIT = 30
# 差分が大きいと返事に時間がかかるので、問題作成（80秒）より長めに待つ。
CLAUDE_TIMEOUT_SECONDS = 120.0

CLAUDE_NOT_FOUND = "Claude Code（claude コマンド）が見つかりません。インストールしてログインしてください"
CHAT_FAILED = "ぽんたの返事を作れませんでした"
CHAT_BUSY = "ぽんたはまだ前の返事を考えています"


# ---- 入出力 -----------------------------------------------------------------------


class TalkQuizStatus(BaseModel):
    status: Literal["ready", "passed"]
    solved_count: int
    total_count: int


class TalkCommitSummary(BaseModel):
    id: UUID  # = quiz の id（commits.id）
    commit_sha: str
    short_sha: str
    title: str  # メッセージの1行目
    branch: str | None
    repository_name: str
    created_at: datetime
    file_count: int
    quiz: TalkQuizStatus


class TalkCommitList(BaseModel):
    commits: list[TalkCommitSummary]


class TalkCommitDetail(BaseModel):
    id: UUID
    commit_sha: str
    short_sha: str
    message: str
    branch: str | None
    repository_name: str
    created_at: datetime
    files: list[str]
    diff: str


class ChatMessageIn(BaseModel):
    role: Literal["ponta", "user"]
    text: str


class ChatIn(BaseModel):
    messages: list[ChatMessageIn] = []
    topics: dict[str, Any] = {}
    learned: list[Any] = []


class ChatOut(BaseModel):
    display: str
    mood: Literal["happy", "curious", "thinking", "gentle"]
    topics: dict[str, Literal["todo", "active", "done"]]
    learned: Annotated[list[str], Field(max_length=5)]
    summary: str


# ---- 依存関数（テストで差し替える） --------------------------------------------------

TalkLlm = Callable[[str, str], dict]  # (prompt, system_prompt) -> JSON


def get_talk_llm() -> TalkLlm:
    return lambda prompt, system: run_claude_json(prompt, system_prompt=system, timeout=CLAUDE_TIMEOUT_SECONDS)


# ---- API ----------------------------------------------------------------------------


@router.get("/commits", response_model=TalkCommitList)
async def list_commits(user_id: WebUserId, conn: Conn):
    """ログイン中ユーザーの commit（新しい順に最大30件）。"""
    cur = await conn.execute(
        """
        select c.id, c.commit_sha, c.message, c.branch, c.created_at, c.status,
               r.name as repository_name,
               jsonb_array_length(c.files) as file_count,
               (select count(*) from public.questions q where q.commit_id = c.id) as total_count,
               (select count(*) from public.questions q
                  join public.answers a on a.question_id = q.id
                 where q.commit_id = c.id and a.is_correct) as solved_count
        from public.commits c
        join public.repositories r on r.id = c.repository_id
        where c.user_id = %s
        order by c.created_at desc, c.id desc
        limit %s
        """,
        (user_id, COMMIT_LIST_LIMIT),
    )
    rows = await cur.fetchall()
    return TalkCommitList(
        commits=[
            TalkCommitSummary(
                id=row["id"],
                commit_sha=row["commit_sha"],
                short_sha=row["commit_sha"][:7],
                title=(row["message"].strip().splitlines() or [""])[0],
                branch=row["branch"],
                repository_name=row["repository_name"],
                created_at=row["created_at"],
                file_count=row["file_count"],
                quiz=TalkQuizStatus(
                    status=row["status"], solved_count=row["solved_count"], total_count=row["total_count"]
                ),
            )
            for row in rows
        ]
    )


async def _fetch_commit(conn, commit_id: UUID, user_id: UUID) -> dict:
    cur = await conn.execute(
        """
        select c.id, c.commit_sha, c.message, c.branch, c.created_at, c.files, c.diff,
               r.name as repository_name
        from public.commits c
        join public.repositories r on r.id = c.repository_id
        where c.id = %s and c.user_id = %s
        """,
        (commit_id, user_id),
    )
    row = await cur.fetchone()
    if row is None:
        raise not_found()
    return row


@router.get("/commits/{commit_id}", response_model=TalkCommitDetail)
async def get_commit(commit_id: UUID, user_id: WebUserId, conn: Conn):
    row = await _fetch_commit(conn, commit_id, user_id)
    return TalkCommitDetail(
        id=row["id"],
        commit_sha=row["commit_sha"],
        short_sha=row["commit_sha"][:7],
        message=row["message"],
        branch=row["branch"],
        repository_name=row["repository_name"],
        created_at=row["created_at"],
        files=row["files"],
        diff=row["diff"],
    )


@router.post("/commits/{commit_id}/chat", response_model=ChatOut)
async def chat(
    commit_id: UUID,
    body: ChatIn,
    user_id: WebUserId,
    request: Request,
    llm: TalkLlm = Depends(get_talk_llm),
):
    """ぽんたの次の返事。messages が空なら、最初のあいさつと最初の問いかけ。"""
    # Claude の返事には数十秒かかるので、`Conn` 依存（関数の終わりまで接続を持つ）は使わず、
    # commit を読んだらすぐ接続をプールへ返す。
    async with request.app.state.pool.connection() as conn:
        row = await _fetch_commit(conn, commit_id, user_id)

    # 同じユーザーの同時実行は1つまで（各自の Claude Code の利用枠を守る）。
    # チェックと追加の間に await が無いので、イベントループ上で取り合いにならない。
    busy: set[UUID] | None = getattr(request.app.state, "talk_busy_users", None)
    if busy is None:
        busy = request.app.state.talk_busy_users = set()
    if user_id in busy:
        raise HTTPException(status.HTTP_409_CONFLICT, CHAT_BUSY)
    busy.add(user_id)
    try:
        topics = clean_topics(body.topics)
        learned = clean_learned(body.learned)
        prompt = build_chat_prompt(
            CommitContext(
                repository_name=row["repository_name"],
                branch=row["branch"],
                commit_sha=row["commit_sha"],
                message=row["message"],
                files=row["files"],
                diff=row["diff"],
            ),
            clip_messages([(m.role, m.text) for m in body.messages]),
            topics,
            learned,
        )
        try:
            out = await asyncio.to_thread(llm, prompt, CHAT_SYSTEM)
            return ChatOut(**apply_turn(out, topics, learned))
        except ClaudeCliNotFound as e:
            raise HTTPException(status.HTTP_503_SERVICE_UNAVAILABLE, CLAUDE_NOT_FOUND) from e
        except Exception as e:  # claude の失敗・タイムアウト・使えない出力（InvalidTurn）
            # 理由（差分の一部を含みうる）はログだけに出す。
            logger.warning("talk chat failed for commit %s: %r", commit_id, e)
            raise HTTPException(status.HTTP_502_BAD_GATEWAY, CHAT_FAILED) from e
    finally:
        busy.discard(user_id)

