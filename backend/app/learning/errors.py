"""問題生成の失敗を表す例外（design.md 6章「共通エラー」）。

Bさんへ：生成処理（generate.py / mcp_client.py）で失敗したら、下の例外を
raise してください。commits.py がそのまま対応する HTTP ステータスに変えます。
それ以外の例外が漏れた場合は 503 として扱います。

    from app.learning.errors import GenerationUnavailable, InsufficientContext

    raise InsufficientContext("3問を作る根拠が足りない")   # → 422
    raise GenerationUnavailable("MCP Serverに接続できない") # → 503

message はサーバーのログにだけ出し、レスポンスの detail には下の定型文を返します
（差分や認証情報を外へ出さないため）。

どの場合も commit と問題は保存されず、CLI は `send --commit SHA` で再送できます。
"""


class GenerationError(Exception):
    """問題生成の失敗の基底クラス。"""

    status_code = 503
    public_detail = "Quiz generation unavailable"


class InsufficientContext(GenerationError):
    """差分から3問を作る根拠が足りない（空commit・機密ファイルだけ等）。"""

    status_code = 422
    public_detail = "Not enough context to generate a quiz"


class InvalidGeneratedQuiz(GenerationError):
    """MCP Server が返した問題データが不正（3問でない・選択肢が壊れている等）。"""

    status_code = 502
    public_detail = "Quiz generator returned invalid data"


class GenerationUnavailable(GenerationError):
    """MCP Server に接続できない・エラーを返した。"""

    status_code = 503
    public_detail = "Quiz generation unavailable"


class GenerationTimeout(GenerationError):
    """生成が時間内に終わらなかった。"""

    status_code = 504
    public_detail = "Quiz generation timed out"
