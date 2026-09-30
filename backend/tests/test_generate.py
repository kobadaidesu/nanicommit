"""generate.py（MCP 経由の問題生成）の失敗の分け方を確かめる。MCP Server は起動しない。"""

import asyncio

import pytest

from app.learning import generate
from app.learning.errors import (
    GenerationTimeout,
    GenerationUnavailable,
    InsufficientContext,
    InvalidGeneratedQuiz,
)
from app.learning.runner import fake_generate_quiz


def run(monkeypatch, result=None, exc=None):
    async def fake_call(*, message, files, diff):
        if exc is not None:
            raise exc
        return result

    monkeypatch.setattr(generate, "call_generate_quiz", fake_call)
    return asyncio.run(generate.generate_quiz(message="m", files=["a.py"], diff="d"))


def valid_result() -> dict:
    quiz = asyncio.run(fake_generate_quiz(message="m", files=["a.py"], diff="d"))
    return quiz.model_dump()


def test_valid_result(monkeypatch):
    quiz = run(monkeypatch, result=valid_result())
    assert len(quiz.questions) == 3


@pytest.mark.parametrize("result", [{"questions": []}, {}, None])
def test_no_questions_is_insufficient_context(monkeypatch, result):
    with pytest.raises(InsufficientContext):
        run(monkeypatch, result=result)


def test_broken_result_is_invalid(monkeypatch):
    result = valid_result()
    result["questions"][0]["correct_index"] = 4
    with pytest.raises(InvalidGeneratedQuiz):
        run(monkeypatch, result=result)


def test_timeout_passes_through(monkeypatch):
    with pytest.raises(GenerationTimeout):
        run(monkeypatch, exc=GenerationTimeout("slow"))


def test_other_errors_are_unavailable(monkeypatch):
    with pytest.raises(GenerationUnavailable):
        run(monkeypatch, exc=RuntimeError("MCP server crashed"))


def test_mcp_call_timeout(monkeypatch):
    from app.learning import mcp_client

    async def hang(**kwargs):
        await asyncio.sleep(10)

    monkeypatch.setattr(mcp_client, "_call_generate_quiz", hang)
    monkeypatch.setattr(mcp_client, "MCP_CALL_TIMEOUT_SECONDS", 0.01)
    with pytest.raises(GenerationTimeout):
        asyncio.run(mcp_client.call_generate_quiz(message="m", files=[], diff=""))


def test_mcp_server_gets_only_its_env(monkeypatch):
    from app.learning import mcp_client

    monkeypatch.setenv("GOOGLE_API_KEY", "key-from-render")
    monkeypatch.setenv("GEMINI_MODEL", "some-model")
    monkeypatch.setenv("DATABASE_URL", "postgresql://secret")
    env = mcp_client._server_params().env
    assert env == {"GOOGLE_API_KEY": "key-from-render", "GEMINI_MODEL": "some-model"}
