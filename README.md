# CommitCoach

`git commit` の変更内容を学習問題に変えるサービスのモノレポです。開発者がcommitするたびに、その差分から理解度チェックの問題を生成し、全問正解でpushを許可する——という体験を目指しています。

```
git commit → post-commitフック（hooks/ のGo CLI） → 6項目のJSON → バックエンド（予定） → 学習UI（web/）
```

## 構成

| ディレクトリ | 内容 | 状態 |
|---|---|---|
| [hooks/](hooks/) | Go製CLI `commitcoach`。post-commitフックを導入し、commitを6項目のJSONにして保存する | 実装済み（[hooks/README.md](hooks/README.md)） |
| [web/](web/) | 学習UI（React + Vite + TypeScript + Tailwind）。モックデータで動くUI先行実装 | UIモック実装済み（`cd web && npm install && npm run dev`） |
| [docs/](docs/) | [commit-payload.schema.json](docs/commit-payload.schema.json) — hooks・web・バックエンドで共有する6項目JSONのスキーマ | — |
| [examples/](examples/) | CLIが実際に生成したサンプルJSON | — |
| backend/ | 問題生成・採点・push許可のバックエンド（Python + Supabase 予定） | 未実装 |

## 6項目のJSON（コンポーネント間の契約）

```json
{
  "repository_id": "550e8400-e29b-41d4-a716-446655440000",
  "commit_sha": "0226edda3ed64eb7821ee827df5a63c8f9d21012",
  "branch": "feature/cache",
  "message": "キャッシュ削除処理を追加\n...",
  "files": ["docs/設計 メモ.md", "src/legacy.py", "src/user_service.py"],
  "diff": "diff --git a/... （unified diffをそのまま連結した文字列）"
}
```

詳細は [docs/commit-payload.schema.json](docs/commit-payload.schema.json) と [hooks/README.md](hooks/README.md) を参照。webのクイズAPI契約案は [web/src/types/quiz.ts](web/src/types/quiz.ts) にあります。
