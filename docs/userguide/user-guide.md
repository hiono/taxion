# 利用者ガイド

このガイドは、Taxion で依頼を Task 契約にし、Human が範囲・設定・予算を承認してから Coder と独立 Reviewer を実行する流れを説明します。Task の field、状態遷移、budget stop 後の継続条件は[Task Graph リファレンス](../develop/task-graph.md)が所有します。

## 現在の導入方法

v0.3.0 tag は Go 1.26.0 で導入できます。各 version の変更内容は [GitHub Releases](https://github.com/hiono/taxion/releases) を参照してください。

このガイドは v0.3.0 の機能を説明しています。OpenCode ACP は v0.1.0 に含まれません。
初期設定・診断修復・補完を使う場合は v0.3.0 を導入してください。開発中の変更を試す場合は main のソースからビルドします。
公開 tag を導入しただけでは、後から追加された機能は使えません。

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@v0.3.0
mise exec -- taxion --help
```

最新版へ更新するには、`v0.3.0` を導入したい公開 version に置き換えます。

開発版では `taxion --help` の先頭に `Version:` を表示し、`taxion --version` でも同じビルド情報を確認できます。Go の module version を使うため、commit 固定の導入では `v0.3.1-0.…` のような完全な pseudo-version を表示します。Git 情報を持つソースビルドでは revision と、未コミット変更がある場合の `modified=true` も表示します。情報のないビルドを公開版と推測せず、取得できない旨を表示します。設定・DB・外部 CLI は読みません。公開済み v0.3.0 と初回設定補正版 `dd352cf` には、この版表示は含まれません。

```sh
mise use -g go@1.26.0 'go:github.com/hiono/taxion/cmd/taxion@<VERSION>'
```

削除するには、global mise 設定から Taxion を外します。

```sh
mise unuse -g go:github.com/hiono/taxion/cmd/taxion
```

Go backend の導入済み version が残り、他の設定でも使っていなければ、mise から個別に削除できます。

```sh
mise uninstall go:github.com/hiono/taxion/cmd/taxion@v0.3.0
```

ソースから実行する場合は Go 1.26.0 が必要です。

```sh
git clone https://github.com/hiono/taxion.git
cd taxion
mise install
mise exec -- go build -o ./bin/taxion ./cmd/taxion
export PATH="$PWD/bin:$PATH"
./bin/taxion --help
```

`mise exec --` を使うため、shell への mise 有効化設定はビルドの前提ではありません。

`go:` backend は Go の実行可能 package を `go install` で導入します。[mise の Go backend](https://mise.jdx.dev/dev-tools/backends/go.html) で Go と Taxion の version を選択できます。

## 設定と実行前確認

以下の `init`・`doctor fix`・bash 補完は v0.3.0 の操作です。v0.2.0 の `init` は DB だけを作るため、その版では[設定ガイド](configuration.md)の YAML と Git 除外設定を手動で用意します。

対象の Git repository で `taxion init` を実行します。既存の Global・Project 設定を読み、単一の Git remote から host と namespace/path を取得します。複数 remote は選択が必要です。GitLab Self-Managed など、host だけで provider を確定できない場合は provider も確認します。Git account と Copilot account は別々に指定します。

```sh
taxion init
taxion config check --sources
taxion doctor --worktree .
taxion --help
```

`init` は不足項目を尋ね、設定への追加と Git 除外規則を表示します。`yes` で確認すると、Project 設定と DB を準備します。有効な `.git/info/exclude` の規則があれば再利用し、不足時だけ `.gitignore` へ追記します。既存設定の値は変更せず、省略項目だけを補います。DB が作成済みでも、設定の準備は続行できます。設定ファイルの権限は新規作成・更新時に `0600` とします。

v0.3.0 の対話入力では、候補のない質問に空入力すると、最後に一般的な入力エラーで終了します。開発版では各項目の意味と選択肢を表示し、必須項目の空入力・不正値はその場で再入力を求めます。候補が表示された項目は Enter で候補を選べます。Ctrl-C または入力の終了で中止した場合は、適用前なので設定・Git 除外・DB を変更しません。

開発版では、設定がなくても `taxion doctor` が設定元、Git remote の候補、対応する runtime と PATH 上の実行ファイル、未設定・未確認の項目を表示します。設定不足は非zero終了となり、backend の詳細な一覧取得や Dispatch 検査には進みません。`doctor --json` では `setup` と `problem` を返します。壊れた設定や追跡済みの設定は、その問題を先に報告します。公開済み v0.3.0 の `doctor` は設定不足時にこの一覧を出しません。

`doctor` を先に実行する必要はありません。開発版の `init` と `doctor fix` は同じ検出処理を実行し、その時点の環境を表示してから不足項目を尋ねます。前回の診断を保存・再利用せず、Git remote や PATH が変われば取り直します。検出した候補、既存設定の採用元、入力待ち、適用する追加設定を分けて表示するため、実行ファイルが見つかっただけで backend の選択・認証・承認が済んだことにはなりません。

生成する YAML は今回不足した設定であり、全項目のひな型ではありません。Global・環境変数から継承した値は複写せず、任意の `execution.budgetGuidance` と OpenCode 専用項目の要否は別に案内します。全 field と候補値は[設定ガイド](configuration.md#設定ファイル)、保存後の採用元は `taxion config check --sources` で確認します。

`.taxion/config.yaml.bak` などの退避ファイルは自動で読み込みません。既存設定へ戻す場合は、退避内容をローカルで確認し、`config.yaml` が存在しないことを確認して復元します。設定を再作成する場合も DB や退避ファイルを削除する必要はありません。GitLab Self-Managed の provider は `gitlab`、runtime は利用する `copilot` または `opencode` を明示します。account はそれぞれのサービスの識別子、契約・承認参照は実際に確認した根拠の参照名です。token やパスワードは入力しません。

既存設定の不足を直す場合は `taxion doctor fix` を使います。`doctor` 単体は読取り専用です。任意配置の `[missing]` は空ディレクトリを作る指示ではありません。設定の妥当性・探索結果と、実際のログイン・接続・skill ロード成功を分けて確認してください。

非対話環境では、不足する参照値を `--set FIELD=VALUE` で渡します。次は GitLab と Copilot を別 account で使う例です。識別子を実環境の値に置き換え、`--dry-run` で確認後、同じ指定の末尾を `--yes` に替えて適用します。remote が複数あれば `--remote NAME` も指定します。秘密値は渡しません。

```sh
taxion init --dry-run \
  --set repository.provider=gitlab \
  --set repository.account=scm-user \
  --set runtime.provider=copilot \
  --set runtime.account=ai-user \
  --set runtime.contractRef=operator-contract \
  --set execution.approvalRef=operator-reference
```

不足が残る場合は必要な `--set` をまとめて表示し、ファイルを作成せず終了します。`project.id` は directory 名、`execution.requester` は `human`、`execution.worker` は `local` を候補として補います。必要なら `--set` で指定します。`contractRef` と `approvalRef` は記録用の参照名であり、その入力だけで契約資格や Task の承認は成立しません。初期設定ではモデルを呼ばず、ログイン、社内資料の送信許可、予算の承認も行いません。[認証の準備](configuration.md#認証の準備)は別に実施します。

通常は保存先を指定しません。設定も DB も Git 作業ツリールートの `.taxion/` を基準にするため、サブディレクトリから同じ設定と状態を参照します。linked worktree はそれぞれのルートに別の `.taxion/` を持ちます。Git repository 外ではカレントディレクトリを使います。旧版でサブディレクトリに作った設定を検出した場合は、黙って無視せず手動移行を求めます。上位の開発環境に置いた設定や DB は、自動で移動・統合しません。

旧サブディレクトリ設定の移行では、最初に原本と Git 除外状態を確認し、private な場所へ保全します。ルートの `.taxion/config.yaml` へ必要な設定を移します。既存設定があれば値を照合し、丸ごと上書きせず必要な項目だけを明示編集します。旧位置の設定を残すと、そのサブディレクトリからの起動は引き続き拒否されるため、保全先と内容を確認してから旧設定を退避します。ルートと元のサブディレクトリで `taxion config check --sources` を実行し、同じ採用元になることを確認してください。設定の移行と DB の移送は別の操作です。DB を削除して初期化し直す手順ではありません。

修復中の失敗は、既に保存した設定・除外規則と、未実施の DB 初期化を区別して報告します。内容を確認して再実行してください。`.taxion/setup.lock` が残った場合は、他の設定処理が動いていないことを確認してから、その lock だけを手動で除去します。DB や `.taxion/` 全体を削除する手順ではありません。

隔離テストや以前の別配置を参照する場合だけ、command の前に `--store /path/to/workspace/ledger.json` を指定します。実際の SQLite は同じ directory の `workspace.db` に保存されますが、その DB 自体や同一ファイルの別名は指定できません。以前サブディレクトリへ作った状態は自動で移動・統合しないため、必要なら旧配置を明示します。`--store` で Project 設定の基点は変わりません。

設定確認は schema と採用 layer を示すだけです。Task の承認と一致するかは `config check --task TASK-ID`、Dispatch の保存済み条件も含むかは `config check --dispatch DISPATCH-ID` で確認します。実際の Copilot account は Dispatch 発行後に `runtime auth` で確認します。これらの確認は scope や予算の Human 承認に代わりません。

### bash の補完

現在の bash で補完を有効にするには、次を実行します。

```bash
source <(taxion completion bash)
```

永続化する場合は、bash-completion と mise の初期化より後の `~/.bashrc` に次を加えます。Taxion は `.bashrc` を自動編集しません。新しい shell を開くか、この行を現在の shell でも実行してください。

```bash
command -v taxion >/dev/null 2>&1 && source <(taxion completion bash)
```

コマンド・サブコマンド・option を Tab で補完します。設定・DB・ログインは不要で、候補の取得でもモデルや外部 API を呼びません。値や Task ID の動的補完、zsh・fish は未対応です。

### OpenCode ACP を選ぶ場合

Linux では `runtime.provider: opencode` を選び、承認した OpenCode 1.19.0 の executable と
server 設定を使えます。[OpenCode の設定と制約](configuration.md#opencode-acp-の設定と制約)で
必要な隔離環境、内容 hash、未対応操作を確認してください。現在は認証情報を注入せず、
実受入れは匿名の Big Pickle に限定しています。`doctor` と `runtime auth` は local asset の
照合だけで、認証・quota・無料利用の保証ではありません。

以降の Copilot 向け例にある非ゼロ `--max-ai-credits` や CLI/home override は OpenCode に
流用しません。明示 deadline と repair 上限を承認し、skills は内容固定方式で指定します。
取消は所有 runner で process 終了と部分成果の保存まで確認し、別 process の session attach は
行いません。停止後は旧記録を保持し、新しい Task・設定・期限を承認して成果を引き継ぎます。

### 文書用 skill を使う場合

Copilot の通常構成では、バックエンドが標準配置から skill・plugin・指示・custom agent を
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

Copilot の固定方式では、skill が参照する補助ファイルも、通常の worktree / fileAccess の
許可内で読める必要があります。skill の承認だけでは外部ファイルの `view` は許可されません。
OpenCode では、file read が許可された role に限り、承認済み skill の補助資料を
`/config/opencode/skills/<skill-name>/` から読めます。host の元の path ではありません。
この directory は OS の read-only mount で保護し、未承認の外部 directory は拒否します。
どちらの方式でも、skill の承認だけでは shell 実行は許可されません。
付随検査は、承認した `validationCommands` で Controller に実行させるか、
操作者が明示した検査環境で実施して証拠を残します。必要な依存や入力を事前に
準備し、検査不能を品質合格に置き換えないでください。

実行後は、Taxion が保存した session ID に対応する SDK ログまたは ACP execution event の skill 呼出しと
成功・失敗の完了結果を確認し、Copilot の固定方式では `evidenceDirectory` の原本と成果物の commit SHA
または tree SHA、付随検査、独立レビューを対応付けます。skill のロード成功だけで
文書を合格にせず、親の開発支援 session の起動ログも代用しません。
原本は private に保持し、公開記録には秘密値・私用パスを含めません。
OpenCode の tool 原文は workspace の receipt に `acp_tool_update` として保存し、
共通形式の実行証拠と同じ call ID で照合します。Copilot 用の原本 directory に記録したとは扱いません。

## 今の coding agent に操作を任せる

対話している coding agent に、次のコマンドを実行し、出力を読んで依頼を進めるよう伝えます。設定や workspace の準備前でも実行できます。

```sh
taxion ready
```

`ready` は、その会話中の agent が Taxion を操作するためのメタプロンプトを stdout に出します。依頼の整理、設定・探索・保存状態の確認、Task の登録と承認、予算承認、Coder・検証・独立 Reviewer、停止後の成果再利用を案内します。必要な JSON は agent が作り、proposal の hash や結果を次の入力へ渡します。利用者が JSON や role prompt を転記する手順ではありません。

操作を探すときは、`taxion --help` の初期設定、計画、実行、記録、復旧などの分類を選び、`taxion COMMAND --help` で option を確認します。分類名はサブコマンドではありません。`ready` は最初に状況と操作・承認境界の対応を示し、その後に具体的な手順と JSON 例を置きます。全コマンドを順番に実行する必要はありません。

agent は目的、変更範囲、検証コマンド、設定選択、修正回数、期限、session soft ceiling を利用者に示し、明示承認後に実行します。各 model call の観測利用量も報告しますが、予測不能な token 数で承認済み目標を止めません。承認済みの安全な次の操作がある限り続け、判断待ちや期限到達では理由を説明します。既存 Task や保存成果がある場合は、最初からやり直す前に照合します。

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

同梱 skill の版は `1.1.0`、CLI 操作契約は `taxion.cli.v1` です。本体は許容 skill 版を明示リストで保持し、未知の minor・major 版を推測で許可しません。skill の metadata にも必要な CLI 契約、ready 原稿と内容のハッシュ、生成した本体の build 情報を保存します。確認は、本体の許容版と skill の要求契約の一致、現在の ready 原稿との一致、配置内容の整合性を判定します。公開前の本体について、未確認の製品 SemVer 範囲を互換と宣言する代わりに、版を付けた CLI 操作契約を照合します。

v0.2.0 から更新した場合、配置済み skill `1.0.0` はそのまま使いません。`taxion ready --check-skill` で不一致を確認し、利用者の許可後に `taxion ready --update-skill` を実行します。既存 skill の自動更新は行わず、改変済みファイルは明示更新でも保全します。skill を配置していない場合は、引数なしの `taxion ready` を直接使えます。

`--check-skill` は JSON の `compatible`・`problem` と終了コードで判断します。不一致なら古い手順を使わず、明示的な更新の許可を得てください。改変を検出した場合は、利用者が内容を確認し退避・移動してから新規配置します。強制上書き option はありません。ハッシュは内容の整合性であり署名や承認ではありません。skill がロードされたことの証明にもならず、通常の CLI 操作を配置済み skill に依存させません。project の同名 skill などの優先順位は外側 agent の探索規則に従います。

## 人のことばで依頼して計画を相談する

設定と `taxion init` が済んでいれば、`request` で Planner に変更を相談できます。JSON や Coder / Reviewer の prompt ファイルを作る必要はありません。作業用の clean な Git worktree で実行します。repository は設定と一致する名前を明示します。

```sh
taxion request --repo owner/repository \
  --timeout 20m --max-ai-credits 30 --context-path README.md
```

最初に「この画面に検索機能を追加して」のように依頼を書きます。表示された repository・worktree・実行 account・上限を確認し、`/plan` で有料の Planner 対話を開始します。Planner の質問には人のことばで答え、提案が意図と違えば修正を伝えます。Planner は目的、変更範囲、完了条件、検証コマンドを提案します。質問が残っている間は実行できません。

計画と検証コマンドの副作用を確認し、`/approve` でコード作成、ホストでの検証、別 worktree での独立レビューを開始します。Task、承認 hash、Flow、Reviewer Task ID、role prompt、指示の受渡しは Taxion が準備します。終了時に結果、変更が残る worktree、Flow ID、記録の場所を表示します。commit、push、外部 Issue の作成は行いません。

この入口は snapshot mode と `fileAccess: all` を明示して使います。worktree 内の新規ファイルを許可し、Coder には `view,create,edit,apply_patch`、Reviewer には `view` を渡します。変更結果の scope 検査、worktree 外と保護された path の拒否、Reviewer の読み取り専用制約は維持します。細かい file access や外部 skill の承認が必要な場合は、次節の個別手順を使います。

期限は `/plan` から数えます。`--max-ai-credits` は session ごとの soft ceiling で、依頼全体の金額上限や一 turn の hard cap ではありません。Planner と Coder / Reviewer の利用量は各 model call ごとに記録します。account 残高・利用率は、同じ単位の allowance を取得できない限り unavailable と表示します。修正は既定で最大1回、`--max-repairs 0` なら再修正しません。Planner の各回答には費用が発生するため、同じ内容を繰り返し送らないようにします。Planner の失敗を自動 retry しません。

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
      "credentialRef": "gh:active"
    },
    "runtime": {
      "provider": "copilot",
      "account": "copilot-account",
      "credentialRef": "copilot:logged-in",
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

`workflow start` は READY Task に対して Coder Dispatch を発行します。この時点で repair 回数と期限を明示し、別の Human 判断として Flow を承認します。token 閾値で Flow は停止しません。

```json
{
  "id": "task-example-flow",
  "taskId": "task-example",
  "repository": "owner/repository",
  "reviewRoot": "/absolute/path/to/reviews",
  "maxRepairs": 1,
  "timeoutSeconds": 1800,
  "authorization": {
    "id": "task-example-flow-budget",
    "source": "human:budget-1",
    "text": "最大1回の修正を含む実行と独立レビューを、記載した期限内で許可する"
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

現在は `workflow show --id FLOW-ID` で承認済み期限・利用量・未完了 role を確認します。
Copilot の `--max-ai-credits` は session 単位の soft ceiling であり、
アカウント全体の残 credit を表示する option ではありません。
各 model call で取得できた nano-AIU と AI-credit status を表示します。
account 残高、使用率、残り率は、同じ単位の allowance を確認できない場合は unavailable です。
既定の `flow` は承認済み期限と未完了 role 等を指示へ補足し、追加のモデル呼出しはしません。

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
観測と実際の補足は、相談分を含めて workspace の SQLite に保存し、`records show` で確認します。
設定がない旧 Task / Dispatch へ補足を遡及適用しません。

残量が少なくても acceptance や独立レビューは省略しません。
途中の成果を保存して停止し、続行には新しい予算を承認する、という境界を維持します。

### 保存結果と停止後の継続

`workflow show` の status、stop code、利用量、保存結果、Git evidence、Reviewer verdict を確認します。`COMPLETED` や `APPROVE` だけで作業成果を判断せず、対象 SHA と acceptance evidence が依頼を満たすかを確認してください。

Copilot で Flow が停止しても Dispatch に保存 session が残る場合は、別の runner が操作中でないことを確認してから `taxion runtime stop --dispatch DISPATCH-ID` で終了します。この操作に元の prompt や tool 設定は不要です。保存 session への停止専用接続は、新しい session やモデルへの指示を作らず、保留中の作業再開とすべての tool permission を拒否します。古い契約版でも停止できますが、接続・取消・結果保存に失敗した場合は停止済みとは記録しません。

OpenCode は別 process の `runtime stop` で保存 session へ接続できません。所有 runner で停止し、process の終了と部分成果の保存を確認します。`CANCEL_REQUESTED` のままなら取消完了とは扱わず、後続 Task の承認・実行を重ねません。詳しくは[OpenCode の設定と制約](configuration.md#opencode-acp-の設定と制約)を参照してください。

無料価格のモデルでも、rate limit により待機・失敗する場合があります。ACP 接続後に tool・assistant の出力がないことだけでは、原因や利用量を確定できません。操作者が OpenCode 側の診断で `Rate limit exceeded` を確認した場合は、同時実行や無条件の再送を重ねず、提供側の再試行案内に従います。承認済み期限で停止した場合も正式な取消と runner 解放を確認し、未完了のレビューを成功にはしません。別モデル・認証情報・接続方式への変更を、無料という理由だけで承認済みとは扱いません。

OpenCode 1.19.0 の実受入れでは、モデルが `read` に `path` を渡して失敗し、`filePath` へ直すと読取りに成功しました。`edit` の `oldString` に read 出力の行番号を含めた場合も、実ファイルの本文と一致せず編集に失敗しました。後続 Task の指示では、その版の native tool schema に合わせた引数名を使い、行番号を本文へ含めないよう明示します。tool の失敗は編集済み・検証済みの根拠にはしません。

現在の隔離 agent には、Flow の期限とは別に12ステップの上限があります。実受入れでは、tool の失敗を重ねてこの上限に達し、モデルが JSON ではない作業要約を返したため Dispatch は `FAILED` になりました。要約を操作者が成功 JSON に書き換えず、元の結果を保持します。続行する場合は、保存成果と未完了作業を照合して後続 Task を新たに承認します。成功済み Coder の成果をレビューするときは、Reviewer に編集のやり直しを指示せず、その成果物の検査に範囲を限定します。

停止後は `workflow show --id FLOW-ID` と `task graph` で `CANCELLED`、`runtime-stop:` 証拠、部分 Git 証拠を確認します。`records show` では元の suspension と cancellation の結果 receipt を確認できます。取消要求だけが成功し、保存が失敗する場合もあるため、コマンドを呼んだだけで停止確認を済ませず、確認前に同じ成果物を使った実行を重ねないでください。

新しい Flow は token 閾値で停止しません。既存 workspace に残る旧 `BUDGET_LIMIT` の停止記録は変更せず、部分成果や完了済み Coder 結果から続ける場合は、同じ成果物を持つ別 worktree と後続 Task の再承認が必要です。期限切れ・cancel 等で停止した作業も保存結果を確認してから続けます。commit mode は clean worktree、snapshot mode は保存 tree と同じ作業ファイルを要求します。`workflow recover` は残った runner 所有権だけを解放します。具体的な Git 手順と `continueFromDispatchId` の使い分けは[停止した作業の継続](../develop/task-graph.md#停止した作業の継続)を参照してください。

snapshot mode で完了済み Coder 結果を引き継ぐ場合は、保存済み `outputSha`（baseline commit）、`outputTreeSha`、変更 path を承認した別 worktreeを用意します。後続 Task の target tree が一致することを `conversation confirm` が検査し、`workflow start` には元の Coder Dispatch ID を `continueFromDispatchId` として指定します。新しい Flow は Coder を再実行せず Reviewer から始まります。停止時点が `CANCELLED` の部分成果なら、同じ baseline と保存 tree を持つ別 worktreeを用意し、後続 Task を承認したうえで Coder から続けます。

成功した Coder の後、Reviewer の実行中に期限または旧予算上限へ達した場合も、Coder をやり直す必要はありません。元の Reviewer が `CANCELLED` で、対応する `runtime-stop:` 証拠が保存されていることを確認し、同じ Coder 成果物に対して後続 Task と新しいレビュー予算を承認します。新しい Reviewer の設定承認には、その後続 Flow の正確な Reviewer Task ID を含めます。元の Flow や取消結果は変更しません。

Reviewer-only の後続実行も停止した場合は、成功 Coder を含む元 Flow を `replacesFlow` に指定し、別の後続 Task を承認します。元 Task が既に `SUPERSEDED` でも、exact hash・保存成果・出典の照合は必要です。tools/skills も後続 Task の新しい承認から解決し、以前の承認を転用しません。

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
