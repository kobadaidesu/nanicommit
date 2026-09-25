# nanicommit backend

FastAPI のバックエンドです。設計は [docs/design.md](../docs/design.md) を参照してください。
このディレクトリには Aさん担当分（基盤・認証・リポジトリ登録・commit 受信・push 確認・DB）が入っています。
問題取得・回答・採点（`routers/quizzes.py`・`learning/grade.py`）と MCP による問題生成（`learning/mcp_client.py`・`learning/generate.py`）は Bさんが追加します。

## 起動

```sh
cd backend
uv sync
cp .env.example .env   # 値を埋める
uv run uvicorn app.main:app --reload
```

DB のテーブルは [supabase/migrations/](../supabase/migrations/) の SQL を Supabase に適用して作ります（design.md 7.2 の DDL と同じ内容）。

`QUIZ_GENERATOR=fake`（既定）では、MCP Server なしで commit の内容から機械的に3問を作ります。MCP で本物の問題を作るときは `QUIZ_GENERATOR=mcp` にします。

## テスト

```sh
uv run pytest
```

DB を使うテストは、`TEST_DATABASE_URL`（Postgres の管理用接続文字列）があればそれを使い、無ければ dev 依存の `pgserver` で一時的な Postgres を起動します。どちらの場合も `nanicommit_test` というデータベースを作り直し、Supabase の `auth.users` と `anon` / `authenticated` ロールの代わりを作ってからマイグレーションを適用します。

## Render へのデプロイ

Docker は使わず、Render の Python ランタイムで動かします。Render のダッシュボードで New → Web Service を選び、このリポジトリを指定して次のように設定します。

| 項目 | 値 |
|---|---|
| Language | Python 3 |
| Root Directory | `backend` |
| Build Command | `pip install uv && uv sync --frozen --no-dev` |
| Start Command | `uv run --no-dev uvicorn app.main:app --host 0.0.0.0 --port $PORT` |
| Health Check Path | `/health` |

Environment（環境変数）には `.env.example` の項目を登録します。値は Git に入れず、Render の画面にだけ入れてください。

- `DATABASE_URL`：Supabase の Connect → Session pooler の接続文字列
- `SUPABASE_URL` / `SUPABASE_ANON_KEY`：Supabase の Project Settings → API
- `WEB_BASE_URL`：フロントの本番 URL（例 `https://nanicommit.vercel.app`）
- `CORS_ORIGINS`：`["<フロントの本番 URL>"]`（JSON の配列で書く）
- `QUIZ_GENERATOR`：MCP ができるまでは `fake`
- `PYTHON_VERSION`：`3.12`（`.python-version` と同じ。念のため指定）

デプロイ後、`https://<サービス名>.onrender.com/health` が `{"status":"ok"}` を返せば起動しています。
Render の無料プランは、しばらくアクセスが無いと停止します。停止中の最初のリクエストは起動待ちで数十秒かかることがあります。

## API（Aさん担当分）

| メソッド | パス | 識別 | 内容 |
|---|---|---|---|
| POST | `/api/v1/repositories` | `X-User-Id` | R0：リポジトリ登録。`repository_id` を渡すと復旧（200、開始点は上書きしない） |
| POST | `/api/v1/commits` | `X-User-Id` | D1：受信・問題生成・保存。新規 201、登録済み 200、内容違い 409 |
| POST | `/api/v1/push/check` | `X-User-Id` | D5：合格確認。`missing` / `not_passed` を返す |
| GET | `/health` | なし | 疎通確認 |

- `X-User-Id` は小文字ハイフン付き UUID で、`auth.users` に存在する必要があります（無い・不正・不存在は 401）。
- 送信上限は Go CLI（`hooks/internal/event/limits.go`）と同じ、ファイル100個・差分512KiB。超過は 413。
- 生成失敗は 422（根拠不足）/ 502（生成結果が不正）/ 503（接続不可）/ 504（30秒タイムアウト）。どの場合も何も保存しないので、`send --commit SHA` で再送できます。
- DB に繋がらないときは 503 を返します。
- 429（生成リクエストの試行上限）は PoC では未実装です。

## Bさん向け：差し込み方

### 1. 問題生成：`app/learning/generate.py`

`QUIZ_GENERATOR=mcp` のとき、commits.py は `app.learning.generate.generate_quiz` を呼びます。この名前・引数で作ってください。

```python
# app/learning/generate.py
from app.learning.errors import GenerationUnavailable, InsufficientContext
from app.schemas import GeneratedQuiz


async def generate_quiz(*, message: str, files: list[str], diff: str) -> GeneratedQuiz:
    result = ...  # mcp_client 経由で generate_quiz tool を呼ぶ
    return GeneratedQuiz.model_validate(result)  # {"questions": [...]} の dict でも可
```

- **受け取るもの**：D1 の `message`・`files`・`diff`（除外処理済み）。user_id などの認証情報は渡しません。
- **返すもの**：`GeneratedQuiz`（[app/schemas.py](app/schemas.py)）。3問、各問題は選択肢4つ（空でなく重複なし）・`correct_index` 0〜3・`hint`・`explanation`。
- **失敗したとき**：[app/learning/errors.py](app/learning/errors.py) の例外を raise します。

  | 例外 | HTTP | 使う場面 |
  |---|---|---|
  | `InsufficientContext` | 422 | 3問を作る根拠が足りない |
  | `InvalidGeneratedQuiz` | 502 | MCP Server の結果が壊れている |
  | `GenerationUnavailable` | 503 | MCP Server に繋がらない・エラー |
  | `GenerationTimeout` | 504 | 時間切れ |

  それ以外の例外は 503 として扱います。例外の message はログにだけ出し、レスポンスには定型文を返します。
- **こちらでやること**：30秒のタイムアウト、結果の再検証（不正なら 502）、commit と3問の保存。generate.py から DB を触る必要はありません。
- 保存先：`questions` テーブルに、並び順どおり `position` 1〜3 で入ります。

### 2. 問題取得・回答：`app/routers/quizzes.py`

Web 向け API なので、識別は `WebUserId`（Supabase access token を Auth サーバーで検証）を使います。`X-User-Id` だけでは通りません。

```python
# app/routers/quizzes.py
from uuid import UUID

from fastapi import APIRouter

from app.auth import WebUserId
from app.db import Conn
from app.routers.common import not_found

router = APIRouter(prefix="/api/v1", tags=["quizzes"])


@router.get("/quizzes/{quiz_id}")
async def get_quiz(quiz_id: UUID, user_id: WebUserId, conn: Conn):
    cur = await conn.execute(
        "select * from public.commits where id = %s and user_id = %s",
        (quiz_id, user_id),
    )
    commit = await cur.fetchone()   # 行は dict で返る
    if commit is None:
        raise not_found()           # 無い・別ユーザーのものは 404
    ...
```

作ったら [app/main.py](app/main.py) のコメントに従って `app.include_router(quizzes.router)` を追加してください。

使える部品：

| 部品 | 場所 | 内容 |
|---|---|---|
| `WebUserId` | [app/auth.py](app/auth.py) | ログイン中ユーザーの UUID。無効なトークンは本体が呼ばれる前に 401 |
| `Conn` | [app/db.py](app/db.py) | リクエスト1回分の DB 接続（psycopg 3 非同期、行は dict）。正常終了で commit、例外で rollback |
| `not_found()` | [app/routers/common.py](app/routers/common.py) | 404 の例外 |
| `quiz_url(id)` | [app/routers/common.py](app/routers/common.py) | 問題ページの URL |
| `QUESTION_COUNT` など | [app/schemas.py](app/schemas.py) | 問題数・選択肢数の定数 |

回答処理（D4）の注意：

- `async with conn.transaction():` の中で `select ... from public.commits where id = %s and user_id = %s for update` で commit 行をロックし、回答保存・進捗計算・`commits.status` 更新を同じトランザクションで行ってください。
- `status='passed'` にするときは `passed_at = now()` も同時に設定します（DB の check 制約で、片方だけだとエラー）。
- push 確認（push.py）は `commits.status = 'passed'` だけを見て合否を判断します。

### テストで生成を差し替える

```python
from app.learning.generator import get_quiz_generator

client.app.dependency_overrides[get_quiz_generator] = lambda: my_fake_generate_quiz
```

`tests/conftest.py` の `client`・`user`・`register_repo`・`commit_body` を使うと、DB 付きのテストをすぐ書けます。
