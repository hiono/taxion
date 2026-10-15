# 利用者ガイド

このガイドは、Taxion で依頼を Task 契約にし、Human が範囲・設定・予算を承認してから Coder と独立 Reviewer を実行する流れを説明します。Task の field、状態遷移、budget stop 後の継続条件は[Task Graph リファレンス](../develop/task-graph.md)が所有します。

## 現在の導入方法

v0.1.0 tag と GitHub Release はまだ公開されていません。そのため、現時点では source checkout から実行します。Go 1.26.0 が必要です。

```sh
git clone https://github.com/hiono/taxion.git
cd taxion
mise install
mise exec -- go build -o ./bin/taxion ./cmd/taxion
export PATH="$PWD/bin:$PATH"
./bin/taxion --help
```

`mise exec --` を使うため、shell への mise 有効化設定はビルドの前提ではありません。

`go:` backend は Go の実行可能 package を `go install` で導入します。Taxion の `v0.1.0` tag が公開された後、[mise の Go backend](https://mise.jdx.dev/dev-tools/backends/go.html) に Go 1.26.0 と tag を指定します。

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@v0.1.0
mise exec -- taxion --help
```

## 設定と実行前確認

作業 repository の `.gitignore` に `/.taxion/` と `/.env` を加え、[設定ガイド](configuration.md)の Global または Project 設定を用意します。先に同じ実行環境で GitHub CLI と Copilot CLI の導入・ログインを済ませ、設定例の account を実際に使う login 名へ置き換えます。[認証の準備](configuration.md#認証の準備)に手順と credential の選び方を示します。設定には secret を書きません。

```sh
taxion config check --sources
taxion doctor --worktree .
taxion init
taxion --help
```

通常は保存先を指定しません。Git 作業ツリールートの `.taxion/` を使うため、サブディレクトリから実行しても同じ状態を参照します。linked worktree はそれぞれのルートに別の `.taxion/` を持ちます。Git repository 外では、従来どおりカレントディレクトリの `.taxion/` を使います。設定ファイルの探索規則は[設定ガイド](configuration.md#設定ファイル)を参照してください。

隔離テストや以前の別配置を参照する場合だけ、command の前に `--store /path/to/workspace/ledger.json` を指定します。実際の SQLite は同じ directory の `workspace.db` に保存されますが、その DB 自体や同一ファイルの別名は指定できません。以前サブディレクトリへ作った状態は自動で移動・統合しないため、必要なら旧配置を明示します。

設定確認は schema と採用 layer を示すだけです。Task の承認と一致するかは `config check --task TASK-ID`、Dispatch の保存済み条件も含むかは `config check --dispatch DISPATCH-ID` で確認します。実際の Copilot account は Dispatch 発行後に `runtime auth` で確認します。これらの確認は scope や予算の Human 承認に代わりません。

### 文書用 skill を使う場合

通常は、設定したバックエンドが標準配置から skill・plugin・指示・custom agent を
探索します。Copilot 単体で使う skill を Taxion 用 directory へコピーしたり、
skill ごとの hash 承認 JSON を作ったりする必要はありません。

Copilot の skill 検索先は project の `.agents/skills`・`.github/skills`・
`.claude/skills` と、個人用の `~/.agents/skills`・`~/.copilot/skills` です。
追加登録した skill directory とインストール済み plugin も native な設定に従います。
`AGENTS.md` 等の指示や `.github/agents`・`~/.copilot/agents` も、
Copilot の探索規則に従います。選択していない Pi 等の固有 directory は探しません。
Taxion が特定の外部執筆 skill を固定で同梱するわけではありません。

```sh
taxion doctor
taxion doctor --json
```

`doctor` は実行と同じ CLI・worktree・Copilot home で native な一覧を取得し、
検索先、見つかった設定、無効化、同名 agent の優先順位、利用できない理由を表示します。
通常実行で `--cli`・`--base-directory` を指定する場合は doctor にも同じ値を渡します。
空の検索先と取得不能は区別されます。診断のためのモデル session 作成、
hook・MCP の起動、設定の書換えはしません。認証・実ロード・品質の成功は別途確認します。

通常実行はローカルの設定を信頼して読みます。file hooks は host のコマンドを実行し、
MCP や plugin はプロセス起動・外部接続を伴い得ます。初めて使う repository の設定は
実行前に内容を確認してください。これらの host 処理は SDK のファイル権限 callback の
sandbox ではありません。Coder / Reviewer に提供する通常 tool と成果物検証は維持します。
custom agent の検出と、その担当で呼出し tool が許可されていることも別です。

#### 内容を固定して監査する場合

ここからは任意の固定方式です。役割別の skill 内容、固定 MCP request、RTK と
原本 evidence を Task 契約へ固定するときだけ、既存の `taxion.tools.v1` を指定します。
この方式では通常の自動探索を無効にし、明示した asset だけを使います。
directory の内容は実行中に変更しません。

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
詳細な許可・公開条件は [runtime 契約](../develop/runtime-contract.md#skillspluginsmcphooks-と出力圧縮)を参照してください。

固定方式では承認済み asset があれば、Taxion が `skill` tool を追加します。通常の
`--coder-tools` / `--reviewer-tools` は引き続き必要なファイル操作を指定します。
固定方式で skill の名前を role prompt に書くだけでは、未承認 asset は利用可能になりません。
`workflow run|step` は両 role の asset 欠落・改変をモデル実行前に検査します。

skill が参照する補助ファイルは、通常の worktree / fileAccess の許可内で読める必要が
あります。skill の承認だけでは、外部ファイルの `view` や shell 実行は許可されません。
付随検査は、承認した `validationCommands` で Controller に実行させるか、
操作者が明示した検査環境で実施して証拠を残します。必要な依存や入力を事前に
準備し、検査不能を品質合格に置き換えないでください。

実行後は、Taxion が保存した session ID に対応する SDK ログの skill 呼出しと
成功・失敗の完了結果を確認し、固定方式では `evidenceDirectory` の原本と成果物の commit SHA
または tree SHA、付随検査、独立レビューを対応付けます。skill のロード成功だけで
文書を合格にせず、親の開発支援 session の起動ログも代用しません。
原本は private に保持し、公開記録には秘密値・私用パスを含めません。

## 今の coding agent に操作を任せる

対話している coding agent に、次のコマンドを実行し、出力を読んで依頼を進めるよう伝えます。設定や workspace の準備前でも実行できます。

```sh
taxion ready
```

`ready` は、その会話中の agent が Taxion を操作するためのメタプロンプトを stdout に出します。依頼の整理、設定・探索・保存状態の確認、Task の登録と承認、予算承認、Coder・検証・独立 Reviewer、停止後の成果再利用を案内します。必要な JSON は agent が作り、proposal の hash や結果を次の入力へ渡します。利用者が JSON や role prompt を転記する手順ではありません。

agent は目的、変更範囲、検証コマンド、設定選択、修正回数、token 上限と期限を利用者に示し、明示承認後に実行します。外側で済んだ相談を内部 Planner に繰り返させる必要はありません。承認済みの安全な次の操作がある限り続け、判断待ちや予算終了では理由を説明します。既存 Task や保存成果がある場合は、最初からやり直す前に照合します。

引数なしの `ready` はモデル、認証・課金照会、設定の読込み、workspace の作成を行いません。準備完了の診断ではなく、診断には `doctor` を使います。外側の agent の費用と、後続の `workflow run` などのモデル費用は別です。出力だけで agent の従属や継続を保証するものでもありません。無料の合成操作回帰と、実 agent が指示を理解して進める受入れは区別します。

別の会話でも操作知識を発見できるようにする場合は、次の明示的な配置を選べます。

```bash
taxion ready --install-skill
taxion ready --check-skill
# 更新内容を確認し、更新を許可した場合だけ実行する
taxion ready --update-skill
```

配置先は OS が解決した home の `.agents/skills/taxion/SKILL.md` です。home の解決には Windows の `%USERPROFILE%` も使います。ただし、現在の Taxion 本体は検証コマンドの停止処理に Unix 専用 API を含み、Windows 向けビルドは通りません。Windows 実機での配置・実行も未確認であり、このパス対応だけで Windows 対応済みとは扱いません。

配置に symlink は使わないため、その作成用の権限は不要です。既存パスの symlink は追跡せず拒否し、管理対象外・改変済みファイルは上書きしません。初回配置は既存ファイルを置換せず、更新時も一時ファイルから完成した一ファイルを入れ替えます。filesystem の書込み許可・hard link・rename の対応は必要です。

同梱 skill の版は `1.0.0`、CLI 操作契約は `taxion.cli.v1` です。本体は許容 skill 版を明示リストで保持し、未知の minor・major 版を推測で許可しません。skill の metadata にも必要な CLI 契約、ready 原稿と内容のハッシュ、生成した本体の build 情報を保存します。確認は、本体の許容版と skill の要求契約の一致、現在の ready 原稿との一致、配置内容の整合性を判定します。公開前の本体について、未確認の製品 SemVer 範囲を互換と宣言する代わりに、版を付けた CLI 操作契約を照合します。

`--check-skill` は JSON の `compatible`・`problem` と終了コードで判断します。不一致なら古い手順を使わず、明示的な更新の許可を得てください。改変を検出した場合は、利用者が内容を確認し退避・移動してから新規配置します。強制上書き option はありません。ハッシュは内容の整合性であり署名や承認ではありません。skill がロードされたことの証明にもならず、通常の CLI 操作を配置済み skill に依存させません。project の同名 skill などの優先順位は外側 agent の探索規則に従います。

## 人のことばで依頼して計画を相談する

設定と `taxion init` が済んでいれば、`request` で Planner に変更を相談できます。JSON や Coder / Reviewer の prompt ファイルを作る必要はありません。作業用の clean な Git worktree で実行します。repository は設定と一致する名前を明示します。

```sh
taxion request --repo owner/repository --max-tokens 40000 \
  --timeout 20m --max-ai-credits 30 --context-path README.md
```

最初に「この画面に検索機能を追加して」のように依頼を書きます。表示された repository・worktree・実行 account・上限を確認し、`/plan` で有料の Planner 対話を開始します。Planner の質問には人のことばで答え、提案が意図と違えば修正を伝えます。Planner は目的、変更範囲、完了条件、検証コマンドを提案します。質問が残っている間は実行できません。

計画と検証コマンドの副作用を確認し、`/approve` でコード作成、ホストでの検証、別 worktree での独立レビューを開始します。Task、承認 hash、Flow、Reviewer Task ID、role prompt、指示の受渡しは Taxion が準備します。終了時に結果、変更が残る worktree、Flow ID、記録の場所を表示します。commit、push、外部 Issue の作成は行いません。

この入口は snapshot mode と `fileAccess: all` を明示して使います。worktree 内の新規ファイルを許可し、Coder には `view,create,edit,apply_patch`、Reviewer には `view` を渡します。変更結果の scope 検査、worktree 外と保護された path の拒否、Reviewer の読み取り専用制約は維持します。細かい file access や外部 skill の承認が必要な場合は、次節の個別手順を使います。

token 上限は Planner の全対話と Coder / Reviewer で共有し、期限は `/plan` から数えます。`--max-ai-credits` は session ごとの soft ceiling で、依頼全体の金額上限や一 turn の hard cap ではありません。修正は既定で最大1回、`--max-repairs 0` なら再修正しません。Planner の各回答には費用が発生するため、同じ内容を繰り返し送らないようにします。Planner の失敗を自動 retry しません。

Planner はファイル・shell tool を持たず、依頼、これまでの対話、追跡済み path の一覧と `--context-path` で選んだ commit 内のテキストを受け取ります。元依頼の全文は保存した decision に一度だけ含め、対話の先頭はその出典を参照します。同じ Task を別の wrapper に重ねて送りません。path は directory ごとにまとめ、全件を保持したまま共通部分の繰り返しを減らします。通常は native な skill と指示も読み込みます。必要な source を渡す場合は option を繰り返します（最大16ファイル、計64 KiB）。未読のコードを調査済みとは扱いません。対話中の `/quit` または入力終了ではコードを実行せず、相談記録を残します。保存した相談の自動再開は未対応です。

相談記録と実行先・失敗の記録は request ID・連番・hash とともに workspace の SQLite DB に保存します。実行イベントは Flow から正式な Dispatch・receipt を参照し、二重保存しません。新しい `requests/` の JSON や `execution.log` は作りません。確認・明示 export は `taxion records show` を使います。この出力には相談本文が含まれるため、公開先への転記には注意してください。相談保存前に DB の file mode を所有者だけが読み書きできる設定にします。過去の外部ファイルは削除しません。

停止後の成果物と利用量は従来の Task / Flow / receipt に残ります。無料の合成 runtime と実 Git で依頼、確認質問、計画修正、承認、コード作成、同一 tree の独立レビューまで検証しています。実 Copilot モデルでこの入口を通した受入れは未確認です。別マシンへの移送には、通常の DB 単純コピーではなく、[一ファイルでの移送](#別マシンへの一ファイル移送)を使います。

## 依頼から独立レビューまで

以下は Task 契約や role 設定を個別に指定する手順です。通常の依頼では `ready` の出力を読んだ外側の agent に操作を任せるか、内部 Planner と相談する `request` を使います。

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
新しい依頼では、既定の `flow` が承認済み Flow の上限・観測済み tokens・
残り・期限・未完了 role を同じ指示へ補足します。追加のモデル呼出しはありません。

Copilot SDK の account quota、session の累積 cost、公式 Billing API の期間別使用量は
別の情報です。requests の残り割合を AI Credit の絶対残量と読み替えたり、
個人向け report に組織負担の利用量がないことを「使用量ゼロ」と判断したりしないでください。
非公開 API や管理者の token は、通常の実行準備の必須条件にはしません。

選択は [ADR-0029](../develop/adr/0029-separate-budget-guidance-from-provider-quota.md)で定義しています。
既定は、外部照会せず承認済み Flow の残りを指示に含める `flow`。
実行ごとに明示しなければ config の既定値を継承し、`off` でも停止条件は変わりません。
`account` は操作者が承認した provider の残量読取りを追加する方式で、
未対応・取得失敗・古い観測を理由付きで表示します。たとえば通常の `request`
コマンドに `--budget-guidance account` を加えると、表示される読取り範囲を
`/plan` で Planner に、`/approve` で実装・レビューに承認します。
未指定時は環境変数 `TAXION_EXECUTION_BUDGET_GUIDANCE`、設定ファイルの
`execution.budgetGuidance`、最後に `flow` の順で値を決めます
（設定ファイル内の優先順位は [設定ガイド](configuration.md)を参照）。
観測と実際の補足は `records show`、相談分は private planning JSON に残ります。
設定がない旧 Task / Dispatch へ補足を遡及適用しません。

残量が少なくても acceptance や独立レビューは省略しません。
途中の成果を保存して停止し、続行には新しい予算を承認する、という境界を維持します。

### 保存結果と停止後の継続

`workflow show` の status、stop code、利用量、保存結果、Git evidence、Reviewer verdict を確認します。`COMPLETED` や `APPROVE` だけで作業成果を判断せず、対象 SHA と acceptance evidence が依頼を満たすかを確認してください。

Flow が停止しても Dispatch に保存 session が残る場合は、別の runner が操作中でないことを確認してから `taxion runtime stop --dispatch DISPATCH-ID` で終了します。この操作に元の prompt や tool 設定は不要です。保存 session への停止専用接続は、新しい session やモデルへの指示を作らず、保留中の作業再開とすべての tool permission を拒否します。古い契約版でも停止できますが、接続・取消・結果保存に失敗した場合は停止済みとは記録しません。

停止後は `workflow show --id FLOW-ID` と `task graph` で `CANCELLED`、`runtime-stop:` 証拠、部分 Git 証拠を確認します。`records show` では元の suspension と cancellation の結果 receipt を確認できます。取消要求だけが成功し、保存が失敗する場合もあるため、コマンドを呼んだだけで停止確認を済ませず、確認前に同じ成果物を使った実行を重ねないでください。

token 上限停止では、停止済み Flow の上限を変更して再開することはできません。部分成果や完了済み Coder 結果から続ける場合は、同じ成果物を持つ別 worktree、後続 Task の再承認、新しい予算承認が必要です。commit mode は clean worktree、snapshot mode は保存 tree と同じ作業ファイルを要求します。`workflow recover` は残った runner 所有権だけを解放します。具体的な Git 手順と `continueFromDispatchId` の使い分けは[予算上限後の作業継続](../develop/task-graph.md#予算上限後の作業継続)を参照してください。

snapshot mode で完了済み Coder 結果を引き継ぐ場合は、保存済み `outputSha`（baseline commit）、`outputTreeSha`、変更 path を承認した別 worktreeを用意します。後続 Task の target tree が一致することを `conversation confirm` が検査し、`workflow start` には元の Coder Dispatch ID を `continueFromDispatchId` として指定します。新しい Flow は Coder を再実行せず Reviewer から始まります。停止時点が `CANCELLED` の部分成果なら、同じ baseline と保存 tree を持つ別 worktreeを用意し、後続 Task を承認したうえで Coder から続けます。

成功した Coder の後、Reviewer の実行中に予算上限へ達した場合も、Coder をやり直す必要はありません。元の Reviewer が `CANCELLED` で、対応する `runtime-stop:` 証拠が保存されていることを確認し、同じ Coder 成果物に対して後続 Task と新しいレビュー予算を承認します。新しい Reviewer の設定承認には、その後続 Flow の正確な Reviewer Task ID を含めます。元の Flow や取消結果は変更しません。

## 別マシンへの一ファイル移送

`workspace snapshot` は、停止済み workspace、存在する Memory、保存成果・部分成果に必要な Git object を、一つの非公開 SQLite ファイルへ収めます。通常の `workspace.db` をそのままコピーしても、Memory と Git は移りません。実行中の Flow・runner・未終了 Dispatch・未照合の外部送信がある場合は、先に上記の停止と照合を済ませてください。

移送元の Git 作業ツリー内で実行すると、そのルートの `.taxion/` が選ばれます。別の保存先を使っている場合だけ、command の前に `--store /path/to/.taxion/ledger.json` を指定します。DB ファイル自体を `--store` に渡さないでください。出力先の親 directory は事前に用意し、出力ファイルは存在しないパスを選びます。取得中は workspace と存在する Memory の書込みを予約するため、他の更新操作は並行して行いません。

```sh
taxion workspace snapshot --output /srv/backups/taxion-transfer.db
taxion workspace inspect-snapshot --file /srv/backups/taxion-transfer.db
```

出力の `hash` はファイル全体の SHA-256 です。移送先でも同じファイルを inspect し、確認した hash と承認元を指定します。`--output` は存在しない絶対 directory にします。例のパスは各マシンの保存場所に置き換えてください。

```sh
taxion workspace inspect-snapshot --file /srv/backups/taxion-transfer.db
taxion workspace restore --file /srv/backups/taxion-transfer.db \
  --output /srv/taxion-restored --expected-hash CONFIRMED-SHA256 \
  --actor human --source human:approved-machine-transfer
taxion --store /srv/taxion-restored/.taxion/ledger.json records show
```

inspect と restore は現在の workspace を移行先に使わず、`--file` と `--output` で指定した場所を扱います。復元先の親 directory も事前に用意してください。どちらもローカル Git と一時保存領域を使いますが、provider・ネットワーク・モデルには接続せず、保存された検証 command も実行しません。

成功後は `.taxion/` に正式状態と、移送元に存在した場合は Memory が入ります。Git 成果がある場合は `repositories/` に Git object、`worktrees/` に再生成した作業ファイルを配置します。`records show` の `restore.paths` は旧絶対パスと新しい配置の対応です。元の Task・Dispatch・承認・hash・利用量を変更せず、この対応を成果の再検証に使います。解放済み Reviewer の worktree は再作成しません。出力 JSON の `worktrees` は解放済みを含む保存配置数で、作成された directory 数とは限りません。

旧 SDK session は再開しません。移送先で認証と設定を用意し、対応表で見つけた source worktree と同じ Git repository 内に別 worktree を作り、保存済み commit/tree を再現します。その新しい配置、設定、予算を successor Task として承認し、上記の停止後の継続手順を使ってください。成功済み Coder の作り直しは不要です。移送後の操作には、上例の `--store` を付けて復元した正式状態を選びます。

snapshot は不足 object、観測した未記録変更、submodule、Git LFS、禁止されたローカル状態の path、既知の token・秘密鍵パターンを拒否します。親 commit の履歴も含めるため、現在の tree から削除済みの dotenv ファイルが履歴にある場合も拒否します。Memory の参照先も履歴分まで照合します。ignored file、外部 skill/MCP の実体、SDK home、環境変数、Git config・hook は移送しません。未知の秘密を完全に検出する保証はないため、移送ファイルを公開しないでください。

Memory には既存の256 MiB上限があります。archive 全体と Git object をメモリに読み込むため、必要なメモリと一時保存領域は移送データ量に応じて増えます。大規模 repository を一定量のメモリで順次処理する機能は未実装です。

復元中の directory はコマンド成功前に使わず、処理がエラーを返した場合は今回作成した先だけが除去されます。既存先は保持します。強制終了や電源断では途中の directory が残る場合があります。その配置を成功済みとして使わず、承認した archive を使って別の新規 directory へやり直してください。
