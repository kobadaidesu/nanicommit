"""環境変数から読む設定。値は .env.example を参照。

Aさん担当のファイルです。DB 接続文字列などを
コードに直接書かないために、設定をここにまとめています。
"""

from functools import lru_cache
from typing import Literal

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    # Supabase PostgreSQL のサーバー用接続文字列。バックエンドだけが持つ。
    database_url: str = "postgresql://postgres:postgres@localhost:54322/postgres"
    db_pool_min_size: int = 1
    db_pool_max_size: int = 10

    # Web の access token を Auth サーバーで検証するために使う（公開用キーで足りる）。
    supabase_url: str = ""
    supabase_anon_key: str = ""

    # 問題画面（フロント）の URL。CLI が開く quiz_url の先頭部分になる。
    web_base_url: str = "http://localhost:3000"
    # バックエンドを呼んでよいフロントの配信元。ブラウザはここに無いページからの呼び出しを止める。
    # 開発中は Next.js（3000）と Vite（5173）のどちらでも動くよう両方許可しておく。
    cors_origins: list[str] = ["http://localhost:3000", "http://localhost:5173"]

    # fake: 固定の3問を返す開発用生成器 / mcp: Bさんの app.learning.generate を使う。
    quiz_generator: Literal["fake", "mcp"] = "fake"
    # design.md 8.2: FastAPIの生成処理30秒。
    generation_timeout_seconds: float = 30.0

    # リクエスト本文の上限（413）。Goの上限（差分合計512KiB）のJSONエスケープ分を見込む。
    max_request_bytes: int = 4 * 1024 * 1024


@lru_cache
def get_settings() -> Settings:
    return Settings()
