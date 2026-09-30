# app/learning/mcp_client.py
import asyncio
import json
import os
import sys
from contextlib import AsyncExitStack
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

from app.learning.errors import GenerationTimeout

# MCP Server の起動から generate_quiz の結果までの上限（design.md 8.2）。
# FastAPI 側の生成全体の上限（30秒）より短くする。
MCP_CALL_TIMEOUT_SECONDS = 20.0

# MCP SDK は子プロセスに HOME・PATH などしか引き継がないので、ここに列挙した
# 環境変数だけが MCP Server に届く。DATABASE_URL などの他の秘密は渡さない。
_SERVER_ENV_KEYS = ("GOOGLE_API_KEY", "GEMINI_MODEL")


def _server_params() -> StdioServerParameters:
    return StdioServerParameters(
        command=sys.executable,
        args=["-m", "app.learning.mcp_server"],
        env={k: os.environ[k] for k in _SERVER_ENV_KEYS if k in os.environ},
    )


async def call_generate_quiz(*, message: str, files: list[str], diff: str) -> dict:
    try:
        async with asyncio.timeout(MCP_CALL_TIMEOUT_SECONDS):
            return await _call_generate_quiz(message=message, files=files, diff=diff)
    except TimeoutError as e:
        raise GenerationTimeout(f"MCP call exceeded {MCP_CALL_TIMEOUT_SECONDS}s") from e


async def _call_generate_quiz(*, message: str, files: list[str], diff: str) -> dict:
    async with AsyncExitStack() as stack:
        read, write = await stack.enter_async_context(stdio_client(_server_params()))
        session = await stack.enter_async_context(ClientSession(read, write))
        await session.initialize()

        result = await session.call_tool(
            "generate_quiz",
            arguments={"message": message, "files": files, "diff": diff},
        )

        if result.is_error:
            raise RuntimeError(str(result.content))

        # structured_content が入っていればそれを使う
        if result.structured_content:
            return result.structured_content

        # フォールバック：TextContent（JSON文字列）をパースする
        text_parts = [block.text for block in result.content if hasattr(block, "text")]
        if not text_parts:
            raise RuntimeError(f"Unexpected tool result shape: {result.content!r}")
        return json.loads("".join(text_parts))