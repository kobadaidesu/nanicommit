# app/learning/generate.py
import logging

from pydantic import ValidationError

from app.learning.errors import (
    GenerationUnavailable,
    InsufficientContext,
    InvalidGeneratedQuiz,
)
from app.learning.mcp_client import call_generate_quiz
from app.schemas import GeneratedQuiz

logger = logging.getLogger(__name__)


async def generate_quiz(*, message: str, files: list[str], diff: str) -> GeneratedQuiz:
    try:
        result = await call_generate_quiz(message=message, files=files, diff=diff)
    except Exception as e:
        # mcp_client側のGenerationTimeout/GenerationUnavailableはここをすり抜けてそのまま伝播する
        logger.exception("MCP経由の問題生成に失敗しました")
        raise GenerationUnavailable("MCP Serverへの接続に失敗しました") from e

    # 1. 形式チェックの前に「根拠不足で0問」を先に見分ける
    #    GeneratedQuizはちょうど3問を要求するので、0問のままmodel_validateに渡すと
    #    本来422にすべきものが502(InvalidGeneratedQuiz)になってしまうため。
    questions = result.get("questions") if isinstance(result, dict) else None
    if not questions:
        raise InsufficientContext("3問を作る根拠が足りませんでした")

    # 2. 残りの形式不正（4択でない・correct_indexが範囲外・余計なキーがある等）はここで検出
    try:
        return GeneratedQuiz.model_validate(result)
    except ValidationError as e:
        logger.exception("生成結果の形式が不正でした: %s", e)
        raise InvalidGeneratedQuiz("生成された問題データの形式が不正です") from e