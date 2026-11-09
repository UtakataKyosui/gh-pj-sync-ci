# 設定ファイル

`gh pj-synker`の必須設定（Projectの所有者と番号）と既定のフィルタは、YAMLの設定ファイルに書けます。
リポジトリにコミットしておけば、ローカルでもCIでも同じ設定で動き、コマンドは`gh pj-synker add 42`だけで済みます。

```yaml
# .github/pj-synker.yml
# yaml-language-server: $schema=https://raw.githubusercontent.com/UtakataKyosui/gh-pj-synker/main/docs/pj-synker.schema.json
version: 1

project:
  owner: acme        # ProjectのUser／Organization（必須。--ownerでも可）
  number: 1          # ProjectのURL末尾の番号（必須。--projectでも可）

repositories:        # 省略時は現在のリポジトリ
  - acme/web

filters:
  types: [issue, pull-request]
  states: [open]
  labels: [triaged]          # すべて必要
  exclude-labels: [blocked]  # いずれかがあれば除外（labelsより優先）
  authors: []                # 空リストは「制限なし」
  drafts: false              # Draft PRを含めるか
  bots: false                # Botが作成した項目を含めるか
```

## 作成

`gh pj-synker init`で、gitチェックアウトのルートに`.github/pj-synker.yml`を作ります。

```bash
gh pj-synker init                                    # 端末では所有者と番号を質問する
gh pj-synker init --owner acme --project 1 --label triaged --exclude-label blocked
gh pj-synker init --owner acme --project 1 --dry-run # 書かずに内容をstdoutへ
```

- **プロンプト**: 端末では、足りない`--owner`と`--project`だけを質問します。所有者は、現在のリポジトリの所有者を既定値として示します。質問に答えて作った場合は、プロンプトなしで同じ結果になるコマンドを表示します。
- **非対話時**: CIなどでは質問せず、足りないオプションを示して終了コード2で止まります。
- **フィルタ**: `add`と同じオプション（`--label`、`--exclude-label`、`--repo`など）で指定します。指定しなかったフィルタは、コメントアウトした例として書き出します。
- **上書きの防止**: 既存のファイルは`--force`なしでは置き換えません。別名の設定ファイル（例：`.pj-synker.yml`）がすでにある場合も止まります。2つあると探索が曖昧になるためです。
- **書き込み**: 一時ファイルを経由してアトミックに書くので、途中で止まっても壊れたファイルは残りません。GitHubへの書き込みはしません。`--dry-run`以外では、Projectの[Workflows](../README.md#github-projectsのworkflowsとの併用)を読み取り、項目を自動で追加するものが有効なら警告します。
- **保存先**: `--path <FILE>`で変えられます。

## 確認

設定ファイルの内容と、最終的に使われる値は次のコマンドで確認できます。

```bash
gh pj-synker config check
```

`project.owner`と`project.number`があれば、`init`と同じ警告も出します。

## 探索順序

次の順で最初に見つかったものを使います。

1. `--config <FILE>`
2. 環境変数`GH_PJ_SYNKER_CONFIG`
3. カレントディレクトリからgitチェックアウトのルートまで、各ディレクトリで次のファイルを探します。
   1. `.github/pj-synker.yml`
   2. `.github/pj-synker.yaml`
   3. `.pj-synker.yml`
   4. `.pj-synker.yaml`

例外は次の通りです。

- gitチェックアウトの外では、カレントディレクトリだけを探します。チェックアウトの外にある無関係なファイルは読みません。
- 同じディレクトリに候補が複数ある場合は、推測せずにエラー（`ambiguous_config`、終了コード2）にします。
- `--config`や`GH_PJ_SYNKER_CONFIG`で指定したファイルが存在しない場合はエラーです。
- `--no-config`を付けると、設定ファイルを読みません。

## キー

| キー | 型 | 既定値 | 対応するオプション |
| --- | --- | --- | --- |
| `version` | 整数 | `1` | |
| `project.owner` | 文字列 | なし | `--owner` |
| `project.number` | 正の整数 | なし | `--project` |
| `repositories` | `OWNER/REPO`のリスト | 現在のリポジトリ | `--repo` |
| `filters.types` | `issue`／`pull-request`のリスト | 両方 | `--type` |
| `filters.states` | `open`／`closed`のリスト | `[open]` | `--state` |
| `filters.labels` | 文字列のリスト | 制限なし | `--label` |
| `filters.exclude-labels` | 文字列のリスト | なし | `--exclude-label` |
| `filters.authors` | ログイン名のリスト | 制限なし | `--author` |
| `filters.drafts` | 真偽値 | `false` | `--drafts`／`--no-drafts` |
| `filters.bots` | 真偽値 | `false` | `--bots`／`--no-bots` |

リストは必ずYAMLのリスト（`[a, b]`または`- a`）で書きます。ラベルと作成者は大文字小文字を区別しません。

## 優先順位

コマンドラインのオプションは、設定ファイルより常に優先します。

- **リスト**: オプションの値で置き換えます。設定ファイルの値とは結合しません。たとえば`--label ""`とすると、設定ファイルの`labels`を無効にできます。
- **真偽値**: `--drafts`／`--no-drafts`のように、どちらの向きにも上書きできます。
- **リポジトリ**: `--repo`、`GH_REPO`、設定ファイルの`repositories`、現在のgitチェックアウト、`GITHUB_REPOSITORY`の順で決まります。

設定ファイルを使ったときは、出力の`Config`行にそのパスを表示します。リポジトリが設定ファイルから決まった場合は、`Source`行にもパスを表示します。`--json`の出力では、`config`フィールドにパスが入ります。

## エラー

壊れた設定ファイルは常にエラーです（`invalid_config`、終了コード1）。ないものとして扱ったり、既定値で続行したりはしません。オプションで必須設定を渡していても、設定ファイルが壊れていればAPIを呼ぶ前に止まります。

検出する問題は次の通りです。

- YAMLの構文エラー
- 未知のキー（近いキーを提案します）
- 同じキーの重複
- 型の誤り（例：`drafts: yes`は真偽値ではありません）
- 不正な値（ログイン名、`OWNER/REPO`、種類、状態）
- 未対応の`version`

問題はすべてまとめて、行と列の位置付きで報告します。1件だけのときは、その箇所を示すコードフレームも表示します。

```text
✗ .github/pj-synker.yml:3:3 "project.ownr" isn't a setting. Did you mean "project.owner"?

    2 | project:
    3 |   ownr: acme
      |   ^

  Fix the file, then check it:
    gh pj-synker config check
```

`--json`では、`data.issues`に`line`、`column`、`message`の配列が入ります。

## GitHub Actionsで使う

設定ファイルを読むには、ワークフローでリポジトリをチェックアウトする必要があります。`pull_request_target`では、**ベースブランチの設定ファイルだけ**をチェックアウトしてください。PRのコードはチェックアウトしません。[`examples/sync.yml`](../examples/sync.yml)は、`sparse-checkout`で設定ファイル1つだけを取得する例です。
