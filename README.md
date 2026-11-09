# gh-pj-synker

Issue／Pull Requestを検査し、条件を満たした項目だけをGitHub Projects（v2）に**追加**する`gh`拡張機能です。
同じコマンドをローカルでもCI（GitHub Actions）でも実行できます。個人・OrganizationのProjectに対応し、Projects（classic）は対象外です。

## インストール

```bash
gh extension install UtakataKyosui/gh-pj-synker
```

## 使い方

```bash
# 1件を同期（dry-runで条件だけ確認）
gh pj-synker --project-owner my-org --project-number 1 --issue 42 --dry-run

# リポジトリのopenな項目をまとめて同期
gh pj-synker --project-owner my-org --project-number 1 --all \
  --required-labels 'triaged, accepted' --excluded-labels 'blocked'

# 複数リポジトリ
gh pj-synker --project-owner my-org --project-number 1 --all \
  --repo my-org/api --repo my-org/web --json
```

`--repo`を省略すると`GH_REPO`、`GITHUB_REPOSITORY`、カレントのgitリポジトリの順で決まります。
認証は`gh auth login`の資格情報、または`GH_TOKEN`／`GITHUB_TOKEN`を使います。

### フラグ

| フラグ | 既定値 | 内容 |
| --- | --- | --- |
| `--project-owner` | 必須 | Project所有者のUser／Organizationログイン名 |
| `--project-number` | 必須 | ProjectのURL末尾の番号（node IDではない） |
| `--repo` | 上記の順で解決 | 対象`owner/repo`。`--all`では複数指定可 |
| `--issue` | Actionsではイベントの番号 | 対象のIssue／PR番号 |
| `--all` | off | 対象リポジトリの許可された状態の全項目を検査 |
| `--types` | `issue,pull_request` | 許可する種類 |
| `--states` | `open` | 許可する状態（`open`／`closed`） |
| `--required-labels` | 空 | すべて必要なラベル |
| `--excluded-labels` | 空 | いずれかがあれば除外（必須条件より優先） |
| `--allowed-authors` | 空 | 許可する作成者（空は制限なし） |
| `--include-drafts` | off | Draft PRも追加（既定は除外） |
| `--include-bots` | off | Bot作成の項目も追加（既定は除外） |
| `--dry-run` | off | 条件とProjectの参照権限だけ確認し、追加しない |
| `--json` | off | 結果をJSONで出力 |

リスト値はカンマまたは改行区切りで、前後の空白を除去し大文字小文字を区別しません（フラグの繰り返しも可）。
不正な設定は終了コード2、APIエラーは1で終了し、その項目は追加しません。条件に合わず追加しなかった場合は正常終了（0）です。

出力は`synced`／`skipped`／`dry-run`と理由、`synced`ならProject項目のnode IDです。
GitHub Actionsでは`$GITHUB_OUTPUT`に`result`、`item-id`、`reason`を書き出します（最後の項目の結果）。

## CIで使う

[`examples/sync.yml`](examples/sync.yml)をワークフローとして配置します。`--issue`を省略するとイベントペイロードのIssue／PR番号を使います。
`workflow_dispatch`や`schedule`では`--issue`か`--all`を指定してください。

### 認証と安全性

- `GH_TOKEN`にはProjectsへの書き込み権限が必要です。通常の`GITHUB_TOKEN`では足りません。
- Organizationでは、GitHub Appのinstallation token（OrganizationのProjects: read/write、対象リポジトリのIssues／Pull requests: read）を推奨します。個人のProjectではPATを使い、classic PATには`project`スコープ（非公開リポジトリは`repo`も）が必要です。SSOが必要な組織では承認してください。
- トークンの対象は必要なOrganization／リポジトリに限定してください。
- 外部forkのPRには`pull_request`でSecretが渡りません。`pull_request_target`を使う場合は**PRのコードをcheckout・実行しないでください**。このツールはAPIだけを呼び、本文やタイトルを実行しません。
- 外部投稿が無制限にProjectへ入らないよう、ラベルや作成者の許可条件を設定してください。
- ワークフロー内の拡張機能は`--pin <tag>`で版を固定してください。

## 同期の範囲

- **追加のみ**行います。Projectからの書き戻し、Status・カスタムフィールドの更新、削除、アーカイブはしません。追加後に条件を満たさなくなっても削除しません。
- 判定はイベントペイロードではなく、その時点のAPIの情報で行います。検査と追加は単一トランザクションではありません。
- 閉じたProjectには追加しません。既存項目の再追加はGitHub APIが同じIDを返すため、再実行・同時実行でも重複しません（`synced`は既存項目を含みます）。
- `--dry-run`は書き込みを試さないため、書き込み権限までは検証しません。まず`--dry-run`で確認してから実行してください。

## 開発

```bash
go test ./...
go build -o gh-pj-synker . && gh extension install .
```

`v*`タグをpushすると、`cli/gh-extension-precompile`が各OS向けバイナリをリリースに添付します。

## ライセンス

[LICENSE](LICENSE)を参照してください。
