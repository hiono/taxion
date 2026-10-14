# 利用者ガイド

このガイドは、Taxion で依頼を Task 契約にし、Human が範囲・設定・予算を承認してから Coder と独立 Reviewer を実行する流れを説明します。Task の field、状態遷移、budget stop 後の継続条件は[Task Graph リファレンス](10-task-graph.md)が所有します。

## 現在の導入方法

v0.1.0 tag と GitHub Release はまだ公開されていません。そのため、現時点では source checkout から実行します。Go 1.26.0 が必要です。

```sh
git clone https://github.com/hiono/taxion.git
cd taxion
mise install
go build -o ./bin/taxion ./cmd/taxion
export PATH="$PWD/bin:$PATH"
./bin/taxion --help
```

`go:` backend は Go の実行可能 package を `go install` で導入します。Taxion の `v0.1.0` tag が公開された後、[mise の Go backend](https://mise.jdx.dev/dev-tools/backends/go.html) に Go 1.26.0 と tag を指定します。

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@v0.1.0
mise exec -- taxion --help
```

## 設定と実行前確認

作業 repository の `.gitignore` に `/.taxion/` と `/.env` を加え、[設定ガイド](configuration.md)の Global または Project 設定を用意します。設定には secret を書かず、Git と Copilot の認証は各実行環境で設定します。

```sh
taxion config check --sources
taxion init
taxion --help
```

設定確認は schema と採用 layer を示すだけです。Task の承認と一致するかは `config check --task TASK-ID`、Dispatch の保存済み条件も含むかは `config check --dispatch DISPATCH-ID` で確認します。実際の Copilot account は Dispatch 発行後に `runtime auth` で確認します。これらの確認は scope や予算の Human 承認に代わりません。

### 文書用 skill を使う場合

Taxion は特定の skill を同梱したり、利用者の home から自動で選んだりしません。
使う skill の利用許可と内容を確認し、SDK 用の skill directory または skill-only
plugin directory を準備します。directory の内容は実行中に変更しません。

```sh
taxion tools hash --path /absolute/approved/skills
```

表示された `path` と `hash` を、対象 Task の出典付き decision に保存する
`taxion.tools.v1` 承認へ設定します。次は承認 JSON の形です。placeholder は実際の
値に置き換え、この JSON を decision の `text` に文字列として保存し、
`source` に Human の判断の出典を記録します。Task 契約の承認前に組み込んでください。

```json
{
  "kind": "taxion.tools.v1",
  "taskId": "TASK-ID",
  "actor": "human",
  "roles": {
    "CODER": {
      "skills": [{"path": "/absolute/approved/skills", "hash": "sha256:CONTENT-HASH"}],
      "evidenceDirectory": "/absolute/private/coder-tool-evidence"
    },
    "REVIEWER": {
      "skills": [{"path": "/absolute/approved/skills", "hash": "sha256:CONTENT-HASH"}],
      "evidenceDirectory": "/absolute/private/reviewer-tool-evidence"
    }
  }
}
```

役割ごとに必要な skill だけを指定できます。Reviewer に Coder 用 skill を渡す必要は
ありません。plugin を使う場合は `skills` の代わりに `plugins` へ、root に
`plugin.json` と `skills/` を持つ directory の `path` / `hash` を設定します。
詳細な許可・公開条件は [runtime 契約](03-runtime-contract.md#skillspluginsmcphooks-と出力圧縮)を参照してください。

承認済み asset があれば、Taxion が `skill` tool を追加します。通常の
`--coder-tools` / `--reviewer-tools` は引き続き必要なファイル操作を指定します。
skill の名前を role prompt に書くだけでは、未承認 skill は利用可能になりません。
`workflow run|step` は両 role の asset 欠落・改変をモデル実行前に検査します。

skill が参照する補助ファイルは、通常の worktree / fileAccess の許可内で読める必要が
あります。skill の承認だけでは、外部ファイルの `view` や shell 実行は許可されません。
付随検査は、承認した `validationCommands` で Controller に実行させるか、
操作者が明示した検査環境で実施して証拠を残します。必要な依存や入力を事前に
準備し、検査不能を品質合格に置き換えないでください。

実行後は、Taxion が保存した session ID に対応する SDK ログの skill 呼出しと
成功・失敗の完了結果を確認し、`evidenceDirectory` の原本と成果物の commit SHA
または tree SHA、付随検査、独立レビューを対応付けます。skill のロード成功だけで
文書を合格にせず、親の開発支援 session の起動ログも代用しません。
原本は private に保持し、公開記録には秘密値・私用パスを含めません。

## 依頼から独立レビューまで

Coder が作業する repository で、承認する baseline から専用 worktree を作ります。Taxion は通常の Coder Dispatch 用 worktree を自動作成しません。`branch`、`worktree`、`baselineSha` はこの worktree と同じ値を Task に記録します。

```sh
BASE_SHA=$(git rev-parse HEAD)
git worktree add -b work/task-example /absolute/path/to/task-worktree "$BASE_SHA"
```

### 依頼を未承認 Task として記録する

依頼内容、scope、禁止範囲、acceptance、証拠、検証 command を `request.json` にまとめて `conversation intake` を実行します。Task の `status` は `DRAFT`、`owner` は `CODER`、`reviewer` は独立した `REVIEWER` とし、authorization は入力しません。

```json
{
  "request": "実施したい変更の元の依頼文",
  "actor": "human",
  "source": "human:request-1",
  "task": {
    "id": "task-example",
    "title": "変更の短い説明",
    "repo": "owner/repository",
    "type": "IMPLEMENTATION",
    "issue": "NOT_REQUIRED: この局所作業では外部 Issue を作成しない",
    "createdFrom": "human:request-1",
    "parentId": "",
    "dependsOn": [],
    "owner": "CODER",
    "reviewer": "REVIEWER",
    "status": "DRAFT",
    "outputMode": "worktree-tree-snapshot",
    "objective": "完了状態を具体的に記す",
    "scope": ["src"],
    "outOfScope": ["docs"],
    "fileAccess": {"level": "committed"},
    "acceptance": ["変更が指定した振る舞いを満たす"],
    "requiredEvidence": ["test-report"],
    "validationCommands": ["go test ./..."],
    "branch": "work/task-example",
    "worktree": "/absolute/path/to/task-worktree",
    "baselineSha": "FULL_BASELINE_COMMIT_SHA",
    "exitCondition": "acceptance と evidence を確認する",
    "decisions": [],
    "memoryRefs": [],
    "exceptionIds": []
  }
}
```

```sh
taxion conversation intake --file request.json
taxion conversation proposal --task task-example
```

`fileAccess` は Task の承認対象になる SDK file permission です。省略時も `committed` が適用され、Dispatch 開始時点で Git 追跡済みのパスだけを対象にします。新規ファイルなど特定の相対パスを許可する場合は `{"level":"exact","paths":["src/new-file.go"]}`、割当 worktree 全体を許可する場合は `{"level":"all"}` を指定します。`all` でも worktree 外と `.git` は許可されません。Reviewer はどの level でも読み取り専用です。これは `scope` による変更範囲、`--coder-tools` / `--reviewer-tools` による SDK tool allowlist とは別の制御です。

`outputMode` は省略時に `commit` となり、従来どおり commit SHA で成果物を識別します。commit を作らずにレビューする場合は `worktree-tree-snapshot` を選びます。この場合、Controller が `outputTreeSha` と変更 path を算出し、baseline commit から Reviewer 用の別 worktree に同じ tree を再現します。Controller は tree object をローカルの `refs/taxion/snapshots/` に保持します。branch、commit、実 index、remote は変更しませんが、Git ref は追加されます。Git tree に含まれない ignored file は Reviewer に渡らないため、成果物や検証結果に使わないでください。

proposal の内容と hash を読み、目的・scope・file access・禁止範囲・acceptance・検証方法・repository・worktree が依頼に一致するか確認します。Taxion は依頼文から実行承認を推定しません。契約を直す場合は新しい Task ID で再提案します。

### 契約と設定を明示承認する

`conversation confirm` は proposal の完全な hash と、その Task に選ぶ設定を必要とします。Human は proposal の JSON と hash を確認してから `confirmation.json` を作ります。

```json
{
  "taskId": "task-example",
  "expectedHash": "sha256:EXACT_HASH_FROM_PROPOSAL",
  "actor": "human",
  "source": "human:approval-1",
  "reason": "確認した scope と acceptance に限って実行を許可する",
  "additionalTaskIds": ["task-example-flow-review-0", "task-example-flow-review-1"],
  "selection": {
    "version": 1,
    "project": {"id": "example-project"},
    "repository": {
      "provider": "github",
      "host": "github.com",
      "path": "owner/repository",
      "account": "scm-account",
      "credentialRef": "scm-login"
    },
    "runtime": {
      "provider": "copilot",
      "account": "copilot-account",
      "credentialRef": "copilot-login",
      "contractRef": "approved-contract"
    },
    "execution": {
      "requester": "human",
      "worker": "local",
      "approvalRef": "task-example-approval"
    }
  }
}
```

```sh
taxion conversation confirm --file confirmation.json
```

confirmation は Task を `READY` にしますが、Dispatch は発行しません。actor/source の文字列は保存されますが、CLI が Human の身元を認証するわけではありません。

`additionalTaskIds` は後述する Flow が作る Reviewer Task の設定承認です。この例の Flow ID は `task-example-flow`、修正上限は1回なので、初回レビューと再レビューの2件を列挙します。Flow ID または修正上限を変える場合は、この一覧も承認前に合わせてください。空の一覧では Coder だけが承認対象になり、独立 Reviewer の実行が拒否されます。`selection` は採用する設定ファイルの値と一致させます。

### Flow の上限を決めて開始する

`workflow start` は READY Task に対して Coder Dispatch を発行します。この時点で repair 回数、token ceiling、期限を明示し、別の Human 判断として Flow の予算を承認します。

```json
{
  "id": "task-example-flow",
  "taskId": "task-example",
  "repository": "owner/repository",
  "reviewRoot": "/absolute/path/to/reviews",
  "maxRepairs": 1,
  "maxTokens": 30000,
  "timeoutSeconds": 1800,
  "authorization": {
    "id": "task-example-flow-budget",
    "source": "human:budget-1",
    "text": "最大1回の修正を含む実行と独立レビューに、記載した上限で予算を承認する"
  }
}
```

```sh
taxion workflow start --file flow.json
taxion workflow show --id task-example-flow
taxion config check --dispatch DISPATCH-ID
taxion runtime auth --dispatch DISPATCH-ID --system-prompt-file prompts/coder.md
taxion workflow run --id task-example-flow \
  --coder-prompt-file prompts/coder.md \
  --reviewer-prompt-file prompts/reviewer.md \
  --max-ai-credits 30 \
  --coder-tools view,create,edit,apply_patch \
  --reviewer-tools view
taxion workflow show --id task-example-flow
```

`DISPATCH-ID` は start 結果の `dispatchIds[0]` で置き換えます。prompt file は各 role の system prompt です。`workflow run` に tool allowlist を渡さないと、Coder と Reviewer に SDK tool は渡りません。Coder には `view,create,edit,apply_patch`、読み取り専用の Reviewer には `view` を許可します。`workflow run` は Copilot runtime を実際に呼び出し、承認済み条件に従って Coder、独立 Reviewer、許可された修正を進めます。`--max-ai-credits` は per-session の soft ceiling です。

実行前に `prompts/coder.md` と `prompts/reviewer.md` をローカルファイルとして用意します。Coder には「保存済み Dispatch の scope と fileAccess の範囲だけを変更し、commit は作らない。変更 path と構造化結果を返す。検証コマンドは Controller が実行するため、未実行の検証を成功と報告しない」、Reviewer には「Controller が再現した target tree を読み取り専用で確認し、acceptance ごとの判断と根拠、必要な指摘を構造化結果で返す」と記します。Taxion が Dispatch の契約を各 prompt に追加します。結果の `outputTreeSha` はモデルではなく Controller が算出します。

この実行例は `worktree-tree-snapshot` 用です。commit mode を選ぶ場合、file tool だけでは commit を作れません。必要な secret scanner を用意し、[CLI リファレンス](cli-reference.md)の `--managed-commit` と `--managed-stage` を使うか、承認されたホスト操作で commit を作る手順を別に定めます。

## 結果の確認と停止後の継続

### 残予算の案内と現在の確認方法

現在は `workflow show --id FLOW-ID` で承認済み上限・利用量・未完了 role を確認します。
Copilot の `--max-ai-credits` は session 単位の soft ceiling であり、
アカウント全体の残 credit を表示する option ではありません。
Taxion は現在、account 残量を自動取得して Coder へ伝えてはいません。

Copilot SDK の account quota、session の累積 cost、公式 Billing API の期間別使用量は
別の情報です。requests の残り割合を AI Credit の絶対残量と読み替えたり、
個人向け report に組織負担の利用量がないことを「使用量ゼロ」と判断したりしないでください。
非公開 API や管理者の token は、通常の実行準備の必須条件にはしません。

今後の選択は [ADR-0029](adr/0029-separate-budget-guidance-from-provider-quota.md)で定義しています。
既定は、外部照会せず承認済み Flow の残りを指示に含める `flow`。
実行ごとに明示しなければ config の既定値を継承し、`off` でも停止条件は変わりません。
`account` は操作者が承認した provider の残量読取りを追加する方式で、
未対応・取得失敗・古い観測を理由付きで表示します。現在はこれらを指定する
config / CLI は未実装なので、既存の実行手順へ架空の option を追加しないでください。

残量が少なくても acceptance や独立レビューは省略しません。
途中の成果を保存して停止し、続行には新しい予算を承認する、という境界を維持します。

### 保存結果と停止後の継続

`workflow show` の status、stop code、利用量、保存結果、Git evidence、Reviewer verdict を確認します。`COMPLETED` や `APPROVE` だけで作業成果を判断せず、対象 SHA と acceptance evidence が依頼を満たすかを確認してください。

Flow が停止しても Dispatch に保存 session が残る場合は、別の runner が操作中でないことを確認してから `taxion runtime stop --dispatch DISPATCH-ID` で終了します。この操作に元の prompt や tool 設定は不要です。保存 session への停止専用接続は、新しい session やモデルへの指示を作らず、保留中の作業再開とすべての tool permission を拒否します。古い契約版でも停止できますが、接続・取消・結果保存に失敗した場合は停止済みとは記録しません。

停止後は `workflow show --id FLOW-ID` と `task graph` で `CANCELLED`、`runtime-stop:` 証拠、部分 Git 証拠を確認します。`records show` では元の suspension と cancellation の結果 receipt を確認できます。取消要求だけが成功し、保存が失敗する場合もあるため、コマンドを呼んだだけで停止確認を済ませず、確認前に同じ成果物を使った実行を重ねないでください。

token 上限停止では、停止済み Flow の上限を変更して再開することはできません。部分成果や完了済み Coder 結果から続ける場合は、同じ成果物を持つ別 worktree、後続 Task の再承認、新しい予算承認が必要です。commit mode は clean worktree、snapshot mode は保存 tree と同じ作業ファイルを要求します。`workflow recover` は残った runner 所有権だけを解放します。具体的な Git 手順と `continueFromDispatchId` の使い分けは[予算上限後の作業継続](10-task-graph.md#予算上限後の作業継続)を参照してください。

snapshot mode で完了済み Coder 結果を引き継ぐ場合は、保存済み `outputSha`（baseline commit）、`outputTreeSha`、変更 path を承認した別 worktreeを用意します。後続 Task の target tree が一致することを `conversation confirm` が検査し、`workflow start` には元の Coder Dispatch ID を `continueFromDispatchId` として指定します。新しい Flow は Coder を再実行せず Reviewer から始まります。停止時点が `CANCELLED` の部分成果なら、同じ baseline と保存 tree を持つ別 worktreeを用意し、後続 Task を承認したうえで Coder から続けます。
