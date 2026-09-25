"""API の入出力モデル（design.md 6章の JSON 定義）。

CLI・Web・バックエンドの契約なので、フィールドを変えるときは3人で確認する。
D1 の 6 項目は docs/commit-payload.schema.json とも一致させる。
"""

from typing import Annotated, Literal
from uuid import UUID

from pydantic import AfterValidator, BaseModel, ConfigDict, Field, StringConstraints

# Gitの完全なオブジェクトID（SHA-1: 40桁 / SHA-256: 64桁、小文字）。
Sha = Annotated[str, StringConstraints(pattern=r"^([0-9a-f]{40}|[0-9a-f]{64})$")]
NonEmptyStr = Annotated[str, StringConstraints(min_length=1)]

# 送信上限。Go CLI の hooks/internal/event/limits.go（DefaultLimits）と一致させる。
# 超過は 422 ではなく 413 にするため、モデルではなくルーターで確認する。
MAX_FILES = 100
MAX_DIFF_BYTES = 512 << 10

# push確認で一度に受け付けるSHAの数。
MAX_PUSH_CHECK_SHAS = 1000

QUESTION_COUNT = 3
CHOICE_COUNT = 4

CommitStatus = Literal["ready", "passed"]


def _unique(items: list[str]) -> list[str]:
    if len(set(items)) != len(items):
        raise ValueError("items must be unique")
    return items


class _Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


# ---- R0: リポジトリ登録 ------------------------------------------------------


class RepositoryIn(_Strict):
    name: Annotated[str, StringConstraints(min_length=1, max_length=200)]
    learning_base_sha: Sha | None
    # 復旧用：以前登録した repository_id を指定すると、再登録せず同じIDを返す。
    repository_id: UUID | None = None


class RepositoryOut(BaseModel):
    repository_id: UUID


# ---- D1: commit送信 -----------------------------------------------------------


class CommitIn(_Strict):
    repository_id: UUID
    commit_sha: Sha
    branch: NonEmptyStr | None
    message: str
    files: Annotated[list[NonEmptyStr], AfterValidator(_unique)]
    diff: str


class CommitOut(BaseModel):
    quiz_id: UUID
    quiz_url: str
    status: CommitStatus
    question_count: int


# ---- D2: 問題生成の結果（Bさんの生成処理 → commits.py） ---------------------


class GeneratedQuestion(_Strict):
    """生成された1問。Bさんの生成処理はこの形で3問を返す（design.md D2）。

    - question / hint / explanation: 空でない文字列
    - choices: ちょうど4つ、空でなく重複しない
    - correct_index: 正解の choices の位置（0〜3）

    questions テーブルの1行にそのまま保存される（position は並び順から 1〜3）。
    """

    question: NonEmptyStr
    choices: Annotated[
        list[NonEmptyStr],
        Field(min_length=CHOICE_COUNT, max_length=CHOICE_COUNT),
        AfterValidator(_unique),
    ]
    correct_index: Annotated[int, Field(ge=0, le=CHOICE_COUNT - 1)]
    hint: NonEmptyStr
    explanation: NonEmptyStr


class GeneratedQuiz(_Strict):
    """生成結果全体。ちょうど3問。"""

    questions: Annotated[
        list[GeneratedQuestion],
        Field(min_length=QUESTION_COUNT, max_length=QUESTION_COUNT),
    ]


# ---- D5: push確認 -------------------------------------------------------------


class PushCheckIn(_Strict):
    repository_id: UUID
    commit_shas: Annotated[list[Sha], Field(min_length=1, max_length=MAX_PUSH_CHECK_SHAS)]


class PendingCommit(BaseModel):
    commit_sha: str
    reason: Literal["missing", "not_passed"]
    quiz_url: str | None


class PushCheckOut(BaseModel):
    allowed: bool
    pending_commits: list[PendingCommit]
