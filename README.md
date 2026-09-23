# commitcoach

`git commit` で作られたcommitの情報と変更差分を、構造化されたJSON（`commit.snapshot`）としてローカルに保存するCLIツールです。
Gitの `post-commit` フックから起動されます。任意のcommitを同じ形式で標準出力へ出すこともできます。

最終的には「開発者が変更したコードを教材にする学習サービス」の入口になる想定です。今回実装したのは次の最初の段階だけです。

```
git commit → post-commitフック → commitcoach（Go） → commit情報と差分を取得 → JSONを生成 → .git/commitcoach/events/ に保存
```

## 目次

- [今回の範囲](#今回の範囲)
- [動作環境と検証状況](#動作環境と検証状況)
- [ビルド](#ビルド)
- [使い方](#使い方)
- [最短の動作確認手順](#最短の動作確認手順)
- [JSON形式](#json形式)
- [比較方針（初回commit・merge commit）](#比較方針初回commitmerge-commit)
- [サイズ制限・バイナリ・機密ファイル](#サイズ制限バイナリ機密ファイル)
- [フックの設計と競合時の扱い](#フックの設計と競合時の扱い)
- [バイナリを移動・削除した場合の復旧](#バイナリを移動削除した場合の復旧)
- [差分取得の仕組みと安全性](#差分取得の仕組みと安全性)
- [テスト](#テスト)
- [ディレクトリ構成](#ディレクトリ構成)
- [将来Webバックエンドへ接続する箇所](#将来webバックエンドへ接続する箇所)
- [仕様からの調整と理由](#仕様からの調整と理由)
- [既知の制限・注意点](#既知の制限注意点)

## 今回の範囲

### 実装したもの

| コマンド | 内容 |
|---|---|
| `commitcoach init` | 現在のリポジトリに `post-commit` フックを導入する。既存のフックや `core.hooksPath` があれば何も変更せず、手動で組み込む方法を表示する |
| `commitcoach hook post-commit` | フックから呼ばれる内部コマンド。HEADのcommitをJSONにして保存する |
| `commitcoach export` | 任意のcommitを同じ形式のJSONにして、標準出力またはファイルへ出す。フックを導入していなくても使える |
| `commitcoach status` | リポジトリ、フックの導入状態、有効なフックの場所、呼び出されるバイナリの状態、保存先、競合する設定を表示する |
| `commitcoach uninstall` | このツールが導入し、編集されていないフックだけを削除する。保存済みJSONは残す |

### 実装していないもの

Webフロントエンド、HTTP APIサーバー、外部への送信、LLM連携、問題生成・採点、ブラウザの起動、認証、データベース、pushの制限、pre-pushフック、`git add` への反応、全リポジトリへの一括導入、パッケージ公開、AST解析・言語判定。

**このツールにはpushを制限する機能はありません。** また、あらゆる履歴操作（rebase、mergeなど）で作られるcommitをフックだけで漏れなく捕捉するものでもありません（[既知の制限](#既知の制限注意点)を参照）。

## 動作環境と検証状況

| 項目 | 要件 | 今回の検証環境 |
|---|---|---|
| Go | 1.22以上（`go.mod`） | Go 1.26.3 linux/amd64 |
| Git | **2.31以上**（`git rev-parse --path-format=absolute` を使うため。古い場合は起動時にエラーで知らせる） | Git 2.43.0 |
| OS | macOS / Linux（POSIX `sh` でフックを実行） | Linux（Ubuntu on WSL2、カーネル 6.6） |

- **検証済み**: Linux（WSL2）上での `go test ./...`、`go vet ./...`、`go test -race ./...`、手動でのフック起動確認。
- **未検証**: macOS。POSIXの機能だけを使う設計で、テストにもmacOS向けの分岐（`/var` → `/private/var` のシンボリックリンク、UTF-8以外のファイル名が作れないこと）を入れていますが、macOS上では実行していません。
- **未対応・未検証**: Windows。フックのシェルスクリプトとPOSIXのパーミッションを前提にしています。
- 外部ライブラリは使っていません（標準ライブラリのみ）。実行時にネットワーク接続は不要です。
- 対応を確認したリポジトリ構成（自動テストあり）: 通常のリポジトリ、サブディレクトリからの実行、`git worktree` のリンクされた作業ツリー、submodule、`--separate-git-dir`、SHA-256リポジトリ（`export`／差分取得）。
- bareリポジトリでは `init` を拒否します（作業ツリーがなく `git commit` もフックも動かないため）。`export` は使えます。

## ビルド

```sh
git clone https://github.com/kobadaidesu/hook-test.git
cd hook-test
go build -o bin/commitcoach ./cmd/commitcoach
```

または `go install ./cmd/commitcoach` で `$(go env GOPATH)/bin/commitcoach` に入れても構いません。

> **`go run` で `init` しないでください。** `go run` が作るバイナリは一時ディレクトリにあり、すぐ消えます。フックはバイナリの絶対パスを埋め込むので、`init` は必ずビルド済み・インストール済みのバイナリから実行してください（`go run` の一時バイナリからの `init` はエラーになり、何も変更されません）。

## 使い方

以下では、ビルドしたバイナリを `/path/to/commitcoach` と書きます。

### 導入（init）

対象リポジトリの中（サブディレクトリでも可）で実行します。

```sh
cd /path/to/your-repo
/path/to/commitcoach init
```

```
commitcoach: installed the post-commit hook: /path/to/your-repo/.git/hooks/post-commit
  runs:      /path/to/commitcoach hook post-commit
  snapshots: /path/to/your-repo/.git/commitcoach/events
```

- 何度実行しても重複しません（2回目は `already installed`）。
- 別の場所に置いたバイナリで再実行すると、このツールが作った未編集のフックに限り、呼び出し先を更新します。
- Git管理外で実行するとエラーになります（`no usable git repository found: fatal: not a git repository ...`）。
- Gitの設定（ローカル・グローバルとも）は一切変更しません。

### 普段どおりcommitする

```sh
git commit -m "Fix addition"
```

commitが作られた後、フックが次の1行だけを標準エラー出力に表示します。JSONの本文は表示しません。

```
commitcoach: recorded the snapshot of 7132952bb5d80a411bcea23ebc9d72bbc469f3b8 in /path/to/your-repo/.git/commitcoach/events/7132952bb5d80a411bcea23ebc9d72bbc469f3b8.json
```

JSONの取得や保存に失敗しても、**commit自体は作成済みのまま**です。フックは次のような警告を出して正常終了し、commitを取り消したりはしません。

```
commitcoach: error: the commit was created, but its snapshot could not be recorded: saving the snapshot: ...
commitcoach: warning: the commit was created, but its snapshot was not recorded (exit status 1)
```

### 保存先の確認

保存先は Git管理ディレクトリ（全worktreeで共有される common dir）の下です。作業ツリーには何も作りません。

```sh
ls "$(git rev-parse --path-format=absolute --git-common-dir)/commitcoach/events"
# 通常のリポジトリなら .git/commitcoach/events/<commitの完全なID>.json
```

- ディレクトリは初回保存時に作られます（パーミッション 0700、JSONは 0600）。
- 同じcommitを再収集すると、同じファイルが置き換えられます。
- `commitcoach status` の `snapshots:` 行にも場所と件数が出ます。

### 任意のcommitをJSONにする（export）

```sh
commitcoach export                                   # HEAD を標準出力へ
commitcoach export --commit HEAD
commitcoach export --commit 1a2b3c4                  # 短縮IDやブランチ名、タグも可
commitcoach export --commit HEAD --output -          # "-" は標準出力（既定値）
commitcoach export --commit HEAD~1 --output ./event.json
commitcoach export | jq '.files[] | {status, new_path, additions, deletions}'
```

- 標準出力にはJSONオブジェクトを1つだけ出します。診断メッセージはすべて標準エラー出力です。
- 存在しないcommitや、commitに解決できない指定（tree、blobなど）は終了コード1のエラーになり、標準出力には何も出しません。
- `--output` にファイルを指定した場合も、同じディレクトリの一時ファイルに書いてから置き換えます（パーミッション 0600。既存ファイルは置き換わります。親ディレクトリは作りません）。
- `--timeout`（既定 2分）で上限時間を変えられます。
- bareリポジトリでも使えます（テスト済み）。
- フックと同じ処理（`event.Build`）を使うため、同じcommitなら `captured_at` 以外は同じJSONになります（テストで確認済み）。

### 状態確認（status）

```
repository:        demo-repo
working tree:      /path/to/demo-repo
git directory:     /path/to/demo-repo/.git
git:               git version 2.43.0
hooks directory:   /path/to/demo-repo/.git/hooks (Git default)
post-commit hook:  installed by commitcoach, unmodified
  hook file:       /path/to/demo-repo/.git/hooks/post-commit
  executable:      /path/to/commitcoach (ok, the binary you are running)
snapshots:         /path/to/demo-repo/.git/commitcoach/events (4 JSON file(s))
conflicts:         none
```

未導入なら `not installed (run "commitcoach init")`、バイナリが消えていれば `MISSING` と表示します。`core.hooksPath` や他人のフックがあれば `conflicts:` に列挙します。状態確認だけで何も変更しません。

### 解除（uninstall）

```sh
commitcoach uninstall
```

- このツールが作り、**編集されていない** `post-commit` だけを削除します。
- 他人のフックは削除しません（`left unchanged` と表示して終了コード0）。
- 導入後に編集されたフックは削除せず、警告を出して終了コード1で終わります。必要なら内容を確認して手で削除してください。
- 未導入でも安全に実行できます（`nothing to do`）。
- 保存済みのJSONは削除しません。不要なら `.git/commitcoach/` を手で削除してください。

### 終了コード

| コード | 意味 |
|---|---|
| 0 | 成功（`status` は未導入でも0） |
| 1 | 失敗（Gitの失敗、保存失敗、タイムアウト、存在しないcommit、既存フックとの競合など） |
| 2 | 使い方の誤り（不明なコマンド、余分な引数） |

## 最短の動作確認手順

一時ディレクトリにリポジトリを作って試す手順です（既存のリポジトリには触れません）。

```sh
# 1. ビルド
cd hook-test
go build -o bin/commitcoach ./cmd/commitcoach
CC="$PWD/bin/commitcoach"

# 2. 試験用リポジトリ
tmp=$(mktemp -d) && cd "$tmp"
git init -q -b main
git config user.name "Example Developer"
git config user.email dev@example.com

# 3. 導入してcommit
"$CC" init
printf 'package calc\n\nfunc add(a, b int) int {\n\treturn a - b\n}\n' > add.go
git add add.go && git commit -m "Initial commit"      # 初回commit（空treeと比較）
sed -i.bak 's/a - b/a + b/' add.go && rm -f add.go.bak
git commit -am "Fix addition"                          # 通常のcommit（第一親と比較）

# 4. 保存されたJSONを見る
ls .git/commitcoach/events/
cat ".git/commitcoach/events/$(git rev-parse HEAD).json"

# 5. 未commitの変更が混ざらないことを確認
echo "// uncommitted" >> add.go
"$CC" export --commit HEAD | grep -c uncommitted       # → 0

# 6. 状態確認と解除
"$CC" status
"$CC" uninstall
```

## JSON形式

- JSON Schema: [docs/commit-snapshot.schema.json](docs/commit-snapshot.schema.json)（draft 2020-12）
- 実際に生成したサンプル: [examples/](examples/)
  - [commit-snapshot.json](examples/commit-snapshot.json): フックが保存したもの。変更・追加・削除・リネーム（日本語と空白を含むパス）・バイナリ・`.env` を含む
  - [root-commit-snapshot.json](examples/root-commit-snapshot.json): 初回commitを `export --output` で出したもの
  - [merge-commit-snapshot.json](examples/merge-commit-snapshot.json): `git merge --no-ff` のmerge commitを `export` で出したもの

抜粋（[examples/commit-snapshot.json](examples/commit-snapshot.json) より）:

```json
{
  "schema_version": "1.0",
  "event_type": "commit.snapshot",
  "captured_at": "2026-09-23T22:28:11Z",
  "repository": { "name": "demo-repo", "current_branch": "main" },
  "commit": {
    "sha": "70298ce05b6fa1c0811d3fe0f870e1dfd841ce15",
    "parents": ["8cd948cf3e93db50e6b30eaab2c366131fdd538d"],
    "message": "Fix addition and tidy up\n\n- add() が引き算になっていた不具合を修正\n- 未使用の legacy.go を削除し、メモを \"使い方 メモ.txt\" に改名\n",
    "author_name": "Example Developer",
    "authored_at": "2026-09-24T09:59:00+09:00",
    "committed_at": "2026-09-24T10:00:00+09:00"
  },
  "comparison": { "strategy": "first_parent", "base_sha": "8cd948cf3e93db50e6b30eaab2c366131fdd538d" },
  "files": [
    { "status": "A", "old_path": null, "new_path": ".env", "additions": 1, "deletions": 0, "binary": false,
      "patch": null, "patch_truncated": false, "omitted_reason": "sensitive_path" },
    { "status": "A", "old_path": null, "new_path": "assets/logo.png", "additions": null, "deletions": null, "binary": true,
      "patch": null, "patch_truncated": false, "omitted_reason": "binary" },
    { "status": "M", "old_path": "src/add.go", "new_path": "src/add.go", "additions": 1, "deletions": 1, "binary": false,
      "patch": "diff --git a/src/add.go b/src/add.go\nindex 2367180..1bc3191 100644\n--- a/src/add.go\n+++ b/src/add.go\n@@ -1,5 +1,5 @@\n package calc\n \n func add(a, b int) int {\n-\treturn a - b\n+\treturn a + b\n }\n",
      "patch_truncated": false, "omitted_reason": null }
  ],
  "summary": { "changed_files": 5, "included_files": 5, "omitted_files": 0, "truncated": false },
  "warnings": []
}
```

### フィールド

| フィールド | 型 | 意味 |
|---|---|---|
| `schema_version` | string | 常に `"1.0"` |
| `event_type` | string | 常に `"commit.snapshot"`。フックでもexportでも同じ（「commitを新しく作った」ことではなく「commitの内容」を表す） |
| `captured_at` | string | このJSONを作った日時（RFC 3339、UTC）。commitの日時とは別 |
| `repository.name` | string | リポジトリのディレクトリ名。表示用で、一意なIDではない |
| `repository.current_branch` | string \| null | **収集時点でcheckoutしていたブランチ**。そのcommitを含むブランチとは限らない（過去のcommitをexportしても今のブランチが入る）。detached HEADならnull |
| `commit.sha` | string | commitの完全なオブジェクトID（SHA-1なら40桁、SHA-256なら64桁） |
| `commit.parents` | string[] | commitオブジェクトに記録された親。第一親が先頭。初回commitは `[]` |
| `commit.message` | string | commitメッセージ全文（改行・引用符・日本語もそのまま。通常は末尾に改行がある） |
| `commit.author_name` | string | author名。メールアドレスは含めない |
| `commit.authored_at` / `committed_at` | string | author日時・committer日時（RFC 3339。記録されたタイムゾーンのオフセットを保持） |
| `comparison.strategy` | string | `first_parent`（第一親と比較）または `empty_tree`（初回commit、空treeと比較） |
| `comparison.base_sha` | string \| null | 比較元（第一親）。`empty_tree` のときはnull |
| `files[]` | array | 変更ファイル。Gitの出力順（パス順）で安定。最大100件 |
| `files[].status` | string | Gitの変更種別の1文字: `A` 追加、`M` 変更、`D` 削除、`R` リネーム、`T` 種別変更（通常ファイル↔シンボリックリンクなど） |
| `files[].old_path` | string \| null | 変更前のリポジトリ相対パス。追加ならnull |
| `files[].new_path` | string \| null | 変更後のリポジトリ相対パス。削除ならnull。変更では両方に同じパス |
| `files[].additions` / `deletions` | int \| null | 追加・削除行数（Gitの `--numstat` の値。patchを切り詰めても全体の行数）。バイナリで数えられない場合はnull（0とは偽らない） |
| `files[].binary` | bool | Gitがバイナリと判定したか |
| `files[].patch` | string \| null | unified diff（`git diff-tree -p` の出力そのまま、`diff --git` ヘッダ付き）。`omitted_reason` があるときだけnull |
| `files[].patch_truncated` | bool | サイズ上限で `patch` を途中で切ったか |
| `files[].omitted_reason` | string \| null | `patch` がnullの理由: `binary`、`sensitive_path`（機密ファイルらしい名前）、`total_patch_limit`（1イベントの合計上限に達した） |
| `summary.changed_files` | int | 比較で検出したファイル総数 |
| `summary.included_files` | int | `files` 配列の件数 |
| `summary.omitted_files` | int | 件数上限で `files` から外したファイル数 |
| `summary.truncated` | bool | 上限によってファイル一覧かパッチのどれかを切り詰めた・省略したとき true（バイナリ・機密による省略は含まない） |
| `warnings` | string[] | 人が読むための注意書き（merge commitの比較方針、上限到達、UTF-8への置換、Gitの警告など）。なければ `[]` |

- 空の配列は常に `[]`（nullにはしない）。値がないことはnullで表し、キー自体は省略しません。
- JSONの文字列はUTF-8しか表せないため、UTF-8として不正なバイト（Latin-1のソース、不正なファイル名など）はU+FFFDに置き換えます。**その場合は必ず `warnings` に、どのフィールドが置き換えられたか**（例: `files[0].new_path is not valid UTF-8; ...`）を記録します。無言では変換しません。

## 比較方針（初回commit・merge commit）

| commitの種類 | `comparison.strategy` | `base_sha` | 比較対象 |
|---|---|---|---|
| 通常のcommit | `first_parent` | 第一親 | 第一親のtree |
| 初回commit（親なし） | `empty_tree` | `null` | 空tree。IDは `git hash-object -t tree --stdin`（書き込みなし）でリポジトリのハッシュ方式に合わせて算出し、SHA-1の値をハードコードしない |
| merge commit | `first_parent` | 第一親 | 第一親のtreeのみ。全親をまとめた特殊な形式（combined diff）には切り替えない。`warnings` に `merge commit with 2 parents: ... relative to the first parent only` と明記 |

- 親の一覧は `git cat-file commit` で**commitオブジェクトから直接**読みます。`git log --format=%P` はshallow cloneの境界にあるcommitの親を空にして返すため、使うと初回commitと誤認します（実測で確認）。
- 第一親のオブジェクトが手元にない場合（shallow cloneの境界など）は、初回commitとして扱わずに理由を示してエラーにします。

  ```
  commitcoach: error: commit 40ff146b...: its first parent 288299e4... is not in the local repository, so the diff cannot be computed (the commit is not treated as a root commit); this is a shallow clone, run 'git fetch --unshallow' (or fetch more history) to get it
  ```

## サイズ制限・バイナリ・機密ファイル

### サイズ制限

上限は [internal/event/limits.go](internal/event/limits.go) の `DefaultLimits` の一か所で定義しています。

| 上限 | 既定値 | 到達時の動作 |
|---|---|---|
| `files` 配列の件数 | 100件 | 101件目以降は `files` に入れず件数だけ数える（`summary.omitted_files`）。Gitの出力もそこで読むのをやめてプロセスを止める |
| パッチ本文（1ファイル） | 64 KiB | そのファイルの `patch` を切り詰めて `patch_truncated: true` |
| パッチ本文（1イベント合計） | 512 KiB | 残りの枠で切り詰め、枠を使い切った後のファイルは `patch: null`、`omitted_reason: "total_patch_limit"` |

- これらは**JSONにエンコードする前のパッチ本文のバイト数**です。JSON全体のサイズの上限ではありません（エスケープでJSONはこれより大きくなります）。
- 差分はストリームで読み、ファイルごとに上限＋数バイトを超えた分は捨てます。差分全体をメモリに読み込んでから切ることはしません（3 MiBの1行ファイルなどでテスト済み）。
- 切り詰めはUTF-8の文字境界で行い、文字の途中では切りません。
- 上限による切り詰め・省略と、Gitコマンド自体の失敗は区別します。Gitが失敗した場合はエラーになり、空の成功データやダミーデータで置き換えません。
- 上限に達したときは `summary.truncated: true` と `warnings` に記録します。

### バイナリ

- Gitがバイナリと判定したファイルは本文を埋め込みません（`--binary` は使わない）。`patch: null`、`omitted_reason: "binary"`、行数は数えられないので `additions`/`deletions` は `null` です。

### 機密ファイル

次のような名前のファイルは、既定でパッチ本文を記録しません（`patch: null`、`omitted_reason: "sensitive_path"`）。パス、変更種別、行数は記録します。判定は [internal/event/sensitive.go](internal/event/sensitive.go) にあり、大文字小文字は区別しません。

- `.env`、`.env.*`（`.env.local`、`.env.production`、`.env.example` なども含む）
- 秘密鍵・鍵ストア: `id_rsa`・`id_dsa`・`id_ecdsa`・`id_ed25519`（`.pub` は除く）、`*.pem`、`*.key`、`*.p12`、`*.pfx`、`*.jks`、`*.keystore`、`*.ppk`
- 認証情報: `.netrc`、`_netrc`、`.npmrc`、`.pypirc`、`.git-credentials`、`.htpasswd`、`credentials`、`credentials.json`

リネームでは**変更前と変更後の両方のパス**で判定します（`.env` → `config.txt` も、`settings.ini` → `.env.local` も除外）。除外したファイルの本文はGitの出力から読み捨て、保存もログ出力もしません。

> **この除外で機密情報の完全な除去は保証できません。** ファイル名による判定だけなので、通常のソースコード、設定ファイル、テストデータ、**commitメッセージ**に書かれた秘密情報はそのまま記録されます。上の一覧にない名前の鍵ファイルも記録されます。JSONを外部に送る機能を今後追加する場合は、この前提で扱ってください。

そのほか、次の情報はJSONに含めません。

- authorやcommitterのメールアドレス
- リモートURLや認証情報
- 環境変数（取得もしません）
- PC上の絶対パス（`repository.name` はディレクトリ名だけ）

## フックの設計と競合時の扱い

### 生成されるフック

`init` は次のような薄いシェルスクリプトを `<Git管理ディレクトリ>/hooks/post-commit`（パーミッション 0755）に書きます。処理の本体はシェルに書かず、バイナリを絶対パスで呼ぶだけです。

```sh
#!/bin/sh
# commitcoach-managed-hook: v1
# commitcoach-executable: "/path/to/commitcoach"
#
# Created by "commitcoach init". Remove it with "commitcoach uninstall".
# If you edit this file, commitcoach will no longer update or remove it.
#
# The commit already exists when this hook runs. Recording its snapshot may
# fail, but that never undoes or blocks the commit, so this hook exits 0.
commitcoach_bin='/path/to/commitcoach'
if [ -x "$commitcoach_bin" ]; then
	"$commitcoach_bin" hook post-commit ||
		printf 'commitcoach: warning: the commit was created, but its snapshot was not recorded (exit status %s)\n' "$?" >&2
else
	printf 'commitcoach: warning: the commit was created, but the commitcoach executable is missing: %s\n' "$commitcoach_bin" >&2
	printf 'commitcoach: build or install it again and rerun "commitcoach init", or delete this hook: %s\n' "$0" >&2
fi
exit 0
```

- `PATH` の違いで起動に失敗しないよう、`init` を実行したバイナリの絶対パスを埋め込みます。
- パスはシェルの単一引用符でクォートします（`'` は `'"'"'` に置換）。空白、`'`、`"`、`$`、バッククォート、`\`、改行、日本語を含むパスでも壊れないことをテストしています。
- Go側（`commitcoach hook post-commit`）は失敗すると非ゼロで終わり、フック側はそれを警告として表示して **常に0で終わります**。フック全体には30秒のタイムアウトがあります（`--timeout` で変更可）。
- **識別方法**: 2行目のマーカーで「このツールが作ったフック」と判定し、3行目（Goの文字列リテラル形式）から埋め込んだパスを読み取ります。そのパスでスクリプトを再生成してバイト単位で比較し、一致しなければ「導入後に編集された」とみなします。編集されたフックは上書きも削除もしません。
- 新規作成では、一時ファイルを作ってからハードリンクで配置するため、既存のファイルを上書きしません（ハードリンクが使えないファイルシステムでは排他作成に切り替えます）。

### 競合時の扱い

`init` は次の場合に**何も変更せず**終了コード1で終わり、原因と手動で組み込む方法を表示します。

| 状況 | 表示 |
|---|---|
| `core.hooksPath` が設定されている（ローカル・グローバル・システム・worktree・`-c` のどれでも） | 設定値・スコープ・設定ファイルと、Gitが実際にフックを探すディレクトリ |
| 他人の `post-commit` がある（シンボリックリンクも含め、中身をたどらない） | そのファイルのパス |
| このツールが作った `post-commit` が編集されている | そのファイルのパス |

表示例（`core.hooksPath` がある場合）:

```
commitcoach: not installed: core.hooksPath is set, so Git runs hooks from /path/to/repo/.husky instead of /path/to/repo/.git/hooks; commitcoach does not change this setting or that directory.
  core.hooksPath = .husky (local scope, file:.git/config)
No file or setting was changed.

To run commitcoach from your own hook, add this line to /path/to/repo/.husky/post-commit
(create the file with "#!/bin/sh" as its first line and make it executable if it does not exist):

    '/path/to/commitcoach' hook post-commit || echo 'commitcoach: warning: the commit was created, but its snapshot was not recorded' >&2
```

既存フックへの自動統合はしません。表示された1行を既存の `post-commit` に手で追加してください。`status` は、手動で組み込んだフックも `appears to call commitcoach (manual integration)` と表示します。

`git worktree` のリンクされた作業ツリーでは、フックとJSONの保存先はリポジトリ全体で共有されます（`init` はその旨を表示します）。

## バイナリを移動・削除した場合の復旧

フックはバイナリの絶対パスを持っているので、バイナリを移動・削除するとcommitのたびに次の警告が出ます。commit自体は成功します。

```
commitcoach: warning: the commit was created, but the commitcoach executable is missing: /old/path/commitcoach
```

`status` でも `executable: ... (MISSING: ...)` と表示されます。復旧方法:

1. バイナリをビルドし直すかインストールし直し、**新しいバイナリで `init` を再実行**します。このツールが作った未編集のフックなら、呼び出し先が新しいパスに更新されます（`updated the post-commit hook`）。
2. もう使わない場合は、任意の場所の `commitcoach` で `uninstall` を実行します。バイナリがなければ `.git/hooks/post-commit` を手で削除しても構いません（中身がこのツールのものであることを確認してから）。

## 差分取得の仕組みと安全性

「作業中のファイル」ではなく「指定したcommitの内容」だけを取得するため、次のようにしています。

- **commitを固定**: フックでは開始時点のHEADを `git rev-parse --verify --end-of-options HEAD^{commit}` で完全なIDに解決し、以降はそのIDだけを使います。exportの `--commit` も同様に検証・解決してから使います。
- **作業ツリーとindexを読まない**: `git diff-tree` は、indexの情報とblobが一致するファイルについて、オブジェクトDBではなく**作業ツリーのファイルから**内容を読むことがあります（blobを消しても作業ツリーから差分が出ることを実測で確認）。これを防ぐため、`diff-tree` の実行時だけ `GIT_INDEX_FILE` を存在しないパス（空のindex）に向け、commitされたオブジェクトだけを読むようにしています。また `GIT_ATTR_SOURCE` を対象commitにし、`.gitattributes`（バイナリ判定など）も作業ツリーではなくcommitから読みます（Git 2.40以上で有効。それより古いGitでは作業ツリーの `.gitattributes` が使われます）。
- **1プロセスで一貫した出力**: `git diff-tree -r -z --raw --numstat --patch --find-renames <base> <commit>` を1回だけ実行します。同じ内部リストから、`--raw`（NUL区切り）→ `--numstat`（NUL区切り）→ NUL → patch の順に出力されるので、レコードは位置で対応づけます。
- **patchの対応づけを検証**: patchは「行頭の `diff --git `」で区切りますが、区切った各セクションのヘッダ行が、そのファイルについてGitが出すはずのヘッダ（Gitと同じ規則でクォートしたパス）と**完全に一致するか**を毎回検証します。一致しなければエラーにします。改行・タブ・引用符・`\` を含むパスはGitがヘッダ内でクォートするので、ヘッダが複数行に割れることはありません。種別変更（`T`）はGitが削除＋追加の2セクションで出すので、それも考慮しています。
- **パスは空白で分割しない**: ファイル一覧はNUL区切りで読みます。日本語、空白、タブ、改行、`-` で始まる名前、`:(glob)` のようなpathspecに見える名前、`diff --git` を含む名前でテストしています。ファイルパスはGitの引数として渡しません（pathspecを使わないため、オプションやpathspecとして誤解釈される余地がありません）。
- **外部プロセスを起動しない**: `--no-ext-diff`、`--no-textconv`、`--no-color`、`--no-pager`、`-c core.fsmonitor=false`、`git log --no-show-signature`（gpgを起動しない）。フックの本文取得でシンボリックリンクをたどることもありません（シンボリックリンクはリンク先の文字列がblobとして差分に出ます）。
- **シェルを通さない**: Gitはコマンド名と引数を分けて `os/exec` に渡します。`sh -c` に文字列を渡すことはありません。ユーザー入力（`--commit`）の前には `--end-of-options` を置きます。
- **無限に待たない**: すべてのGit呼び出しに `context` のタイムアウトを付け、固まったGit（や子プロセスがパイプを握ったまま残るケース）でも戻ってくることをテストしています。
- **Gitに書き込まない**: 読み取り専用のコマンドだけを使い、`GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0` を付けます。フックの中から `git commit` や `git add` を呼ぶことはありません。
- **保存**: 一時ファイルに書いて `fsync` してから `rename` するので、途中までのJSONは残りません。同時に実行されても各プロセスが別の一時ファイルを使うため、中身が混ざりません（最後の `rename` が勝つ）。保存先のファイル名は16進数のcommit IDであることを検証してから組み立てます。

## テスト

```sh
go test ./...
go vet ./...
go test -race ./...
```

今回の環境（Linux/WSL2、Go 1.26.3、Git 2.43.0）で、上の3つすべてが成功しています（トップレベルのテスト75件とサブテスト7件がすべてPASS、スキップ0件）。

テストはモックではなく、一時ディレクトリに実際のGitリポジトリを作って行います。各テストバイナリは `TestMain` で次のように環境を隔離するので、利用者のグローバル設定（署名、`core.hooksPath`、属性ファイルなど）は混入しません。

- `HOME`・`XDG_CONFIG_HOME` を空の一時ディレクトリに、`GIT_CONFIG_GLOBAL` を空ファイルにし、`GIT_CONFIG_NOSYSTEM=1` を設定。それ以外の `GIT_*` 環境変数は削除
- リポジトリごとにローカルの `user.name`・`user.email` を設定し、`commit.gpgsign=false`
- 利用者のリポジトリにはcommitを作らない

`internal/cli` のE2Eテストは、**空白と引用符を含むディレクトリ**（`bin dir with 'single' and "double" quotes/`）にCLIをビルドし、そのバイナリで `init` してから実際に `git commit` を実行してフックを起動します。

| 観点 | 主なテスト |
|---|---|
| 基本動作 | init後のcommitでJSONが保存される／exportのstdoutがJSON1個だけでログが混ざらない／過去のcommitのexport／`--output` ファイル（0600）／フックとexportが同じJSONになる |
| 未commitの変更 | 部分commit（`git commit -- a.txt`）で、ステージ済み・未ステージ・未追跡の変更が混入しない／commit後の変更がexportに混入しない |
| 差分 | 初回commit、通常の変更、追加・削除・リネーム・種別変更、日本語・空白・タブ・改行・引用符・`\`・`diff --git` を含むファイル名、不正なUTF-8のファイル名と本文、バイナリ、空commit、detached HEAD、merge commitの第一親比較、`--amend` 後の新しいcommit、SHA-256リポジトリ |
| 制限と異常系 | 64 KiB／512 KiB／100件の上限（UTF-8の境界で切ること、途中で読むのをやめること）、3 MiBの1行、`.env`・秘密鍵の本文除外（リネームの両側）、存在しないcommit・tree・blob・`--help` などオプションに見える指定、Git管理外、空のリポジトリ、blob欠損によるGitの失敗、保存失敗（読み取り専用ディレクトリ）、shallow cloneの境界、タイムアウト（固まる偽の `git` を `PATH` に置く）、同時書き込み |
| 導入・解除 | initを2回実行しても重複しない、他人のフック・シンボリックリンクを変更しない、ローカル・グローバルの `core.hooksPath` を変更しない（設定ファイルがバイト単位で不変）、uninstallが他人のファイルを消さない、編集されたフックを消さない、空白や引用符を含むバイナリパスでフックが起動する、保存に失敗してもcommitは残る、バイナリを消してもcommitは残り `init` で復旧できる、bareリポジトリでinitを拒否、linked worktree・submodule・`--separate-git-dir` |
| JSON Schema | Schemaのプロパティ・必須キーとGoの構造体の一致、`examples/` がSchemaの制約（const・enum・pattern・if/then・oneOf 相当）を満たすこと。テストで生成したすべてのイベントにも同じ検査をかける |

## ディレクトリ構成

```
cmd/commitcoach/main.go     エントリーポイント（cli.Run を呼ぶだけ）
internal/cli/               サブコマンド、入出力、終了コードの決定（ここだけが表示と終了コードを扱う）
internal/gitrepo/           Git実行（runner.go）、リポジトリ検出（repo.go）、commit取得（commit.go）、差分取得（diff.go）
internal/event/             JSONの型（event.go）、組み立て（build.go）、上限値（limits.go）、機密ファイル判定（sensitive.go）
internal/hooks/             フックの生成・識別・導入・確認・解除
internal/storage/           JSONの保存（一時ファイル＋rename）
internal/testutil/          テスト用のGit環境の隔離と一時リポジトリ
docs/commit-snapshot.schema.json   JSON Schema
examples/                   実測から生成したサンプルJSON
```

Goのモジュールパスは `github.com/kobadaidesu/hook-test` です（このリポジトリの `origin` に合わせています）。

## 将来Webバックエンドへ接続する箇所

差分取得とJSON生成は、出力先から独立しています。

- `event.Build(ctx, repo, commitID, limits, capturedAt)` がイベント（Goの構造体）を作り、`event.Marshal` がJSONにします。どちらもファイルや標準出力に依存しないので、HTTP送信を追加してもGitまわりのコードは書き直す必要がありません。
- 送信を足す場所は `internal/cli/snapshot.go` の `recordHead`（フック）と `runExport` です。たとえば `internal/sender` のようなパッケージを作り、保存済みのJSONを送る形にできます。
- フックの中で同期的に送信すると、ネットワークの遅延がそのまま `git commit` の待ち時間になります。`.git/commitcoach/events/` を送信待ちキュー（outbox）とみなし、別プロセスや次回実行時に送る設計を推奨します。送信済みかどうかの管理は今回実装していません。

## 仕様からの調整と理由

依頼時の仕様（JSONの基本形）から変えた点、決めた点です。キー名と型は変えていません。

| 項目 | 内容 | 理由 |
|---|---|---|
| `authored_at`・`committed_at` のタイムゾーン | 例は `Z` だったが、commitに記録されたオフセットを保持（例: `+09:00`）。`captured_at` はUTC（`Z`） | どちらもRFC 3339。作者の現地時刻という情報を失わないため |
| `patch` の中身 | `git diff-tree -p` の出力をそのまま入れるので、例にはなかった `index ...` 行や、リネーム時の `similarity index` などの行も含まれる | Gitの出力を加工しない方が正確で、`git apply` などとの互換性も保てるため |
| `patch` と `omitted_reason` | 常にどちらか一方だけがnull。切り詰めたときは `patch` があり、`omitted_reason` はnullで `patch_truncated: true` | 「本文がない理由」と「本文が一部だけ」を区別するため |
| `omitted_reason` の値 | `binary`・`sensitive_path`・`total_patch_limit` の3つに限定 | 利用側が分岐しやすくするため |
| `status` | Gitの1文字（`R086` のような類似度は落として `R`）。`T`（種別変更）もあり得る | 仕様の「Gitの変更種別」に合わせつつ、値を安定させるため |
| 種別変更（`T`）のpatch | 1つの `patch` に削除と追加の2セクションが入る | Git自身がそう出力するため |
| merge commitの明示方法 | `strategy` は通常のcommitと同じ `first_parent`。merge commitであることは `parents` が2つ以上であることと `warnings` の文言で示す | 新しいキーを増やさずに比較方針を明記するため |
| 不正なUTF-8 | U+FFFDに置換し、必ず `warnings` に記録 | JSON文字列はUTF-8しか表せない。無言で別の名前に変えないため |
| Gitの最低バージョン | 2.31 | `rev-parse --path-format=absolute` で、どこから実行しても絶対パスを得るため |
| `.gitattributes` の読み取り元 | Git 2.40以上では対象commitから読む | 作業ツリーの未commitの変更で、バイナリ判定などの結果が変わらないようにするため |

## 既知の制限・注意点

- **フックが捕捉しない操作がある**: Git 2.43で実測したところ、`git merge` が自動で作るmerge commitでは `post-commit` は起動しませんでした（`post-merge` フックの管轄のため）。`git cherry-pick` と `git rebase` では起動しましたが、Gitのバージョンや操作によって変わり得るので保証しません。取りこぼしたcommitは `commitcoach export --commit <id>` で取得できます。
- **pushの制限はしません**。今回はJSONを保存するだけです。
- **機密情報の除外はファイル名による判定だけ**です。コード中やcommitメッセージ中の秘密情報は記録されます（[機密ファイル](#機密ファイル)）。
- **JSONは自動では削除されません**。`--amend` やrebaseで古くなったcommitのJSONも残ります。`uninstall` しても残ります。
- **`core.hooksPath` を使う構成（husky、lefthookなど）へは自動で導入しません**。表示される1行を既存のフックに手で追加してください。
- partial clone（`--filter=blob:none` など）は未検証です。`GIT_NO_LAZY_FETCH=1` を付けていますが、この環境変数が効くのはGit 2.44以上で、それより古いGitでは欠けたオブジェクトを取得しにいく（ネットワークを使う）可能性があります。
- `repository.current_branch` は収集時点のブランチです。過去のcommitをexportしたときは、そのcommitが属するブランチとは限りません。
- macOSは未検証、Windowsは未対応です（[動作環境と検証状況](#動作環境と検証状況)）。
