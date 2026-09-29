# app/routers/quizzes.py
from uuid import UUID

from fastapi import APIRouter, HTTPException, status

from app.auth import WebUserId
from app.db import Conn
from app.routers.common import not_found
from app.schemas import (
    QUESTION_COUNT,
    AnswerIn,
    AnswerOut,
    QuizCommitInfo,
    QuizDetailOut,
    QuizProgress,
    QuizQuestionOut,
)

router = APIRouter(prefix="/api/v1", tags=["quizzes"])


@router.get("/quizzes/{quiz_id}", response_model=QuizDetailOut)
async def get_quiz(quiz_id: UUID, user_id: WebUserId, conn: Conn):
    """D3: 問題・差分・進捗を取得する（正解は伏せて返す）。"""
    # 1. commit 情報とリポジトリ名を取得
    cur = await conn.execute(
        """
        select c.*, r.name as repository_name
        from public.commits c
        join public.repositories r on r.id = c.repository_id
        where c.id = %s and c.user_id = %s
        """,
        (quiz_id, user_id),
    )
    commit = await cur.fetchone()
    if commit is None:
        raise not_found()

    # 2. 設問と回答状況を取得
    cur = await conn.execute(
        """
        select q.id, q.position, q.question, q.choices, q.hint,
               coalesce(a.is_correct, false) as solved
        from public.questions q
        left join public.answers a on a.question_id = q.id
        where q.commit_id = %s
        order by q.position asc
        """,
        (quiz_id,),
    )
    question_rows = await cur.fetchall()

    questions = [
        QuizQuestionOut(
            question_id=row["id"],
            position=row["position"],
            question=row["question"],
            choices=row["choices"],
            hint=row["hint"],
            solved=row["solved"],
        )
        for row in question_rows
    ]

    solved_count = sum(1 for q in questions if q.solved)

    return QuizDetailOut(
        quiz_id=commit["id"],
        status=commit["status"],
        commit=QuizCommitInfo(
            repository_id=commit["repository_id"],
            repository_name=commit["repository_name"],
            commit_sha=commit["commit_sha"],
            branch=commit["branch"],
            message=commit["message"],
            files=commit["files"],
            diff=commit["diff"],
        ),
        questions=questions,
        progress=QuizProgress(
            solved_count=solved_count,
            total_count=len(questions),
        ),
    )


@router.post("/quizzes/{quiz_id}/answers", response_model=AnswerOut)
async def submit_answer(
    quiz_id: UUID, answer_in: AnswerIn, user_id: WebUserId, conn: Conn
):
    """D4: 1問回答を受け付け、採点・保存して合否状態を更新する。"""
    async with conn.transaction():
        # 1. 対象 commit をロック（直列化）
        cur = await conn.execute(
            """
            select id, status
            from public.commits
            where id = %s and user_id = %s
            for update
            """,
            (quiz_id, user_id),
        )
        commit = await cur.fetchone()
        if commit is None:
            raise not_found()

        # 2. 回答対象の設問を取得
        cur = await conn.execute(
            """
            select id, correct_index, hint, explanation
            from public.questions
            where id = %s and commit_id = %s
            """,
            (answer_in.question_id, quiz_id),
        )
        question = await cur.fetchone()
        if question is None:
            raise not_found()

        # 3. 既存の回答を取得（正解済み回答の変更を防ぐ）
        cur = await conn.execute(
            "select selected_index, is_correct from public.answers where question_id = %s",
            (answer_in.question_id,),
        )
        existing_answer = await cur.fetchone()

        if existing_answer and existing_answer["is_correct"]:
            if existing_answer["selected_index"] != answer_in.selected_index:
                # すでに正解済みの問題を別の選択肢に変えようとした場合は 409
                raise HTTPException(
                    status_code=status.HTTP_409_CONFLICT,
                    detail="正解済みの問題を再回答することはできません",
                )
            is_correct = True
        else:
            is_correct = answer_in.selected_index == question["correct_index"]
            # 4. answers テーブルに最新の回答を保存（UPSERT）
            await conn.execute(
                """
                insert into public.answers (question_id, selected_index, is_correct, answered_at)
                values (%s, %s, %s, now())
                on conflict (question_id) do update set
                    selected_index = excluded.selected_index,
                    is_correct = excluded.is_correct,
                    answered_at = excluded.answered_at
                """,
                (answer_in.question_id, answer_in.selected_index, is_correct),
            )

        # 5. 現在の正解数を集計
        cur = await conn.execute(
            """
            select count(*) as count
            from public.answers a
            join public.questions q on q.id = a.question_id
            where q.commit_id = %s and a.is_correct = true
            """,
            (quiz_id,),
        )
        count_row = await cur.fetchone()
        solved_count = count_row["count"] if count_row else 0

        # 全問正解（3問）なら passed に更新（passed_at も同時に設定）
        is_passed = solved_count >= QUESTION_COUNT
        if is_passed and commit["status"] != "passed":
            await conn.execute(
                """
                update public.commits
                set status = 'passed', passed_at = now()
                where id = %s
                """,
                (quiz_id,),
            )

        feedback = question["explanation"] if is_correct else question["hint"]

        return AnswerOut(
            question_id=answer_in.question_id,
            correct=is_correct,
            feedback=feedback,
            progress=QuizProgress(
                solved_count=solved_count,
                total_count=QUESTION_COUNT,
            ),
            passed=is_passed,
        )