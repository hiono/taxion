# 設定ガイド

Taxion の設定は project、Git repository、Copilot runtime、実行記録の識別子を選びます。設定ファイルに token や password を書かず、各 credential の保管と認証は Git／Copilot の実行環境で行います。

## 設定ファイル

Taxion は次の固定位置だけを読みます。

| 優先順位 | 場所 | 用途 |
| --- | --- | --- |
| 低 | `os.UserConfigDir()/taxion/config.yaml` | 利用者共通の値 |
| 中 | 作業ディレクトリの `.taxion/config.yaml` | project 固有の値 |
| 高 | 対応する `TAXION_*` 環境変数 | CI や実行環境ごとの値 |

Linux では Global 設定は通常 `$XDG_CONFIG_HOME/taxion/config.yaml`、未設定なら `~/.config/taxion/config.yaml` です。macOS と Windows は Go の `os.UserConfigDir()` が返す標準設定ディレクトリを使います。Project 設定は現在の作業ディレクトリ直下だけを読み、親ディレクトリは検索しません。少なくとも一つの YAML ファイルが必要です。

各 layer は `version: 1` を持ちます。上位 layer は空でない値だけを上書きし、省略した項目は下位 layer から引き継ぎます。各ファイルは merge 前に個別検証されるため、後の layer で上書きしても不正な値は隠せません。明示的な空文字、null、未知の field、重複 key、alias、sequence、複数 YAML document は拒否されます。

| Field | 意味・制約 |
| --- | --- |
| `version` | 必須の整数。現在は `1` のみ。 |
| `project.id` | project の識別子。 |
| `repository.provider` | `github`、`gitlab`、`bitbucket` のいずれか。provider ごとの実装範囲は異なります。 |
| `repository.host` | 接続先 hostname と任意の port。例: `github.com`。 |
| `repository.path` | owner または namespace と repository 名。例: `owner/repository`。 |
| `repository.account` | Git repository 操作に使う予定の account 識別子。認証済み account の証明ではありません。 |
| `repository.credentialRef` | repository credential の参照名。secret 本体ではなく、この値だけで認証は行いません。 |
| `runtime.provider` | `copilot` のみ。 |
| `runtime.account` | Copilot runtime に使う予定の account 識別子。 |
| `runtime.credentialRef` | runtime credential の参照名。secret 本体ではありません。 |
| `runtime.contractRef` | AI runtime の契約・利用権を指す記録上の参照名。実際の契約適格性は確認しません。 |
| `execution.requester` | Task を依頼した人または process の識別子。 |
| `execution.worker` | 実行環境・worker の識別子。 |
| `execution.approvalRef` | Task に結び付ける設定承認の識別子。 |

```yaml
version: 1
project:
  id: example-project
repository:
  provider: github
  host: github.com
  path: owner/repository
  account: scm-account
  credentialRef: scm-login
runtime:
  provider: copilot
  account: copilot-account
  credentialRef: copilot-login
  contractRef: approved-contract
execution:
  requester: human
  worker: local
  approvalRef: task-approval
```

識別子は空白を含まない文字列です。`version` だけが整数です。repository provider は `github`、`gitlab`、`bitbucket`、runtime provider は `copilot` を指定します。`repository.host` は hostname と任意の port、`repository.path` は owner または namespace と repository 名の slash 区切りです。

`account`、`credentialRef`、`contractRef`、`requester`、`worker`、`approvalRef` は承認・記録上の参照値であり、secret や認証そのものではありません。Git 操作の主体と Copilot runtime の主体は別々に指定できます。設定だけで契約適格性や費用負担者を証明することはできません。

## 環境変数

設定値の優先順位は環境変数、Project、Global の順です。Taxion は次の変数だけを設定 override として読みます。

| 環境変数 | 設定 field |
| --- | --- |
| `TAXION_PROJECT_ID` | `project.id` |
| `TAXION_REPOSITORY_PROVIDER` | `repository.provider` |
| `TAXION_REPOSITORY_HOST` | `repository.host` |
| `TAXION_REPOSITORY_PATH` | `repository.path` |
| `TAXION_REPOSITORY_ACCOUNT` | `repository.account` |
| `TAXION_REPOSITORY_CREDENTIAL_REF` | `repository.credentialRef` |
| `TAXION_RUNTIME_PROVIDER` | `runtime.provider` |
| `TAXION_RUNTIME_ACCOUNT` | `runtime.account` |
| `TAXION_RUNTIME_CREDENTIAL_REF` | `runtime.credentialRef` |
| `TAXION_RUNTIME_CONTRACT_REF` | `runtime.contractRef` |
| `TAXION_EXECUTION_REQUESTER` | `execution.requester` |
| `TAXION_EXECUTION_WORKER` | `execution.worker` |
| `TAXION_EXECUTION_APPROVAL_REF` | `execution.approvalRef` |
| `TAXION_EXECUTION_BUDGET_GUIDANCE` | `execution.budgetGuidance` |

設定値はすべて文字列です。未設定の変数は下位 layer の値を使い、明示的な空文字や不正値はエラーになります。`TAXION_TOKEN`、`GH_TOKEN`、`GITHUB_TOKEN`、`COPILOT_GITHUB_TOKEN` は設定 override ではなく、それぞれ別の認証用途で扱います。Taxion は `.env` を読みません。

## 設定を置くときの Git 制約

Git worktree 内の設定ファイルは未追跡で、かつ Git ignore 対象でなければなりません。作業ディレクトリの `.taxion/` 全体と、存在する `.env` も同じ条件で検査されます。通常は `.gitignore` に次を追加します。

```gitignore
/.taxion/
/.env
```

Git 管理下の共有設定例には secret を含めず、`.taxion/` の外に置きます。Ignore rule を追加しても、既に index にあるファイルは追跡対象のままです。設定を読み込む前に不正があれば、Taxion は値を読まずエラーを返します。

mise からローカル `.env` を読み込む場合は、共有 `mise.toml` に読み込み設定だけを書きます。`.env` 本体は未追跡・ignore 対象にしてください。

```toml
[env]
_.file = ".env"
```

## どの確認で何が分かるか

```sh
taxion config check
taxion config check --sources
taxion config check --capabilities
taxion config check --task TASK-ID
taxion config check --dispatch DISPATCH-ID
taxion runtime auth --dispatch DISPATCH-ID --system-prompt-file prompts/coder.md
```

`config check` は設定 schema と必須 field を検査します。`--sources` は値を表示せず各 field の採用元を示します。`--capabilities` は adapter の実装範囲を表示するだけで、credential、live access、契約適格性を確認しません。

`--task` は現在の設定が Task の記録済み承認に一致するか、`--dispatch` は Task 承認と発行済み Dispatch 指示の両方に一致するかを検査します。いずれも Human の身元や認証を証明しません。

`request` は設定の `execution.approvalRef` を変えず、`REF@task:PRIMARY_TASK_ID` という依頼ごとの設定承認 ID を生成します。同じ設定で次の依頼を始めるたびに設定ファイルを書き換える必要はありません。承認内容はその Task と明示した Reviewer Task だけを対象にし、他の依頼には使えません。従来の `approvalRef` と同名の decision も引き続き使えます。

`runtime auth` は Dispatch の承認済み設定に対して、実効 Copilot account が一致するかを確認し、session を作成しません。CLI は `--system-prompt-file` も要求します。成功しても repository 操作主体、契約適格性、legacy credential reference の解決は未確認です。設定確認や runtime auth の成功を実行許可の代わりにしないでください。

固定 file を選べた Task 承認に Dispatch の設定をひも付けるときは `taxion config bind --dispatch DISPATCH-ID` を使います。これは認証、contract 適格性、runtime session binding を確認しません。

**確認結果は認証や実行準備完了の証明ではありません。** `ready` / `doctor` コマンドは実装されていません。CLI 全体の option と制約は[CLI リファレンス](cli-reference.md)を参照してください。

## 残予算を考慮する指示の設定

[ADR-0029](adr/0029-separate-budget-guidance-from-provider-quota.md) は、
残量をモデルへ伝える方式を定義しています。`execution.budgetGuidance` に
`off` / `flow` / `account` を指定できます。環境変数は
`TAXION_EXECUTION_BUDGET_GUIDANCE`、依頼ごとの選択は
`request --budget-guidance MODE` です。不正値は実行前に拒否します。

Global / Project / 環境変数で既定値を選び、実行依頼の明示選択を
最後に適用します。指示ごとの未指定は既定値を継承し、すべて未指定なら
外部照会のない `flow` とします。`off` は補足指示なし、
`account` は明示承認した provider account の quota 読取りを追加する方式です。
方式・採用元・指示ごとの変更許可は承認と session identity に保持し、
既存 session に config 編集を遡及適用しません。
設定がない旧 Task / Dispatch は従来の無補足を維持し、新方式への継続には再承認を要求します。

方式の選択で予算や権限は増えません。`off` でも既存の停止判定は維持し、
account 残量が取得できなくてもゼロや無制限と扱いません。
Copilot session の `max-ai-credits`、Flow の `maxTokens`、
context window、provider account 全体の quota は別の値です。
quota 照会には runtime 用の選択済み credential を使う設計であり、
Git repository 用の token を流用しません。

Copilot SDK の account quota は resource ごとの requests・残り割合等を扱い、
session の累積 cost とは別です。公式 Billing API も期間別使用量の report で、
`netQuantity` を account 残量としてそのまま使えません。
組織・enterprise が課金するライセンスの使用量は個人 Billing endpoint に含まれません。
SDK 照会が失敗しても、管理者用 token や非公開 endpoint に自動で切り替えません。
一次資料との対応は [ADR-0029](adr/0029-separate-budget-guidance-from-provider-quota.md)を参照してください。

通常は `flow` のままで外部照会を避けられます。`account` を選ぶと、選択した
account の読取りが Task 承認に含まれます。新規の `task track` /
`conversation intake` も未指定値を設定から解決し、既存 Task 更新の省略値は
保存済み policy を維持します。明示した Task JSON の例は次のとおりです。

```json
{
  "budgetGuidance": {
    "mode": "account",
    "source": "request",
    "allowedModes": ["off", "flow", "account"],
    "accountReadApproved": true
  }
}
```

これは Task の部分例です。`runtime start|send --budget-guidance off` などで
一回の指示を変更できるのは、承認済み `allowedModes` の範囲内だけです。
`account` を含まない旧契約へ、この option で読取り権限を追加できません。
観測値と送信した補足は `records show` の `budget-guidance` receipt に残ります。
quota 照会は送信ごとに一回、5秒と実行残時間の短い方で打ち切り、retry しません。
取得不能・未対応・古い観測は理由付きで示し、account 不一致や既知の枠消尽は
新しいモデル指示を拒否します。追加課金枠は暗黙に使いません。
