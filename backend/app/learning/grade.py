# app/learning/grade.py
from dataclasses import dataclass
from typing import Sequence


@dataclass(frozen=True)
class QuestionResult:
    question_id: str
    user_choice: int
    correct_choice: int
    is_correct: bool
    explanation: str


@dataclass(frozen=True)
class GradeResult:
    score: int
    total: int
    is_passed: bool
    results: list[QuestionResult]


def grade_answers(
    *,
    user_answers: Sequence[dict],
    questions: Sequence[dict],
) -> GradeResult:
    """ユーザーの回答をDBに保存されている設問データと突き合わせて採点する。

    :param user_answers: ユーザーが送ってきた回答リスト
           例: [{"question_id": "...", "choice_index": 1}, ...]
    :param questions: DBに保存されている設問リスト（正解情報を含む）
           例: [{"id": "...", "correct_index": 1, "explanation": "..."}, ...]
    """
    if len(user_answers) != len(questions):
        raise ValueError(
            f"回答数 ({len(user_answers)}) と設問数 ({len(questions)}) が一致しません"
        )

    # 設問IDをキーにした辞書を作る（順不同で送られてきても正しく照合するため）
    question_map = {str(q["id"]): q for q in questions}

    results: list[QuestionResult] = []
    correct_count = 0

    for ans in user_answers:
        qid = str(ans["question_id"])
        if qid not in question_map:
            raise ValueError(f"存在しない設問IDが含まれています: {qid}")

        q_data = question_map[qid]
        user_choice = int(ans["choice_index"])
        correct_choice = int(q_data["correct_index"])

        is_correct = user_choice == correct_choice
        if is_correct:
            correct_count += 1

        results.append(
            QuestionResult(
                question_id=qid,
                user_choice=user_choice,
                correct_choice=correct_choice,
                is_correct=is_correct,
                explanation=q_data.get("explanation", ""),
            )
        )

    total = len(questions)
    # 3問中3問正解で合格
    is_passed = correct_count == total

    return GradeResult(
        score=correct_count,
        total=total,
        is_passed=is_passed,
        results=results,
    )