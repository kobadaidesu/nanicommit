# app/learning/claude_cli.py
"""Claude Code CLI（`claude -p`）を JSON を返す LLM として呼ぶ共通処理（ローカル開発用）。

問題作成（mcp_server.py の QUIZ_LLM=claude）とキャラクターのセリフ作成で共用する。
入力には commit の diff や LLM が作った文章が入るので、プロンプトインジェクションを前提に
次のように閉じ込めて実行する。

- `--tools ""`: ファイル操作・シェル等のツールを全部無効にする（文章を返すことしかできない）
- `--strict-mcp-config` / `--setting-sources ""`: ユーザーの MCP サーバー・hooks・設定を読まない
- `--no-session-persistence`: 会話履歴を残さない
- 空の一時ディレクトリを cwd にする（CLAUDE.md 等を拾わない）
- プロンプトは引数ではなく stdin で渡す（diff が大きくても引数長の上限に当たらない）
"""

import json
import os
import shutil
import subprocess
import tempfile

DEFAULT_TIMEOUT_SECONDS = float(os.environ.get("CLAUDE_TIMEOUT_SECONDS", "80"))


class ClaudeCliError(RuntimeError):
    """claude CLI が見つからない・失敗した・JSON を返さなかった。"""


class ClaudeCliNotFound(ClaudeCliError):
    """claude コマンドが PATH に無い（Claude Code が入っていない）。"""


def extract_json(text: str) -> dict:
    """コードフェンス等が混ざっても最初の { から最後の } までを JSON として読む。"""
    start = text.find("{")
    end = text.rfind("}")
    if start == -1 or end <= start:
        raise ClaudeCliError(f"Claude returned no JSON object: {text[:200]!r}")
    try:
        value = json.loads(text[start : end + 1])
    except json.JSONDecodeError as e:
        raise ClaudeCliError(f"Claude returned invalid JSON: {e}") from e
    if not isinstance(value, dict):
        raise ClaudeCliError("Claude returned JSON that is not an object")
    return value


def run_claude_json(prompt: str, *, system_prompt: str, timeout: float = DEFAULT_TIMEOUT_SECONDS) -> dict:
    """system_prompt と prompt で claude -p を1回実行し、応答の JSON オブジェクトを返す。

    同期関数。async の中から呼ぶときは asyncio.to_thread で包む。
    """
    claude_bin = shutil.which("claude")
    if not claude_bin:
        raise ClaudeCliNotFound("claude CLI が見つかりません（Claude Code をインストールしてください）")

    args = [
        claude_bin,
        "-p",
        "--output-format", "json",
        "--tools", "",
        "--strict-mcp-config",
        "--setting-sources", "",
        "--no-session-persistence",
        "--system-prompt", system_prompt,
    ]
    with tempfile.TemporaryDirectory(prefix="nanicommit-claude-") as workdir:
        try:
            proc = subprocess.run(
                args,
                input=prompt,
                capture_output=True,
                text=True,
                timeout=timeout,
                cwd=workdir,
            )
        except subprocess.TimeoutExpired as e:
            raise ClaudeCliError(f"claude CLI timed out after {timeout}s") from e

    if proc.returncode != 0:
        raise ClaudeCliError(f"claude CLI failed (exit {proc.returncode}): {proc.stderr[-300:]}")
    try:
        envelope = json.loads(proc.stdout)
    except json.JSONDecodeError as e:
        raise ClaudeCliError(f"claude CLI printed non-JSON output: {proc.stdout[:200]!r}") from e
    if envelope.get("is_error"):
        raise ClaudeCliError(f"claude CLI returned error: {str(envelope.get('result'))[:300]!r}")
    return extract_json(envelope.get("result") or "")
