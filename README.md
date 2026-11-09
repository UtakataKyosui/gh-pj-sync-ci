# gh-pj-synker

Issue／Pull Requestを検査し、条件を満たした項目だけをGitHub Projects（v2）に**追加**する`gh`拡張機能です。
同じコマンドをローカルでもCI（GitHub Actions）でも実行できます。個人・OrganizationのProjectに対応し、Projects（classic）は対象外です。

## インストール

```bash
gh extension install UtakataKyosui/gh-pj-synker
```

トークンにはProjectsの`project`スコープが必要です。ローカルでは次のコマンドで追加できます。

```bash
gh auth refresh --scopes project
```

## 設定ファイル

Projectの所有者と番号、既定のフィルタは、リポジトリにコミットする設定ファイル`.github/pj-synker.yml`に書けます。

```yaml
# .github/pj-synker.yml
version: 1
project:
  owner: acme
  number: 1
filters:
  labels: [triaged]
  exclude-labels: [blocked]
```

```bash
gh pj-synker init           # 対話的に作成（--owner と --project を渡せばプロンプトなし）
gh pj-synker config check   # 検証し、最終的に使われる値と既定値を表示する
gh pj-synker add 42         # --owner と --project は設定ファイルから補われる
```

- **優先順位**: コマンドラインのオプションは、常に設定ファイルより優先します。リストは置き換え、真偽値は`--no-drafts`のように上書きします。
- **壊れたファイル**: 未知のキー、型の誤り、YAMLの構文エラーは、行と列の位置付きのエラーになります。ないものとして扱って既定値で続行することはありません。
- **補完**: エディタの補完用に[JSON Schema](docs/pj-synker.schema.json)を用意しています。

探索順序やキーの一覧は[docs/config.md](docs/config.md)を参照してください。

## 使い方

```bash
# 1件を追加（設定ファイルがなければ --owner と --project を指定）
gh pj-synker add 42 --owner acme --project 1

# 条件に合う項目をまとめて確認（何も変更しない）
gh pj-synker add --all --owner acme --project 1 --label triaged --exclude-label blocked --dry-run

# 確認した内容で追加（--allは対話時に確認し、非対話時は--yesが必要）
gh pj-synker add --all --owner acme --project 1 --label triaged --exclude-label blocked --yes
```

出力例です。

```text
  Project         acme/1
  Repository      acme/web
  Source          git remote

✓ Added           acme/web#5
  Title           Fix the login redirect
  Item ID         PVTI_lADOBn2kc84A

  View the project:
    gh project view 1 --owner acme
```

すべてのオプションは`gh pj-synker add --help`で確認できます。

| オプション | 既定値 | 内容 |
| --- | --- | --- |
| `[<number>]...` | | 追加するIssue／PR番号。`--all`と同時には使えない |
| `--owner <LOGIN>` | 設定ファイル | Project所有者のUser／Organization（どちらかで必須） |
| `--project <NUMBER>` | 設定ファイル | ProjectのURL末尾の番号（どちらかで必須） |
| `--config <FILE>` | 自動探索 | 読む設定ファイル |
| `--no-config` | | 設定ファイルを読まない |
| `-R, --repo <REPOS>` | 現在のリポジトリ | `OWNER/REPO`。`--all`ではカンマ区切りで複数指定可 |
| `--all` | | リポジトリ内の全項目を検査する |
| `--type <TYPES>` | `issue,pull-request` | 対象の種類 |
| `--state <STATES>` | `open` | 対象の状態（`open`／`closed`） |
| `--label <LABELS>` | | すべて必要なラベル |
| `--exclude-label <LABELS>` | | いずれかがあれば除外するラベル（`--label`より優先） |
| `--author <LOGINS>` | すべて | 許可する作成者 |
| `--drafts` | `false` | Draft PRも追加する |
| `--bots` | `false` | Botが作成した項目も追加する |
| `--dry-run` | | 何を追加するかだけを表示し、変更しない |
| `-y, --yes` | | `--all`で見つかった項目の追加を承認する |
| `--json` | | 結果をJSONでstdoutに出力する |
| `--non-interactive` | | プロンプトを出さず、入力や確認が足りなければ失敗する |
| `--no-color` | | 色を付けない（`NO_COLOR`／`FORCE_COLOR`にも対応） |

リストはカンマ区切りです（`--label triaged,accepted`）。大文字小文字は区別しません。オプションを繰り返すと追記されます。

リポジトリは`--repo`、`GH_REPO`、設定ファイルの`repositories`、現在のgitチェックアウト、`GITHUB_REPOSITORY`の順で決まります。`--repo`以外から決まった場合は、出力の`Source`行に出所を表示します。値が不正な場合は、別の候補にフォールバックせずに失敗します。

## CIで使う

[`examples/sync.yml`](examples/sync.yml)をワークフローとして、[`examples/pj-synker.yml`](examples/pj-synker.yml)を`.github/pj-synker.yml`として配置します。

ワークフローは、デフォルトブランチの設定ファイルだけをsparse checkoutします。イベントの項目番号はワークフロー側で明示的に渡します。CLIはイベントペイロードを推測して読みません。

```yaml
- env:
    GH_TOKEN: ${{ secrets.PROJECT_SYNC_TOKEN }}
    NUMBER: ${{ github.event.issue.number || github.event.pull_request.number }}
  run: gh pj-synker add "$NUMBER"
```

CI（`CI`環境変数あり）では、自動的に非対話モードになります。stderrは色やグリフを使わない文章形式になり、stdoutには追加・既存の項目IDだけが1行ずつ出ます。結果を機械的に扱う場合は`--json`を使ってください。

### 認証と安全性

- `GH_TOKEN`にはProjectsへの書き込み権限が必要です。通常の`GITHUB_TOKEN`では足りません。
- Organizationでは、GitHub Appのinstallation token（OrganizationのProjects: read/write、対象リポジトリのIssues／Pull requests: read）を推奨します。個人のProjectではPATを使ってください。classic PATには`project`スコープ（非公開リポジトリには`repo`も）が必要です。
- 外部forkのPRには`pull_request`でSecretが渡りません。`pull_request_target`を使う場合は、**PRのコードをcheckout・実行しないでください**。このツールはAPIだけを呼び、本文やタイトルを実行しません。
- タイトルなどリモートのテキストは、制御文字、エスケープシーケンス、双方向制御文字を取り除いてから表示します。
- ワークフロー内では`--pin <tag>`で拡張機能の版を固定してください。

## 結果と終了コード

| 終了コード | 意味 |
| --- | --- |
| `0` | 完了。追加なし（条件外・既存）、空の結果、dry-run、ヘルプを含む |
| `1` | 操作の失敗（認証、権限、見つからない、APIエラー）、または一部の項目の失敗 |
| `1` | 設定ファイルが壊れている（`invalid_config`。APIを呼ぶ前に検出） |
| `2` | 使い方の誤り、または入力・確認の不足（ネットワークに触れる前に検出） |
| `130` | Ctrl+C、または確認プロンプトでの拒否 |

各項目の`outcome`は次のいずれかです。

- `added`: 追加した
- `unchanged`: すでにProjectにある
- `planned`: dry-runで追加予定
- `skipped`: 条件外
- `failed`: 失敗した

`--json`の出力は、stdoutに1つのJSON文書として出ます。TTYでは整形し、パイプでは1行で出します。

```json
{
  "project": { "owner": "acme", "number": 1, "id": "PVT_...", "title": "Roadmap", "url": "..." },
  "config": ".github/pj-synker.yml",
  "dryRun": false,
  "results": [
    { "repository": "acme/web", "number": 5, "type": "issue", "title": "...", "url": "...",
      "outcome": "added", "projectItemId": "PVTI_...", "reason": null, "message": null }
  ],
  "summary": { "added": 1, "unchanged": 0, "planned": 0, "skipped": 0, "failed": 0 },
  "next": [{ "command": "gh project view 1 --owner acme", "argv": ["gh", "project", "view", "1", "--owner", "acme"] }]
}
```

`skipped`の`reason`は次のいずれかの安定したコードです。

- `type_not_allowed`
- `state_not_allowed`
- `bot_author`
- `author_not_allowed`
- `draft`
- `excluded_label`
- `missing_label`

`--json`でエラーになった場合、stdoutには何も出さず、stderrに1行のJSONを出します（`status`、`reason`、`message`、`next`など）。`confirmation_required`のエラーには、`data.effects`（追加予定の項目）と、人の承認後に実行するコマンドが含まれます。

## 同期の範囲

- **追加のみ**です。Projectからの書き戻し、Status・カスタムフィールドの更新、削除、アーカイブはしません。
- 判定は、その時点のAPIの情報で行います。検査と追加は単一トランザクションではありません。
- 閉じたProjectには追加しません。追加はGitHub API上で冪等なので、再実行しても重複しません。

### GitHub ProjectsのWorkflowsとの併用

Projectの設定にある組み込みのWorkflowsとは、役割が重なる部分があります。このツールはAPIで項目を追加するだけで、Workflowsの設定を読み書きしません。

| Workflow | 併用時の挙動 |
| --- | --- |
| Auto-add to project | 追加条件が重なります。どちらが先に追加しても、もう一方は「すでにProjectにある」（`unchanged`）になるだけで、重複や失敗にはなりません。条件が違う場合、両方の和集合が追加されます。このツールの`filters`で絞っても、Workflow側の条件に合う項目は追加されます |
| Item added to project | 項目の追加を契機にStatusなどを設定するWorkflowは、このツールの追加でも動作する想定です。このツールはStatusを書かないため、値を取り合うことはありません。動作は自分のProjectで確認してください |
| Auto-archive items | アーカイブされた項目が条件に合い続けていると、このツールの再実行で再び追加されうるため、追加とアーカイブが繰り返される可能性があります。`--state`、`--label`、`--exclude-label`をアーカイブ条件と逆の関係に揃えてください |
| Item closed／Pull request merged など | Statusを変えるWorkflowです。このツールはStatusを更新しないため、競合しません |

追加条件はどちらか一方に寄せることを推奨します。このツールを使う場合は、Auto-add to projectを無効にしておくと、項目が入る理由を1か所で追えます。

## 設計方針

CLIの振る舞いは[Vercel CLI design](https://vercel.com/design/cli)のガイドラインに沿っています。

- **出力ストリーム**: stdoutには答えだけを出し、進捗、受領表示、エラーはstderrに出します。
- **終了コード**: 結果どおりに返します。
- **ヘルプ**: 認証やネットワークを使わずに表示します。
- **入力の扱い**: 対象を推測せず、不正な値でフォールバックしません。
- **設定の優先順位**: オプション、環境変数、設定ファイル、既定値の順です。壊れた設定ファイルは位置付きのエラーにします。
- **同意**: 推測で決まる一括追加（`--all`）は、確認を取ってから実行します。
- **表示モード**: TTY、パイプ、CI、`NO_COLOR`、`FORCE_COLOR`に応じて表示を切り替えます。

## 開発

```bash
make test     # 単体・golden・E2Eテスト（-race）
make lint     # go vet + golangci-lint
make golden   # 出力を意図的に変えたときにgoldenファイルを更新
make install  # ビルドしてローカルのghに拡張機能として入れる
```

テストは次の3層です。

- **単体テスト**: 判定ロジック、パーサー、設定ファイルの検証と探索、モード解決、APIエラーの対応表を検証します。
- **goldenテスト**: 人が読む出力（`internal/cli/testdata/*.golden`）を固定します。
- **E2Eテスト**: ビルドしたバイナリを偽のGitHub APIに対して実行し、終了コード、ストリーム、冪等性、SIGINTを検証します。

CIは、Linux、macOS、Windowsでの`-race`付きテスト、カバレッジ85%以上、golangci-lint、govulncheck、gh拡張としてのスモークテストを実行します。

`v*`タグをpushすると、テスト後に`cli/gh-extension-precompile`が各OS向けバイナリと証明書（attestation）をリリースに添付します。

## ライセンス

[LICENSE](LICENSE)を参照してください。
