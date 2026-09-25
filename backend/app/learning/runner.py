"""問題生成の「呼び出し役」。commits.py はこのファイル経由でだけ問題生成を呼ぶ。

Aさん担当のファイルです。役割は3つだけで、
MCP には一切触れません（MCP 本体は Bさんの generate.py / mcp_client.py）。

1. 切り替え：環境変数 QUIZ_GENERATOR で、どの生成関数を使うか選ぶ
   - fake（既定）: 下の fake_generate_quiz。MCP Server 無しで commit の内容から機械的に3問作る。
     Bさんの generate.py が完成する前でも、commit 受信〜問題画面〜push 確認を試せる。
   - mcp: Bさんの app.learning.generate.generate_quiz を呼ぶ。
2. 時間制限：生成全体に30秒（GENERATION_TIMEOUT_SECONDS）のタイムアウトを掛ける。
3. 保険の検証：戻り値を GeneratedQuiz で検証し直し、壊れていれば 502 にする
   （不正な問題が DB に入らないよう、保存する側でも確認する）。

Bさんへ：`app/learning/generate.py` に次の関数を作れば、このファイルは触らなくて大丈夫です。

    # app/learning/generate.py
    from app.schemas import GeneratedQuiz

    async def generate_quiz(*, message: str, files: list[str], diff: str) -> GeneratedQuiz:
        # MCP Server の generate_quiz tool を呼び、結果を GeneratedQuiz にして返す。
        # 失敗したら app.learning.errors の例外を raise する。
        ...

- 引数は D1 で受け取った message・files・diff（除外処理済み）。user_id 等は渡しません。
- 戻り値は `GeneratedQuiz`（3問）。`{"questions": [...]}` 形式の dict を返しても構いません。
  design.md の「結果検証」は `GeneratedQuiz.model_validate(結果)` の1行で済みます
  （3問・選択肢4つで重複なし・correct_index 0〜3・空文字なし を確認する）。
- MCP 呼び出し自体の20秒タイムアウトは generate.py 側で設定してください。
- 問題の保存はこちら（commits.py）で行います。generate.py から DB を触る必要はありません。
"""

import asyncio
import importlib
import logging
from typing import Annotated, Any, Protocol

from fastapi import Depends
from pydantic import ValidationError

from app.config import Settings, get_settings
from app.learning.errors import (
    GenerationError,
    GenerationTimeout,
    GenerationUnavailable,
    InvalidGeneratedQuiz,
)
from app.schemas import GeneratedQuestion, GeneratedQuiz

logger = logging.getLogger(__name__)


class QuizGenerator(Protocol):
    async def __call__(self, *, message: str, files: list[str], diff: str) -> Any: ...


async def fake_generate_quiz(*, message: str, files: list[str], diff: str) -> GeneratedQuiz:
    """開発用：commit の内容から機械的に3問を作る（MCP Server 不要）。"""
    summary = message.strip().splitlines()[0] if message.strip() else "(空)"
    first_file = files[0] if files else "(なし)"
    n = len(files)
    return GeneratedQuiz(
        questions=[
            GeneratedQuestion(
                question="このcommitのメッセージの1行目はどれですか？",
                choices=_with_decoys(summary, ["Initial commit", "Fix typo", "WIP", "Update"], 0),
                correct_index=0,
                hint="commitメッセージの先頭行を確認してください。",
                explanation=f"メッセージの1行目は「{summary}」です。",
            ),
            GeneratedQuestion(
                question="このcommitで変更されたファイルの数はいくつですか？",
                choices=_with_decoys(f"{n}個", [f"{n + i}個" for i in (1, 2, 3)], 1),
                correct_index=1,
                hint="変更ファイル一覧を数えてください。",
                explanation=f"変更ファイルは{n}個です。",
            ),
            GeneratedQuestion(
                question="変更ファイル一覧の先頭にあるファイルはどれですか？",
                choices=_with_decoys(first_file, ["README.md", "main.go", "index.ts", "setup.py"], 3),
                correct_index=3,
                hint="差分の最初の diff --git 行を見てください。",
                explanation=f"先頭のファイルは {first_file} です。",
            ),
        ]
    )


def _with_decoys(answer: str, candidates: list[str], index: int) -> list[str]:
    """answer と重複しない誤答3つを選び、answer を index の位置に置く。"""
    decoys = [c for c in candidates if c != answer][:3]
    return decoys[:index] + [answer] + decoys[index:]


def _load_mcp_generator() -> QuizGenerator:
    try:
        module = importlib.import_module("app.learning.generate")
        return module.generate_quiz
    except (ImportError, AttributeError) as e:
        raise GenerationUnavailable("app.learning.generate.generate_quiz is not available") from e


def get_quiz_generator(settings: Annotated[Settings, Depends(get_settings)]) -> QuizGenerator:
    """設定に応じた生成関数を返す FastAPI 依存関数。テストでは差し替える。"""
    if settings.quiz_generator == "mcp":
        return _load_mcp_generator()
    return fake_generate_quiz


async def run_generation(
    generator: QuizGenerator,
    *,
    message: str,
    files: list[str],
    diff: str,
    timeout: float,
) -> GeneratedQuiz:
    """生成関数を呼び、タイムアウト・例外・結果の形式をまとめて処理する。

    失敗はすべて GenerationError（のサブクラス）にして raise する。
    """
    try:
        async with asyncio.timeout(timeout):
            result = await generator(message=message, files=files, diff=diff)
    except TimeoutError as e:
        raise GenerationTimeout(f"generation exceeded {timeout}s") from e
    except GenerationError:
        raise
    except Exception as e:  # 生成側の想定外の失敗は 503 にする
        logger.exception("quiz generator raised an unexpected error")
        raise GenerationUnavailable(str(e)) from e

    try:
        if isinstance(result, GeneratedQuiz):
            # model_validate はモデルをそのまま通すので、dump して検証し直す。
            result = result.model_dump()
        return GeneratedQuiz.model_validate(result)
    except ValidationError as e:
        raise InvalidGeneratedQuiz(f"{e.error_count()} validation errors") from e
