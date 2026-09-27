# app/learning/mcp_client.py
import json
import sys
from contextlib import AsyncExitStack
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

_SERVER_PARAMS = StdioServerParameters(
    command=sys.executable,
    args=["-m", "app.learning.mcp_server"],
)


async def call_generate_quiz(*, message: str, files: list[str], diff: str) -> dict:
    async with AsyncExitStack() as stack:
        read, write = await stack.enter_async_context(stdio_client(_SERVER_PARAMS))
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