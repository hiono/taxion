# CLI と動作のリファレンス

`provenance configure --file POLICY.json` は明示 mode と対象 store、product時は正式artifactのpinを初回登録し、既存policyを上書きしません。`provenance verify` は実binaryとmanifest/archiveをread-onlyで照合します。詳細は [正式バイナリと運用モード](binary-provenance.md) を参照してください。policy未登録または不一致のoperationは、store接続前に `provenance-rejected` / `stateChanged: false` で拒否します。

この文書は、現在の CLI コマンドと実行上の制約をまとめる。初めて使う手順は[利用者ガイド](user-guide.md)、設定ファイルは[設定ガイド](configuration.md)を参照する。

`--file JSON` を使う操作例は、オーケストレータや自動化の実装者向けの入力仕様である。通常の利用者は `ready` を読んだ agent または `request` の Planner に自然言語で依頼し、作業条件・設定・実行上限を承認する。JSON の生成、ID・hash の転記、コマンド実行は agent または `request` が担当する。「Task 契約」は承認後に勝手に変更できない作業条件を指す。

コマンド一覧は `taxion --help`、各コマンドの入力条件はこの文書と所有仕様書に従う。採択した設計や未実装の要件は[要件と到達条件](../develop/orchestration-requirements.md)に分けて記録する。

## CLI

v0.7.21-rc.4 の `conversation confirm` は、承認束を要求する保存形式では、正式確認から ApprovalBundle を同じ transaction で追記する。同じ契約への二度目の承認は不要。以前の正式確認がある未実行・未変更の `READY` Task も、保存済みの完全一致する確認要求を再実行すれば、不足する bundle だけを追記できる。旧形式を暗黙移行せず、Flow・Dispatch・役割委譲は生成しない。

その正式確認を再利用できない場合は、`task authorize --manifest FILE` で明示的に承認する。manifest の `task` は保存済み record 全体と完全一致させ、`actor` と `reason` を指定する。Flow・Dispatch が存在せず、immutable proposal reference と既存の設定承認が現在の設定に一致する場合だけ、preview と `--confirm` で bundle を追記する。正式確認から導出済みの bundle は元の承認者・出典を保って読み戻し、新しい token を求めない。詳細は [Task の検証と承認](task-validate-and-authorize.md#fresh-ready-task-の不足-bundle-を生成する)を参照する。

インストール済みの目的別コマンド一覧は `taxion --help`、各コマンドの flag は `taxion COMMAND -h` で確認する。分類は表示上の案内であり、コマンド名や option の構文は変えない。`ready` は状況ごとの操作と承認境界を先に示す。以下は代表的な操作例で、契約の詳細は Task Graph、設定、実行記録の各文書に記載する。

v0.3.1 の `--help`・`-h`・`help` は先頭に `Version:` を表示する。`taxion --version` は `taxion VERSION` を出力し、追加引数を拒否する。Go のビルド情報にある module version（pseudo-version を含む）と、利用可能な VCS revision・`modified=true` を表示する。module version がなければ `(devel)`、module version と revision が両方なければ revision の取得不能も示す。情報自体がなければ `unknown` とする。版確認では設定・Git コマンド・DB・ネットワークを使わない。公開済み v0.3.0 と初回設定補正版 `dd352cf` は未対応。

通常は保存先を指定せず、Git 作業ツリールートの `.taxion/` を使う。サブディレクトリの起動も同じ保存先となり、linked worktree は各ルートに分離する。Git のルート解決に失敗した場合はエラーとし、別の状態へ切り替えない。Git repository 外では従来のカレントディレクトリ配下を使う。保存先の判断は [ADR-0032](../develop/adr/0032-resolve-default-workspace-from-git-worktree-root.md)、v0.3.0 で設定の基点も揃える変更は [ADR-0039](../develop/adr/0039-prepare-project-configuration-with-explicit-repair.md)に記録する。以下の新しい init・doctor fix・completion は v0.2.0 には含まれない。

例外用途の global option `--store PATH` は command の前に置き、旧 ledger のパスを選ぶ。明示した相対パスはカレントディレクトリ基準となる。旧 ledger の既定名は `ledger.json`、SQLite の保存先はその directory の `workspace.db`。DB 自体を `--store` に渡す指定、既存 DB と同一ファイルを指す symlink・hard link、未解決の symlink は、DB を開く前に拒否する。`init` はこの拒否時に directory や DB を作成せず、`serve` も audit directory を作成しない。別名の legacy ledger の読込み・移行は維持し、以前の別配置の自動移動・統合は行わない。

```bash
taxion config check --sources
taxion git check-index
taxion git check-index --secrets --scanner /absolute/path/to/betterleaks
taxion git hook status
taxion init
taxion conversation intake --file <request-and-contract-proposal.json>
taxion tools hash --path /absolute/path/service.docx
taxion specification scope-hash --scope <approved-local-scope.json>
taxion specification search --scope <approved-local-scope.json> --query "202"
taxion conversation search --task <id> --scope <approved-local-scope.json> --question "202"
taxion specification serve --scope <approved-local-scope.json>
taxion conversation proposal --task <id>
taxion conversation confirm --file <exact-human-confirmation.json>
taxion task create --title "…" --repo <repo>
taxion task plan --repo <repo>
taxion task validate --manifest <task-graph-request.json>
taxion task authorize --manifest <task-graph-request.json> --confirm <confirmationToken>
taxion task reauthorize --task <task-id>
taxion task reauthorize --task <task-id> --confirm <confirmationToken>
taxion task authorize-packets --request <packet-lifecycle.json>
taxion task authorize-packets --request <packet-lifecycle.json> --confirm <confirmationToken>
taxion workflow start --file <authorized-flow.json>
taxion workflow advance --id <flow-id>
taxion workflow run --id <flow-id> --coder-prompt-file <path> --reviewer-prompt-file <path>
taxion workflow show --id <flow-id>
taxion workflow evaluate --id <flow-id> --require-quality
taxion workflow lanes --task <task-id> --root <repository-root> --timeout 5s
taxion gate contract --task <task-id> --json
taxion gate resume --flow <flow-id> --json
taxion gate execution --flow <flow-id> --json
taxion gate publication --flow <flow-id> --json
taxion role-packet verify --file <packet.json> --expected-hash <recorded-packet-hash>
taxion role-packet list --task <task-id>
taxion role-packet read --expected-hash <recorded-packet-hash>
taxion workflow planning-context --id <flow-id>
taxion workflow planner-draft --file <selected-planning-proposal.json>
taxion workflow planner-action --file <selected-non-execution-advice.json>
taxion workflow question-draft --file <selected-question-findings.json>
taxion conversation plan --task <id> --question "Repair or revisit the plan?" --timeout 2m --max-ai-credits 30
taxion conversation investigate --task <id> --path src/app.go --question "What did the saved implementation miss?" --max-bytes 8192 --timeout 2m --max-ai-credits 30
taxion workflow recover --file <recovery-confirmation.json>
taxion workflow handoff --file <hold-or-release.json>
taxion workflow interrupt --file <targeted-interruption.json>
taxion workflow feedback-target --id <flow-id>
taxion workflow feedback --file <targeted-feedback.json>
taxion workflow feedback-resolve --file <keep-contract-decision.json>
taxion workflow draft --file <instruction-proposal.json>
taxion workflow draft-discard --file <discard-proposal.json>
taxion workflow draft-approve --file <approve-supplement.json>
taxion dispatch --task <id> --repo <repo>
taxion runtime start --dispatch <id> --system-prompt-file <path>
taxion runtime send --dispatch <id> --system-prompt-file <path> --prompt-file <path>
taxion runtime resume --dispatch <id> --system-prompt-file <path>
taxion runtime events --dispatch <id> --system-prompt-file <path>
taxion runtime stop --dispatch <id>
taxion runtime commit --dispatch <id> --system-prompt-file <path> --message-file <path> --scanner <path>
taxion review --task <id> --sha <coder-sha> --repo <repo>
```

### 依頼の登録と Flow 実行

`taxion request --repo REPO --max-ai-credits 30` は新規依頼の入口である。v0.7.19 からの既定は、申告した提供元の停止に任せる `provider`。課金条件に適合する提案を承認した場合、Task 上限を省略できる。条件・方式・移行は[課金ガイド](billing.md)にまとめる。`--timeout D` の既定は1時間。Copilot は30以上の session 別 soft ceiling を要求し、OpenCode はこの指定を省略する。利用者は `/plan` で計画相談を開始し、範囲・完了条件・ホスト検証コマンドと課金条件を確認して `/approve` を選ぶ。保存済み契約と正の明示上限は変更しない。

Taxion が intake、完全な hash の承認、Reviewer の設定承認、Flow、同梱の role 別指示を構成する。利用者が JSON や prompt ファイルを作る必要はない。計画中の `/quit`・EOF は記録を残すが、コード実行を開始しない。Planner error は自動 retry しない。

期限は `/plan` から数え、入力を待つ時間も含む。実行には snapshot mode と明示承認した worktree 全体の file access を使う。Planner には今回の依頼または回答、最小の提案・質問、commit 済み path 一覧、任意選択した commit 済み context を渡し、tool は渡さない。利用量は観測して予算に集計し、中間報告はしない。

計画 receipt は version・operation ID・入力／結果 digest・成否を SQLite の `records.requests` に保存する。checkpoint v2 は最小の計画・質問・依頼 digest・承認参照を持ち、会話全文、完全な入力／結果、private reasoning は checkpoint・receipt・実行 Task へ保存しない。承認済み再開は正式な intake confirmation と Task ID・operation ID・proposal hash・authorization reference・契約・設定・予算を照合し、`Approved` flag 単独では実行しない。旧 checkpoint の会話 field は読み取り時に捨て、承認束縛のない旧版と未知版は再開・取消しを拒否する。本文の移行・再保存はしない。

実行イベントは Flow ID から正式な Dispatch・event receipt を参照し、失敗本文は digest だけを残す。`taxion records show` で確認・明示 export できる。新しい `requests/` の JSON と `execution.log` は作らず、過去のファイルは削除しない。保存前に DB の file mode を所有者だけが読み書きする設定にする。このコマンドは commit、公開、外部 Issue 作成を行わない。通常 CI は合成 Runtime と実 Git で全体を確認する。この入口の実モデル受入れは未完了。

`request --status` は read-only の JSON で現在の operation と Flow 予算を返す。`request --resume ID` は保存済みの設定・上限・期限・質問・承認を再利用し、他の option を受け付けない。EOF では質問待ちを残し、`/quit` では終了する。`request --cancel ID` は実行停止と照合を済ませた idle request の operation だけを終了し、Task や既存判断を変更しない。稼働中 Flow・未終了 Dispatch・成否不明の Planner は取消しを拒否する。同じ workspace で別の active operation や workflow を開始できない。質問待ちには回答だけを適用し、承認後の4 lane preflight は一括実行する。同一契約内の Coder・非モデル validation・Reviewer の間では再承認せず、中間の進捗を表示しない。最終 JSON の `gate` は `COMPLETED`、`BLOCKED`、`USAGE_EXCEEDED`、明示取消しは `CANCELLED`。Planner の実測 nano-AIU を全体の上限から控除し、残量だけを Flow に割り当てる。取得量が未知の場合は次のモデル呼出しを止める。期限と予算は再開で増えず、未照合の実行・送信を自動 retry しない。詳細は[利用者ガイド](user-guide.md#人のことばで依頼して計画を相談する)を参照する。

`conversation search` は、承認済みの Markdown、PDF、DOCX、PPTX の抜粋を、編集可能な Task に自動で添付する。会話 export は private に分類し、明示選択した user message だけを検索対象にする。console は metadata を表示し、ローカルの非公開状態には引用した出典全文と用途別の承認を保持する。取込み、選択した runtime への送信、外部公開は別々に承認する。

`conversation ask`・`conversation plan`・`conversation investigate` は別々にモデルを呼び出す操作。それぞれ明示 deadline を設け、Copilot は session 別 credit ceiling も要求する。OpenCode の料金・account quota は未確認とし、Copilot の credit ceiling を指定して代用しない。token 利用量は呼出しごとに報告するが、停止条件にはしない。通常 CI はローカル回帰テストを使う。

実 SDK の独立 Reviewer 受入れは、再承認した実行で、保持した Coder SHA の承認と worktree の解放を確認して完了した。scope 承認、parser の制約、受入れ記録は[仕様入力](../develop/specification-input.md)を参照する。

`workflow planning-context` は、保存済み Task、Memory、concern、未充足の依存、Flow 上限、関連する Coder・Reviewer の結果から、上限付き Planner 入力を再構成する。target は各指示・結果・SHA、observation hash は返した内容を識別する。`recordedNextCheck` は既存 controller の確認であり、AI 判断や実行承認ではない。モデルを呼ばず記録も変更せず、別の計画操作に使う証拠を返す。

`conversation plan` は、整理した入力を隔離した、file・shell tool を持たない Orchestrator session へ渡し、`REVIEW`、`REPAIR`、`REPLAN`、`INVESTIGATE`、`HOLD`、`HUMAN_REQUIRED`、`REPORT` を提案させる。`REQUEST_CHANGES` を自動的な修正指示とせず、保存済み Memory、過去の finding、依存、上限を評価する。

応答は、出典付き confirmed claim、仮定、不明な点、observation hash、報告された利用量を含む。出典検証は引用 field の存在を確認するもので、推論内容の正しさは検証しない。このモデル操作は明示選択と deadline が必要で、Copilot は session 別 credit ceiling も要求する。role 終了ごとの自動 Planner 呼出しはない。

`conversation watch` は、設定した質問上限で `/plan TEXT` を受け付ける。助言だけでは hold を解除せず、判定を変更せず、Dispatch も発行しない。既存の自動 workflow は変更しない。

実行中の Flow を hold した場合、`/adopt` は最後に成功した `/plan` の結果を、未承認のバージョン付き補足として選ぶ。この処理に入れるのは、成功済み Coder の結果を保存した後の `REVIEW`、または独立した `REQUEST_CHANGES` を保存した後で修正回数上限内の `REPAIR` だけ。

`/approve` は、変更のない契約内でその draft の完全一致を別途承認する。`/step` は別途 hold を解除し、実行する。計画・採用時には、これらの選択を推測しない。観測、Memory、target、hold ID、draft version の変更は拒否する。`/plan` が失敗した場合は console の proposal cache を消す。他の提案は修正へ強制的に変換せず、別の調査、再計画、利用者の判断を要求する。

同じ操作は `workflow planner-draft --file PATH` でも使える。request は `flowId`、`id`、`expectedDraftId`、`expectedChangeId`、`workflow feedback-target` の `target`、`actor`、`source`、`reason` を含む。`proposal` には `conversation plan` の JSON 結果をそのまま入れる。

draft には観測時刻・hash、出典の結果・SHA 対象、Memory version、推論、不確実性を保存し、全履歴を再帰的に埋め込まない。同じ ID・内容の再送は元の version を返し、異なる内容での ID 再利用は拒否する。取り込んだ session ID と actor・source は提供された出典情報であり、認証済み provider・利用者 identity ではない。内容の妥当性と契約の維持には、引き続き利用者による補足の明示承認が必要。scope・acceptance・identity・予算の改訂、Orchestrator が選ぶ無人実行、新規依頼の intake は、補足承認とは別に扱う。

`conversation intake --file PATH` は、元の依頼本文 `request`、明示した `status: DRAFT` の `task`、`actor`、`source` を記録する。提案 Task は独立した `CODER`・`REVIEWER` を持ち、`authorizationId` と持込みの判断は持たない。scope・acceptance の未解決な不足は残せるが、不完全な契約を実行可能として確認することはできない。

結果と `conversation proposal --task ID` は、契約全文と内容の `hash` を表示する。モデル・worker は呼ばない。任意の `planningSelection`・`planningReason` は、その draft への別途上限付き `conversation ask|plan` だけを承認し、`READY` にはしない。修正版は新しい Task ID で記録し、前の version を保持する。

`conversation confirm --file PATH` は、`taskId`、`expectedHash`、`actor`、`source`、`reason`、設定全体の `selection`、明示した `additionalTaskIds` を受け取る。後続 Task の例は、上限付き Flow の正確な Reviewer Task ID。利用者は scope、acceptance、検証 command、repository、identity、設定を、その hash と照合して承認する。

確認は transaction 内で承認を記録して `READY` を準備するが、Dispatch は発行しない。実際の修正回数・時間・session ceiling は、別の `workflow start` で承認する。同一の確認の再送は状態を変えず、確認内容の変更や古い hash は拒否する。設定承認の対象を広げる場合は、新しい global 一意の decision ID が必要。

旧 decision は `selection.execution.approvalRef` をそのまま使う。intake 計画・確認の任意の `configurationDecisionId` は、代わりに `REF@task:PRIMARY_TASK_ID` を使える。REF は変更していない設定の approvalRef。承認は引き続き許可する全 Task を正確に指定する。Reviewer は主 Task の保存済み decision を使い、Reviewer 別に生成した新 ID は使わない。`request` はこの形式を使い、依頼のたびの設定変更を不要にする。

発行済み契約を変更する場合は、前 Task を `supersedes` に指定し、`createdFrom: flow:FLOW_ID` を持つ successor intake Task を使う。確認には `replacesFlow` と表示した `target` の完全一致も必要。前 Flow は hold 済み・runner claim なし・active Dispatch なしを要求し、停止の証拠が不完全な場合は拒否する。

停止した Flow から部分成果または成功済み Coder の成果を引き継ぐ場合は、[作業継続手順](../develop/task-graph.md#停止した作業の継続)に従って別 worktree を用意する。元の baseline と保存済み commit／tree・変更 path を保持し、後続 Task の承認前に照合する。これは commit mode・snapshot mode のどちらにも必要で、成功済み Coder を再実行しない場合だけ `continueFromDispatchId` を指定する。一般の契約変更で同じ worktree を使う場合には、最後に記録した出力 SHA を後続 baseline に保持する追加条件があるが、予算停止の継続に必要な別 worktree の代わりにはならない。

明示確認は前 Flow を停止し、Task を `superseded` にする。発行済み指示、結果、承認、元の上限は残す。successor は新しい承認と別の上限付き Flow を取得し、過去の承認を旧契約の拡大許可に読み替えない。actor・source は提供された出典情報であり、認証済み利用者 identity ではない。

監視中の `/adopt` は、他の Planner 提案を `workflow planner-action` により、出典付き未解決 feedback として採用する。修正補足にはしない。`HOLD`・`HUMAN_REQUIRED` は hold と理由を保持する。`INVESTIGATE` は別の上限付き調査、`REPLAN` は明示的な successor intake・再承認へ進める。`REPORT` は作業を承認せず、選択した報告を記録する。

これらの判断が未解決なら、利用者が `feedback-resolve` で明示処置するか、承認済み finding を適用するか、successor を確認するまで継続を拒否する。助言の選択だけでは調査を実行せず、置換契約を承認しない。この利用者による明示確認は、自然言語契約の完全自動作成や Orchestrator の無人実行を提供する操作ではない。

質問の観測は、Task 履歴、Dispatch 指示、Flow 契約内の過去の結果判断も整理する。結果全文と出典 hash は残し、再帰的に繰り返される指示は省く。整理した場合は `projection` で説明する。instruction hash は元の保存記録を識別し、その記録は `task graph` で確認できる。追加モデル呼出しは行わない。

`conversation investigate` は、明示した上限で別の調査を行う。出典付き設定承認と今回の認証・repository access を確認し、保存 scope 内かつ禁止 path 外で、明示選択した commit 済み UTF-8 ファイルだけを読む。出典は最後に保存した成功 Coder SHA、または Task baseline であり、変更可能な HEAD や作業ファイルではない。

`--path` は繰り返して1〜16ファイルを指定する。必須の `--max-bytes` は総出典量の上限で、最大65536。上限超過、欠落、binary は error とし、切り詰めた証拠を返さない。shell command、filter、外部 diff tool、model tool は許可しない。隔離した質問は通常の明示的な時間・session soft ceiling と service 利用許可確認を保つ。新しい Git 証拠は独自の SHA、観測期間、内容 hash を持ち、過去の担当がその file を検討したこと、テストしたこと、実行状態を確認したことの証拠にはならない。

監視中の同等操作は `/investigate PATH QUESTION`。`--investigation-max-bytes` と設定した質問上限を使う。`/apply REVIEWER 0,2 USER-INSTRUCTION` は、最後に成功した質問・調査から、0始まりの confirmed finding index を明示選択する。`CODER` は、独立した `REQUEST_CHANGES` の保存後で、修正回数上限内の場合だけ使う。

選択した主張、引用、不確実性、出典 target、Memory version、上限内の調査証拠を保持した未承認補足を提案する。未選択の会話は次の指示にコピーしない。`/approve` と `/step` は別の操作。記録の変更、出典 bytes・hash の偽造、不正 index、非対応 role は拒否する。質問失敗時は応答 cache を消す。通常の質問を自動適用することはない。

`workflow question-draft --file PATH` は `planner-draft` と同じ field に加え、`nextRole`、整数 index の `selected`、`humanInstruction` を受け取る。`proposal` は `conversation ask|investigate` の結果をそのまま含める。取り込んだ応答・session ID は提供された出典情報であり、model から出力されたことの証明ではない。承認時には、選択した推論と契約が変わらないことを引き続き評価する。

`workflow interrupt` は、`flowId`、`id`、`expectedChangeId`、表示した `target`、`actor`、`source`、`reason` を受け取る。対象付き中断 request と handoff hold を atomic に保存する。実行中の Dispatch は `CANCEL_REQUESTED` にするが、停止結果を創作せず、Task も cancel しない。

所有 runner がその状態を観測して Runtime safe stop を実行する。停止成功時だけ `runtime-stop:` の証拠と cancel 済み Dispatch を記録する。停止失敗は未確認のままとし、継続を拒否する。発行済み・未開始の実行は、架空の停止証拠を作らず hold にする。同一入力の再送で別の role を中断できず、終了・古い target は拒否する。部分ファイルと Git 履歴は巻き戻さない。

Copilot で active session を所有する runner がない場合は、`runtime stop` で保存 session の完全一致を確認して接続する。OpenCode は別 process からの停止専用接続に対応せず、所有 runner で終了と部分成果の保存を確認する。新たな承認済み実行の前に所有権を照合する。

監視中の `/interrupt`・`いま止める` は、`/hold` や Task cancellation と別の操作。`conversation watch --interactive-execution` は、選択した `/step`・`/auto` の実行中も利用者の入力を受け付けることを明示選択する。二つ目の所有実行を拒否し、進捗と実行失敗を報告する。対象付き hold・中断も引き続き使える。

EOF・`/quit` はこの console が所有する実行だけを安全に中断し、cleanup を待つ。別 process の実行は cancel しない。flag がなければ同期的に実行する。非対話 workflow の既定と、変更不可の Flow 上限は維持する。中断は Flow を停止するものであり、再開や契約変更の許可ではない。

`runtime`・`workflow run|step` の `--compact-context` は、role prompt から再帰的に入れ子になった過去の指示を省く。過去の結果全文、finding、検証の証拠、出典 hash、利用者の判断、Memory、現在の契約は保つ。整理する各結果判断を実際の保存 Dispatch と照合し、モデルで要約したり、通知なく切り詰めたりしない。全履歴は `task graph` で参照できる。

この option は system prompt と session configuration hash を変えるため、新規 session に使い、実行接続時も繰り返して指定する。`runtime stop` には指定しない。既存 session は元の option を保つ。繰返し入力を減らすもので、provider credit の保証ではない。context 生成に追加のモデル呼出しは行わない。

`runtime`・`workflow run|step` の `--controller-validation-submission` は、モデルの提出と controller の検証を明示的に分ける。role は検証結果を創作せずに実際の出力と証拠を提出し、controller が成功の保存前に承認済み command の実行と Git の検証を行う。

結果の `evidence` は識別子の配列です。契約の `requiredEvidence` にある値を、そのまま独立した要素として含めます。たとえば `work-report` を要求された場合、`"work-report"` は一致しますが、説明を付けた `"work-report: 変更内容"` は一致しません。実際の作業説明は別の要素に記録し、未実施の証拠を識別子だけで補ったことにはしません。

Reviewer の `changedFiles` は空の配列です。再現された Coder の差分はレビュー対象であり、Reviewer 自身の変更ではありません。`worktreeChanged` は Reviewer 開始後の変更を表します。実際に変更した場合は blocker として報告し、controller による tree の照合も成功扱いにしません。

controller の検証待ちだけでは suspension を要求しない。ただし、実際の失敗、出力欠落、根拠のない Reviewer 承認、必要な利用者の判断は引き続き進行を拒否する。新規 session に使い、実行接続時も繰り返す。`runtime stop` には指定しない。既存 session の既定 prompt は変更しない。

### 設定

`config check` は、固定の Global・Project YAML 設定と対応する環境変数 override を、バージョン付き設定 schema と照合する。解決後の Copilot 設定では、任意の `execution.budgetGuidance`・`billing` と OpenCode 専用の4項目を除く field が必須。OpenCode 設定では `runtime.acpCommand`・`runtime.acpConfig`・`runtime.acpConfigHash` も必須となり、Copilot 設定には混在させない。v0.4.0 では `runtime.acpCommandHash` は再現性のための任意の実行ファイル固定とし、通常利用では省略できる。`version` は整数、他の値は whitespace を含まない空でない文字列。未知の field、重複キー、暗黙の非文字列値、alias、複数 document は拒否する。

v0.7.19 からの `config check --billing` は、新規提案用の課金条件・方式・適用先・設定元を JSON で表示する。提供元の照会・課金設定変更・モデル起動はしない。他の表示・承認照合 option とは併用しない。[課金ガイド](billing.md)を参照。

Global file は Go の `os.UserConfigDir()` 配下の `taxion/config.yaml`。Linux は `$XDG_CONFIG_HOME` または `~/.config`、macOS は `~/Library/Application Support`、Windows は `%AppData%`。Project file は Git 作業ツリールートの `.taxion/config.yaml`、Git 外は cwd。v0.2.0 の cwd 基点から変更した。cwd に旧サブディレクトリ設定があれば移行を求める。任意の親 directory は設定候補として検索しない。存在しない既定 file は省略し、読取り不能・不正な file は error にする。file が一つもなければ error を報告する。

`--file`・`--user-file`・`--project-file` は削除済み。実設定は固定 Global・Project path に移し、環境別 override には対応する `TAXION_*` 変数を使う。削除した option は移行案内付き error を返し、指定 path を読まない。

Git worktree 内で読み込む全設定 file は、untracked（`git ls-files` に entry がない）かつ Git ignore 対象である必要がある。後の層で上書きする場合も、追跡済み・ignore されていない設定は本文の読取り前に拒否する。symlink の指定 path と解決先の両方を確認する。worktree 外の file には Git ignore を要求しない。共有する例は実設定 file と分ける。

Git worktree では、Global 設定だけを使う場合や directory が存在しない場合も、Project 設定の基点にある `.taxion/` 全体に ignore 規則が必要。`.gitignore` に `/.taxion/` を追加するか、ローカル専用規則を `.git/info/exclude` に置く。file だけの `/.taxion/config.yaml` 規則では不十分。追跡済みの子 path と有効な ignore 例外は、設定読込み前に拒否する。

directory と設定 symlink は、指定 path と解決先の両方を確認する。directory symlink には `/.taxion` など、link 自体に一致する ignore 規則が必要。

Project 設定の基点に `.env` があれば、その file も untracked かつ ignore 対象である必要がある。symlink の解決先も確認する。index 内の `.env` は作業 file がなくても拒否する。環境変数 override の有無にかかわらず検査する。Taxion は dotenv を読まず、環境変数値の元の取得先も推測しない。

`.taxion/` はローカル設定、workspace 状態、cache を保持する。共有仕様、判断、レビュー済み Memory export は、その directory の外に置く。既存ローカル Memory は workspace の保存先に残す。共有 Memory の保存と移行は #69 で追跡している。

追跡済みのローカル file は、index の path を確認してから追跡を外し、ignore 規則を追加する。ignore だけでは追跡や既存履歴を削除しない。公開してしまった credential を確認し、必要に応じてローテーションする。`config check` は失敗を報告し、Git の状態は変更しない。`init` と `doctor fix` も追跡済み file の自動修復は行わない。

優先順位は、対応する環境変数値、Project、Global の順。各 file は `version: 1` が必要。他の field は層内で省略でき、下位層から継承する。明示的な空値・null は error。merge 前に各層を検証するため、優先する値で下位の不正値を隠せない。

`--sources` は、値を表示せずに各 field を採用した層を表示する。環境変数 override は `environment` と報告する。固定 YAML file は最低一つ必要で、環境変数は file 設定を補う。

v0.3.1 の `doctor` は、設定不足でも `init`・`doctor fix` と同じ処理で現在の環境を検出する。設定・DB・Git は変更せず、診断履歴を保存・引継ぎしない。設定不足時の `--json` は `{"setup":{...},"problem":"..."}` を出し、終了値は非zero。`setup` は Project 設定位置、workspace の存在、設定元、remote・runtime 候補、不足・任意・未確認項目、次操作を持ち、設定済みの値や credential は含めない。backend の native inventory と保存 Dispatch は検査しない。壊れた設定や Git policy 違反は、一覧を出す前に error とする。Git ignore の不足だけは修復候補の表示を許す。完全な設定での既存 backend 別 JSON 形式は変更しない。公開済み v0.3.0 には、この設定不足時の表示は含まれない。

v0.3.1 の対話入力は、各項目の説明と候補値を表示する。候補のない必須空欄と不正値は同じ質問で再入力を求め、EOF は対象項目と無変更を報告する。秘密らしい値は表示せず拒否する。生成 YAML は不足分であり全項目のひな型ではない。継承元・任意／backend 固有の項目・全項目の参照先を別に示す。

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
| `TAXION_BILLING_CONTRACT` | `billing.contract` |
| `TAXION_BILLING_OVERAGE` | `billing.overage` |
| `TAXION_BILLING_AUTO_RECHARGE` | `billing.autoRecharge` |
| `TAXION_BILLING_PAID_FALLBACK` | `billing.paidFallback` |
| `TAXION_RUNTIME_ACP_COMMAND` | `runtime.acpCommand`（OpenCode 専用） |
| `TAXION_RUNTIME_ACP_COMMAND_HASH` | `runtime.acpCommandHash`（OpenCode 専用・任意の固定） |
| `TAXION_RUNTIME_ACP_CONFIG` | `runtime.acpConfig`（OpenCode 専用） |
| `TAXION_RUNTIME_ACP_CONFIG_HASH` | `runtime.acpConfigHash`（OpenCode 専用） |
| `TAXION_EXECUTION_REQUESTER` | `execution.requester` |
| `TAXION_EXECUTION_WORKER` | `execution.worker` |
| `TAXION_EXECUTION_APPROVAL_REF` | `execution.approvalRef` |
| `TAXION_EXECUTION_BUDGET_GUIDANCE` | `execution.budgetGuidance`（任意） |

設定した環境変数値は、数値に見える識別子も文字列として扱う。明示的な空値・不正値は error。未設定なら下位層の値を保つ。設定 override として読むのは表の変数だけ。`TAXION_TOKEN`、`GH_TOKEN`、`GITHUB_TOKEN`、`COPILOT_GITHUB_TOKEN` は別の credential 用途を維持し、この設定にはコピーしない。

`execution.budgetGuidance` は `provider`・`off`・`flow`・`account` を指定する。課金条件との組合せ、解決順序、旧契約の扱いは[課金ガイド](billing.md)を参照する。

mise は環境変数を渡す任意の方法。共有 `mise.toml` から、ローカル `.env` を参照できる。

```toml
[env]
_.file = ".env"
```

実際の `.env` は untracked とし、`/.env` などで ignore する。共有 mise file には読込み先と安全な既定値を記載し、credential 値は含めない。mise、shell、CI が環境変数を読み込んで渡し、Taxion は出典として `environment` だけを記録する。

任意の dotenv path と、他の file にコピーした秘密は、この path policy の対象外。この設定の確認は commit・公開の保護ではない。

### Git index の検査と管理下の commit

`git check-index` は Git の実際の index 全体を検査し、作業ファイルの内容を読まず、Git 状態も変更しない。path の構成要素に `.taxion` があるもの、basename が `.env` または `.env.` で始まるものを拒否する。入れ子、強制追加、index 内の symlink、変更していない追跡済み local file も対象とし、これらの削除を stage することは許可する。値を含めない共有例は `.taxion/` 外の `docs/environment.example`・`docs/taxion-config.example.yaml` などに置く。`.env.example` も意図的に禁止対象に含める。

部分 commit の `GIT_INDEX_FILE` と linked worktree を含め、Git が選択した repository と index を使う。subdirectory から実行しても index 全体を検査する。error は分類別件数だけを表示し、file 名や値は表示しない。読めない index と unmerged index は不合格とする。手動 commit の直前か pre-commit hook から実行する。このコマンド自体は hook を設置せず、Controller 管理の commit は後述する同じ検査を直接実行する。検査は path だけなので、source file・共有例・`mise.toml` にコピーした秘密には内容検査が必要となる。`--secrets` で後述の local 内容検査を追加する。

`git check-index --secrets` は信頼する [Betterleaks 1.9.0](https://github.com/betterleaks/betterleaks/releases/tag/v1.9.0) binary を要求する。PATH 上の `betterleaks` か `--scanner` の指定を使い、導入前に公開 checksum で release archive を照合する。scanner の欠落・非互換・失敗・中断・不正な report は拒否する。Taxion の Go 依存に scanner package は追加しない。

選択した index の blob を読み、変更していない追跡済み内容と symlink の link text も対象とする。作業ファイルや staged diff は使わず、Git replacement object による内容の差替えも許さない。Gitlink は submodule の commit ID だけなので、submodule 内の file は別に検査する。local scanner には blob を標準入力で渡し、index 上の path を local rule の metadata とする。非公開の一時 directory で、固定版に同梱された既定 rule を明示して実行する。project の config・ignore・baseline、継承 credential、scanner 設定用環境変数は使わず、credential の API 検証も無効にする。inline の許可 comment は無視する。report・診断内容は転送せず、error は検出件数か検査失敗の理由だけを表示する。

検査開始時の index entry を使うため、index を変更したら再検査する。後続の手動 commit まで index を lock するものではない。全体の timeout は2分で、一部検査だけを合格にしない。検出範囲は固定 rule と filter に限られ、低 entropy の値、非対応形式、encoding された内容、誤検出には確認が必要となる。同梱 path filter は Go manifest・checksum、一般的な lockfile、一部 vendor tree、画像などの binary 形式を除外する。依存の hash を credential と誤認しないためだが、除外 file にコピーした秘密は内容検査の対象外となる。安全な template の受入れと、発行していない生成済み GitHub-token 形式の fixture は、API 検証なしで local 検証済み。検出結果を自動で許可対象にせず、有効な credential の証拠とも扱わない。

永続配置した Taxion binary から pre-commit hook を明示的に設置する。

```bash
taxion git hook install --scanner /absolute/path/to/betterleaks
taxion git hook status
taxion git hook uninstall
```

option なしの `install` は PATH 上の Betterleaks 1.9.0 を要求し、欠落時に path 検査だけへ切り替えない。完全な hook は保存した scanner で `git check-index --secrets` を実行する。禁止 path だけの検査を明示的に選ぶ場合は `taxion git hook install --paths-only` を使う。`--paths-only` と `--scanner` の併用は error となる。

Git が実際に使う hooks directory を選び、`core.hooksPath` と linked worktree に対応する。共有配置は、その配置を使う全 repository・worktree に影響する。hook がない場合だけ作成し、独自・編集済み・実行権限のない・symlink の hook は変更せず、error を明示する。既存 hook には、元の検査を保ちながら `taxion git check-index --secrets --scanner /absolute/path/to/betterleaks` を手動で組み込む。同一 hook の再設置は冪等で、uninstall は現在実行する binary の完全一致した hook だけを削除する。path-only hook を自動で upgrade しない。mode・scanner path の変更は旧 hook を明示的に uninstall してから設置する。独自・編集済み hook は手動で確認する。

hook は shell quoting を含めて Taxion と scanner の絶対 path を保存する。status・uninstall には同じ導入 path を使う。移動・編集済みの配置は自動置換せず、手動で確認する。`go run` の一時 executable は消えるため、設置には使わない。status は mode、hook と Taxion binary の実行権限、完全な hook では scanner の版も検査する。scanner の欠落・非互換は commit と status を失敗させるが、完全一致した hook の uninstall は scanner が消えていても可能。Taxion binary の欠落も commit を止める。現在の設置は Unix の実行権限を要求する。

`--paths-only` は限定的な検査を明示的に選び、内容 scanner を呼ばない。完全な hook にも rule・path filter の制約が残る。どちらも部分 commit を含めて Git の実際の index を使う。local hook は `--no-verify` で省略、置換、無効化でき、新しい clone に引き継がれない。Controller 管理の commit は、設置済み hook に依存せず独自に検査する。

`runtime commit` は、同じ role・runtime config hash・worktree に結び付く実行中 Coder Dispatch に対する host 操作である。保存 session と同じ system prompt と runtime option を使う。SDK session の作成・resume、file の stage、Task・Dispatch の完了処理は行わない。message file は Dispatch の Conventional/Contextual 方針に従う。

Controller は repository、branch、baseline、実行開始点からの ancestry、作成した commit message、許可・禁止 path を検査する。実 index を lock し、周囲の Git 転送設定を使わず、staged index を非公開 snapshot にコピーして全 blob と提案 message を scan する。commit する tree は snapshot であり、作業ファイルではない。split index・linked worktree に対応する。branch が並行変更された場合は compare-and-swap による更新を拒否し、外部の変更を置換しない。unmerged index、進行中の merge・rebase、空の変更、不正な scope、scanner の欠落・失敗は操作を止める。

Git の commit porcelain ではなく `commit-tree`・`update-ref` を使うため、project hook は実行しない。`commit.gpgsign=true` なら設定済みの Git signer で署名し、失敗時に署名なしへ切り替えない。Taxion は実 index と作業ファイルを変更しない。branch 更新の失敗では scan 済みの到達不能な commit object が残りうる。更新の失敗・中断時は候補 SHA を報告するので、再試行前に branch を確認する。Git 更新と workspace database は単一 transaction ではなく、更新後の database 保存失敗では更新済み SHA を明示する。

Git file を直接変更する他 process に対する sandbox ではない。過去 commit の秘密検査や公開前検査は行わず、#56 の対象とする。scanner の rule・filter 制約も残る。管理下の commit に成功しても独立レビューと受入れは必要となる。

新しい session の作成時に、Coder 用 `taxion_commit` SDK tool を明示的に有効にする。

```bash
taxion runtime start --dispatch <id> --system-prompt-file <path> \
  --managed-commit --scanner /absolute/path/to/betterleaks
```

同じ session の `send`・`resume`・`events`・host `commit` には、同じ `--managed-commit`、scanner、role prompt、その他の runtime option を指定する。`runtime stop` は取消専用の保存 binding を使う。tool binding は Dispatch ID、解決済み scanner 絶対 path、管理操作の版を config hash に含める。tool なしで作成した session に resume 時だけ追加することはできず、`--tools taxion_commit` は host binding の代用にならない。scanner の絶対 path は維持する。保存 path の executable が欠落していても停止のための接続は可能だが、各 commit は利用可否と版を再検査し、欠落時は失敗する。

管理下の stage・commit tool は tool search に隠さず事前登録する。Auto が選ぶ model によって write tool が `create`・`edit` でなく `apply_patch` となる場合があり、初期 tool metadata は model 選択後の tool 構成を保証しない。scope 内の Coder 編集には `--tools view,create,edit,apply_patch` または対応する `--coder-tools` を明示する。全書込みの permission は割当 worktree を確認し、stage・commit はそれぞれ Dispatch scope を検査する。native の最終 JSON response と Controller の厳密な結果検証は有効のまま保つ。

`workflow run --managed-commit --scanner PATH` は Coder session だけに tool を渡し、Reviewer には渡さない。tool の入力は完全な `message` 文字列だけで、agent が repository・branch・scanner・Dispatch を選べない。SDK は作成・resume 時に permission 検査を省かず登録する。handler は active な root session を確認し、Controller は呼出し session を保存 Dispatch と照合する。host の取消と、保存 review flow の時間・既知使用量の上限を適用する。scanner・認可の失敗は tool call の失敗として残す。

commit tool は **stage 済みの内容だけ** を使う。新しい Coder session で選択 file の stage も許可する場合は `--managed-stage` を追加する。

```bash
taxion runtime start --dispatch <id> --system-prompt-file <path> \
  --managed-commit --managed-stage --scanner /absolute/path/to/betterleaks
```

`workflow run` でも同じ2 option を使う。`--managed-stage` は `--managed-commit` を要求し、同じ session への接続時も両方を指定する。stage の有効化は config hash の操作版と tool 構成を変えるので、既存の commit-only session に resume 時だけ追加できない。

`taxion_stage` の入力は `{"paths":["src/file.go","src/deleted.go"]}` だけとする。重複のない正規化済み repository 相対 file path、または追跡済み file の削除を指定する。wildcard・pathspec を展開せず、directory の再帰追加、ignore file の強制追加、Git metadata の選択はしない。禁止 local file は追加できないが、認可済みの追跡済み file 削除は stage できる。worktree 外を指す親 symlink と特殊 file は拒否する。symlink file は対象内容でなく link text を stage し、親 directory がなくなった file の削除にも対応する。

commit と同じ binding session、active Task、Flow 上限を使う。index を lock・コピーし、指定 file だけを非公開 copy に stage して、結果の全 tree の scope・禁止 path・内容を検査する。未選択の staged file は維持する。成功時は copy を flush して実 index を atomic に置換する。commit は作らず、作業ファイルと HEAD は変更しない。検査失敗時は実 index を変更しない。更新前に branch・HEAD・実 index の変更を検出したら操作を止める。commit は過去の stage を信用せず、snapshot を再検査する。

Git attribute と設定済み clean filter は通常どおり適用し、作業内容だけでなく filter 後の blob を scan する。Git repository 設定と filter executable は信頼する host 入力で、sandbox 化した tool ではない。stage は不合格の scan より先に local object database へ blob を書く場合がある。到達不能 object を消さず、秘密の全 local 保存を防ぐとは主張しない。悪意ある host process との Git file の競合、scanner の検出・filter 制約は保証対象外とする。

登録 SDK handler の stage→commit は Controller と実 Git で local 検証済み。任意 shell command と sub-agent の stage・commit 呼出しは有効にしない。実 SDK の stage→commit→独立レビューと、不許可 stage の拒否は #72 に記録する。対話・修正・再レビューの受入れは、2026-10-12 に完了した M12 の実 SDK 受入れで #53/#54 として確認した。文書作業の実モデル受入れと停止後の復旧は、完了済みの #78・#79・#80 に記録した。すべての CLI 入口の実モデル実行を確認したという意味ではない。

```yaml
version: 1
project:
  id: project-a
repository:
  provider: github
  host: github.com
  path: owner/application
  account: developer-a
  credentialRef: gh:active
runtime:
  provider: copilot
  account: developer-b
  credentialRef: copilot:logged-in
  contractRef: company-contract
execution:
  requester: developer-a
  worker: local-worker
  approvalRef: human-decision-1
```

`project.id` は Taxion project を識別する。Repository の `provider`・`host`・`path` は source code の配置、`account` は期待する操作 identity を示す。上の Copilot 設定例では Runtime の `account` は期待する Copilot identity、`contractRef` は利用予定の契約を示す。`credentialRef` は別に保管する credential への参照で、token を含めない。OpenCode の account・credential 参照は `opencode-managed` に固定し、provider 認証の確認済み identity として扱わない。Execution は依頼者、worker、出典付き承認への参照を示す。

認証は選んだ CLI に任せる。開発基盤の API 操作には、GitHub.com は `gh`、GitLab.com と Self-Managed GitLab は `glab` を使う。Copilot 実行にはこれと別に Copilot CLI を使う。OpenCode のモデル・設定は操作者が OpenCode 側で準備し、Taxion は他の provider の credential を流用しない。現在の OpenCode 隔離実行には host の login や API key を渡さない。[認証の準備](configuration.md#認証の準備)で選択ごとの必要条件を確認する。

Taxion は OAuth、credential vault、token 更新、認証 file の直接解析を実装しない。利用者は provider tool で login し、CI・worker は provider が対応する credential を渡す。明示的な `env:NAME` は対応する provider に credential を渡すだけで、Taxion に保存しない。実装済みの GitLab adapter は、明示 API host、account、namespace・repository、permission を照合して別 host へ切り替えない。API endpoint と Git clone endpoint は別で、独自の clone SSH port は API port ではない。内部配置の識別情報を公開記録に含めない。

Taxion は出典付き作業承認、期待 account と対象の検査、実行上限、観測を担当する。provider login は Taxion Task の作業承認ではない。service 利用資格と課金の判断は provider に任せ、公式 API の観測と利用者が承認した利用根拠を区別する。独自の契約認証・課金システムは実装しない。

実行契約では次を区別する。

| 項目 | 必要な証拠と現在の制約 |
| --- | --- |
| 認証 | 最新の provider 照会で選択した実 account を確認する。credential の所持だけでは一致を証明しない。 |
| 認可 | 現在の Task と不変の Dispatch instruction に、選択する完全な tuple への同一の出典付き承認が必要。provider access は別に検査する。 |
| AI 利用資格 | provider が決める。Copilot の選択済み Dispatch は公式 session quota API の明示的な account access・capacity 判定と出典付き利用者の承認を照合する。OpenCode の provider access・account quota は未確認とする。`contractRef` は予定する契約を示すだけで、SDK の課金契約を選択したり、契約上の資格を証明したりしない。独自の契約認証は対象外。 |
| 契約上の支払者 | 契約または権限ある外部記録で判断し、account 名や token 所有者から推測しない。実装済み adapter では provider の支払者・invoice 情報を取得できない。 |
| 実測使用量 | token、nano-AIU、quota request percentage は別の量。取得できた call 別 nano-AIU と status を報告し、account allowance と同じ単位であることを確認できない残高・割合は unavailable とする。任意の SDK credit ceiling は session 別 soft ceiling で、project 予算や請求額ではない。 |

実験の承認時は利用資格の証拠、支払者、実測使用量を分ける。entitlement の予測や model 応答成功だけでは、どの契約が支払ったか分からない。設定承認は許可する組合せを記録し、取得できない provider 課金情報を補わない。

### 承認の対応付けと runtime identity の確認

`config check --capabilities` は credential・選択 identity なしで、静的な実装範囲を表示する。`--sources`・承認検査と併用でき、既定出力は変えない。network 照会は行わない。

| 選択 repository・runtime | Repository access の検査 | Runtime | 契約・支払者の確認 |
| --- | --- | --- | --- |
| GitHub.com + Copilot | `GITHUB_COM_GH_API` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| GitHub Enterprise + Copilot | `UNIMPLEMENTED` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| GitLab + Copilot | `UNIMPLEMENTED` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| Bitbucket + Copilot | `UNIMPLEMENTED` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| OpenCode を選択した local worktree | remote permission は `UNVERIFIED` | `OPENCODE_ACP_LINUX_ISOLATED` | `UNIMPLEMENTED` |

実装済み adapter でも、対応 credential 参照、導入済み provider CLI、実 access、現在の承認が必要となる。静的な対応だけでは実行を許可せず、未対応の repository 組合せは runtime 検査で停止する。

schema は入れ子 namespace を含む `github`・`gitlab`・`bitbucket` の repository identity と、`copilot`・`opencode` の runtime identity を受け付ける。設定の表現であり、adapter の実装範囲とは別。file 検査は network・credential 解決を行わず、承認権限、account access、契約上の利用資格を検証しない。runtime・workflow は選択 tuple と記録承認を確認し、Copilot では実 account・repository permission・provider service 状態、OpenCode では承認済み local asset と隔離条件を検査する。契約上の利用資格と支払者は未確認で、責務の区別は [#69](https://github.com/hiono/taxion/issues/69) に記録する。TOML・JSON 設定は将来の拡張候補で、既存 JSON API と機械処理用 artifact の形式は保つ。

`config check --task <task-id>` はさらに、選択 workspace の Task 内にある出典付き decision と `execution.approvalRef` を照合する。`text` に構造化した実行設定承認を保存する。

```json
{
  "kind": "taxion.execution-config.v1",
  "taskId": "task-id",
  "reason": "The user approved these identities and this contract for the task",
  "allowed": [
    {
      "version": 1,
      "project": {"id": "project-a"},
      "repository": {
        "provider": "github", "host": "github.com", "path": "owner/application",
        "account": "developer-a", "credentialRef": "gh:active"
      },
      "runtime": {
        "provider": "copilot", "account": "developer-b",
        "credentialRef": "copilot:logged-in", "contractRef": "company-contract"
      },
      "execution": {
        "requester": "developer-a", "worker": "local-worker",
        "approvalRef": "human-decision-1"
      }
    }
  ]
}
```

既存の `task track --file` で、この object を decision の `text` に JSON 文字列として保存し、`id: "human-decision-1"` と実際の利用者の判断を示す `source` を付ける。各 `allowed` entry は完全な設定 tuple で、異なる entry の account・契約を混ぜない。Task の repository path と承認参照は一致が必要で、未解決の利用者の判断と terminal Task は拒否する。検査は Task 状態を保つ。記録 decision は local な認可証拠で、このコマンドは作者認証、外部 credential 検証、runtime 操作の認可を行わない。runtime は後述の独立検査を行う。

利用者の承認の `reason` は選択 account・`contractRef` に対する用途と承認済み根拠、`source` は実 decision を示す。start・resume・send は provider 検査の前後で、現在 Task と発行済み Dispatch に同じ出典付き記録を要求する。参照だけ、または provider 照会成功だけでは利用者の承認にならない。Taxion は記録と tuple を確認し、自由文 reason の法的な真偽は検証せず、provider quota 応答から支払者を特定しない。

`config check --dispatch <dispatch-id>` は固定 Global・Project file と環境変数を、現在 Task 承認と保存 Dispatch instruction の両方に照合する。承認は Dispatch 発行時に含まれている必要があり、後から Task を承認しても既存 Dispatch の設定選択肢は増えない。欠落・停止済み Dispatch、inactive Task、repository 不一致、保存承認にない組合せは拒否する。`--task` と `--dispatch` は排他的とする。

### 保存記録への個別質問

次は Copilot 向けの例である。OpenCode では `--max-ai-credits` を指定せず、承認済み設定と明示 deadline を使う。

```bash
taxion conversation ask --task <task-id> \
  --question "Which evidence supports the current review result?" \
  --timeout 2m --max-ai-credits 30
```

独立した Orchestrator session でモデルを呼び出すが、**tool は与えない**。Task の出典付き設定承認を確認し、Copilot では実 account・repository read access・provider service 状態も照合する。送信前と待機中にも承認を再検査する。選択 tuple は Task の発行済み設定選択にも一致が必要。利用量は各 call 後に報告し、account allowance と同単位でない値から残高や率を算出しない。時間と Copilot の session soft ceiling は総請求額の厳密な上限を保証せず、OpenCode の料金・account quota は未確認とする。

応答は `confirmed`・`assumptions`・`unconfirmed` を分ける。確認済み主張は、渡した保存 Task、Dispatch、Memory version、review flow 内の解決可能な JSON pointer を引用する。記録証拠の説明であり、その真偽を独立検証したものではない。context は観測時刻と content hash を含める。回答中に記録が変わった場合は元 context を保持し、`recordsChangedDuringAnswer` を true にする。回答対象を通知なしに変更しない。context は選択 Task とその Flow に限る。

質問は Dispatch の binding、Graph 更新、feedback 解決、instruction 編集、利用者の承認取得、Coder・Reviewer 履歴への追加を行わない。失敗時も別 session ID と報告 token 使用量を結果に含め、次の有料試行前に取得可能な SDK credit status を確認できるようにする。usage 欠落、不正な引用、provider 失敗は error とする。取消後も質問 session を停止し、cleanup 失敗は明示する。128 KiB を超える入力と64 KiB を超える最終回答は拒否し、通知なしに切り詰めない。

保存記録だけを読み、追加の実調査、handoff の hold・release、回答の instruction 適用は行わない。変更には明示的な `workflow handoff`、feedback、version 付き draft、または後述の監視 console を使う。依頼から計画までの対話と追加調査の受入れは、2026-10-12 に完了した M12 の実 SDK 受入れ（#53/#54・#73/#74）で確認した。文書作業の実モデル受入れと停止後の復旧は、完了済みの #78・#79・#80 に記録した。すべての CLI 入口の実モデル実行を確認したという意味ではない。

### 保存 Flow の監視と明示的な続行

次は Copilot 向けの例である。OpenCode では Copilot 専用の credit・managed 操作・CLI override を指定せず、[OpenCode ACP の実行](#opencode-acp-の実行)に従う。

```bash
taxion conversation watch --flow <flow-id> --actor <human-id> \
  --source <actual-input-source> \
  --question-timeout 2m \
  --question-max-ai-credits 30 -- \
  --coder-prompt-file <path> --reviewer-prompt-file <path> \
  --coder-tools view,create,edit,apply_patch --reviewer-tools view \
  --max-ai-credits 30 --managed-commit --managed-stage \
  --strict-result-identities \
  --scanner /absolute/path/to/betterleaks
```

console は **入力を読む前に** handoff hold を保存する。次の role を止め、実行中 role は止めない。通常の EOF、入力 error、`/quit` では hold を維持する。`--interactive-execution` では入力を受け付けたまま実行する。この mode の EOF・終了では、この console が所有する実行に中断を要求して終了処理を待つ。他 process の実行は中断しない。timeout を承認にしない。actor・source は提供された decision metadata で、認証済み利用者 identity の証拠ではない。

`/step`・`/continue`・`続行` は表示中の引継ぎを明示的に release し、最大1 role turn を実行する。`workflow step` は console なしで同じ runner を使う。`workflow run` と異なり、step 成功時は結果を保存し、次 role を発行せず runner claim を解放する。次の明示 step が結果を使って認可済み role を Dispatch するか、レビュー・受入れが十分なら完了を記録する。Flow が running の間は step 後に hold を戻す。

`/auto` は既存 Flow の上限付き自動 mode を明示的に選び、保存 deadline・利用量の上限・修正回数を増やさない。既存の非対話 `workflow run` は変えない。選択した run 中は event を出力し、実行中 role を続けたまま別 console・process から hold を保存できる。hold は取消ではない。`/interrupt`・`いま止める` は表示中の実行に停止を要求する。要求の保存だけで停止確認や Task 取消にはならず、所有 runner の結果と正式 Dispatch 状態を確認する。Copilot の別 process から保存 session を取り消す場合は `runtime stop` を使う。OpenCode は別 process の停止接続に対応しない。

`/draft TEXT` は version 付きの補足 proposal を記録・修正する。`/approve` は不変の契約内で表示中の pending proposal だけを承認し、`/discard` は明示的に破棄する。承認は hold を解除せず、Dispatch も発行しない。後の明示続行が既存 transaction 検査の下で補足を発行する。scope、acceptance、identity、契約、上限の変更は補足承認で再認可しない。成功した保存結果より前の proposal はまだ承認できない。

その他の文章または `/ask TEXT` は、別の質問上限を要求する独立した保存記録質問 session に渡す。draft・承認にはしない。`/hold`・`保留` は handoff hold を維持する。続行は表示 Dispatch 対象、role、結果、最新 draft、contract hash、hold version を transaction 内で照合する。対象変更は拒否し、新 role に接続し直さない。command 失敗を明示し、同意とは扱わない。この console は契約変更の再承認、指定した停止句以外の一般的な自然言語による取消、依頼から計画への intake をまだ実装しない。

`runtime`・`workflow run|step` の `--strict-result-identities` は、最終 response schema の Dispatch ID、Task ID、role、repository、実行開始 SHA を不変 instruction に固定する。修正開始 SHA も対象とする。output SHA、承認、verdict は推測せず、Controller は報告事実を独立して検証する。この option は SDK config hash を変えるため、同じ session の実行・観測への接続にも指定するが、`runtime stop` には指定しない。既定 schema は変えず、旧 session は元 option で接続できる。拒否された response を通知なしに修復したり、作業成功と扱ったりしない。

`config bind --dispatch <dispatch-id>` は、承認済みの完全 tuple、decision 参照、field 出典、記録時刻を Dispatch とともに SQLite へ保存する。現在 Task と発行済み instruction の両方が tuple を認可している必要がある。選択後は、同じ承認に含まれる別 tuple も `config check --dispatch`・`runtime auth` が拒否する。同一値での再 binding は元出典と時刻を維持し、出典だけを変えても保存選択を置換しない。

初回 runtime session 前の実行可能 Dispatch にだけ binding できる。選択を持たない既存 session に後付けせず、新たに認可した Dispatch を発行する。選択なしの旧記録は読める。この command は候補選択の保存だけで、actor 認証、credential 解決、契約資格の証明はしない。runtime は後述のとおり選択を SDK session hash に含める。

`config check --dispatch` は単一の read-only workspace snapshot を使い、session を作らず、actor を認証しない。成功出力でも未確認の範囲を明示する。

以下の SDK 認証・provider 照会・session 接続・SDK tool・credit ceiling は Copilot 用である。Task・Dispatch の設定 binding と Reviewer の事前承認は共通だが、OpenCode の接続・権限・利用量は次の専用節に従う。

`runtime auth --dispatch <id> --system-prompt-file <path>` は同じ固定設定を解決し、Copilot 接続前に現在 Task と保存 Dispatch の承認を要求する。runtime の `--cli`・`--base-directory`・割当 worktree を使うが、SDK の認証状態 RPC だけを呼び、session 作成・resume と model request は行わない。報告 account は `runtime.account` と一致が必要で、GitHub account では大小文字を区別しない。認証 host は明示的な `github.com` または SDK の HTTPS github.com origin だけを許し、末尾 slash は任意とする。別 scheme・port・path・user 情報・query・fragment は拒否する。field 欠落、未認証、不一致、状態取得不能は error となる。

選択済み設定の `runtime.credentialRef: copilot:logged-in` は SDK の保存 user 認証を選び、子環境から周囲の Copilot・GitHub token 変数を除く。`env:UPPERCASE_NAME` は指定環境 credential だけを使い、保存 user への fallback を無効にする。欠落・空・不正値は SDK client 起動前に拒否する。credential 喪失後も safe stop が取消意図を保存できるよう、offline の adapter 構築は可能とする。保存 session への接続失敗時は Dispatch が `CANCEL_REQUESTED` のまま残り、停止確認とは扱わない。credential 値は設定・session hash に保存しない。同じ参照の値は新 adapter の構築で変更できるが、実 account と既存承認は再検査する。旧任意参照は互換性のため従来の周囲の SDK 認証選択を保ち、credential store の解決は未実装。契約資格や課金は検証しない。返す認証 error に provider 状態 message と詳細は含めない。結果は一時点の観測で、実行認可として保存せず、既存 session hash も変えない。start・resume・send と safe stop はこの明示照会とは別操作とする。

`runtime start|resume|send|events|commit` は adapter 作成前に固定設定を解決・検査する。新 Dispatch が未 binding なら承認済み選択を自動保存する。選択なしの既存 session は safe stop し、新たに認可した Dispatch に置き換える。通知なしに新設定へ移行しない。

runtime・workflow の共通 worker 構築は保存済み完全 tuple を adapter に渡す。session config hash に tuple を含め、field 出典と記録時刻は含めない。start・resume・各 instruction 送信は現在・保存承認を確認し、SDK session 操作前に実 Copilot account と repository API access を検査する。その後、新 session binding 保存時と実行中への遷移前にも承認を再検査する。選択済み Dispatch は管理下の Git 認可 transaction 内でも tuple・承認を再確認する。契約資格の証明や、任意の保護された credential store 参照の解決はしない。

選択済み Dispatch は session 作成・接続後と各 instruction 送信前に provider の session quota API を照会する。Copilot adapter は、取得成功した account snapshot に、明示的な許可または quota 不要の access と、available・unlimited・not-required・not-applicable の capacity を要求する。観測欠落、拒否、枯渇、不明状態、照会失敗は実行を止める。数値残量、plan 名、model 一覧は判定の代用にしない。拒否された新 session は binding 前に切断し、resume 済み session は binding を保って safe stop できる。過去 turn の admission 観測を account snapshot に再利用しない。model を呼ばない一時点の provider 観測であり、次 request の受付保証、課金契約の検査、使用上限ではない。実 request の provider error を最終の判断とする。既存 access receipt は先行する認証・repository 検査だけを示し、service・支払者の確認を推論しない。設定なしの旧 Controller は従来動作を保つ。

Repository access は現在 GitHub.com の `gh api` に対応する。`repository.credentialRef: gh:active` は保存 `gh` login を使い、子 process 環境から周囲の GitHub・Copilot token と host override を除く。`env:UPPERCASE_NAME` は指定 credential だけを `GH_TOKEN` として渡し、保存 login へ fallback しない。欠落・不正値・非対応参照は error となる。各検査で `user` と正確な repository を照会し、期待する `repository.account`、一致する `full_name`、明示 read permission を要求する。Coder は write permission も必要で、他 role は read permission を要求する。permission 不明、identity 不一致、`gh` 欠落、provider error、非対応 host・provider は実行を止める。この検査の GitLab・Bitbucket・GitHub Enterprise は schema での表現だけにとどまる。

これらの read-only API 観測は Git SSH・HTTPS push の identity や local commit 作者の証明ではなく、公開操作もしない。成功検査は local Dispatch の `configuration.accessChecks` に、要求操作、runtime config hash、観測区間、role permission、`contractEligibility: UNVERIFIED` とともに追記する。不変の選択は期待 repository・runtime account、project、依頼者、credential・契約参照、承認を識別する。receipt は access 一致を示すだけで、SDK session 作成・実行成功の証拠ではない。失敗検査は成功 receipt を追加せず error を返す。保存 transaction 内でも承認を再検査する。初回 receipt は session 作成前に確認済み runtime hash を固定し、競合設定で同じ Dispatch を共有させない。runtime 設定の変更には新たに認可した Dispatch が必要となる。

receipt は観測であり、再利用する認可ではない。再起動後も start・resume・send は外部検査を繰り返す。`accessChecks` がない旧記録は読めるが、過去検査の証拠を持たない。選択・session hash の形式は変えない。`task graph`・`task plan` は保存 Dispatch を通して記録を含める。workspace database とともに local 保管し、公開 usage・課金 receipt としない。safe stop に repository access は不要で、明示 `runtime auth` は Copilot 認証だけを検査する。次の read-only 実接続検査は model を呼ばない。

```bash
TAXION_GITHUB_INTEGRATION=1 \
TAXION_TEST_GITHUB_ACCOUNT=<account> \
TAXION_TEST_GITHUB_REPOSITORY=<owner/repo> \
go test ./internal/forge -run '^TestLiveGitHubRepositoryAccess$'
```

`runtime stop --dispatch ID` は保存 session ID、role、worktree、config hash、選択 credential 参照を使う。元 prompt・tool allowlist・scanner 設定の再生成、現在の固定設定の読込み、認証照会は要求しない。任意の `--cli`・`--base-directory`・正の `--timeout` で接続方法を選び、`--system-prompt-file`・`--tools` などの実行 option は拒否する。停止専用 adapter は session 作成、instruction 送信、実行 resume ができない。pending work の続行、resume event、transcript 修復、discovery、tool を明示的に無効にし、全 permission 要求を拒否する。この process が実行中 turn を観測していなくても、切断前に abort を要求する。model instruction は送らない。旧実行契約でも元権限を広げずに使える。接続・取消失敗では停止成功を記録しない。credential 取得不能や transcript 破損は引き続き調査が必要で、credential の通知なしの切替や過去証拠の修復はしない。設定なしの local・proc Controller は従来動作を保つ。

`workflow run` は全 worker に同じ選択・認証検査を適用する。`workflow run|step` は session 作成前に、実装 Task と `maxRepairs` までの今後の全候補 Reviewer Task に選択設定を照合する。review 認可の欠落を Coder turn の費用発生前に報告し、設定 binding・Flow 変更は行わない。生成 Task ごとに、その Task を名指しする承認が発行 instruction に必要となる。親 Task 承認を生成 Reviewer 用に通知なしに書き換えず、Task 固有の認可欠落は Flow を止める。元承認の任意 field `additionalTaskIds` で対象 review Task を認可する。`application-flow`、`maxRepairs: 1` なら、前述の完全承認 object に次の field を追加し、`taskId` を実装 Task ID として、発行前に両候補を承認できる。

```json
{
  "additionalTaskIds": ["application-flow-review-0", "application-flow-review-1"]
}
```

`allowed` は完全な設定 object のまま保つ。review ID は `<flow-id>-review-0` から `<flow-id>-review-<maxRepairs>` までの完全一致で、修正 Coder は実装 Task ID を保つ。承認する review Task だけを列挙し、空 ID、空白、pattern、重複、主 Task ID は拒否する。省略時は従来の単一 Task 範囲を保つ。別 Flow、未列挙の再レビュー、似た prefix の ID は認可しない。

Controller は出典付き decision を変更せず生成 review Task と Dispatch instruction にコピーする。core と設定層は Task の完全一致 membership と同じ完全 allowed tuple を検査する。認可対象は設定選択だけで、Flow 上限、review scope、role 隔離、操作別認証は引き続き適用する。発行後に名前を足しても既存 Dispatch は拡張しない。新 Flow を始める前に利用者の認可を記録する。

Runtime command は active な Task Graph Dispatch と role 専用 system prompt file を要求する。Task の worktree と Dispatch recipient が role・workspace を選ぶ。保存 snapshot を永続 session 契約として role prompt に追加し、turn と SDK compaction をまたいで system context に保つ。SDK tool は明示 `--tools` allowlist を要求する。permission handler は Coder に割当 worktree 内の書込みと許可済み SDK tool の一回使用を許し、Reviewer には worktree の read access を個別承認する。相対 read path は割当 worktree を基準に解決する。worktree 内の既存 directory にある未作成 leaf も許し、tool が通常の file 不在を報告できるようにする。dangling symlink、範囲外 path、解決できない親 directory は拒否する。handler は SDK permission 要求に適用し、host・外部 tool は独自の保護を使う。resume は prompt、CLI、worktree、tool 設定を保存 hash と照合する。

`runtime`・`workflow run` の `--max-ai-credits` は任意の SDK session soft ceiling を設定する。既定のゼロは未設定で、その他は有限かつ30以上が必要。start・resume で同じ上限を SDK に渡し、保存 session hash に含める。送信、resume、観測、commit にも同値を指定するが、`runtime stop` には不要。省略・変更で既存 binding を通知なしに変えられない。

SDK の session credit ceiling は応答後に判定される soft ceiling で、総費用を抑える保証ではない。credit と token は別の単位。`workflow run` の ceiling は各 Coder・Reviewer session に独立して適用し、**Flow 全体の合計予算ではない**。個人の課金 receipt を repository 文書・実行証拠に公開しない。

`task plan --repo <repo>` は SQLite Task Graph と参照 Memory から後続操作を再構成する。延期 Task、その出典と再開条件、既存 decision、各 Dispatch の発行契約と結果を保つ。未解決の利用者の判断、延期作業の再評価、証拠収集、review の照合、実行候補を分ける。計画生成は保存 Task 状態と認可を変えない。同じ出力は session 再生成後も `GET /api/tasks/plan?repo=<repo>` と ACP `taxion/plan` の `{"repository":"<repo>"}` から取得できる。

### OpenCode ACP の実行

`runtime.provider: opencode` は承認した executable と server 設定を使う Linux 専用の
ACP client adapter である。モデル・認証・quota を Taxion から操作しない。
v0.4.0 では CLI 製品版の完全一致を求めず、ACP v1 と必要な session 応答を接続時に確認する。
実行ファイルの内容 hash 固定を省略すれば、同じパスでの CLI 更新だけでは再承認を求めない。
Taxion は起動するファイルを複製し、実際の hash と pin の有無を記録する。
`config check --capabilities` は静的範囲、`doctor` と `runtime auth` は local asset の照合を
表示する。これらの成功は provider 認証・remote repository 権限・課金資格の証明ではない。

`request`・`workflow run/step`・`acp` の worker 選択は共通である。Copilot 専用の
非ゼロ `--max-ai-credits`、`--cli`、`--base-directory`、managed commit・stage、
`conversation search --sdk` は拒否する。明示 deadline と修正回数を承認し、
host の仕様検索・validation と同一 tree の独立レビューを使う。
`runtime start` は起動した所有 process 内の turn を実行するが、別 process の
`resume`・`send`・`events`・`stop` で session を attach することはできない。
停止は所有 runner で行い、終了と部分成果を照合して新 Task で継続する。

承認した file の複製と skills だけを agent に渡し、Reviewer は OS で read-only にする。
shell・MCP・実行型 plugin・RTK は未対応。native `ask` は client が `cancelled` にし、
tool 定義の存在から実行許可を推定しない。最新 assistant message の usage は
`latest-assistant-message` として保存し、turn/Flow 全体や account quota と合算しない。
準備・切替・export 失敗時の確認は[OpenCode 設定](configuration.md#opencode-acp-の設定と制約)を参照する。

### 上限付き review flow

`gate contract --task ID` は新規開始専用で、READY 要件を維持する。`gate resume --flow ID --json` は保存済みの ACTIVE Task / RUNNING Flow と、発行済み未実行の Coder Dispatch を対象にした別の read-only Contract Gate である。runner claim、acknowledge、execution start、runtime session、hold、未発行 draft がある場合は拒否する。Task / Flow / Dispatch、contract / authorization hash、承認 bundle、task-bound configuration、endpoint、canonical packet、Git baseline / branch / start tree、他の active operation、保存済み予算・counter・期限を照合する。診断 code は `resume-` で始まり、評価で Task や実行状態、Git index、packet、budget を変更しない。

`workflow resume --id ID` は `workflow run` と同じ role prompt / tool option を受け付ける。resume Gate の成功後、runner claim の transaction 内でも同じ条件を再確認し、既存 Dispatch を再作成せず通常の runner へ渡す。合格 report は所有権や新しい実行承認の代わりではない。別の operation に所属する Flow は、その operation の正式な所有経路なしでは実行できない。既存の `workflow run`、`workflow step`、`request --resume` の構文と継続動作は変更しない。

Git 観測では `assume-unchanged` / `skip-worktree` など、追跡 file の変更を隠す index flag がある場合も拒否する。flag や index を自動変更して通過させず、実行属性・symlink・stat の検査も省略しない。追跡 file に有効な `filter` 属性がある場合は、観測中に外部 clean / process command を起動する可能性があるため拒否し、属性や filter 設定を変更しない。各 Git 観測の出力上限は 64 KiB で、超過時も拒否する。

resume 用 binding は新しい Dispatch の append-only budget audit に version 1 として保存する。contract / authorization / instruction hash、Dispatch ID、開始時刻・期限・上限と、同時点の利用量・counter を照合する。binding のない旧 audit は補完・上書きせず `resume-budget-binding` で拒否する。canonical packet artifact のない旧記録も検証済みとして扱わない。この Gate は acknowledged / 実行中 session、結果から次 role を発行する段階、STOPPED Flow の復帰には使わず、それぞれ既存の停止・結果照合・後続契約の手順を使う。snapshot mode では、承認済み start tree と index / tracked worktree が一致し、未記録 file がないことも必要になる。

`workflow start` は、明示的な修正回数と経過時間（`timeoutSeconds`、省略時は1時間）を持つ認可済み Coder 契約を保存し、初回 Dispatch を発行する。`workflow advance` は最新の検証済み結果から独立 Reviewer または同じ受入れ条件と出典付き finding を持つ修正 Dispatch を発行する。現在の output SHA の承認で実装 Task を COMPLETED にする。`workflow run` は隔離した Coder・Reviewer SDK adapter を作り、指示・結果のコピーなしに SQLite の Dispatch を順に実行する。usage event 後に利用量を保存・報告し、契約・Memory の変更を監視して、保存 deadline で runtime を止める。token 閾値では停止しない。`--coder-tools`・`--reviewer-tools` は明示 allowlist のまま保つ。永続 runner claim は同時 run を防ぎ、中断 process の claim は明示的な照合なしに再利用しない。旧 process の終了と、その Dispatch の完了・取消を確認した後、`workflow recover --file <path>` で観測 runner ID を比較し、actor、理由、証拠を記録して所有を解放する。保存契約と結果は後続操作にも適用する。認可・停止条件は [Task Graph](../develop/task-graph.md) を参照する。

v0.7.16 の共通 runner は Reviewer の起動前に `review_budget` event を一度出力する。
`payload` の version 1 は `flowId`、`dispatchId`、`observedAt`、`flowDeadline`、
`effectiveDeadline`、`deadlineSource`、`remainingMilliseconds` を持つ。
期限は保存 Flow と呼出し元 context の短い方で、出典は `flow` または `caller` となる。
残り時間は観測時点の値であり、レビューに十分な時間があるという予測や、開始時の新しい時間枠ではない。
表示に失敗した場合は Reviewer を起動しない。期限や credit 上限を延長せず、
追加のモデル呼出しや account 残量の取得も行わない。

`workflow handoff --file <path>` は既存 Flow に出典付き `HOLD`・`RELEASE` を保存し、`expectedChangeId` を最新 handoff change と比較する。hold は次 Dispatch の発行と未開始実行を止め、実行中 role の認可済み作業と最終結果は保つ。runner は引継ぎ時点で handoff-held error を返し、現在の結果を取り消さず claim を解放する。明示 release は不変の契約と上限で後の `workflow run`・`advance` を許すが、作業を直接開始せず、safety stop の Flow も復帰させない。request 形式と競合の制約は Task Graph に記載する。

Controller が停止時に作る `HOLD` の理由は、前後の空白だけを除いて記録する。Flow の停止理由には元の文字列を残し、`HOLD` の保存が失敗した場合も、そのエラーに元の理由を含める。これは停止理由の保存処理の修正であり、Git の再検証や受入れ条件を省略するものではない。保存済み Reviewer 結果が `APPROVE` でも、Flow が `STOPPED` なら通常の `advance`・`recover`・`handoff RELEASE` で完了へは進まない。後述の受理専用操作は、理由消失の不具合で停止した限定条件だけを対象にする。

`workflow feedback-target --id <flow-id>` は最新 Dispatch の instruction と結果 identity を読む。`workflow feedback --file <path>` はその完全一致対象を比較し、出典付き利用者応答と handoff hold を一つの workspace transaction で保存する。元結果、verdict、認可を保ち、文章を自動送信したり契約変更の承認と扱ったりしない。古い対象は明示 error とし、feedback・hold を保存しない。対象変更に関係なく次引継ぎを保留する場合は `workflow handoff` を別に使う。

未解決 feedback は running Flow の release を止める。`workflow feedback-resolve` は最新観測対象と hold ID に出典付き `KEEP_CONTRACT` を記録する。release 前に全 pending feedback を解決する。解決自体は hold 解除も prompt 送信もしない。この初回処置は承認契約を保ち、新 instruction への feedback 適用は行わない。safety stop の Flow を release しても実行権限は戻らない。

`workflow draft` は新 ID、期待する前 draft ID、観測対象、現在 hold ID を持つ未発行 proposal を保存する。各版の文章、次 role と操作、証拠、未確認項目、feedback 参照を保ち、最新の未破棄 draft も release を止める。`workflow draft-discard` は正確な draft に出典付き破棄 decision を記録するが、feedback 解決・hold 解除はしない。これらの操作だけでは proposal の承認・適用・Dispatch 発行はしない。

`workflow draft-approve` は成功した Coder→review・review→修正の引継ぎで、最新 draft に `APPROVE_SUPPLEMENT` を記録する。正確な draft、結果、hold、不変の Flow 契約を対応付ける。明示 release 後の `advance`・`run` は次 Dispatch の `DECISIONS` に補足を含め、instruction hash を atomic に保存する。参照 feedback は発行前に `APPROVED_FOR_HANDOFF`、発行後に `INSTRUCTION_PUBLISHED` となり、どちらも worker の確認実施を証明しない。scope、acceptance、role、実行設定、予算変更は補足承認の対象外。自然言語での介入、契約変更の再承認、個別質問は #73/#74、Controller 管理の Coder Git 操作と実 SDK 受入れは #53/#54 として、2026-10-12 に完了した M12 の実 SDK 受入れで確認した。文書作業の実モデル受入れと停止後の復旧は、完了済みの #78・#79・#80 に記録した。すべての CLI 入口の実モデル実行を確認したという意味ではない。

Reviewer Dispatch の Task worktree は新しい絶対 path にする。`--review-source-worktree <existing-repository-worktree>` で開始し、保存対象 SHA の独立した detached worktree を作る。commit mode の完了処理は SDK 接続を閉じ、clean な worktree を削除してから review 成功を保存する。dirty・locked・未解放 worktree は明示 error として review を未完了にする。

snapshot mode は baseline commit の worktree に対象 tree を再現するため、baseline に対する差分がある。終了時は対象 tree との一致を検証してから、その Controller 管理の worktree を削除する。commit mode の clean 条件と混同しない。tree の保持・照合は [Runtime 契約](../develop/runtime-contract.md#dispatch-の実行と-context)を参照する。

usage event は選択 model と取得できた token・cache 件数を SQLite に保存し、SDK 測定の欠落は不明のまま保つ。infinite session の compaction を有効にし、context と結果の event を報告する。

role 別の file tool allowlist は既定で空。通常の native skill 利用には `skill` を追加するが、repository file の読取り・編集権限は増えない。通常の code・文書変更には `--coder-tools view,create,edit,apply_patch` を指定し、独立 Reviewer は `--reviewer-tools view` の read-only にする。

Task の `fileAccess` は利用者が別途承認する SDK file permission の範囲で、SDK tool の許可や認可済み `scope` の拡張にはならない。`{"level":"exact","paths":["src/file.go"]}` は指定した正規化済み repository 相対 file だけを許し、指定済みの新 file も含む。新 Task の既定 `{"level":"committed"}` は Dispatch 開始 SHA で追跡済みの path を許す。`{"level":"all"}` は割当 worktree 全体の file・directory と新 file を許す。全 level で worktree 外と `.git` を拒否し、Reviewer は read-only を保つ。file permission callback と Git 結果検査は同じ保存 policy を適用する。`fileAccess` なしの旧 Task・Dispatch は従来動作を保つ。intake proposal で確認・承認してから confirm する。

Task の `validationCommands` は Controller の検査操作を認可する。新 Coder Dispatch は Conventional Commits の subject と contextual body を要求する。body は `intent(scope):`・`decision(scope):`・`rejected(scope):`・`constraint(scope):`・`learned(scope):` の action line で実際の理由を記録し、任意の Git trailer を続ける。Controller は結果受入れ前に保存実行開始 SHA 以降の全 commit を確認し、独立 Reviewer は理由と作業の一致を確認する。turn 終了時に構造化応答を検査し、repository・worktree・SHA・scope を実 Git で照合して、割当 worktree で認可 command を実行し、観測出力と完了を SQLite に保存する。検証 command は Controller の host 権限で `sh -c` として実行するため、Task 認可時にその作用も承認する。明示的な `dispatch transition --result-file` も同じ検証を行い、`--validation-timeout` は既定10分となる。

`taxion records proposal|approve|send|cancel|reconcile|show|finding` は承認済み GitHub / GitLab 公開と出典付き失敗分析を管理する。承認は正確な対象・account・content hash とプロジェクトの Communication Profile を対応付ける。`TAXION_COMMUNICATION_PROFILES` で対象プロジェクトの設定ファイルを明示し、`update.statement.at` に記述時点、`update.statement.claims` に参照 URL とその状態を指定する。未設定・対象不一致・未確認の参照は送信できない。形式と更新時の再承認は[公開文章の検証手順](../develop/public-communication-validation.md#プロジェクト設定と-forge-共通検査)を参照する。送信不明は停止済み sender の claim 照合まで明示して保つ。`taxion git check-range --base <SHA>
--head <SHA> --scanner <PATH>` は公開前に未公開の全 commit tree と message を scan する。[実行記録](execution-records.md)を参照する。

#### 工程計画と保存観測

`workflow plan --file flow.json` は、READY Task と実行要求から工程計画を読取り専用で作る。実行要求は `workflow start` と共通で、`stageBudget:{"version":1}` を追加する。返却する `stateChanged` は `false`、`stagePlan.hash` は承認対象の完全なハッシュである。Task、要求、設定、見積もり結果が変われば、古いハッシュを使った開始を拒否する。

返却する `contractGate` は、その時点の承認束の照合結果である。ハッシュが返っても実行可能とは限らない。承認束が不足していれば `task authorize --manifest FILE` の確認トークンを提示し、明示承認後に `--confirm TOKEN` で保存する。役割間の委譲には別の `task authorize-packets --request FILE` の事前確認と承認記録を使う。計画の承認は承認束や役割間委譲を作成しない。これらの操作で Task・設定・要求が変わった場合は計画を再作成し、新しいハッシュを承認する。

要求の `authorization.text` もハッシュの対象である。事前確認だけを許可する文言を実行許可として使わない。必要なら実行条件を正しく記述してから再確認し、新しいハッシュを承認する。停止済み Flow の記録は書き換えない。文言だけから許可を自動解釈する仕組みではない。

承認後は同じ入力に `stagePlanHash` を追加して `workflow start --file flow.json` を実行する。計画を省略した既存 Flow と `request` は従来動作を保ち、後続工程の時間予約を行わない。外側のオーケストレータが JSON を作成しても、利用者の承認を代行することにはならない。

| `stageBudget` の項目 | 意味 |
|---|---|
| `version` | `1` を指定する |
| `validationSeconds` | 各実装提出後の検証に使う秒数。未指定はコマンドごとに60秒と保存実績から見積もる |
| `reviewSeconds` | 各独立レビューの予定秒数。未指定時の初期値は600秒 |
| `repairSeconds` | 各修正の予定秒数。未指定時の初期値は600秒 |
| `coderSessionCredits` / `reviewerSessionCredits` | 各役割のセッションのソフト上限。ゼロまたは未指定は設定なし。指定時は30以上 |
| `reviewNanoAiu` / `repairNanoAiu` | 正の全体 `maxNanoAiu` がある場合だけ使う、各レビュー・修正の内部残額枠。必要な役割の正の見積もりが保存実績にも明示指定にもなければ計画を拒否する |

時間の見積もりには、同じリポジトリと検証コマンドの直近の成功記録を最大20件使う。初期値と、保存した時間に20%の余裕を加えた値の大きい方を採用する。明示指定と見積もりの出典は `basis` に残す。全体期限に必須工程が入らなければ、範囲の分割か新しい外枠の承認を求め、Dispatch を発行しない。時間指定と全体期限は最大1年、修正上限は計画付き Flow では1000回までとする。

実装の提出期限は、元の全体期限から後続の検証・レビュー・許可済み修正分を引いて求める。提出後の検証には検証分だけを使い、残る役割分へ食い込ませない。再接続・再起動・`step` でも元の期限を保つ。境界後の結果は受理せず、部分成果と停止証拠を保存して `BUDGET_STOP` にする。計画付き Flow の成功結果は、汎用の `dispatch transition --result-file` では受理せず、対応する実行環境の終了処理を通す。

計画付きの `run`・`step`・`resume` は保存した役割別ソフト上限を適用する。共通の `--max-ai-credits` が両方の承認値と一致しない場合は拒否する。提供元のクレジット予約や厳密な請求上限の保証ではない。利用額の内部枠は完全な同じ設定の保存使用量か明示値だけを使い、未計測値を換算して埋めない。提供元停止方式には新しい残量照会を追加しない。

`workflow budget --id FLOW-ID` は状態を変更せず、工程の開始・提出・検証・結果受理の観測時刻、適用したソフト上限、利用量の単位・範囲・完全性、停止理由、残る受入れ条件と保存された実装 tree を返す。時間はモデル内部時間ではなく、保存時刻に基づく。保存 tree の表示は現在の Git 同一性や品質検証の合格を意味しない。

計画付き後続 Flow には `continuationChange` と `savedOutputDecision` を追加し、前回から完了可能性を変える条件と、保存成果を引き継ぐか基準から再実装する理由を記録する。この説明だけでは新しい作業を承認しない。旧作業場所と証拠を保持し、既存の後続 Task 承認・成果照合を行ってから、新しい計画を承認する。

保存済み計画は発行監査と固定照合する。既存データへの遡及適用や計画の削除は行わない。計画・工程監査を含むデータを旧版で更新せず、戻す場合は現状を保護して更新前のバックアップを別配置で使う。詳細は [ADR-0053](../develop/adr/0053-enforce-approved-flow-stage-boundaries.md)。

#### 停止後の保存済みレビュー結果の受理

`workflow accept-saved-review` は、保存済み実装結果をレビューする Flow が、引継ぎ保留の入力検証エラーで停止理由を失った場合の受理専用操作である。新しい Flow・Dispatch・モデル session を作らず、検証 command も再実行しない。対象は `continueFromDispatchId` を持つツリースナップショット形式で、Dispatch が成功した Reviewer の1件だけ、判定が `APPROVE`、指摘・阻害事項が0件の `STOPPED / SAFETY_STOP` に限る。別の停止理由や通常の修正フローには使えない。

配布物の照合、必要な provenance policy の登録、`ready` の確認後、同じ設定環境で次の承認ファイルを用意する。これは保存結果の受理だけの明示承認であり、実行・予算・公開の承認ではない。`actor`・`source` は利用者の判断と出典を記録するもので、認証用の資格情報ではない。

```json
{
  "flowId": "<saved-flow-id>",
  "actor": "<approver>",
  "source": "<approval-reference>",
  "reason": "期限前に保存された独立レビュー結果だけを受理する",
  "allowExpired": true
}
```

```sh
taxion workflow accept-saved-review --file acceptance.json --preview
```

preview は workspace と Memory を読取り専用で開く。`stateChanged:false`、対象 Task・Reviewer Dispatch・完全な SHA と tree SHA、保存結果と instruction の hash、元の期限、期限超過の有無、確認できた検査と予定する遷移を表示する。失敗時は理由を示して非ゼロ終了し、受理しない。`allowExpired:true` は、期限前に成功結果を保存した Controller の receipt が確認できる場合に限り、期限後の受理だけを許す。期限そのものは延長せず、期限後に保存された結果は拒否する。

確認後、同じファイルに preview の `hash` を `expectedHash` として追加し、`--preview` なしで実行する。ファイル中の判断、設定、保存状態が変わった場合は再び preview から確認する。適用時も Git を含む全条件を再検証する。

```sh
taxion workflow accept-saved-review --file acceptance.json
```

成功時は同じ Flow を `COMPLETED`、実装 Task を `APPROVED` → `COMPLETED` にする。Reviewer Task は結果保存時の `BLOCKED` と `Dispatch ended: SUCCEEDED` をそのまま保持し、preview の `reviewerTaskFinalStatus` も `BLOCKED` となる。Reviewer Task の再実行や未実施作業の完了を記録する操作ではない。

元の HOLD は解除・削除しない。元の停止状態・理由・期限・HOLD 履歴を含む Flow 全体、結果と preview の hash、actor・source・reason、期限後受理の承認を `reviewAcceptances` へ追記する。`originalReasonUnavailable:true` は元の理由が二次エラーに置き換わって失われたことを示し、推定原因を確定した記録にはしない。

元 Coder の worktree は `detached HEAD` でも、そのコミット・index・実ファイル・snapshotRef と保存 tree の一致を確認する。現在の実装 Task の worktree は承認済みブランチを要求する。両者は同じ Git オブジェクトデータベースに属し、解放済み Reviewer の path が存在しないことも必要になる。隠し index flag、有効な Git filter、未記録 file、契約・設定・承認・packet・Memory の不一致、残存 runner、実行中 Dispatch・operation、未解決 feedback・draft は拒否する。

受理前には workspace・Memory・Git の元状態を保護する。受理は過去の停止を取り消す操作でも、公開許可でもない。受理記録を作った DB を、この記録を理解しない旧版で開いて更新しない。rollback が必要なら現 DB を保持し、受理前のバックアップを別配置で使う。DB の手修正、元 worktree のブランチ付け替え、再レビューによる回避は前提にしない。

CLI は local-first とする。`taxion serve` は HTTP API、`taxion acp` は stdio の ACP、`web/` は browser console を提供する。

## 停止済み初回指示の整合回復

開発版の `workflow reconcile-stopped` は、`STOPPED / SAFETY_STOP`・`HOLD` の Flow と、開始記録のない初回 snapshot Coder の `ISSUED` Dispatch を整合させる。明示的な同じ `--store` を維持する。次は形式の例であり、実際の確認や承認を表さない。

```json
{
  "flowId": "stopped-flow",
  "dispatchId": "stopped-flow-dispatch-0",
  "actor": "human",
  "source": "human:reconciliation",
  "reason": "配送不明を保持して指示を取り消す",
  "evidence": ["人間が継続中の実行がないことを確認した根拠への参照"],
  "noActiveExecutionConfirmed": true,
  "acceptDeliveryUncertainty": true
}
```

```sh
taxion --store /explicit/workspace/ledger.json workflow reconcile-stopped --file reconciliation.json --preview
# 提示した判断と hash の明示承認後、同じ JSON に expectedHash を追加する
taxion --store /explicit/workspace/ledger.json workflow reconcile-stopped --file reconciliation.json
```

preview は `version:1`、`stateChanged:false`、`inputContractHash`、`inputTreeSha`、`inputOrigin:"approved-predecessor-input-contract"`、`deliveryStatus:"unknown"`、`usageStatus:"unavailable"`、予定遷移と `hash` を返す。適用は同じ状態と判断を再照合し、取消・Task の `BLOCKED` 遷移・監査追記を一括保存して `stateChanged:true` を返す。重複適用や変更したハッシュは拒否する。元の Flow・停止理由・承認・期限・予算は維持する。外部処理の停止や未配信・無課金を証明する操作ではない。

後続入力は元の承認済み tree であり、新成果ではない。同一 repository の別 worktree に同一の commit・tree・変更 path を保全し、新しい `conversation intake` の Task に `createdFrom:"flow:stopped-flow"` と `supersedes` を設定する。intake request と `conversation confirm` request の両方に同じ `reconciliationHash` を渡す。confirm には新提案の `expectedHash`、`replacesFlow`、取消後に観測した正確な `target`、新しい設定承認も必要になる。対象が変われば拒否する。READY への確認は実行開始ではなく、承認束・委譲・工程計画と利用量を含む実行前検査は別途必要である。

session・開始・結果・runtime receipt がある場合、入力が変わった場合、他の実行が稼働中の場合には使えない。人間の確認ができない場合も停止を維持する。通常取消だけではこの入力継承の根拠にならない。手順の判断基準は[失敗からの復旧](failure-recovery.md#flow-が停止している)を参照する。

## 全コマンドと option の索引

v0.7.16 の Copilot `workflow run|step|resume` と `runtime` は、`--review-progress` で
Reviewer の途中確認記録の保存を明示的に有効化できる。再接続では同じ option を指定する。
記録は最大32件、1件64 KiBまでで、対象の指示・commit・tree に結び付ける。
新たに承認したレビュー専用継続へ自己申告の確認範囲を渡し、レビュー承認とは区別する。
OpenCode は非対応。停止原因と記録欠落時の扱いは[失敗からの復旧](failure-recovery.md#flow-が停止している)を参照する。

| 追加コマンド | option と制約 |
| --- | --- |
| `gate resume` | `--flow ID`、任意の `--json`。保存済み未実行 Coder 境界を read-only で照合する。新規開始の READY 要件は変更しない。終了 code は 0 pass・1 fail・2 blocked。 |
| `workflow resume` | `--id FLOW_ID` と `workflow run` と同じ runtime / role prompt / tool option。resume Gate と transaction 内の再照合後、保存済み runner 経路へ進む。拒否時は実行 record を変更しない。 |

`taxion --help` は command 名と主用途を表示する。次の索引は実装済み subcommand と option を示す。`--store PATH` は command より前に指定し、旧 ledger のパスで workspace を選ぶ。同じ directory の `workspace.db` が正本となり、DB ファイル自体を `--store` に指定することはできない。各操作には表の名前付き option を使う。

runtime は固定設定の `runtime.provider` で選ぶ。`--cli`・`--base-directory` は Copilot の executable・state directory を指定する option で、provider を切り替えない。OpenCode はこれらの override、非ゼロの `--max-ai-credits`、managed commit・stage を拒否する。別 process の session 接続と停止も未対応であり、[OpenCode ACP の実行](#opencode-acp-の実行)の境界を適用する。

| コマンド | option と意味 |
| --- | --- |
| `role-packet create`, `role-packet verify` | `create` は `--task ID`・`--task-hash SHA256`・`--baseline-sha SHA`・`--target-tree-hash SHA`・`--scope-digest SHA256`・`--acceptance-digest SHA256`・`--role CODER\|VALIDATOR\|REVIEWER` を要求する。`--prohibit TEXT` は繰返し可能。`--previous-evidence-hash SHA256` は Validator・Reviewer で必須。`verify` は `--file JSON`・`--expected-hash SHA256` を要求する。両方とも canonical JSON を標準出力へ返し、状態変更や実行許可を行わない。[field と検査の制限](role-handoff-packets.md)を参照。 |
| `ready` | 引数なしは、今の coding agent が Taxion を操作する静的メタプロンプトを stdout に出し、状態を変更しない。`--install-skill` は home の `.agents/skills/taxion/SKILL.md` に同梱版を明示配置して同じ prompt を出す。`--update-skill` は内容が未改変の Taxion 管理ファイルだけを同梱版へ更新し、同じ prompt を出す。`--check-skill` は本体の許容版・CLI 契約・内容を JSON で確認し、欠落・不一致・改変は非zero終了する。3つの option は併用不可。`--help` は入力形式を表示する。どの経路も設定・workspace・認証を要求せず、モデル・外部 process を起動しない。予算・scope・設定の利用者の承認や独立 Reviewer を代替せず、実行準備やskill実ロードの診断でもない。 |
| `request` | 必須は `--repo REPO`。正の `--max-task-nano-aiu N` は hard ceiling で、使用量が不明なら追加 Dispatch/session を止める。省略・0は承認済み `provider` または `account`・`reset-daily` のみ許可する。[課金ガイド](billing.md)に条件を示す。`off`・`flow` での省略、負値・非有限値は拒否する。`--timeout D` は1秒以上で、既定は1時間。Copilot は30以上の `--max-ai-credits N` も要求し、OpenCode では省略する。任意の `--worktree PATH` は現在の repository root が既定で、clean かつ branch に接続している必要がある。`--max-repairs N` は既定1、範囲0–100。`--context-path FILE` は追跡済み UTF-8 text を最大16 file、合計64 KiB まで繰り返し指定できる。`--cli PATH`・`--base-directory PATH` は Copilot の executable と state directory を指定する。位置引数の文章を元依頼とし、省略時は console で入力する。`/plan` はモデルによる計画、`/approve` は表示計画と期限・repair 回数を持つ coding・review を認可し、`/quit` は実行前に終了する。Dispatch/session 数の上限は repair 上限から導出し、Copilot の credit ceiling は session 別で、合計予算ではない。 |
| `config check` | `--billing` は新規提案用の申告済み課金条件と方式を JSON で表示し、他の option と併用しない。`--sources` は値を含めず設定 field の出典を表示する。`--capabilities` は静的 adapter 対応を示し、実 access・契約資格ではない。`--task ID` は保存 Task 承認、`--dispatch ID` はさらに保存 Dispatch instruction と解決済み設定を照合する。両者は最大一つだけ選ぶ。 |
| `config bind` | `--dispatch ID` は未 binding Dispatch に現在選択中の固定 file 設定を保存する。任意の `--sources`・`--capabilities` は `config check` と同じ表示。credential 認証や契約資格の証明はせず、`--task` は受け付けない。 |
| `git check-index` | `--secrets` は index 内容の local Betterleaks scan を追加する。`--scanner PATH` は scanner を選び、`--secrets` を要求する。指定なしでは禁止 Taxion・dotenv path だけを検査する。 |
| `git check-range` | `--base SHA`・`--head SHA` は40桁または64桁の完全な16進 SHA を指定する必須 option。base は head の祖先でなければならず、範囲には未公開 commit が1件以上必要。`--scanner PATH` は信頼する Betterleaks 1.9.0 executable（既定 `betterleaks`）を選び、範囲内の全 commit tree と message を scan する。 |
| `git hook install` | `--scanner PATH` は Betterleaks 1.9.0 を選ぶ。`--paths-only` は内容 scan なしの path 検査を明示的に設置し、`--scanner` と併用できない。`git hook status`・`git hook uninstall` に option はない。hook は迂回できる。 |
| `init` | 既存 Global・Project・environment と Git remote から不足設定を補い、確認後に Project 設定・Git 除外・workspace を準備する。v0.3.1 の端末では、未確定 provider・複数 remote・適用確認を上下キーと Enter で選ぶ。設定済み項目は再選択せず、適用確認は `Cancel` を初期値にする。`--set FIELD=VALUE` は不足 field の明示指定で繰返し可。`--remote NAME` は複数 remote の選択。`--dry-run` は無変更の確認、`--yes` は表示した変更の非対話適用。参照不足は一覧と非zero終了。未知・重複 field、不正値、既存値の置換、追跡済み設定、symlink、競合する書込みを拒否する。設定・除外の保存後に DB 初期化が失敗した場合はその境界を報告する。 |
| `doctor fix` | init と同じ設定準備を使い、DB を初期化せず不足設定と Git 除外だけを修復する。`--set`・`--remote`・`--dry-run`・`--yes` の意味は init と同じ。変更不要なら再確認なしで終了する。既存設定の値・DB・認証は変更しない。通常の doctor は読取り専用を維持し、設定不足時に fix を案内する。 |
| `completion bash` | bash の補完定義を標準出力へ出す。`source <(taxion completion bash)` で有効にする。設定・Git・DB・外部 CLI・モデルは不要。実 CLI help から生成したコマンドと flag を補完する。flag の値、動的 ID、他の shell は未対応。 |
| `workspace snapshot` | `--output FILE` は新規の絶対パスで、親 directory は事前に用意する。既定または `--store` で選んだ初期化済み workspace、存在する Memory、必要な Git object と親履歴を private SQLite 一ファイルへ保存する。取得中は両 DB の書込みを予約する。実行中 Flow・runner claim・未終了 Dispatch・未照合送信・不足 object・観測した未記録変更を拒否する。元の正式状態と Memory は変更しない。 |
| `workspace inspect-snapshot` | `--file FILE` は既存の移送 SQLite で、相対パスも使える。現在の workspace は読み書きせず、隔離配置で checksum・形式版・DB と Git の必要 object を検証する。JSON の `hash` はファイル全体の SHA-256、`bytes` はファイルのバイト数、`repositories` は repository 数、`worktrees` は解放済みを含む保存配置数、`memory` は Memory の有無。provider・network・モデル・検証 command は呼ばないが、ローカル Git と一時保存領域を使う。 |
| `workspace restore` | `--file FILE`、新規の絶対 directory `--output DIR`、確認済み `--expected-hash SHA256`、`--actor NAME`、`--source REF` が必須。入力は相対パスも使え、出力の親 directory は事前に用意する。現在の workspace や `--store` の選択先には書き込まず、指定した出力先へ復元する。既存先を上書きせず、処理がエラーを返した場合は今回作成した先だけを除去する。`.taxion/workspace.db`、任意の `.taxion/memory.db` と必要な `repositories/`・`worktrees/` を配置する。成功前に復元先を使用しない。旧契約・結果・usage は保持し、配置対応は復元先を選んだ `records show` の `restore` に記録する。旧 session を再開せず、継続は新しい承認済み successor Task を使う。具体例と制約は[一ファイル移送](user-guide.md#別マシンへの一ファイル移送)を参照する。 |
| `task create` | `--title TEXT`・`--repo REPO` が必須。repository identity は明示し、cwd から推測しない。 |
| `task list`, `dispatch list` | 任意の `--repo REPO` で repository を絞り、`--json` で JSON 出力にする。archive 済み Task とその Dispatch は既定で除外し、`--include-archived` で含める。 |
| `task archive` | `--tasks ID[,ID] --actor ACTOR --source SOURCE --reason REASON --dry-run` で対象 Task／Flow／Dispatch と `hash` を確認する。同じ引数と `--expected-hash HASH` で適用する。稼働中・suspended Dispatch、稼働中 Flow、未解放 runner、未解決の外部公開、別の非終了 Task からの参照を拒否する。履歴と Git のファイルは削除しない。 |
| `task plan` | `--repo REPO` の通常計画は archive を除外する。`--include-archived` で履歴を含める場合の action は `ARCHIVED` で、実行許可にはならない。すべて archive 済みなら通常計画は空を返す。 |
| `task authorize` | `--manifest`, `--confirm` confirmation token を提示した後、同じ token でのみ 1 回だけ承認を記録する。承認 bundle は append-only で、configuration 次元を含む |
| `task reauthorize` | `--task READY_TASK_ID` の保存済み Task を読み戻し、既存の設定承認と異なる effective configuration を二段階で確認する。`--confirm` 前は保存せず、確認後は既存 decision と proposal hash を保ったまま設定承認と bundle を追記する。同一 bundle の再送は重複記録しない |
| `task authorize-packets` | `--request JSON` で READY CODER Task の逐次 packet lifecycle と同一契約の派生 Reviewer bundle 追加を preview する。`--confirm TOKEN` でだけ別記録の lifecycle 承認を追記し、Task と既存 ApprovalBundle は変えない。Dispatch 発行前かつ matching bundle が必要。[request 形式・委譲範囲・逐次検証](role-handoff-packets.md)を参照。 |
| `role-packet list`, `role-packet read` | `list --task ID` は lifecycle 承認・欠落参照・packet audit を読み戻す。`read --expected-hash SHA256` は workspace に保存した canonical packet bytes を照合して返す。両方 read-only。未保存 hash からの再構成はしない。 |
| `task validate` | `--manifest FILE`（`--file` も可）が必須で、保存せずに `task track` と同じ規則で Graph request を検査する。`stateChanges` は常に `none` で、workspace が無い場合は空の Graph として検査し database を作らない。違反は全件を一度に返し、合格時は保存される契約の canonical JSON と SHA-256 を返す。Task ID、Memory、依存、supersedes の存在は読み取りだけで確認する。credential らしい値は値も digest も返さず `digestWithheld` を立てるが、判定は変えない。WAL の `-shm` が無い database は side file を作らないため拒否する。判定は `task track` と一致する。 |
| `task track`, `task transition` | `--file JSON` で Task Graph request を渡す。新 Task の `fileAccess` は `{"level":"committed"}` が既定で、Task JSON の `{"level":"exact","paths":["repo/relative/file"]}`・`{"level":"all"}` で範囲を選ぶ。`outputMode` は `commit` が既定。`worktree-tree-snapshot` は commit なしで Controller 取得の Git tree をレビューする。snapshot mode、tree SHA、変更 path は承認契約の一部で、認可済み Task の policy 変更には新しい認可 decision が必要。更新時の `fileAccess` 省略は保存 policy を保つ。snapshot Task の更新では保存 `outputMode` を明示する。`task graph` は保存 Graph を表示し、`task plan --repo REPO` は認可を変えず後続計画を再構成する。 |
| `dispatch` | `--task ID`・`--repo REPO` が必須。`--relationship TYPE` は `NONE`・`CONTINUES`・`AMENDS`・`SUPERSEDES`・`CANCELS`・`SUSPENDS`・`RESUMES`・`INDEPENDENT` を受け付ける。`NONE` は既定値で、`NONE` 以外または関連先を指定する場合は Task Graph の初期化が必要。`NONE` に `--related-dispatch`・`--relationship-reason` は指定できず、他の関係には両方必要。`INDEPENDENT` の関連先は別 Task の Dispatch でなければならない。 |
| `dispatch transition` | `--id`・`--status`・`--actor`・`--reason` が必須。Dispatch を終了する遷移では `--result-file JSON` が必須で、成功時は認可済み検証も実行する。`--validation-timeout DURATION` は既定10分で、正の値を要求する。 |
| `conversation intake`, `conversation confirm` | `--file JSON` は元依頼と提案契約、または正確な利用者 confirm を渡す。intake は proposal 保存、confirm はその完全一致契約の承認だけで、作業を発行しない。 |
| `conversation proposal` | `--task ID` で保存 proposal と正確な hash を読む。 |
| `conversation ask`, `conversation plan` | `--task ID`・`--question TEXT`・`--timeout DURATION` が必須。Copilot は30以上の `--max-ai-credits N` も要求し、OpenCode では省略する。`--cli PATH`・`--base-directory PATH` は Copilot executable と state directory を選ぶ。各 call の取得利用量を報告し、account 残高・利用率は同単位の allowance を確認できない限り unavailable とする。Copilot の credit は session 別 soft ceiling。 |
| `conversation investigate` | ask・plan の option に加え、繰返し可能な `--path REPO_PATH` と必須 `--max-bytes N`（1–65,536）を受け付ける。保存 SHA の明示 file だけを読む。 |
| `conversation watch` | `--flow ID`・`--actor ID`・`--source TEXT` で Flow と利用者の入力を識別する。実行する `/ask`・`/plan` には正の `--question-timeout DURATION` が必要。Copilot は30以上の `--question-max-ai-credits N` も要求し、OpenCode では省略する。`/investigate` にはさらに1–65,536の `--investigation-max-bytes N` が必要。これらの既定値は未設定（0）。`--interactive-execution` はこの console の選択 run 中に利用者の入力を受け、EOF・quit はこの console が所有する実行を安全に中断する。選択する `workflow run`・`workflow step` の option は `--` の後に指定する。 |
| `conversation search` | `--scope JSON`・`--task ID` と、非空で512 byte以内の検索語を `--question TEXT` または `--query TEXT` で指定する。承認済み local corpus を検索し、出典付き結果を Task に付ける。`--sdk` は有料 Orchestrator turn を一回だけ明示的に要求し、`--timeout DURATION`・`--max-ai-credits N` も必須となる。`--cli PATH`・`--base-directory PATH` で executable と state directory を選ぶ。`--sdk` なしでは local 検索だけで model を呼ばない。 |
| `workflow start`, `workflow recover`, `workflow handoff`, `workflow interrupt`, `workflow terminate`, `workflow feedback`, `workflow feedback-resolve`, `workflow draft`, `workflow draft-discard`, `workflow draft-approve`, `workflow planner-draft`, `workflow planner-action`, `workflow question-draft` | 各操作の request を `--file JSON` で渡す。`workflow start` は期限・repair 回数・Task-wide nano-AIU ceiling を持つ Flow を認可し、初回 Coder Dispatch を発行する。`workflow terminate` は未確認・未開始 Coder Dispatch だけを取り消し、Flow を hold して新しい Task 契約による再承認を可能にする。`continueFromDispatchId` は停止後の保存済み Coder 成功結果を引き継ぐ。後続 Task は同じ output SHA、snapshot mode では同じ tree SHA・path も対象にする。recover、handoff、feedback、draft、planning は指定操作だけを行い、変更 scope・予算を通知なしに承認しない。形式は [Task Graph](../develop/task-graph.md) を参照する。 |
| `workflow advance`, `workflow show`, `workflow evaluate`, `workflow feedback-target`, `workflow planning-context` | `--id FLOW_ID` で保存 review flow を選ぶ。`workflow evaluate` は Task 単位の承認上限・消費・残量を含む report を出す。前述の各節に従う読取り・遷移操作。 |
| `workflow evaluate` | `--id FLOW_ID` で保存品質証拠と測定済み／未測定の費用を JSON 表示する。読取り専用 SQLite 接続を使い、workspace 作成・移行・モデル実行をしない。`--require-quality` は完了した独立同一 target レビューと文書整合 criterion がなければ JSON を出したうえで非zero終了する。得点・自動公開承認・現在の Git の検証ではない。 |
| `workflow accept-saved-review` | `--file JSON` で `flowId`・`actor`・`source`・`reason`・`allowExpired` を指定する。`--preview` は状態を変えず対象と検査を表示し、適用には同じ判断と preview の `hash` を `expectedHash` として要求する。引継ぎ保留の理由消失で停止したレビュー専用 snapshot Flow だけを対象に、モデルや検証 command を起動せず保存済み `APPROVE` を受理する。 |
| `workflow reconcile-stopped` | `--file JSON` と任意の `--preview`。停止済み初回 snapshot 指示の取消と承認済み入力の出典を保存する。人間の実行静止確認・配送不明の受入れ・完全ハッシュを要求し、旧 Flow と不明な利用量を保持する。 |
| `gate contract`, `gate execution`, `gate publication` | `gate contract` は `--task ID`、`gate execution`・`gate publication` は `--flow ID` を要求する。保存 Task・Flow・承認 bundle・評価証拠だけを読み、Task Graph・Dispatch・Flow・ledger を変更せずモデルも呼ばない。`--json` は version 1 の機械可読 report（gate 名、verdict、task/proposal/record/bundle/evidence hash、blockingConditions、drift と authorization impact、nextRequiredApproval、nextAuthorizedTransition、`stateChanged:false`）を出す。`--json` なしは同じ内容を人が読める形で出す。終了 code は 0 pass・1 fail・2 blocked で、未検証の項目を pass として報告しない。 |
| `workflow lanes` | `--task ID` で保存 Task を選び、repository identity・baseline/worktree・contract 整合・handoff 準備の4つの読取り専用 lane を並行実行して JSON を表示する。`--timeout DURATION` は lane ごとの上限（既定5秒）、`--root PATH` は worktree を含むべき repository root（既定は現在 directory）を指定する。workspace・Task Graph・Dispatch・Flow・worktree・runtime session を変更せず、モデル session も開始しない。lane が一つでも失敗すると `executionGateOpen` は false になり、JSON を出したうえで非zero終了する。診断は credential を伏せる。 |
| `workflow run`, `workflow step` | `--id FLOW_ID`・`--coder-prompt-file PATH`・`--reviewer-prompt-file PATH` で Flow と role prompt を選ぶ。file の代わりに `--builtin-prompts` を使うと、同梱 snapshot prompt、厳密な結果 identity、compact context、Controller validation submission を選ぶ。snapshot Flow を要求し、managed commit・stage は拒否する。この mode だけは tool 未指定時に Coder `view,create,edit,apply_patch`、Reviewer `view` を既定とし、明示空 list では file tool を追加しない。通常の native skill tool は別に追加する。任意の `--cli PATH`・`--base-directory PATH`・`--max-ai-credits N`・`--coder-tools LIST`・`--reviewer-tools LIST` で runtime と allowlist を選ぶ。`--managed-commit` は binding 済み Coder commit tool、`--managed-stage` は追加の scope 内 stage を有効にし、後者は前者を要求する。`--scanner PATH` は managed commit だけで有効。`--strict-result-identities`・`--compact-context`・`--controller-validation-submission` は各結果・context 検証 mode を変える。credit 上限は session 別で、Flow 全体ではない。 |
| `runtime auth`, `start`, `send`, `resume`, `events`, `commit` | `--dispatch ID`・`--system-prompt-file PATH` が必須。`--cli PATH`・`--base-directory PATH`・任意の `--max-ai-credits N` で runtime を選び、非ゼロ credit 上限は30以上を要求する。`--tools LIST` は SDK allowlist。send は `--prompt-file PATH`、commit は `--message-file PATH` が必須。`--scanner PATH` は `runtime commit` または `--managed-commit` と併用する場合に使い、既定値は `betterleaks`。ほかの操作で明示すると拒否される。`--review-source-worktree PATH` は独立 Reviewer の start だけで使う。`--managed-commit` は binding 済み Coder commit tool、`--managed-stage` は scope 内 stage を追加する。開始時に使った `--strict-result-identities`・`--compact-context`・`--controller-validation-submission` は実行接続時にも指定する。`--timeout DURATION` は既定10分で、turn・event stream の待機を制限する。 |
| `runtime stop` | `--dispatch ID` が必須。任意の `--cli PATH`・`--base-directory PATH`・正の `--timeout DURATION` だけを受け付け、timeout は既定10分。旧実行契約も含め、保存 session binding を取消だけに使う。prompt・tool・scanner・実行 mode の option は不要で、受け付けない。 |
| `acp` | `--coder-system-prompt-file PATH` が必須。Copilot は30以上の `--max-ai-credits N` も要求し、OpenCode では省略する。`--reviewer-system-prompt-file PATH` は任意。`--cli PATH`・`--base-directory PATH` は Copilot 用、`--tools LIST` は role の allowlist。正の `--timeout DURATION` は既定10分。`--compact-context`・`--controller-validation-submission`・`--managed-commit`・`--managed-stage`・`--scanner PATH` は `workflow run` と同じ意味・依存で、`--scanner` は `--managed-commit` を要求する。ACP は承認済み Dispatch を実行し、発行・承認はしない。 |
| `records proposal`, `records finding` | `--file JSON` で正確な公開 request または失敗分析を渡す。 |
| `records approve` | `--file JSON` で公開 request、`--scanner PATH` で local 秘密検査を選ぶ。 |
| `records cancel` | `--file JSON` で未送信の公開予定を失効させる。識別子、`previousState: "PENDING"`、元の承認、内容のハッシュ、理由、実行者、取消の承認を指定する。取消は冪等で、履歴は追記専用。送信済み・送信中・結果不明の予定と、異なる内容での再利用は拒否する。[文章指針と独立レビュー](../develop/writing-guidance.md)に要求形式と制約を示す。 |
| `records send` | `--id ID` で承認済み公開、`--scanner PATH` で local 秘密 scanner を選ぶ。 |
| `records reconcile` | `--id ID`・`--claim CLAIM`・`--sender-stopped` で送信不明を識別し、先行 sender の終了を確認する。 |
| `records show` | 固有 option はない。保存 runtime receipt と公開状態を表示する。 |
| `review` | `--task ID`・`--sha SHA`・`--repo REPO` で Coder 出力証拠を明示 repository に対応付ける。 |
| `resume` | `--session ID` で local な再開確認を表示する stub。Copilot session は再開しない。 |
| `serve` | `--addr ADDRESS` は既定 `127.0.0.1:3938`。`--mode loopback\|remote` は既定 loopback で、remote は token を要求する。`--token VALUE`・`TAXION_TOKEN` は共有 token。`--audit PATH` は監査 log を選び、空値なら無効。 |
| `gen-token` | option はない。serve 用乱数 token を表示し、credential として保護する。 |
| `audit verify` | `--audit PATH` で監査 log を選ぶ。 |
| `memory put` | `--kind`・`--title` が必須。`--body`・`--scope project\|global`・繰返し可能な `--source REF` は任意。 |
| `memory get`, `memory history` | `--kind`・`--id` が必須。get は entry、history は lifecycle を読む。 |
| `memory list` | `--kind` が必須。`--scope project\|global`・`--json` は任意。 |
| `memory revise` | `--kind`・`--id`・`--title`・`--actor`・`--reason` が必須。`--body`・`--scope`・繰返し可能な `--source` は任意で、省略 scope・source は保存値を保つ。 |
| `memory revoke` | `--kind`・`--id`・`--actor`・`--reason` が必須。履歴を保つ。 |
| `memory warn`, `memory source`, `memory configure`, `memory approve`, `memory checks` | warn は `--body` が必須で、`--scope`・`--source` を受け付ける。source は `--warning-id`、configure は正の `--promote-after`、approve は `--id`・`--check`・`--value` が必須。approve は `--scope global` も必要となる場合がある。checks に option はない。 |
| `memory search` | `--query` が必須。`--kind`・`--scope` は任意。 |
| `memory conflict add`, `list`, `resolve` | add は `--left`・`--right`・`--actor`・`--reason`、list は `--ref`、resolve は `--id`・`--actor`・`--reason` が必須。 |
| `memory export` | `--scope` は任意。`--sanitize` は既定 true で、`--sanitize=false` は未加工 entry を表示する。出力は local 標準出力だけで、送信しない。 |
| `memory snapshot`, `memory inspect-snapshot`, `memory restore` | snapshot は絶対 path の `--output`、inspect-snapshot は `--file` が必須。restore は `--file`、未使用の絶対 path の `--output`、`--expected-hash`・`--actor`・`--source` が必須。`--approve-sensitive` は local 移行の検出内容を明示承認し、snapshot を公開するものではない。 |
| `specification hash` | `--file JSON` で構造化 specification を hash 化する。 |
| `specification scope-hash`, `specification search`, `specification serve` | 必須 `--scope JSON` で正確な承認済み local corpus を選ぶ。search は `--query TERMS` も必須。serve は read-only 検索 corpus を MCP stdio で公開する。 |
| `tools hash` | 必須 `--path PATH` で正確な local executable、入力 file、skills directory、skill-only plugin directory を hash 化する。 |
| `doctor` | `--json` は検索先と検出結果を機械可読で表示する。`--worktree PATH`（既定 `.`）・`--cli PATH`・`--base-directory PATH`・`--tools LIST` は実行時と同じ値を使う。`--dispatch ID` は保存 worktree・設定・tools policy を読取り専用で使い、`--worktree` との併用はしない。モデル session、hook・MCP 起動、設定変更は行わない。 |

Copilot の通常実行は native discovery と `skill` tool を使う。
Copilot の標準配置や追加登録した skill を、Taxion 用にコピー・個別 hash 承認する必要はない。
固定 role policy を明示した場合だけ、`skills` / `plugins` の asset が必要となり、
未承認 asset のまま `--tools skill` 等を指定しても拒否する。
この固定方式では `workflow run|step` が両 role の asset を session 作成前に照合する。
OpenCode は承認した skills だけを隔離環境へ複製し、通常の personal・project 設定を自動探索しない。
設定とログ確認の手順は [利用者ガイド](user-guide.md#文書用-skill-を使う場合)、
権限と証拠の境界は [runtime 契約](../develop/runtime-contract.md#skillspluginsmcphooks-と出力圧縮)を参照する。

root usage 文字列は flag 定義とは別に管理する。この索引で実装済み subcommand と option を照合する。`ready` は外側の agent 向け手順、`request` は内部 Planner 対話であり、同じ相談を両方で繰り返す前提にはしない。`doctor`・`config check`・`runtime auth` などの事前確認を、実行準備や契約上の利用資格の証明として説明しない。

`request --budget-guidance provider|off|flow|account` は新規 proposal の方式を上書きする。契約条件の検査は迂回しない。`runtime start|send --budget-guidance MODE` は保存 Task の `budgetGuidance.allowedModes` 内だけで適用し、上限省略方式の制御は外せない。省略時は保存 mode を使う。guidance がない旧 Dispatch へ追加する場合は再承認する。新規 `task track`・`conversation intake` は固定設定から解決し、更新・再開は保存済み policy を引き継ぐ。`records show` に正確な補足文章、条件・観測、送信段階を保存する。どの方式も別の model turn を追加しない。詳細は[課金ガイド](billing.md)を参照する。