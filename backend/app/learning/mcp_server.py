# app/learning/mcp_server.py
import json
import os
import time
import logging

from dotenv import load_dotenv
load_dotenv()

from google import genai
from google.genai import types
from google.genai.errors import ServerError
from mcp.server.mcpserver import MCPServer

from app.learning.prompts import SYSTEM_PROMPT, build_user_message

logger = logging.getLogger(__name__)
logging.basicConfig(level=logging.INFO, stream=__import__("sys").stderr)  # ← stderrへ。stdoutは絶対に使わない

api_key = os.environ.get("GOOGLE_API_KEY")
if not api_key:
    raise RuntimeError("GOOGLE_API_KEY が設定されていません（.envを確認してください）")

mcp = MCPServer("nanicommit-quiz-generator")
client = genai.Client(api_key=api_key)

MODEL_NAME = os.environ.get("GEMINI_MODEL", "gemini-3.8-flash")

RESPONSE_SCHEMA = {
    "type": "object",
    "properties": {
        "questions": {
            "type": "array",
            "items": {
                "type": "object",
                "properties": {
                    "question": {"type": "string"},
                    "choices": {
                        "type": "array",
                        "items": {"type": "string"},
                        "minItems": 4,
                        "maxItems": 4,
                    },
                    "correct_index": {"type": "integer"},
                    "hint": {"type": "string"},
                    "explanation": {"type": "string"},
                },
                "required": ["question", "choices", "correct_index", "hint", "explanation"],
            },
        },
    },
    "required": ["questions"],
}


@mcp.tool()
def generate_quiz(message: str, files: list[str], diff: str) -> dict:
    """commitの内容から4択問題を3問生成する"""
    last_error = None
    response = None
    for attempt in range(3):
        try:
            response = client.models.generate_content(
                model=MODEL_NAME,
                contents=build_user_message(message=message, files=files, diff=diff),
                config=types.GenerateContentConfig(
                    system_instruction=SYSTEM_PROMPT,
                    response_mime_type="application/json",
                    response_schema=RESPONSE_SCHEMA,
                    temperature=0.4,
                ),
            )
            break
        except ServerError as e:
            last_error = e
            logger.warning("Gemini 5xx, retry %d/3", attempt + 1)
            time.sleep(2 ** attempt)
    else:
        raise RuntimeError(f"Gemini generation failed after retries: {last_error}")

    candidate = response.candidates[0] if response.candidates else None
    if candidate is not None and candidate.finish_reason not in ("STOP", None):
        raise RuntimeError(f"Gemini generation stopped: {candidate.finish_reason}")

    if not response.text:
        raise RuntimeError("Gemini returned empty response")

    return json.loads(response.text)


if __name__ == "__main__":
    mcp.run()