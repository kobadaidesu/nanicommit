# commitcoach

`git commit` で作られたcommitの変更内容を、学習問題を生成するバックエンドへ渡すためのJSONにするCLIツールです。
Gitの `post-commit` フックから起動されてローカルに保存するほか、任意のcommitを同じ形式で標準出力やファイルへ出せます。

出力するJSONは次の6項目だけです。

```json
{
  "repository_id": "550e8400-e29b-41d4-a716-446655440000",
  "commit_sha": "0226edda3ed64eb7821ee827df5a63c8f9d21012",
  "branch": "feature/cache",
  "message": "キャッシュ削除処理を追加\n\n- get() でキャッシュを使う\n...",
  "files": ["docs/設計 メモ.md", "src/legacy.py", "src/user_service.py"],
  "diff": "diff --git a/docs/notes.md b/docs/設計 メモ.md\nsimilarity index ..."
}
```

最終的には「開発者が変更したコードを教材にする学習サービス」の入口になる想定です。今回実装したのは、JSONを作ってローカルに保存・出力するところまでです。

```
git commit → post-commitフック → commitcoach（Go） → commitと差分を取得 → 6項目のJSON → .git/commitcoach/events/ に保存
```

## 目次

- [今回の範囲](#今回の範囲)
- [動作環境と検証状況](#動作環境と検証状況)
- [ビルド](#ビルド)
- [使い方](#使い方)
- [最短の動作確認手順](#最短の動作確認手順)
- [JSON形式](#json形式)
- [比較方針（初回commit・merge commit）](#比較方針初回commitmerge-commit)
- [サイズ上限・バイナリ・機密ファイル](#サイズ上限バイナリ機密ファイル)
- [フックの設計と競合時の扱い](#フックの設計と競合時の扱い)
- [バイナリを移動・削除した場合の復旧](#バイナリを移動削除した場合の復旧)
- [差分取得の仕組みと安全性](#差分取得の仕組みと安全性)
- [テスト](#テスト)
- [ディレクトリ構成](#ディレクトリ構成)
- [将来Webバックエンドへ接続する箇所](#将来webバックエンドへ接続する箇所)
- [設計上の判断と理由](#設計上の判断と理由)
- [既知の制限・注意点](#既知の制限注意点)

## 今回の範囲

### 実装したもの

| コマンド | 内容 |
|---|---|
| `commitcoach init --repository-id <UUID>` | 現在のリポジトリに `post-commit` フックを導入し、repository_id をリポジトリローカルのGit設定に保存する。既存のフックや `core.hooksPath` があれば何も変更せず、手動で組み込む方法を表示する |
| `commitcoach hook post-commit` | フックから呼ばれる内部コマンド。HEADのcommitをJSONにして保存する |
| `commitcoach export` | 任意のcommitを同じ形式のJSONにして、標準出力またはファイルへ出す。フックを導入していなくても使える |
| `commitcoach status` | リポジトリ、repository_id、フックの導入状態、有効なフックの場所、呼び出されるバイナリの状態、保存先、競合する設定を表示する |
| `commitcoach uninstall` | このツールが導入し、編集されていないフックだけを削除する。保存済みJSONと repository_id の設定は残す |

### 実装していないもの

HTTP通信（バックエンドへの送信）、ログイン・認証、リポジトリ登録API、問題生成・採点、Webフロントエンド、ブラウザの起動、データベース、pushの制限、pre-pushフック、`git add` への反応、全リポジトリへの一括導入、パッケージ公開、AST解析・言語判定、旧形式や複数の出力形式の切り替え。

**このツールにはpushを制限する機能はありません。** また、あらゆる履歴操作（rebase、mergeなど）で作られるcommitをフックだけで漏れなく捕捉するものでもありません（[既知の制限](#既知の制限注意点)を参照）。

## 動作環境と検証状況

| 項目 | 要件 | 今回の検証環境 |
|---|---|---|
| Go | 1.22以上（`go.mod`） | Go 1.26.3 linux/amd64 |
| Git | **2.31以上**（`git rev-parse --path-format=absolute` を使うため。古い場合は起動時にエラーで知らせる） | Git 2.43.0 |
| OS | macOS / Linux（POSIX `sh` でフックを実行） | Linux（Ubuntu on WSL2、カーネル 6.6） |

- **検証済み**: Linux（WSL2）上での `go test ./...`、`go vet ./...`、`go test -race ./...`、ビルドしたCLIでの手動確認（export とフック起動）。
- **未検証**: macOS。POSIXの機能だけを使う設計で、テストにもmacOS向けの分岐（`/var` → `/private/var` のシンボリックリンク、UTF-8以外のファイル名が作れないこと）を入れていますが、macOS上では実行していません。
- **未対応・未検証**: Windows。フックのシェルスクリプトとPOSIXのパーミッションを前提にしています。
- 外部ライブラリは使っていません（標準ライブラリのみ）。実行時にネットワーク接続は不要です。
- 対応を確認したリポジトリ構成（自動テストあり）: 通常のリポジトリ、サブディレクトリからの実行、`git worktree` のリンクされた作業ツリー、submodule、`--separate-git-dir`、SHA-256リポジトリ（差分取得）。
- bareリポジトリでは `init` を拒否します（作業ツリーがなく `git commit` もフックも動かないため）。`export` は使えます。

## ビルド

```sh
git clone https://github.com/kobadaidesu/hook-test.git
cd hook-test/hooks
go build -o bin/commitcoach ./cmd/commitcoach
```

または `go install ./cmd/commitcoach` で `$(go env GOPATH)/bin/commitcoach` に入れても構いません。

> **`go run` で `init` しないでください。** `go run` が作るバイナリは一時ディレクトリにあり、すぐ消えます。フックはバイナリの絶対パスを埋め込むので、`init` は必ずビルド済み・インストール済みのバイナリから実行してください（`go run` の一時バイナリからの `init` はエラーになり、何も変更されません）。

## 使い方

以下では、ビルドしたバイナリを `/path/to/commitcoach` と書きます。

### repository_id

`repository_id` は、バックエンドに登録されたリポジトリのUUIDです。Gitのcommit IDやディレクトリ名とは別物です。今回はバックエンドに接続しないので、手で設定します。

| 指定方法 | 使われる場面 | 保存 |
|---|---|---|
| `init --repository-id <UUID>` | フック・export の両方 | リポジトリローカルのGit設定 `commitcoach.repositoryId`（`.git/config`）に保存 |
| `export --repository-id <UUID>` | その export 1回だけ（保存済みの値より優先） | 保存しない。保存済みの設定も変更しない |

- フラグも保存済みの設定もなければ、設定方法を示してエラーにします（終了コード1、JSONは出力しない）。空文字、固定値、自動生成したUUIDで埋めることはしません。

  ```
  commitcoach: error: repository_id is not set: save it with "commitcoach init --repository-id <UUID>" (or git config --local commitcoach.repositoryId <UUID>), or pass --repository-id to export
  ```

- 受け付けるのは `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx` 形式（16進数）のUUIDだけです。大文字は小文字にそろえて使います。nil UUID（`00000000-0000-0000-0000-000000000000`）は登録済みのIDではありえないので拒否します。
- 読むのはリポジトリローカルの設定だけです。グローバル設定に同じキーがあっても使いません（IDはリポジトリごとに違うため）。グローバル設定は一切変更しません。
- `git worktree` のリンクされた作業ツリーは、同じ `.git/config` を共有するので同じIDになります。
- 確認は `commitcoach status` の `repository id:` 行、または `git config --local --get commitcoach.repositoryId` で行えます。

### 導入（init）

対象リポジトリの中（サブディレクトリでも可）で実行します。

```sh
cd /path/to/your-repo
/path/to/commitcoach init --repository-id 550e8400-e29b-41d4-a716-446655440000
```

```
commitcoach: installed the post-commit hook: /path/to/your-repo/.git/hooks/post-commit
  runs:      /path/to/commitcoach hook post-commit
  snapshots: /path/to/your-repo/.git/commitcoach/events
  repository id: 550e8400-e29b-41d4-a716-446655440000 (saved as commitcoach.repositoryId in the local git config)
```

- 何度実行しても重複しません（2回目は `already installed`）。IDを保存済みなら、2回目以降は `--repository-id` を省略できます。別のIDを渡すと置き換えます。
- IDがない、または形式が正しくない場合は、フックも設定も変更せずにエラーで終わります。
- 別の場所に置いたバイナリで再実行すると、このツールが作った未編集のフックに限り、呼び出し先を更新します。
- Git管理外で実行するとエラーになります（`no usable git repository found: fatal: not a git repository ...`）。

### 普段どおりcommitする

```sh
git commit -m "キャッシュ削除処理を追加"
```

commitが作られた後、フックが標準エラー出力に短い案内だけを出します。JSONの本文は表示しません。

```
commitcoach: recorded the snapshot of 0226edda3ed64eb7821ee827df5a63c8f9d21012 in /path/to/your-repo/.git/commitcoach/events/0226edda3ed64eb7821ee827df5a63c8f9d21012.json
commitcoach: note: left out .env: the path looks like it holds secrets, so its content is not recorded
commitcoach: note: left out docs/diagram.png: binary file
```

`note:` 行は、機密ファイルやバイナリを files と diff から外したときなどに出ます（パスだけで、中身は出しません）。

JSONの取得や保存に失敗しても（repository_id が未設定、上限超過、保存先に書けないなど）、**commit自体は作成済みのまま**です。フックは次のような警告を出して正常終了し、commitを取り消したりはしません。

```
commitcoach: error: the commit was created, but its snapshot could not be recorded: repository_id is not set: ...
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
commitcoach export                                         # HEAD を標準出力へ（保存済みのIDを使う）
commitcoach export --commit HEAD --repository-id <UUID>    # この実行だけIDを指定
commitcoach export --commit 1a2b3c4                        # 短縮IDやブランチ名、タグも可
commitcoach export --commit HEAD --output -                # "-" は標準出力（既定値）
commitcoach export --commit HEAD --output ./payload.json
commitcoach export | jq '{commit_sha, files}'
```

- 標準出力にはJSONオブジェクトを1つだけ出します（末尾に改行1つ）。進捗や注意書きはすべて標準エラー出力です。
- 存在しないcommitや、commitに解決できない指定（tree、blobなど）は終了コード1のエラーになり、標準出力には何も出しません。
- 失敗したときは標準出力にもファイルにも何も書きません。`--output` のファイルは、同じディレクトリの一時ファイルに書き終えてから置き換えるので、失敗しても既存のファイルは壊れません（パーミッション 0600。親ディレクトリは作りません）。
- `--timeout`（既定 2分）で上限時間を変えられます。
- フックと同じ処理を使うため、同じcommit・同じ repository_id・同じチェックアウト状態なら、フックが保存したファイルと**バイト単位で同じJSON**になります（テストで確認済み）。
- bareリポジトリでも使えます（テスト済み）。

### 状態確認（status）

```
repository:        your-repo
working tree:      /path/to/your-repo
git directory:     /path/to/your-repo/.git
git:               git version 2.43.0
repository id:     550e8400-e29b-41d4-a716-446655440000 (commitcoach.repositoryId, local git config)
hooks directory:   /path/to/your-repo/.git/hooks (Git default)
post-commit hook:  installed by commitcoach, unmodified
  hook file:       /path/to/your-repo/.git/hooks/post-commit
  executable:      /path/to/commitcoach (ok, the binary you are running)
snapshots:         /path/to/your-repo/.git/commitcoach/events (2 JSON file(s))
conflicts:         none
```

未導入なら `not installed (run "commitcoach init")`、IDが未設定なら `repository id: not set (...)`、バイナリが消えていれば `MISSING` と表示します。`core.hooksPath` や他人のフックがあれば `conflicts:` に列挙します。状態確認だけで何も変更しません。

### 解除（uninstall）

```sh
commitcoach uninstall
```

- このツールが作り、**編集されていない** `post-commit` だけを削除します。
- 他人のフックは削除しません（`left unchanged` と表示して終了コード0）。
- 導入後に編集されたフックは削除せず、警告を出して終了コード1で終わります。必要なら内容を確認して手で削除してください。
- 未導入でも安全に実行できます（`nothing to do`）。
- 保存済みのJSONと repository_id の設定は削除しません。不要なら `.git/commitcoach/` を削除し、`git config --local --unset commitcoach.repositoryId` を実行してください。

### 終了コード

| コード | 意味 |
|---|---|
| 0 | 成功（`status` は未導入でも0） |
| 1 | 失敗（Gitの失敗、保存失敗、タイムアウト、存在しないcommit、repository_id の未設定・不正、上限超過、既存フックとの競合など） |
| 2 | 使い方の誤り（不明なコマンド、余分な引数） |

## 最短の動作確認手順

一時ディレクトリにリポジトリを作って試す手順です（既存のリポジトリには触れません）。UUIDはテスト用の値です。

```sh
# 1. ビルド
cd hook-test/hooks
go build -o bin/commitcoach ./cmd/commitcoach
CC="$PWD/bin/commitcoach"

# 2. 試験用リポジトリ
tmp=$(mktemp -d) && cd "$tmp"
git init -q -b main
git config user.name "Example Developer"
git config user.email dev@example.com

# 3. repository_id を保存して導入し、commit
"$CC" init --repository-id 550e8400-e29b-41d4-a716-446655440000
printf 'package calc\n\nfunc add(a, b int) int {\n\treturn a - b\n}\n' > add.go
git add add.go && git commit -m "Initial commit"      # 初回commit（空treeと比較）
sed -i.bak 's/a - b/a + b/' add.go && rm -f add.go.bak
git commit -am "Fix addition"                          # 通常のcommit（第一親と比較）

# 4. フックが保存したJSONを見る
ls .git/commitcoach/events/
cat ".git/commitcoach/events/$(git rev-parse HEAD).json"

# 5. export（未commitの変更が混ざらないことも確認）
echo "// uncommitted" >> add.go
"$CC" export --commit HEAD | grep -c uncommitted       # → 0
"$CC" export --commit HEAD~1 --repository-id 9b8a7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d   # この実行だけ別のID

# 6. 状態確認と解除
"$CC" status
"$CC" uninstall
```

## JSON形式

- JSON Schema: [docs/commit-payload.schema.json](../docs/commit-payload.schema.json)（draft 2020-12）
- 実際に生成したサンプル: [examples/](../examples/)
  - [commit.json](../examples/commit.json): フックが保存したもの。変更・削除・リネーム（日本語と空白を含むパス）に加え、`.env` とバイナリを含むcommit（この2つは files・diff に入らない）
  - [root-commit.json](../examples/root-commit.json): 初回commitを `export --output` で出したもの。`feature/cache` をチェックアウトした状態で出したので、branch は `feature/cache`（[branch](#branch-の意味) を参照）
  - [merge-commit.json](../examples/merge-commit.json): `git merge --no-ff` のmerge commitを `export` で出したもの（第一親との差分）

[examples/commit.json](../examples/commit.json) の全文:

```json
{
  "repository_id": "550e8400-e29b-41d4-a716-446655440000",
  "commit_sha": "0226edda3ed64eb7821ee827df5a63c8f9d21012",
  "branch": "feature/cache",
  "message": "キャッシュ削除処理を追加\n\n- get() でキャッシュを使う\n- delete() でキャッシュも削除する\n- 未使用の legacy.py を削除し、メモを改名\n",
  "files": [
    "docs/設計 メモ.md",
    "src/legacy.py",
    "src/user_service.py"
  ],
  "diff": "diff --git a/docs/notes.md b/docs/設計 メモ.md\nsimilarity index 55%\nrename from docs/notes.md\nrename to docs/設計 メモ.md\nindex 63d8dfd..a95bf21 100644\n--- a/docs/notes.md\n+++ b/docs/設計 メモ.md\t\n@@ -1,4 +1,4 @@\n # Notes\n \n - users are loaded from the repository\n-- nothing is cached yet\n+- 削除時にキャッシュも消す\ndiff --git a/src/legacy.py b/src/legacy.py\ndeleted file mode 100644\nindex 274c900..0000000\n--- a/src/legacy.py\n+++ /dev/null\n...\ndiff --git a/src/user_service.py b/src/user_service.py\n...\n+    def delete(self, user_id):\n+        self.repo.delete(user_id)\n+        self.cache.pop(user_id, None)\n"
}
```

（`diff` は途中を `...` で省略しています。実物は [examples/commit.json](../examples/commit.json) を見てください。）

### フィールド

トップレベルのキーはこの6つだけです。これ以外（コミット日時、統計、警告など）は出力しません。

| フィールド | 型 | 内容 |
|---|---|---|
| `repository_id` | string | バックエンドに登録されたリポジトリのUUID（小文字）。[repository_id](#repository_id) を参照 |
| `commit_sha` | string | 対象commitの完全なオブジェクトID（SHA-1なら40桁、SHA-256なら64桁。短縮IDは出さない） |
| `branch` | string \| null | **収集時点でチェックアウトしていたブランチ名**。detached HEADならnull |
| `message` | string | commitメッセージ全文。タイトル行だけでなく、本文・改行・日本語・引用符をそのまま保持（通常は末尾に改行がある） |
| `files` | string[] | `diff` に含まれるファイルのリポジトリ相対パス。追加・変更・リネームは変更後のパス、削除は変更前のパス。重複なし、`diff` と同じ順（Gitのパス順）。0件なら `[]` |
| `diff` | string | `files` のファイルの unified diff（`git diff-tree -p` の出力そのまま、`diff --git` ヘッダ付き）をつないだもの。変更がなければ `""` |

#### branch の意味

`branch` は「そのcommitが属するブランチ」ではなく、**JSONを作った時点で作業ツリーにチェックアウトしていたブランチ**です。

- フックでは、commitした直後のブランチになります（普段の使い方では、commitしたブランチと一致します）。
- 過去のcommitを `export --commit` したときは、そのcommitとは関係なく、いまチェックアウトしているブランチが入ります。
- detached HEAD（`git checkout --detach`、rebase中など）ではnullです。

#### files と diff

- `files` と `diff` は常に同じファイルを指します。機密ファイルとバイナリは両方から外します（[サイズ上限・バイナリ・機密ファイル](#サイズ上限バイナリ機密ファイル)）。
- リネームは `diff` の中で1つのセクション（`rename from` / `rename to`）として出て、`files` には変更後のパスだけが入ります。重複したり、別ファイルの変更が混ざったりしないことをテストで確認しています。
- 種別変更（通常ファイル↔シンボリックリンクなど）は、Git自身が削除＋追加の2セクションで出力するので、`diff` には2セクション、`files` には1件入ります。
- 空commitは `"files": []`、`"diff": ""` です。
- JSONの文字列はUTF-8しか表せないため、UTF-8として不正なバイト（Latin-1のソース、不正なファイル名など）はU+FFFDに置き換えます。その場合は必ず標準エラー出力に `note: the path "bad-\xff.txt" is not valid UTF-8; ...` のように、どれを置き換えたかを表示します。無言では変換しません。

## 比較方針（初回commit・merge commit）

| commitの種類 | 比較対象 |
|---|---|
| 通常のcommit | 第一親 |
| 初回commit（親なし） | 空tree。IDは `git hash-object -t tree --stdin`（書き込みなし）でリポジトリのハッシュ方式に合わせて算出し、SHA-1の値をハードコードしない |
| merge commit | 第一親だけ。全親をまとめた特殊な形式（combined diff）には切り替えない。標準エラー出力に `note: merge commit with 2 parents: files and diff show the changes relative to the first parent only` と表示 |

- 親の一覧は `git cat-file commit` で**commitオブジェクトから直接**読みます。`git log --format=%P` はshallow cloneの境界にあるcommitの親を空にして返すため、使うと初回commitと誤認します（実測で確認）。
- 第一親のオブジェクトが手元にない場合（shallow cloneの境界など）は、初回commitとして扱わずに理由を示してエラーにします。

  ```
  commitcoach: error: commit 40ff146b...: its first parent 288299e4... is not in the local repository, so the diff cannot be computed (the commit is not treated as a root commit); this is a shallow clone, run 'git fetch --unshallow' (or fetch more history) to get it
  ```

## サイズ上限・バイナリ・機密ファイル

### サイズ上限

上限は [internal/event/limits.go](internal/event/limits.go) の `DefaultLimits` の一か所で定義しています。

| 上限 | 既定値 |
|---|---|
| 変更ファイル数 | 100件 |
| 差分本文（1ファイル） | 64 KiB |
| 差分本文（1commitの合計） | 512 KiB |

JSONには「切り詰めた」ことを表す項目がないので、**上限を超えたcommitについては、一部分だけのJSONを出さずに出力を中止します**（部分的な差分が、完全な差分に見えてしまうため）。

```
commitcoach: error: limit exceeded: the diff of big.txt is larger than 65536 bytes; no JSON was written, because a partial diff would look complete
```

- export は終了コード1で、標準出力にもファイルにも何も書きません（既存の出力ファイルもそのまま）。
- フックでは警告を出し、JSONは保存しません。commitは残ります。
- 差分本文の上限は**JSONにエンコードする前のバイト数**です。JSON全体のサイズの上限ではありません（エスケープでJSONはこれより大きくなります）。
- 差分はストリームで読み、ファイルごとに上限＋数バイトを超えた分は捨てます。ファイル数が上限を超えた時点、または合計の上限に達した時点で、Gitの出力を読むのをやめてプロセスを止めます。差分全体をメモリに読み込んでから判定することはしません（3 MiBの1行ファイルなどでテスト済み）。
- 上限による中止（`limit exceeded`）と、Gitコマンド自体の失敗（`git diff-tree exited with status ...`）は別のエラーとして表示します。どちらの場合も、空データやダミーデータで置き換えることはしません。
- 機密ファイル・バイナリを外すのは意図した除外なので、上限超過とは違い、JSONは出力されます（次項）。上限の計算にも、外したファイルの本文は数えません（ファイル数には数えます）。

### バイナリ

Gitがバイナリと判定したファイルは、`files` と `diff` の両方から外します。本文もバイナリパッチ（`--binary`）も含めません。標準エラー出力に `note: left out <path>: binary file` と表示します。

### 機密ファイル

次のような名前のファイルは、`files` と `diff` の両方から外し、本文を記録しません。標準エラー出力に `note: left out <path>: the path looks like it holds secrets, so its content is not recorded` と表示します（パスだけで、中身は表示しません）。判定は [internal/event/sensitive.go](internal/event/sensitive.go) にあり、大文字小文字は区別しません。

- `.env`、`.env.*`（`.env.local`、`.env.production`、`.env.example` なども含む）
- 秘密鍵・鍵ストア: `id_rsa`・`id_dsa`・`id_ecdsa`・`id_ed25519`（`.pub` は除く）、`*.pem`、`*.key`、`*.p12`、`*.pfx`、`*.jks`、`*.keystore`、`*.ppk`
- 認証情報: `.netrc`、`_netrc`、`.npmrc`、`.pypirc`、`.git-credentials`、`.htpasswd`、`credentials`、`credentials.json`

リネームでは**変更前と変更後の両方のパス**で判定します（`.env` → `config.txt` も、`settings.ini` → `.env.local` も除外）。除外は差分を取得する段階で行い、6項目にまとめるときに未フィルタの差分を取り直すことはありません。除外したファイルの本文はGitの出力から読み捨て、保存もログ出力もしません。

> **この除外で機密情報の完全な除去は保証できません。** ファイル名による判定だけなので、通常のソースコード、設定ファイル、テストデータ、**commitメッセージ**に書かれた秘密情報はそのまま記録されます。上の一覧にない名前の鍵ファイルも記録されます。JSONを外部に送る機能を今後追加する場合は、この前提で扱ってください。

そのほか、次の情報はJSONに含めません: authorやcommitterの名前・メールアドレス、リモートURLや認証情報、環境変数（取得もしません）、PC上の絶対パス。

## フックの設計と競合時の扱い

### 生成されるフック

`init` は次のような薄いシェルスクリプトを `<Git管理ディレクトリ>/hooks/post-commit`（パーミッション 0755）に書きます。処理の本体はシェルに書かず、バイナリを絶対パスで呼ぶだけです。repository_id はスクリプトに埋め込まず、実行のたびにGit設定から読みます。

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

`init` は次の場合に**何も変更せず**（repository_id も保存せず）終了コード1で終わり、原因と手動で組み込む方法を表示します。

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

and save the repository ID:

    git config --local commitcoach.repositoryId 550e8400-e29b-41d4-a716-446655440000
```

既存フックへの自動統合はしません。表示された1行を既存の `post-commit` に手で追加し、repository_id を保存してください。`status` は、手動で組み込んだフックも `appears to call commitcoach (manual integration)` と表示します。

`git worktree` のリンクされた作業ツリーでは、フック・JSONの保存先・repository_id はリポジトリ全体で共有されます（`init` はその旨を表示します）。

## バイナリを移動・削除した場合の復旧

フックはバイナリの絶対パスを持っているので、バイナリを移動・削除するとcommitのたびに次の警告が出ます。commit自体は成功します。

```
commitcoach: warning: the commit was created, but the commitcoach executable is missing: /old/path/commitcoach
```

`status` でも `executable: ... (MISSING: ...)` と表示されます。復旧方法:

1. バイナリをビルドし直すかインストールし直し、**新しいバイナリで `init` を再実行**します（保存済みの repository_id がそのまま使われるので、`--repository-id` は不要です）。このツールが作った未編集のフックなら、呼び出し先が新しいパスに更新されます（`updated the post-commit hook`）。
2. もう使わない場合は、任意の場所の `commitcoach` で `uninstall` を実行します。バイナリがなければ `.git/hooks/post-commit` を手で削除しても構いません（中身がこのツールのものであることを確認してから）。

## 差分取得の仕組みと安全性

「作業中のファイル」ではなく「指定したcommitの内容」だけを取得するため、次のようにしています。内部ではファイルごとの詳細（変更種別、行数、除外理由など）を持つスナップショットを作り、それを6項目のJSONに変換しています。

- **commitを固定**: フックでは開始時点のHEADを `git rev-parse --verify --end-of-options HEAD^{commit}` で完全なIDに解決し、以降はそのIDだけを使います。exportの `--commit` も同様に検証・解決してから使います。
- **作業ツリーとindexを読まない**: `git diff-tree` は、indexの情報とblobが一致するファイルについて、オブジェクトDBではなく**作業ツリーのファイルから**内容を読むことがあります（blobを消しても作業ツリーから差分が出ることを実測で確認）。これを防ぐため、`diff-tree` の実行時だけ `GIT_INDEX_FILE` を存在しないパス（空のindex）に向け、commitされたオブジェクトだけを読むようにしています。また `GIT_ATTR_SOURCE` を対象commitにし、`.gitattributes`（バイナリ判定など）も作業ツリーではなくcommitから読みます（Git 2.40以上で有効。それより古いGitでは作業ツリーの `.gitattributes` が使われます）。
- **1プロセスで一貫した出力**: `git diff-tree -r -z --raw --numstat --patch --find-renames <base> <commit>` を1回だけ実行します。同じ内部リストから、`--raw`（NUL区切り）→ `--numstat`（NUL区切り）→ NUL → patch の順に出力されるので、レコードは位置で対応づけます。
- **patchの対応づけを検証**: patchは「行頭の `diff --git `」で区切りますが、区切った各セクションのヘッダ行が、そのファイルについてGitが出すはずのヘッダ（Gitと同じ規則でクォートしたパス）と**完全に一致するか**を毎回検証します。一致しなければエラーにします。改行・タブ・引用符・`\` を含むパスはGitがヘッダ内でクォートするので、ヘッダが複数行に割れることはありません。`diff` はこうしてファイルに対応づけたパッチをつないだものなので、同じ変更が重複したり、別ファイルの変更が混ざったりしません。
- **パスは空白で分割しない**: ファイル一覧はNUL区切りで読みます。日本語、空白、タブ、改行、`-` で始まる名前、`:(glob)` のようなpathspecに見える名前、`diff --git` を含む名前でテストしています。ファイルパスはGitの引数として渡しません（pathspecを使わないため、オプションやpathspecとして誤解釈される余地がありません）。
- **外部プロセスを起動しない**: `--no-ext-diff`、`--no-textconv`、`--no-color`、`--no-pager`、`-c core.fsmonitor=false`、`git log --no-show-signature`（gpgを起動しない）。シンボリックリンクをたどって本文を取得することもありません（シンボリックリンクはリンク先の文字列がblobとして差分に出ます）。
- **シェルを通さない**: Gitはコマンド名と引数を分けて `os/exec` に渡します。`sh -c` に文字列を渡すことはありません。ユーザー入力（`--commit`）の前には `--end-of-options` を置きます。JSONは `encoding/json` で生成します。
- **無限に待たない**: すべてのGit呼び出しに `context` のタイムアウトを付け、固まったGit（や子プロセスがパイプを握ったまま残るケース）でも戻ってくることをテストしています。
- **Gitの状態を変えない**: `init` が `commitcoach.repositoryId` をローカル設定に書く以外は、読み取り専用のコマンドだけを使い、`GIT_OPTIONAL_LOCKS=0`、`GIT_TERMINAL_PROMPT=0` を付けます。フックの中から `git commit` や `git add` を呼ぶことはありません。
- **保存**: 一時ファイルに書いて `fsync` してから `rename` するので、途中までのJSONは残りません。同時に実行されても各プロセスが別の一時ファイルを使うため、中身が混ざりません（最後の `rename` が勝つ）。保存先のファイル名は16進数のcommit IDであることを検証してから組み立てます。

## テスト

```sh
go test ./...
go vet ./...
go test -race ./...
```

今回の環境（Linux/WSL2、Go 1.26.3、Git 2.43.0）で、上の3つすべてが成功しています（トップレベルのテスト83件とサブテスト9件がすべてPASS、スキップ0件）。

テストはモックではなく、一時ディレクトリに実際のGitリポジトリを作って行います。repository_id にはテスト用のUUIDを使います。各テストバイナリは `TestMain` で次のように環境を隔離するので、利用者のグローバル設定（署名、`core.hooksPath`、属性ファイルなど）は混入しません。

- `HOME`・`XDG_CONFIG_HOME` を空の一時ディレクトリに、`GIT_CONFIG_GLOBAL` を空ファイルにし、`GIT_CONFIG_NOSYSTEM=1` を設定。それ以外の `GIT_*` 環境変数は削除
- リポジトリごとにローカルの `user.name`・`user.email` を設定し、`commit.gpgsign=false`
- 利用者のリポジトリにはcommitを作らない

`internal/cli` のE2Eテストは、**空白と引用符を含むディレクトリ**（`bin dir with 'single' and "double" quotes/`）にCLIをビルドし、そのバイナリで `init` してから実際に `git commit` を実行してフックを起動します。

| 観点 | 主なテスト |
|---|---|
| 出力形式 | トップレベルが6キーだけ（余分なキーがあれば失敗）、stdoutがJSON1個だけでログが混ざらない、JSON Schemaのプロパティ・必須キーと `Payload` 構造体の一致、`examples/` とテスト中に生成したすべてのJSONが Schema の pattern などの規則を満たす |
| files と diff | 複数ファイル（追加・変更・削除・リネーム・種別変更）が files と diff に正しく入る、順序と重複なし、リネームが1回だけ出る、日本語・空白・タブ・改行を含むパス |
| 比較 | 初回commit（空tree）、通常commit（第一親）、merge commit（第一親）、shallow cloneの境界をエラーにする、SHA-256リポジトリ |
| 対象commit | 過去のcommitの指定、部分commit（`git commit -- a.txt`）でステージ済み・未ステージ・未追跡の変更が混入しない、commit後の変更がexportに混入しない、`--amend` 後の新しいcommit |
| branch・空commit | detached HEADでnull、過去のcommitでも収集時点のブランチ、空commitで `files: []`・`diff: ""` |
| repository_id | フラグ指定・保存済み設定・未設定時のエラー、不正な値の拒否、フラグが保存済み設定より優先され設定ファイルを変えない、init がローカル設定だけに保存しグローバル設定を変えない、グローバル設定の値は使わない、IDがなければフックは警告してcommitは残る |
| 機密・バイナリ | `.env`・秘密鍵をリネームの両側で判定し、本文がJSONにも標準エラー出力にも出ない、files と diff の両方から外れる、`note:` 行が出る |
| 上限 | 100件はOKで101件は中止、64 KiB超のファイル、合計512 KiB超で、JSONを出さない（stdout空、フックは保存しない、既存の出力ファイルは不変）。内部では64 KiB・512 KiB・UTF-8の境界での切り詰めと早期停止も検証 |
| 異常系 | 存在しないcommit・tree・blob・`--help` などオプションに見える指定、Git管理外、空のリポジトリ、blob欠損によるGitの失敗、保存失敗（読み取り専用ディレクトリ）、タイムアウト（固まる偽の `git` を `PATH` に置く）、同時書き込み |
| 導入・解除 | 実際の `git commit` でフックが新形式のJSONを保存する、initを2回実行しても重複しない、他人のフック・シンボリックリンクを変更しない、ローカル・グローバルの `core.hooksPath` を変更しない（設定ファイルがバイト単位で不変）、uninstallが他人のファイルを消さない、編集されたフックを消さない、空白や引用符を含むバイナリパスでフックが起動する、バイナリを消してもcommitは残り `init` で復旧できる、bareリポジトリでinitを拒否、linked worktree・submodule・`--separate-git-dir` |

## ディレクトリ構成

```
cmd/commitcoach/main.go     エントリーポイント（cli.Run を呼ぶだけ）
internal/cli/               サブコマンド、入出力、終了コードの決定（ここだけが表示と終了コードを扱う）
internal/gitrepo/           Git実行（runner.go）、リポジトリ検出・設定（repo.go）、commit取得（commit.go）、差分取得（diff.go）
internal/event/             内部スナップショット（event.go, build.go）、出力する6項目への変換（payload.go）、上限値（limits.go）、機密ファイル判定（sensitive.go）
internal/repoid/            repository_id の検証・読み込み・保存
internal/hooks/             フックの生成・識別・導入・確認・解除
internal/storage/           JSONの保存（一時ファイル＋rename）
internal/testutil/          テスト用のGit環境の隔離と一時リポジトリ
../docs/commit-payload.schema.json   JSON Schema（リポジトリルート。バックエンド・webと共有する契約）
../examples/                実測から生成したサンプルJSON（リポジトリルート）
```

Goのモジュールパスは `github.com/kobadaidesu/hook-test` です（このリポジトリの `origin` に合わせています）。

## 将来Webバックエンドへ接続する箇所

差分取得とJSON生成は、出力先から独立しています。

- `event.Build` がcommitを読んで内部スナップショットを作り、`event.NewPayload` が6項目の `Payload` に変換し、`event.Marshal` がJSONにします。どれもファイルや標準出力に依存しないので、HTTP送信を追加してもGitまわりのコードは書き直す必要がありません。バックエンドへ送る本文は、いま保存・出力しているJSONそのものです。
- 送信を足す場所は `internal/cli/snapshot.go` の `recordHead`（フック）と `runExport` です。たとえば `internal/sender` のようなパッケージを作り、保存済みのJSONを送る形にできます。
- repository_id は今は手で設定しています。バックエンドのリポジトリ登録APIができたら、`init` でその結果を `commitcoach.repositoryId` に保存する形にできます（保存・読み込みは `internal/repoid`）。
- フックの中で同期的に送信すると、ネットワークの遅延がそのまま `git commit` の待ち時間になります。`.git/commitcoach/events/` を送信待ちキュー（outbox）とみなし、別プロセスや次回実行時に送る設計を推奨します。送信済みかどうかの管理は今回実装していません。

## 設計上の判断と理由

| 項目 | 内容 | 理由 |
|---|---|---|
| バイナリファイル | `files` と `diff` の両方から外す（`Binary files differ` だけのセクションも入れない） | `files` と `diff` が同じファイルを指すようにするため。本文のないファイルは問題生成に使えないため |
| 上限超過 | 一部分のJSONを出さず、エラーで中止する | JSONに「不完全」を示す項目がなく、部分的な差分が完全な差分に見えてしまうため |
| `files` の順序 | `diff` と同じ順（Gitの `diff-tree` のパス順） | `files[i]` と `diff` のセクションの並びを一致させ、順序を安定させるため |
| `diff` の中身 | `git diff-tree -p` の出力をそのまま使うので、`index ...` 行、リネーム時の `similarity index` 行、空白を含むパスの `+++` 行末のタブなども含まれる | Gitの出力を加工しない方が正確で、`git apply` などとの互換性も保てるため |
| repository_id の形式 | 8-4-4-4-12 の16進数だけ受け付け、小文字にそろえる。nil UUIDは拒否 | バックエンドのIDと確実に照合できるようにし、プレースホルダーを誤って使わないため |
| repository_id の保存先 | リポジトリローカルのGit設定 `commitcoach.repositoryId`。読み込みもローカルだけ | フックからも読めて、新しい設定機構を増やさずに済むため。グローバル設定に置くと全リポジトリが同じIDになってしまうため |
| 注意書きの出し先 | merge commitの比較方針、除外したファイル、UTF-8の置換などは標準エラー出力の `note:` 行に出す | JSONを6項目に限定するため |
| 保存先とメッセージ | 保存先 `.git/commitcoach/events/<commit ID>.json` と、フックのメッセージ（`recorded the snapshot ...`）は以前のまま | 導入済みのフックのスクリプトを変えると「編集された」と判定され、更新も解除もできなくなるため |
| Gitの最低バージョン | 2.31 | `rev-parse --path-format=absolute` で、どこから実行しても絶対パスを得るため |
| `.gitattributes` の読み取り元 | Git 2.40以上では対象commitから読む | 作業ツリーの未commitの変更で、バイナリ判定などの結果が変わらないようにするため |

## 既知の制限・注意点

- **フックが捕捉しない操作がある**: Git 2.43で実測したところ、`git merge` が自動で作るmerge commitでは `post-commit` は起動しませんでした（`post-merge` フックの管轄のため）。`git cherry-pick` と `git rebase` では起動しましたが、Gitのバージョンや操作によって変わり得るので保証しません。取りこぼしたcommitは `commitcoach export --commit <id>` で取得できます。
- **上限を超えるcommitはJSONになりません**。大きな生成ファイルや依存ファイルを含むcommitでは、フックが警告を出して保存を見送ります。
- **pushの制限はしません**。バックエンドへの送信もしません。今回はJSONを保存・出力するだけです。
- **repository_id は手で設定します**。値がバックエンドに実在するかは確認しません（形式だけを検証します）。
- **機密情報の除外はファイル名による判定だけ**です。コード中やcommitメッセージ中の秘密情報は記録されます（[機密ファイル](#機密ファイル)）。
- **JSONは自動では削除されません**。`--amend` やrebaseで古くなったcommitのJSONも残ります。`uninstall` しても残ります。
- **`core.hooksPath` を使う構成（husky、lefthookなど）へは自動で導入しません**。表示される1行を既存のフックに手で追加してください。
- partial clone（`--filter=blob:none` など）は未検証です。`GIT_NO_LAZY_FETCH=1` を付けていますが、この環境変数が効くのはGit 2.44以上で、それより古いGitでは欠けたオブジェクトを取得しにいく（ネットワークを使う）可能性があります。
- `branch` は収集時点のブランチです。過去のcommitをexportしたときは、そのcommitが属するブランチとは限りません。
- macOSは未検証、Windowsは未対応です（[動作環境と検証状況](#動作環境と検証状況)）。
