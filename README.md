# gh-pj-sync-ci

Issue／Pull Requestを検査し、条件を満たした項目だけをGitHub Projects（v2）に追加する再利用可能なComposite Actionです。個人・OrganizationのProjectに対応します。Projects（classic）は対象外です。

## 利用例

呼び出し元リポジトリのワークフローに以下を記載します。`uses`の`<commit SHA>`は本リポジトリの利用するコミットに置き換えてください。呼び出し元のcheckoutは不要です。

```yaml
name: Sync eligible items
on:
  issues:
    types: [opened, reopened, labeled, unlabeled]
  pull_request_target:
    types: [opened, reopened, labeled, unlabeled, ready_for_review, converted_to_draft]

permissions:
  contents: read

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: UtakataKyosui/gh-pj-sync-ci@<commit SHA>
        id: project
        with:
          github-token: ${{ secrets.PROJECT_SYNC_TOKEN }}
          project-owner: your-organization
          project-number: '1'
          required-labels: 'triaged, accepted'
          excluded-labels: 'blocked, invalid'
          allowed-authors: 'trusted-maintainer, another-maintainer'
```

この例では、2つの必須ラベルが両方あり、除外ラベルがなく、許可された作成者によるopen項目のみを追加します。Draft PRとBot作成の項目は既定で除外します。検査条件はイベントペイロードではなく、呼び出し元リポジトリのAPIから取得します。ラベルは全ページを取得します。

### 認証と安全性

- `PROJECT_SYNC_TOKEN`をActionsのSecretとして登録してください。通常の`GITHUB_TOKEN`ではユーザー／OrganizationのProjectsへの書き込み権限が足りません。
- OrganizationのProjectでは、GitHub Appのinstallation token（OrganizationのProjects: read/write、対象リポジトリのIssues／Pull requests: read権限）を推奨します。個人のProjectなどでは、対象ProjectとリポジトリにアクセスできるPATを利用してください。classic PATには`project`スコープ、非公開リポジトリには`repo`スコープも必要です。OrganizationでSSOが必要な場合は承認してください。
- トークンの対象を必要なOrganization／リポジトリに限定してください。App tokenの生成は呼び出し元で行い、その出力を`github-token`へ渡します。
- 外部forkのPRでは`pull_request`ワークフローにSecretが渡されません。上記の`pull_request_target`を使う場合は、**PRのコードをcheckout・実行しないでください**。このActionはAPIだけを呼び出し、Issue／PRの本文・タイトルをスクリプトとして実行しません。
- 書き込みトークンを使うワークフローは信頼できる既定ブランチで管理してください。制限なく外部投稿をProjectへ追加しないよう、ラベルや作成者の許可条件を設定してください。
- 内部依存の`actions/github-script`はコミットSHAで固定しています。Node.js 24に対応するrunner（v2.327.1以降）が必要です。

## 入力

リスト入力はカンマまたは改行区切りで、前後の空白を除去し、大文字小文字を区別しません。カンマ／改行を含むラベル名は指定できません。真偽値は`'true'`または`'false'`のみです。不正な設定・APIエラーはActionを失敗させ、追加を行いません。

| 入力 | 既定値 | 内容 |
| --- | --- | --- |
| `github-token` | 必須 | Projectsへの書き込み権限を持つトークン |
| `project-owner` | 必須 | Project所有者のUser／Organizationログイン名 |
| `project-number` | 必須 | ProjectのURL末尾の番号（node IDではない） |
| `issue-number` | イベントの番号 | 呼び出し元リポジトリのIssue／PR番号。手動・定期実行では明示する |
| `allowed-repositories` | 呼び出し元リポジトリ | 許可する`owner/repo`のリスト。対象項目は常に呼び出し元リポジトリから取得 |
| `allowed-types` | `issue,pull_request` | 許可する種類 |
| `allowed-states` | `open` | 許可する状態（`open`／`closed`） |
| `required-labels` | 空 | すべて必要なラベル |
| `excluded-labels` | 空 | いずれかがあれば除外するラベル（必須条件より優先） |
| `allowed-authors` | 空 | 許可する項目作成者（空は制限なし、イベント実行者ではない） |
| `exclude-drafts` | `true` | Draft PRを除外 |
| `exclude-bots` | `true` | Botアカウント作成の項目を除外 |
| `dry-run` | `false` | 条件とProjectの参照権限だけを確認し、追加しない |

`workflow_dispatch`や`schedule`からも`issue-number`を指定して呼び出せます。項目のないイベントはスキップします。複数項目の一括探索は行わないため、必要なら呼び出し元でmatrixなどを組んでください。

## 出力と一方向同期の範囲

- `result`: `synced`／`skipped`／`dry-run`。
- `item-id`: 同期したProject項目のnode ID。それ以外は空。
- `reason`: 同期・スキップなどの理由。

閉じたProjectには追加しません。既存項目の再追加はGitHub APIが同じIDを返すため、再実行・同時実行で重複項目を作りません（`synced`は既存項目も含みます）。

このActionは**追加のみ**を行います。ProjectsからIssue／PRへ書き戻すことはなく、ProjectのStatus・カスタムフィールドも更新しません。追加後に条件を満たさなくなっても項目を削除・アーカイブしません。Statusの自動設定などはProjectsの組み込みワークフローを併用してください。API取得と追加の間に項目が変更される可能性があり、検査と追加は単一トランザクションではありません。

`dry-run`は書き込みを試さないため、書き込み権限の有無までは検証しません。初回は`dry-run: 'true'`で条件を確認してから実際の同期を有効にしてください。
