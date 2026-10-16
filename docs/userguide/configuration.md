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
| `repository.credentialRef` | GitHub の保存済みログインは `gh:active`、環境変数で指定する場合は `env:NAME`。secret 本体を書きません。 |
| `runtime.provider` | `copilot` または `opencode`。OpenCode は Linux の隔離実行に限定します。 |
| `runtime.account` | Copilot runtime に使う予定の account 識別子。OpenCode は `opencode-managed` とし、account 認証は未確認として記録します。 |
| `runtime.credentialRef` | Copilot CLI の保存済みログインは `copilot:logged-in`、環境変数で指定する場合は `env:NAME`。OpenCode は `opencode-managed` とし、他の credential を転用しません。secret 本体を書きません。 |
| `runtime.contractRef` | AI runtime の契約・利用権を指す記録上の参照名。実際の契約適格性は確認しません。 |
| `runtime.acpCommand` | OpenCode 専用。承認した OpenCode 1.19.0 実行ファイルの絶対パス。PATH や依頼ごとの `--cli` からは選びません。 |
| `runtime.acpCommandHash` | 実行ファイルの内容の `sha256:` hash。起動前に照合し、検査した内容の複製を起動します。更新時は新しい Task / Dispatch の設定承認が必要です。 |
| `runtime.acpConfig` | OpenCode 専用。操作者が準備した server 設定 JSON の絶対パス。初期対応では `model` field だけを受け付け、個人設定、認証情報、plugins を読み込みません。 |
| `runtime.acpConfigHash` | server 設定の内容の `sha256:` hash。設定を変更した場合は新しい設定承認が必要です。 |
| `execution.requester` | Task を依頼した人または process の識別子。 |
| `execution.worker` | 実行環境・worker の識別子。 |
| `execution.approvalRef` | Task に結び付ける設定承認の識別子。 |
| `execution.budgetGuidance` | 残予算の送信補足。`off`・`flow`・`account`。省略時は `flow`。依頼ごとの明示指定を優先する。 |

```yaml
version: 1
project:
  id: example-project
repository:
  provider: github
  host: github.com
  path: owner/repository
  account: scm-account
  credentialRef: gh:active
runtime:
  provider: copilot
  account: copilot-account
  credentialRef: copilot:logged-in
  contractRef: approved-contract
execution:
  requester: human
  worker: local
  approvalRef: task-approval
```

識別子は空白を含まない文字列です。`version` だけが整数です。repository provider は `github`、`gitlab`、`bitbucket`、runtime provider は `copilot` または `opencode` を指定します。`repository.host` は hostname と任意の port、`repository.path` は owner または namespace と repository 名の slash 区切りです。OpenCode 専用の4項目は Copilot 設定に混在させません。

`account`、`contractRef`、`requester`、`worker`、`approvalRef` は承認・記録上の参照値です。`credentialRef` は使用する credential の選択方法であり、secret 本体やログイン操作ではありません。Git 操作の主体と Copilot runtime の主体は別々に指定できます。設定だけでは契約適格性や費用負担者を証明できません。

### 認証の準備

Taxion を起動する環境に [GitHub CLI](https://cli.github.com/) と [Copilot CLI](https://docs.github.com/en/copilot/how-tos/set-up/install-copilot-cli) を導入し、それぞれのログインを用意します。次の操作は provider へ接続して認証情報を保存しますが、モデルへ依頼は送りません。保存先は各 CLI の案内で確認し、secret を設定ファイルや作業ログへ転記しないでください。

```sh
gh --version
copilot --version
gh auth login --hostname github.com
gh auth status --active --hostname github.com
copilot login
```

設定例の `repository.account` は GitHub CLI の active account、`runtime.account` は Copilot CLI にログインした account の login 名に置き換えます。両者は異なる account でも構いません。`repository.path` も実際の `owner/repository` に置き換えてください。`gh:active` は保存済み GitHub CLI ログイン、`copilot:logged-in` は保存済み Copilot CLI ログインを選び、Taxion の該当処理では ambient token の優先順位に任せません。

環境変数で credential を渡す運用では `env:NAME` を使い、`NAME` は大文字・数字・underscore の環境変数名にします。先頭は大文字か underscore が必要です。値は実行環境の secret 管理で用意し、表示・commit しません。旧設定の任意の参照名が自動的に解決されるとは限らないため、新規設定では上の明示方式を使います。

この準備後に `taxion config check --sources` と `taxion doctor --worktree .` へ進みます。ログインできても repository access、Task の設定承認、Copilot の契約適格性や費用負担者を保証するものではありません。Dispatch 発行後の `runtime auth` と各検査の範囲は、後述の「どの確認で何が分かるか」で確認してください。

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
| `TAXION_RUNTIME_ACP_COMMAND` | `runtime.acpCommand` |
| `TAXION_RUNTIME_ACP_COMMAND_HASH` | `runtime.acpCommandHash` |
| `TAXION_RUNTIME_ACP_CONFIG` | `runtime.acpConfig` |
| `TAXION_RUNTIME_ACP_CONFIG_HASH` | `runtime.acpConfigHash` |
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
taxion doctor --worktree .
taxion doctor --dispatch DISPATCH-ID --json
taxion runtime auth --dispatch DISPATCH-ID --system-prompt-file prompts/coder.md
```

`config check` は設定 schema と必須 field を検査します。`--sources` は値を表示せず各 field の採用元を示します。`--capabilities` は adapter の実装範囲を表示するだけで、credential、live access、契約適格性を確認しません。

`--task` は現在の設定が Task の記録済み承認に一致するか、`--dispatch` は Task 承認と発行済み Dispatch 指示の両方に一致するかを検査します。いずれも Human の身元や認証を証明しません。

`request` は設定の `execution.approvalRef` を変えず、`REF@task:PRIMARY_TASK_ID` という依頼ごとの設定承認 ID を生成します。同じ設定で次の依頼を始めるたびに設定ファイルを書き換える必要はありません。承認内容はその Task と明示した Reviewer Task だけを対象にし、他の依頼には使えません。従来の `approvalRef` と同名の decision も引き続き使えます。

Copilot の `runtime auth` は Dispatch の承認済み設定に対して、実効 account が一致するかを確認し、session を作成しません。CLI は `--system-prompt-file` も要求します。成功しても repository 操作主体、契約適格性、legacy credential reference の解決は未確認です。OpenCode の同じ操作は承認した実行ファイルと server 設定の内容を検査するだけで、provider 認証や quota を確認しません。設定確認や runtime auth の成功を実行許可の代わりにしないでください。

GitHub repository の実行前検査は、Taxion を起動した環境の PATH から `gh` を起動します。実行可能な `gh` が見つからない場合、見つかった CLI が起動できない場合、CLI 起動後の認証・access 問合せが失敗した場合は、それぞれ異なるエラーになります。起動失敗を認証失敗とみなしてログインを繰り返さず、まず同じ環境で `gh --version` を確認してください。API 問合せの失敗は認証・権限だけでなく通信等の障害も含み、秘密を含み得る provider の詳細は表示しません。これらの失敗ではモデル session を開始しません。`doctor` の native inventory 成功だけでは `gh` の起動や repository access を確認したことにはなりません。

固定 file を選べた Task 承認に Dispatch の設定をひも付けるときは `taxion config bind --dispatch DISPATCH-ID` を使います。これは認証、contract 適格性、runtime session binding を確認しません。

Copilot の `doctor` は選択中バックエンドの検索先と skills・plugins・指示等の検出結果を、
実行時と同じ worktree・CLI・ユーザー環境で表示します。Copilot の home は
`--base-directory`、`COPILOT_HOME`、`~/.copilot` の順に決まります。
個人用 `.agents/skills` は実行環境の HOME から探します。
`--dispatch` は保存済み worktree・設定・固定 tools policy を読取り専用で調べ、
Task や database を作成・移行しません。明示した `--tools` は runtime と同じ
role tool list です。省略時は file / custom-agent 呼出し tool の利用を推測しません。
無効化・形式不正・呼出し tool 不足を区別し、native inventory を取得できない場合は
確認不能を表示して失敗します。秘密値や MCP の引数・環境変数は一覧に転載しません。

**確認結果は認証や実行準備完了の証明ではありません。** `doctor` はモデル session、
hooks・MCP の起動、認証・課金照会を行いません。native inventory の表示と、
実行時のロード・server 接続の成功は別です。`ready` は設定を読まず、今の coding agent に操作手順を出力します。設定・探索の診断には `doctor` を使います。
CLI 全体の option と制約は[CLI リファレンス](cli-reference.md)を参照してください。

## OpenCode ACP の設定と制約

OpenCode を使う場合は Linux の `unshare`、`mount`、`chroot`、`setpriv`、
PATH 上の native `rg` と user namespace が必要です。
`rg` は OpenCode 内部のファイル検索と skill ロードに使います。Taxion はその実行ファイルを
隔離内へ複製して read-only mount にし、起動できなければモデル接続前に拒否します。
shim や shell script ではなく、隔離内でも動く native executable を PATH に置いてください。
Taxion は承認した OpenCode 1.19.0 の実行ファイルを
複製して隔離内で `opencode acp` を起動します。準備不足で Copilot に切り替えることはありません。
判断の背景は [ADR-0038](../develop/adr/0038-run-opencode-acp-with-sealed-role-boundaries.md)に記録しています。

操作者が OpenCode 側で用意した設定を使います。初期対応の server 設定は、たとえば
`{"model":"opencode/big-pickle"}` のように `model` だけを持つ JSON です。
Taxion は接続後の model を照合しますが、ACP のモデル選択操作は送りません。
現在は認証情報を隔離内に渡す設定がなく、host の OpenCode login や API key は引き継ぎません。
実受入れは匿名利用できる Big Pickle に限定しています。
モデルの価格・利用条件は操作者が確認し、無料という名称から外部送信の許可や
利用量の上限を推定しないでください。Big Pickle の無料期間は入力のモデル改善利用が
あり得るため、公開可能な文書など承認済み資料だけを渡します。

既存 YAML の `runtime` を次の形にします。他の project・repository・execution 設定も必要です。
hash は `sha256sum` による生の内容 hash で、`tools hash` の asset hash とは区別します。
実行ファイルや設定の更新後は、古い Dispatch を再利用せず、新しい設定を承認します。

```yaml
runtime:
  provider: opencode
  account: opencode-managed
  credentialRef: opencode-managed
  contractRef: operator-reviewed-model-terms
  acpCommand: /absolute/path/opencode
  acpCommandHash: sha256:<executable content hash>
  acpConfig: /absolute/private/opencode-server.json
  acpConfigHash: sha256:<configuration content hash>
```

この構成の `doctor --json` は実行ファイルと設定の照合、隔離方式と未対応操作を表示し、
process・session・モデルを起動しません。個人用 OpenCode の skills や plugins を
一覧・自動読込みする構成ではありません。skills は Task の tools 承認で指定した
directory と内容 hash だけを隔離内へ複製します。native skill の実ロードは別途確認します。

`request`・`workflow run`・`acp` は共通の Runtime 選択を使います。OpenCode では
`--max-ai-credits` を省略または `0` とし、明示 deadline と repair 上限を承認します。
非ゼロの Copilot credit ceiling、`account` guidance、`--cli`、`--base-directory`、
managed commit・SDK search は未対応のため拒否します。host 側の
`conversation search` による仕様添付と承認済み validation は使えます。
仕様の送信には `opencode` 用承認が必要で、Copilot の承認は流用しません。

workspace には承認 path の複製だけを渡し、host HOME・credential・Git metadata・shell は
渡しません。Reviewer は OS の read-only mount で保護します。未承認の native 操作は
permission request を `cancelled` にし、shell、MCP、実行型 plugin、RTK は実行しません。
skill 付随 script は、別途承認した `validationCommands` を host が実行します。
symlink、承認外の変更、host 側の同時変更を検出した場合は export を拒否します。
export の I/O 失敗では一部の変更が host に残る場合があり、エラーに示す隔離側の成果と
Git 状態を確認して復旧します。

取消通知の送信だけでは停止完了とせず、所有 runner が process の終了と部分成果の保存を
確認します。別 process の `runtime stop` や session 再接続は未対応です。
所有 runner への停止操作後に旧実行の終了・保存証拠を確認し、
新しい Task・設定・期限の承認で部分成果を引き継ぎます。旧 session ID を転用しません。
token の欠落・最新 message の部分値は Flow 全体の利用量にせず、期限と必須の
検証・独立レビューを維持します。

## 残予算を考慮する指示の設定

[ADR-0029](../develop/adr/0029-separate-budget-guidance-from-provider-quota.md) は、
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

方式の選択で予算や権限は増えません。`off` でも期限、repair 回数、契約・権限の
検査と安全停止は維持し、account 残量が取得できなくてもゼロや無制限と扱いません。
Copilot session の `max-ai-credits`、call ごとの利用量、context window、
provider account 全体の quota は別の値です。token 利用量は新しい Flow を停止しません。
quota 照会には runtime 用の選択済み credential を使う設計であり、
Git repository 用の token を流用しません。

Copilot SDK の account quota は resource ごとの requests・残り割合等を扱い、
session の累積 cost とは別です。公式 Billing API も期間別使用量の report で、
`netQuantity` を account 残量としてそのまま使えません。
組織・enterprise が課金するライセンスの使用量は個人 Billing endpoint に含まれません。
SDK 照会が失敗しても、管理者用 token や非公開 endpoint に自動で切り替えません。
一次資料との対応は [ADR-0029](../develop/adr/0029-separate-budget-guidance-from-provider-quota.md)を参照してください。

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
