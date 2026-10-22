# Runtime 契約

## SDK 共通インターフェース

```go
type Runtime interface {
    Start(ctx context.Context, taskID string) (sessionID string, err error)
    Send(ctx context.Context, sessionID, prompt string) error
    Resume(ctx context.Context, sessionID string) error
    Stop(ctx context.Context, sessionID string) error
    Events(ctx context.Context, sessionID string) (<-chan Event, error)
}
```

## 実行・承認・質問の条件

core（`internal/core`）は標準ライブラリの型だけを使い、SDK の struct を使わない。adapter は core の外で Runtime interface を実装する。各 `Runtime` は Coder と Reviewer の session を隔離する必要がある（ROLE-001）。event は共通の `{Type, Payload}` とし、SDK event 型を core に含めない。

adapter は、未知・停止済み session、順序違いの step、空の Task ID に error を返す。有効な request の場合だけ状態を進め、準備完了を報告する。`runtime.Identity` は、session を一つの承認済み Dispatch に対応付けるための role、worktree、configuration hash を返す。

### 設定と利用許可の照合

設定を選択した Dispatch には `runtime.ConfigurationIdentity` が必要。保存した設定の組合せ、実効 Runtime 認証、repository API access を確認する。controller は start・resume・send 前に認証と API access を確認し、その後に承認を再確認する。

GitHub.com adapter は `gh:active` または `env:NAME` を選び、予定した repository account と対象の完全一致を確認する。読取り権限が必要で、Coder には書込み権限も必要。access 不明や非対応 provider は実行を止める。成功した確認は、操作、Runtime hash、観測期間、role の権限、未検証の契約とともに `configuration.accessChecks` に追記する。記録時には transaction 内で承認を再確認し、session が作成されたとは扱わない。

最初の観測は session 作成前でも、確認した Runtime identity を固定する。再起動後に過去の確認を再利用しない。旧記録に観測がなければ未確認とする。この確認は AI 契約の検証や Git push の認証ではない。

session の作成・接続後と各モデル送信前に、設定を選択した Dispatch は、adapter の SDK 共通 `CheckServiceAccess` も呼ぶ。Copilot adapter は `session.quota.get` を照会し、明示的な service access と容量を持つ account snapshot の取得成功を要求する。snapshot の欠落、拒否、枯渇、不明は、数値の残量を見ずに拒否する。前の turn の利用許可の観測は account snapshot ではない。

照会後に承認を再確認する。新規 session が拒否された場合は、Dispatch に対応付けず停止する。この観測は前段の access receipt には追加せず、課金主体を識別したり、将来の利用許可を保証したりしない。safe stop は三つの外部確認を省略する。設定のない旧 controller の動作は変えない。

現在の Task と発行済み指示で、選択 account と contract reference を同じ出典付き Human 承認が対象にしている必要がある。reason は承認した用途を記録する。その記録の照合で、記載内容の法的な正しさを検証したことにはならない。

### 保存記録への質問

保存記録についての質問には、別の file・shell tool を持たない Orchestrator Runtime を使う。通常は native skills と指示を利用する。出典付き Task 設定承認、今回の認証確認、repository の読取り権限、provider の service 確認、明示的な期限と session soft ceiling が必要。回答は、渡した観測に存在する field を引用し、記録された事実、仮定、不明な点を分ける。観測は現在の実行状態の証明にはならない。

Dispatch を対応付けず、Task・主 Flow の履歴や承認を変更しない。排他制御用の `Records.Operation` だけを記録する。返された session・usage は質問に属し、主 Flow には属さない。利用量不明と不正な引用は質問を止める。cancel 後の safe cleanup は独自の上限付き context を使い、error を隠さない。

過去の結果判断は実際の保存済み Dispatch と照合し、Task・履歴、指示、Flow 契約に繰り返される内容を整理する。結果全文、Memory、出典 hash は残す。`projection` は、元の instruction hash が整理後の指示の hash ではないことを説明する。保存履歴は変更しない。4 role の質問入力テストは、追加モデル呼出しなしで JSON 入力を元の半分未満にすることを要求する。

### commit 済みファイルの調査

`AskInvestigation` は質問と同じ確認・上限を使い、明示選択した commit 済み UTF-8 ファイルを、モデルだけへの送信前に追加する。`ReadCommittedFiles` が許可するのは、固定 SHA の Git object 読取り、標準形で scope 内の path、1〜16ファイル、最大64 KiB の総出典量だけ。欠落、binary、上限超過は拒否し、部分的な証拠を返さない。環境の Git routing 変数も使わない。

作業ファイル、shell command、hook、textconv、agent tool は使わない。別に取得した Git 観測は今回の調査を識別するもので、過去の推論や検証成功の証拠にはならない。

### 質問結果の採用

`RecordQuestionDraft` は、Planner 採用の transaction と既存の補足承認・公開処理を使う。選択した confirmed index と明示的な Human 指示だけを適用し、通常の会話を通知なくコピーしない。保存 context、Memory、role、修正回数上限、現在の結果対象を確認する。調査 SHA、file bytes、hash は採用前に再構成する。

上限内の出典 snapshot、選択した主張、不確実性、proposal hash を保存する。native session ID は提供された出典情報として扱う。proposal は認証済み provider 出力の証拠ではなく、内容の正しさも証明しない。

### 中断

`RequestFlowInterrupt` は、対象付き feedback・hold と、実行中なら `CANCEL_REQUESTED` を atomic に記録する。停止確認や Task cancellation は記録しない。所有する runner が既存の監視・cleanup で safe stop を実行する。失敗した request は未確認のままで、継続を許可しない。

発行済み実行は hold とし、停止したと創作しない。現在の状態に新しい request を適用する前に、同一入力の再送を照合する。部分成果と発行済み指示は保持する。

### 依頼の登録と再承認

intake は、元の依頼本文を出典付きの未承認 `DRAFT` Task に保存する。任意の planning 設定承認が許可するのは、隔離した上限付き質問だけ。完全な hash に対する Human 確認は、`READY` 前に実行承認と設定承認を別々に記録する。scope・acceptance・検証 command が不足した契約は確認を通せない。確認だけでは作業を発行せず、Flow 上限も選ばない。

発行済み契約を改訂する場合は successor Task を作る。前 Task の hold 済み対象が必要で、runner と active Dispatch がないことを要求する。旧 Flow の上限、承認、指示、結果は変更しない。明示的な再承認で前 Task を停止・置換し、別の上限を持つ作業を準備する。

実行を伴わない Planner の採用 action は、出典付きの未解決 feedback として扱い、修正指示にはしない。明示的な処置、別の調査、successor の再承認まで hold を保つ。同一入力の再送で、旧助言を別の role に適用しない。自動進行に追加のモデル呼出しは入れない。

隔離した CLI 質問は、native response schema の引用先を、実際に渡した記録 path に限定する。複合記録も有効な出典であり、入れ子の履歴を引用一覧へコピーする必要はない。採用時には現在の context 全体、hash、出典本文を独立して検証する。JSON 設定の整形だけでは、変更のない証拠を期限切れにしない。値を変えて旧観測 hash を残した場合は拒否する。

### controller による検証

Runtime・workflow 実行の `--controller-validation-submission` は、モデルの提出と controller の検証を分ける。担当は架空の検査を足さずに実際の結果を返し、controller が成功を保存する前に検証する。出力欠落、検査失敗、根拠のない Reviewer 承認は引き続き進行を拒否する。session 接続時にも同じ flag を指定する。既定 prompt は変更せず、既存 session の configuration hash を保つ。

### 一担当ずつの実行と対話中の入力

`StepReviewFlow` は自動実行と同じ Runtime・保存 runner claim を使うが、最大一つの role だけを実行し、turn 完了後に後続担当を発行しない。次へ進むには別の明示 request が必要。

監視中の会話は入力を読む前に hold とし、EOF で継続を許可しない。観測済み継続は、表示した target、draft、契約、hold version を transaction 内で比較する。補足の承認と公開は別々に行い、既存の自動 runner の動作は変えない。

`--interactive-execution` は、選択 runner の実行中も入力を受け付ける。同時出力を直列化し、二つ目の所有実行を拒否して、失敗を報告する。EOF・quit はその console が所有する runner だけを cancel し、終了を待つ。flag の明示選択がなければ、同期的な監視動作を保つ。

### operation の直列化と request checkpoint

`Records.Operation` v1 は workspace ごとの現在の admission を保存する。`state` は `RUNNING`、`WAITING_FOR_ANSWER`、`WAITING_FOR_APPROVAL`、`BLOCKED`、`USAGE_EXCEEDED`、`COMPLETED`、`CANCELLED`。終了・取消し以外は他の operation を拒否する。`phase` は `PLANNING`、`PREFLIGHT`、`EXECUTION`、`QUESTION`。Task と ApprovalBundle を変更する権限は含まない。旧 workspace の field 欠落は operation 未登録として扱い、複数の既存 RUNNING Flow がある場合は新規実行を拒否する。

request・独立質問・review runner は workspace DB と別の `.operation-lock` SQLite に排他 transaction を保持する。workspace 自体の保存 transaction は妨げず、process 終了で予約を解放する。同一 request 内の Planner と runner だけが現在の owner を context で引き継ぐ。別 request・別 Flow の開始、別 caller による advance、実行中の別質問、外部からの runtime start/send/resume は共通入口で拒否する。safe stop は妨げない。この予約は同じ workspace に対するもので、別 workspace の実行を排他制御するものではない。

checkpoint v2 は既存の正規化 document 保存を使い、operation ID、最小の提案・未回答質問、依頼 digest、承認参照、preflight と再開用 option を保持する。会話全文、prompt・response 全文、推論、調査ファイル本文は保存しない。planning receipt は入力・結果の digest と成否だけを持ち、Task の元依頼 decision も digest を参照する。credential pattern を含む最小計画は保存前に拒否する。

`Approved` は状態表示であり、再開の承認根拠ではない。承認済み再開では、正式な intake confirmation の Task ID・operation ID・proposal hash・authorization reference・設定・basis digest を照合し、元の DRAFT 契約、承認後の契約と現在の Graph／Flow の整合を検証する。basis は最小計画・option・設定・期限・予算・消費量を含む。成功済み preflight は、この basis と report digest を持つ execution receipt と照合する。checkpoint checksum は保存内容の変化の検出に使い、承認の代用にはしない。旧 checkpoint の会話 field は読み取り時に捨て、再保存・暗黙移行しない。承認束縛のない旧版と未知版は再開・取消しを拒否し、保存済み記録を保持して明示的な照合を求める。

再開は保存済み期限と上限を維持し、Planner の実測使用量を Flow 上限から差し引く。利用量不明なら追加モデル呼出しを止める。`request --status` では owner と checkpoint 本文を除き、state と予算を返す。`claimRecorded` は保存した claim の有無であり、process の生存証明ではない。承認後は既存4 lane を一括実行し、既存 bounded runner に Coder・非モデル validation・Reviewer を任せる。実行 event は保存するが console へ逐次報告しない。未終了 operation は portable snapshot の対象にできない。

入力終了後の質問再表示はモデルを使わない。送信開始済みで結果が不明の Planner は checkpoint で検出し、再送しない。実行中に process が止まって runner claim が残った場合は、既存の停止確認・receipt/Git 照合を済ませてから再開する。復旧のために Task 契約、proposal hash、既存 decision、ApprovalBundle、budget hard-stop を書き換えない。旧 binary は未知の `operation` field を拒否するため、rollback は operation 保存前の snapshot を使用する。
## 保存結果と checkpoint の復旧

`workflow recover` は、process の停止確認と終了 Dispatch の照合後に、保存した runner claim の完全一致を確認して解放する。最新結果が成功した Coder 提出なら、保存結果の契約と現在の Git の事実も確認する。対象は repository origin、branch、HEAD、baseline の祖先関係、変更 path、clean worktree、検証の出典、commit parent。今回の観測は、観測時刻を除き保存済み Git 証拠と一致する必要がある。元の結果と timestamp は変更しない。

通常の Coder から Reviewer への引継ぎでも同じ検査を行う。復旧時の不一致は claim を残して明示 error を返す。引継ぎの不一致は、理由付きの `STOPPED` Flow を記録する。一致した結果は、保存した出力 SHA と完全一致する対象の独立レビューへ進める。この検査は Git と保存済み検証記録を読み、検証 command を再実行しない。command 実行は元の controller 提出時に行う。

解放済み Reviewer の結果には、管理 worktree が存在しないことと registry からの削除、元 repository の origin、変更していない commit parent、直前の Coder と同じ出力 SHA も要求する。完了 checkpoint にも同じ検査を再適用する。成功結果には、保存結果と利用量に一致する controller receipt が必要。receipt のない旧 checkpoint は、証拠の照合が未完了として明示的に復旧を拒否する。

実行承認は指示送信前に保存する。`RUNNING` Dispatch では観測と停止が可能で、新しい指示には新しい承認済み Dispatch が必要。Runtime の出力、利用量、終了 event は stream ID と sequence を付けて保存する。同一 stream の同一 sequence の再送は冪等に扱い、内容の矛盾や cancel 後の結果は error とする。正規化した SDK sequence は現在の adapter instance に属するため、再接続の別 stream からの採用には明示的な照合が必要。

復旧、承認済み公開、失敗記録の手順は[実行記録](../userguide/execution-records.md)、provider の対応範囲と採択した後続作業は[開発基盤の対応評価](forge-capabilities.md)に記載している。

### 逐次 role packet の復旧

READY Task の packet lifecycle を明示承認した Flow では、`Records.Packets` に canonical bytes、`Records.PacketAuthorizations` に Task とは別の承認 basis、`Records.PacketAudits` に instruction / predecessor と実検査 report を保存する。`UpdateWorkspace` は既存 prefix の変更・短縮を拒否し、nested な欠落申告も変更できない。保存形式は既存の正規化 document store を使い、専用 transport や新しい SQL schema は追加しない。

初回 Coder packet と Dispatch は同じ transaction で保存する。Coder の結果 receipt が存在してから Validator packet を別 transaction で保存し、その後の `advanceReviewFlow` が既存の結果契約と Git 再検証を行う。Validator はモデルを呼ばず、validation command も再実行しない。合格 report と Reviewer packet、同一契約の派生 Reviewer bundle を保存してから、変更していない実行入口の検査を通して Reviewer Dispatch を発行する。

runner の factory 呼出し前、Reviewer worktree の作成前、Controller の実行承認時に packet を再検査する。未承認の旧 Flow は従来どおりで、未保存の参照を検証済みとする移行は行わない。request と互換性の制限は [利用手順](../userguide/role-handoff-packets.md)を参照する。

### 別マシンへの移送

`workspace snapshot` は停止・照合済みの状態を対象に、workspace と存在する Memory に書込み予約を保持し、`VACUUM INTO` の整合コピーと必要な Git object の到達閉包を一つの SQLite へ保存する。内容 hash が同じ payload は共有する。実行中 Flow・runner claim・未終了 Dispatch・未照合の外部送信、不足 object、観測した未記録変更、submodule・Git LFS は明示拒否する。

`workspace inspect-snapshot` は隔離配置で checksum・形式版・DB と Git の対応を照合する。`workspace restore` はファイル全体の SHA-256 と actor・source を要求し、新規 directory に限定する。元契約・結果・usage・hash は維持し、配置対応を `Records.Restore`、過去の配置 receipt を `Records.Restores` に記録する。元 Task への新規 Dispatch と旧 SDK session の再開は拒否する。Git 再検証と successor の照合では、保存原本を変更せず新配置を使う。継続には新しい配置・設定・予算の明示承認が必要となる。

通常の DB ファイルコピーと異なり、元 DB・JSON・Git の場所を参照せず復元できる。SDK home・credential・Git config・hook は収集しない。既知の token・秘密鍵パターンを検出した移送は拒否するが、未知の秘密の不存在を保証しない。処理は入力全体を読み、使用メモリは payload に依存する。詳細と制約は [ADR-0034](adr/0034-portable-sqlite-workspace-snapshots.md) と[移送手順](../userguide/user-guide.md#別マシンへの一ファイル移送)に記録する。

## adapter の動作

- `internal/runtime/local` は、process 内で状態遷移を確認する参照 adapter。session ごとに `core.Sequence` を使い、`CompleteStep` で CHK-002 の順序を強制する。受け付けた指示を共通の `input` event として記録し、`checkpoint`・`done` event を出す。
- `internal/runtime/proc` は、設定した外部 command を起動する。標準出力・標準errorを `output` event として送り、指示を標準入力に書き込む。process 終了時に `done`・`failed` を報告する。`Resume` は既存 session の検証だけを行い、process 起動は `Start` が担当する。checkpoint の順序管理は別に行う。
- `internal/runtime/copilot` は公式 Go Copilot SDK を使う。role・worktree ごとに adapter を設定し、session は Auto と明示 tool allowlist を使う。infinite-session compaction は有効。CLI は保存済み Dispatch snapshot を role 別 system prompt に追加し、turn と compaction をまたいで承認済み契約を system context に保つ。resume 時も同じ system prompt、worktree、tool を渡す。SDK permission handler は、Coder に worktree 内の file 書込みと allowlist 内の SDK tool の一回使用を許可し、Reviewer に割当 worktree 内の読取りを許可する。shell request と worktree 外の path は拒否する。この handler の対象は SDK permission request であり、host・外部 tool は独自の保護を使う。
- `internal/runtime/controller` は、対応付けた session の作成、再開、送信、停止、利用量記録前に、Dispatch の role、割当 worktree、active status、configuration hash を確認する。session ID、hash、model、token・cache 利用量を Dispatch とともに SQLite に保存する。管理下の staging・commit には、active Task と、tool 呼出し時の呼出し元 session も必要。Dispatch が Flow に属する場合は、review flow が実行中であり、保存期限と契約内であることも要求する。token 数は新しい Flow の停止条件ではない。

### OpenCode ACP adapter

`internal/runtime/openacp` は ACP v1 の stdio client として、承認した OpenCode を
role ごとの Linux namespace で起動する。Taxion が server になる `taxion acp` と
接続の向きを区別する。Controller の共通契約を使い、OpenCode SDK・provider API は追加しない。
実行ファイルの絶対パスと server 設定の絶対パス・内容 hash は Dispatch の設定承認に含む。
実行ファイルの hash 固定は任意とし、指定時だけ内容一致を要求する。
接続後の protocol、agent mode、操作者設定と同じ model を確認してから prompt を送る。
OpenCode の製品版の完全一致は要求せず、ACP v1 と必要な session 応答で判定する。
`acp_connection` に protocol、報告された agent の名前・版、model、実行したファイルの
内容 hash と pin の有無を記録し、送信時にも再掲して Start 後の event cursor から取得できる
ようにする。仕様上任意の agent 情報の欠落だけでは拒否しない。pin のない同じパスでの
CLI 更新には再承認を求めず、起動した process は読取り済みの複製を使い続ける。
設定・権限や明示 pin の変更には再承認を要求し、旧契約・session は書き換えない。

承認 path の複製を workspace とし、Git metadata・host HOME・credential・shell を渡さない。
Reviewer workspace と agent 設定は read-only mount とし、process の capability を落とす。
Coder の export は承認 path、regular file、初期 host 内容との一致を照合する。
I/O 失敗で export が途中まで進んだ場合は明示エラーとし、隔離側の成果を保持する。
host network はモデル通信に使用し、通信先や provider 内部の処理を隔離したとは扱わない。

native permission の `ask` は client が一律 `cancelled` にする。client filesystem・terminal は
提供しない。承認 skills だけを複製し、MCP・実行型 plugin・RTK・shell は未対応とする。
tool call と完了更新の原文は `acp_tool_update` に保存し、call ID で対応付けた
共通形式の execution event を別に保存する。これは tool の完了観測であり、
skill が出力品質を改善したことは別に検証する。

prompt response の usage は `scope: latest-assistant-message` を付け、tool iteration 全体や
Flow 全体の既知 token 合計として使わない。`usage_update` の context 使用量と累積 USD cost は
`context_usage`、usage 欠落は `acp_usage_unavailable` として別に保存する。
`acp_capabilities` は送信時に記録して Start 後の cursor からも取得できるようにする。
account quota・Copilot AI-credit ceiling は未対応で、指定された上限を黙って無効にしない。

停止では取消通知の後に process の終了・reap と export を確認する。異常切断を正常完了にせず、
prompt を自動再送しない。別 process/provider の session への再接続は拒否し、
新しい設定と予算を承認した successor Task で継続する。provider 認証と repository permission は
`UNVERIFIED` として記録する。[設定と復旧手順](../userguide/configuration.md#opencode-acp-の設定と制約)と
[ADR-0038](adr/0038-run-opencode-acp-with-sealed-role-boundaries.md)を参照する。

## Skills・plugins・MCP・hooks と出力圧縮

通常の Copilot SDK session は create・resume ともに探索を明示的に有効にする。SDK に `nil` を渡すだけでは project・personal skill が読み込まれず、CLI の一覧と一致しない場合がある。探索方式は config hash の `native-cli-discovery-v2` に含め、旧方式の session を新しい実行契約で再開しない。明示 ToolPolicy と取消専用 adapter の探索抑止、role 別 file・shell permission は維持する。

外側の coding agent 向けの操作 skill は `ready --install-skill` で明示配置する。
v0.4.0 の同梱 skill は `1.3.2`、metadata の形式は `"1"`、CLI 操作契約は `taxion.cli.v1` とする。ゴール指向方針と文書整合の原稿に、中間成果で最終依頼を完了扱いしない継続規則と、最終受入判定後に依頼された ADR・Issue・milestone・PR の整合更新、コミット、必要な公開確認を経て完成を報告する規則を追加した。公開済み v0.3.1 の `1.2.0` と旧開発版 `1.3.0`・`1.3.1` を含む旧管理ファイルは互換とせず、未改変の管理ファイルだけを明示更新する。これは操作者への指示であり、Controller が GitHub の記録を自動更新・強制する契約ではない。未達条件を完了扱いにせず、依頼外の記録や公開を要求しない。
v0.3.0 の ready は状況別案内と初期設定の導線を更新した。旧 skill `1.0.0` は互換とせず、整合性を検査できる未改変の管理ファイルだけを利用者の明示許可で更新する。コマンド構文と JSON schema は変えず、CLI 操作契約は維持する。
skill の本文・指示を変更するときは skill 版を更新し、CLI の破壊的変更では操作契約も更新する。
本体の許容 skill 版は明示リストで管理し、未知の版は minor 変更でも自動許可しない。
ready 原稿の hash は同じ版でも手順の陳腐化を検出する。本体の build 情報は生成元の記録であり、
製品 SemVer の対応範囲や署名ではない。変更時は互換・不一致・改変保護の回帰も更新する。
配置・更新・検査の契約は[利用者ガイド](../userguide/user-guide.md#今の-coding-agent-に操作を任せる)に記載する。
通常の CLI と内部 role の実行を、この skill の配置や検査成功には依存させない。

doctor 結果の永続キャッシュと tool shim は未実装である。追加する場合は、操作知識、
日時・環境付きの診断結果、正式な作業状態を分け、前回の探索成功を現在の認証・実行成功と扱わない。
tool の確認済み実体パスを子 process の起動へ接続し、shim の配置だけで PATH の解決を保証しない。
Windows では symlink を必須にせず、`.exe` と `.cmd` / `.bat` の起動方式を区別する。
既存の検証コマンドには Unix 専用の process group 停止処理と `sh` の前提が残っており、
Windows 向けビルド・実行の対応は別途必要である。

固定した Go SDK `v1.0.19` の `SessionConfig` と `ResumeSessionConfig` は、
`SkillDirectories`、`PluginDirectories`、`MCPServers`、`Hooks` を持つ。
`EnableSkills` は skills の読込み、`EnableFileHooks` は `.github/hooks/` の読込みを制御する。
SDK の `Hooks` callback と file hooks は別の設定である。
明示した `PluginDirectories` は `EnableConfigDiscovery: false` の場合にも読み込まれる。
MCP の resident resume では、既に実行中の server を `DisabledMCPServers` だけで停止する機能は提供されない。

Taxion は、Task の出典付き decision に保存した `taxion.tools.v1` の承認から、
役割別の skills・skill-only plugins・固定 MCP リクエスト・RTK を接続する。
実行ファイル、入力、directory の内容 hash、引数、tool 名、承認済み JSON、
private evidence directory を session の設定 hash に含め、create/resume で同じ条件を使う。
この内容固定方式では config discovery・file hooks の自動読込みを無効にし、
明示した directory と SDK hooks だけを接続する。通常利用にはこの承認を要求しない。

通常の session は SDK の `ModeCopilotCli` を使い、config discovery・skills・file hooks・
on-demand instruction discovery の既定値を上書きしない。project の
`.agents/skills`・`.github/skills`・`.claude/skills`、個人の `.agents/skills` と
Copilot home、追加登録済みの配置、plugin・agent・指示は Copilot 本来の規則で探索する。
Copilot home は明示 `BaseDirectory`、`COPILOT_HOME`、`~/.copilot` の順に解決する。
使用しないバックエンドの directory は探索しない。通常は `skill` tool を追加し、
Planner / Orchestrator も利用できる。file・shell・subagent tool の許可は追加しない。
file hooks と native server は信頼する host 設定であり、role permission callback による
sandbox の対象ではない。設定の内容は利用者が実行前に確認する。

`taxion doctor` は同じ adapter の CLI・worktree・環境・Copilot home から native な
`skill`・`plugin`・`instruction`・`mcp`・`lsp` の `list --json` を取得する。
agent / hook のファイルと未作成の標準探索先を補足し、呼出し tool の不足、
無効化、形式不正、個人用 agent の優先を示す。取得できない項目を空一覧へ置き換えない。
native な一覧はモデルによる選択・実ロード・動的接続の証拠ではない。
診断では session 作成、指示送信、hooks・MCP の起動、認証照会を行わず、
固定 policy がある場合は通常探索をせず同じ asset を検査する。
新しい通常探索方式を session config hash に含め、旧方式の保存 hash で実行再接続しない。
旧 session の停止専用経路は引き続き使え、receipt は書き換えない。

skills と plugins は canonical な絶対パスを指定する。
plugin は root の `plugin.json` と `skills/` からなる skill-only 形式を使う。
`taxion tools hash --path /absolute/path` の hash は、相対名・mode・size・内容を含む
asset の識別値であり、仕様入力の `documentHash` は原本 bytes の SHA-256 である。
承認済み asset の変更は再承認を必要とする。

承認済み role policy に `skills` または `plugins` があれば、Taxion は
SDK の許可 tool に `skill` を追加する。名前を prompt に書くだけでは接続しない。
固定方式で asset のない policy に `skill` を明示指定すると、実行前に拒否する。
skills のない MCP / RTK のみの policy では `EnableSkills` を有効にしない。
Coder と Reviewer は、承認済み skill の通常の custom-tool permission を扱えるが、
managed approval を要求する操作の拒否、Reviewer read-only、worktree 外の拒否は維持する。
skill に書かれた追加ファイルの読み取りやスクリプト実行も、この境界を越える許可にはならない。

`workflow run|step` は、Coder と将来の Reviewer の role policy を発行済み
decision と現在の Task で照合し、asset の存在と hash、明示した `skill` tool の承認を
session 作成前に検査する。実行直前と各 tool hook でも照合を続けるため、
事前検査後の asset 改変を許可したことにはならない。

MCP は host が exact arguments を照合してから、承認された stdio server を一回起動する。
server の実行ファイルは信頼された host code として承認し、ambient credential を子プロセスへ渡さない。
SDK の role permission は引き続き通常の read/write と管理 commit を判断する。
独立 Reviewer は、controller が発行した元 Coder との対応と同じ decision を照合して、
元 Task の Reviewer 向け tools 承認を引き継ぐ。停止用の resume は extensions を追加せず接続する。

SDK hooks は原本の tool result と failure を先に private JSON へ保存し、
opaque filename と hash の receipt を返す。directory は `0700`、原本ファイルは `0600` とする。
保存失敗と圧縮失敗は runtime の失敗イベントにも通知し、controller が停止処理へ進めるようにする。
RTK は isolated HOME で `pipe --filter` を実行し、成功した 4096 bytes 以上の plain text を圧縮する。
JSON/XML と failure は原形を保持し、圧縮結果には warning/error/fail 行を保持する。
SDK built-in result の `textResultForLlm` と custom result の `textResultForLLM` を扱い、
status・exit code・telemetry をそのまま残す。
RTK 自身の stdout・stderr・exit status も原本として保存する。

承認形式は `kind`、`taskId`、`actor`、役割名を key にした `roles` を持つ JSON である。
各 role policy は `skills`、`plugins`、`mcp`、`rtk`、`compressionFilter`、
`evidenceDirectory`、`allowPublication` を保持する。
MCP binding は `name`、`command`、`args`、`inputs`、`tool`、`arguments` を固定する。
この JSON を Task decision の `text` に保存し、decision の `source` に Human の判断の出典を記録する。
通常の Task 承認・発行手順は [Task Graph reference](task-graph.md) に従う。
tools を使った Task の公開には `allowPublication: true` の明示承認を必要とする。

`--compact-context` は保存済みの結果・exit code・証拠を保持して重複した指示を省く。
元の完全な instruction は SQLite に保持する。
native plugin discovery、実 stdio MCP、実 RTK の圧縮率と原本保持、
SDK の create/resume 設定と権限を無料回帰で確認する。
実 SDK による仕様検索から独立レビューまでの受入れ状況は
[仕様入力](specification-input.md) と #60 に記録する。
通常テストは独自の合成 skill と一時 directory を使い、特定の外部 skill、
個人の home、インストール済み Copilot、認証、ネットワーク、有料モデルを要求しない。
native plugin discovery は `TAXION_TEST_LOCAL_PLUGIN_DISCOVERY=1` による
無料の明示 opt-in とし、指定時に CLI がなければ失敗する。

SDK 設定・permission・hook の合成回帰は、実モデルが skill を選び、正常に
ロードし、文書の品質工程を完了した証拠とは区別する。実利用は Taxion 配下の
session の tool request と完了結果、private tool evidence、成果物の SHA、
検査結果と独立レビューを対応付けて確認する。
親の開発支援 session の skill 起動や discovery の成功を代替証拠にしない。
Release 0.1.0 の文書 dogfood における実 skill 利用は、Issue #78 の文書成果物について Coder の実 invocation と同一 tree の独立レビュー受入れまで確認済みである。
そこで選択する外部 skill は製品の固定依存ではなく、操作者が利用許可と内容を承認する。

## Dispatch の実行と context

v0.4.0 は共通のゴール指向方針と担当別の責務を ready・Planner・質問回答・runtime・workflow・ACP の原稿へ接続する。追加作業には成果への具体的な寄与を求め、質問数やレビュー回数そのものを評価しない。承認・予算・独立性を維持し、関連文書も実装と同じ target で確認する。新しい request は文書整合 criterion を承認前の表示計画へ追加する。host の全件 acceptance 検査で FAIL・欠落・証拠なしを拒否するが、意味上の真偽や外部 Release 操作まで自動強制するものではない。

`workflow evaluate --id FLOW_ID --require-quality` は保存 graph の妥当性と、完了・独立同一 target 承認・文書 criterion を確認し、未達なら非zero終了する。`RECORDED_PASS` は保存証拠上の条件充足であって、実モデルの判断品質や安全性を独立実測した値ではない。集計範囲・欠損・未測定を JSON に明示し、品質未達と費用を加算して順位付けしない。[ADR-0040](adr/0040-constrain-goal-oriented-efficiency-by-quality-evidence.md) に対象経路と限界を記録する。

Planner→Coder、Planner→Reviewer、Coder→Reviewer は、Taxion に保存した契約・結果を参照して引き継ぐ。モデルに ID・承認・hash の転記や、送り手の SDK session ファイルの再読取りを要求しない。結果 ID の解決では、保存元 Dispatch と instruction hash を照合し、存在しない参照や保存結果との不一致をエラーにする。権限内で本文まで解決できることを引継ぎの条件とする。

新しい bound session が `work-report` を提出するときは、役割を問わず識別子を `evidence` に、報告本文を `workReport` に保存する。本文が空の成功結果は拒否する。SDK session のファイルパスだけでは報告を引き継げない。旧結果は書き換えず、必要な証拠を出典付きで明示的に補足する。報告本文は送り手の主張であり、実施済みの証明とは区別する。

CLI・ACP・workflow の `--controller-validation-submission` は、いずれも bound result schema を使う。組込み prompt の workflow もこの方式に含む。旧形式の結果読取りは維持するが、旧 session の構成 hash を新方式に合わせて書き換えない。

SDK の tool 開始・終了と skill invocation は、名前・呼出し ID・必要な path・成否・skill 本文の hash だけを中立 event として DB に保存する。tool の引数全体・出力全文・shell command・skill 本文はこの証拠へ複写しない。controller が同じ session の receipt と hash を照合し、`executionEvidence` として正式結果に付与する。モデルからこの欄を提出することは認めない。skill の invocation、tool の成功、報告された規範適用は別の証拠であり、品質合格を自動判定しない。

Reviewer の context には、保存済み Coder の基準 commit と固定対象 commit／tree の Git object から生成した差分を含める。compact 形式では `reviewDiff`、通常形式では本文末尾へ付ける。外部 diff helper・textconv は使わない。Reviewer に shell や worktree 外 read を許可して差分を取得させない。`workReport` と `executionEvidence` は共通の結果参照解決を通し、受け手の role によらず一度だけ渡す。

Coder と Reviewer に同じモデルが割り当てられても、役割の分離が成立する設計とする。
CLI・ACP の共通 system prompt 組立てで、Coder には承認要件の実装と未確認・失敗の開示、
Reviewer には要件から期待動作を組み立て、固定成果物と独立に照合する責務を追加する。
Reviewer は Coder の報告・テスト成功を未検証の主張として扱い、会話履歴の共有を前提にしない。
境界条件・失敗経路・回帰を確認し、指摘には実在するファイル・行・不具合の発生理由・根拠を示す。
欠陥の存在は決めつけず、指摘を捏造しない。必要な証拠を読めない場合は承認せず BLOCKED とする。
これらの指示は tool や書込みの許可を増やさず、同じモデルに共通する見落としをなくす保証でもない。
プロンプトへの指示の接続と、既知の不具合を含む差分で実モデルが正しく判断する受入れは区別する。

`taxion ready` は、今の coding agent に既存 CLI の操作手順を渡す。静的な出力だけを行い、
設定・workspace・Runtime を開かない。外側の agent が会話を改善して契約 JSON を生成し、
Human の明示承認後に既存 intake・confirm・review flow を操作する。
内部 Planner を使う次の `request` とは別の入口であり、外側の相談を再実行する前提にしない。
出力だけで agent の判断品質を保証せず、合成操作回帰と実 agent の受入れを分ける。
判断は [ADR-0031](adr/0031-bootstrap-the-current-coding-agent-with-ready.md) に記録する。

`taxion request` は元の依頼を DRAFT として保存し、file・shell tool を持たない Planner
観測を使って確認質問と実装計画を提案する。Planner の提案は実行承認ではない。
Human の `/approve` を受けて CLI が intake confirmation と review flow を作り、
既存の Controller で Coder、検証、独立 Reviewer を実行する。
Planner の各 call の観測利用量を記録する。token 閾値は後続作業を停止しない。
相談記録・usage・失敗と実行先は request ID・連番・hash を持つ SQLite の
`records.requests` に保存し、失敗時の自動再送は行わない。
実行イベントを二重保存せず、Flow ID から正式な Dispatch・event receipt を参照する。
`records show` で明示 export できる。外部 JSON・log の新規保存は行わない。
`workflow run|step --builtin-prompts` は snapshot mode の明示 opt-in で、
同梱 role prompt、file tool、厳密な結果照合、context 圧縮、
Controller への検証提出を選ぶ。従来の prompt 指定・空 allowlist の既定は変えない。
依頼ごとの設定 decision は `REF@task:PRIMARY_TASK_ID` で識別し、
設定 tuple と追加 Reviewer Task の exact approval を維持する。
判断根拠は [ADR-0030](adr/0030-refine-natural-language-requests-with-planner.md)、
操作は[利用者ガイド](../userguide/user-guide.md)が所有する。

残予算を指示へ付加する設計は [ADR-0029](adr/0029-separate-budget-guidance-from-provider-quota.md)
に採択し、Controller の送信補足と Copilot adapter の quota 読取りを実装した。
Controller は保存済み Flow usage と adapter の非推論 read-only 観測を分離し、
`off` / `flow` / `account` の解決結果に応じて既存の `Send` に補足する。
動的観測は送信ごとの receipt に保存し、system prompt の変更や別の有料 turn を必要としない。
単位・account・resource・対象期間・取得元・観測時刻と
`available` / `unsupported` / `unavailable` / `stale` を保持する。
quota の未知を成功やゼロに変えず、Copilot の実験的 API 型を core に漏らさない。
stop-only 接続は quota 照会や新しい指示を発行しない。

`taxion runtime start|send|resume|events|stop` は Copilot の CLI 実行経路とする。ACP server の `session/new` は CLI 発行の新 Dispatch ID だけを受け付け、Task・Dispatch は作らない。ACP・CLI は共通 Controller で session binding、tool 認可、event 保存、結果検証、safe stop を行う。新 ACP session は保存 Dispatch 契約への追加 context として prompt を一回送る。実行中 Dispatch への再接続は prompt を再送せず、既存 turn を観測する。Runtime 出力の差分は ACP session update として stream する。`taxion acp` は session 別 credit 上限、timeout、Coder system prompt を要求し、Reviewer Dispatch には Reviewer prompt も要求する。SDK の commit・stage tool は `--managed-commit`・`--managed-stage` の明示選択を保つ。ACP で再開できない保存 session は CLI runtime command で調査・照合する。

Task の `outputMode` は `commit`（省略時の既定）または `worktree-tree-snapshot` を選ぶ。
snapshot mode では Coder の `outputSha` は開始時の commit SHA のままにし、
Controller が算出した `outputTreeSha` と変更 path で成果物を識別する。
Controller は tree object をローカルの `refs/taxion/snapshots/` に保持する。
これは branch、commit、実 index、remote を変更しないが、Git ref を追加する。
Reviewer は baseline commit から同じ tree を別 worktree に再現し、開始時と終了時に照合する。
Git tree に含まれない ignored file はレビュー対象に含まれない。

`runtime`・`workflow run|step`・ACP の `--compact-context` は、担当に必要な目的・範囲・受入れ・検証・証拠・レビュー対象・Human decision・Memory・仕様引用・予算方針を JSON で渡す。設定と intake 承認は ID・出典・理由を保持し、設定全体の転記を省く。各 `result:` decision を保存済みの出典と照合し、必要な結果を渡す。欠落・改変した参照は拒否する。完全な不変 instruction と承認は DB に保持する。表示変更は system prompt・config hash を変えるため、旧 session に新 hash で再接続しない。取消専用 `runtime stop` は表示変更に依存しない。既存の非 compact session の原稿は維持する。

新しい Flow の結果 decision は `taxion.result-ref.v1` の Dispatch ID・instruction hash・result hash を保存し、過去 instruction を再帰的に埋め込まない。旧形式も読み取り、必要な結果は Controller が同じ workspace 内から解決する。Planner・質問 context では結果を別に含む箇所の decision を参照にする。個別質問では同じ Task の結果を `task.dispatches` に保持し、別 Task の関連 Reviewer 等の結果を `results` に一度だけ含める。`/results/N/result` 以下も引用先として検証する。

`--builtin-prompts` は束縛済み結果 schema `urn:taxion:dispatch-result:bound:v1` を使う。モデルは Dispatch・Task・role・repository・開始 SHA と snapshot mode の出力 commit SHA を転記せず、host が保存 session と観測済み開始 SHA から付与する。モデルがこれらの値を返して矛盾した場合は拒否する。verdict・finding・role の証拠と Git の独立検証は維持する。独自 prompt と CLI/ACP の既存結果 schema は変えない。保存形式と互換性の判断は [ADR-0033](adr/0033-reference-results-and-persist-workspace-records-incrementally.md) に記録する。

`workflow planning-context` は、入れ子履歴全体なしで保存 Memory、acceptance、blocker、依存、懸念、関連 result・SHA 対象、既存 Flow 上限を表示する。観測時刻と content hash を持つ read-only projection であり、atomic な実状態 snapshot、model 判断、実行認可ではない。workspace と Memory は別に読み、実行時は既存 Controller 検査を繰り返す。provider は呼ばない。

`conversation plan`・`AskPlanner` は、隔離質問の通信、出典付き設定承認、認証、repository・service 検査、入出力利用量の記録、期限・session soft ceiling、承認再検査、独立 cleanup を再利用する。圧縮 Planner context を、file・shell tool を持たない独立した Orchestrator session に送る。通常は native skills と指示を利用する。native schema は助言 recommendation を加え、確認済み・仮定・未確認の証拠を明示する。実 JSON pointer の出典を必須とし、`APPROVE`・実行 verdict は recommendation に許さない。推論中に記録が変わっても元観測を結果に付けて保つ。助言を自動適用せず、通常の Flow 進行にも model call を増やさない。

モデルへ渡す Planner context は `planner` と `observation` だけとし、同じ Task の legacy `task.task` wrapper は送らない。schema の引用候補と回答の出典検証もこの送信表現に合わせ、保存側にしかない `/task/task/...` は拒否する。保存した完全 context・観測 hash と、採用時の改変・鮮度検査は維持する。新規 `request` の計画 Task は計画の目的を持ち、元依頼を title・objective に複写しない。元依頼の全文は intake decision に保持し、対話の先頭は `/planner/task/decisions/0/text` を参照する。実行 Task を作る際にはその参照を元依頼へ戻し、計画 Task ID と全対話を保存する。計画専用の全 path 一覧と参照本文は実行 Task に複写しない。通常の個別質問の `/task/task/...` は変更しない。

`RecordPlannerDraft`・`workflow planner-draft`・監視中の `/adopt` は、変更していない計画助言を未承認 `SUPPLEMENT` draft に採用する。発行前に再構成 context、結果対象、Memory、Graph、Flow 契約、保留した引継ぎを照合する。context field の欠落・偽造で有効な観測 hash を流用させない。transaction で Graph・Memory を再確認するが、独立 Memory 保存は database 間の atomic snapshot を提供しない。既存の契約維持 draft には結果に適合する `REVIEW`・`REPAIR` だけを入れる。draft に観測・出典 metadata と不確実性を残す。session ID は報告された出典で、検証済み provider receipt ではない。core の完全一致 ID による replay 検査で過去版を保つ。Human draft 承認と明示 handoff release は別操作で、Dispatch・実行には Controller 検査が引き続き適用される。`REPLAN`・`INVESTIGATE`・`HOLD`・`HUMAN_REQUIRED`・`REPORT` を通知なしに修正 instruction にしない。契約変更の再承認は #73 として実装し、M12 の実 SDK 受入れで確認した。decision に基づく無人進行は、この受入れでは確認していない。

`runtime`・`workflow run|step` は任意の `--strict-result-identities` を受け付ける。native response schema は発行 Dispatch ID、Task ID、role、repository、期待実行開始 SHA を固定する。修正時は初期 baseline でなく不変の対象 SHA を使う。output SHA・verdict は報告事実のままで、Controller が独立検証する。`nextTransition` は既存 Task status の語彙に限定するが、遷移の選択や保存状態の変更はしない。config hash が変わるため実行接続にも同じ flag を指定し、取消専用 `runtime stop` には指定しない。旧 session 用の既定 schema は保ち、拒否応答を成功結果へ修復しない。shell 権限のない agent は `validations: []` を提出できる。Controller は成功した実装提出後に認可 command を実行し、実 exit code、時刻、Git 出典を保存する。実施前の command 実行を創作せず、検証待ちを test 成功の証拠としない。

Controller の実行完了と Git 検証後に、未記載なら `controller-validation` の evidence marker を追加する。agent は実施前にその段階を主張しなくてよい。Controller validation の error、取消、未実行では生成せず、無関係な出典参照・verdict も作らない。marker は command の観測を示し、全 test 成功を意味しない。有効な REQUEST_CHANGES review は実際の非ゼロ test exit code を保持できる。

Reviewer の `acceptance[].evidence`・`findings[].evidence` は、最上位の `evidence` array に完全一致で含まれる文字列を参照する必要がある。verdict・finding が妥当そうでも未接続の参照は拒否する。source 分析を実施済み test として表示しない。

`runtime auth` は明示的な事前照会で、実行操作ではない。Copilot adapter の `auth.getStatus` 前に固定設定と現在・保存 Dispatch 承認を照合する。`CheckAuthentication` は認証済み account が要求 account と一致し、host が明示 `github.com` であることを確認する。SDK session を作らず、結果を cache せず、adapter config hash も変えない。返す error に provider 状態 message と生 RPC error は含めないが、取消は識別できる。

この照会は credential 参照の解決、repository actor 選択、契約資格の証明を行わない。実行時は同じ認証 method を別に使って確認する。local protocol fixture の成功は login 済み受入れの証拠ではない。

`config bind --dispatch` は完全な承認済み設定選択、decision 参照、field 出典、記録時刻を Dispatch に保存する。Controller は一つの workspace transaction で現在 Task と発行 instruction の承認を確認して保存する。core は SDK 非依存 JSON を保存し、identity、出典、保存承認への membership を検査する。設定層は完全 tuple の schema を検査する。

選択後の Dispatch 設定検査と明示認証照会は、その tuple を要求する。同一値の再指定は初回出典と時刻を保つ。別値の binding や、既存 session に初めて選択を付けるには、新たに認可した Dispatch が必要。選択のない旧記録は読める。選択記録だけでは実認証・契約資格の証拠にならない。

Copilot adapter は任意の選択 tuple を検証・正規化して所有し、config hash に含める。`runtime.ConfigurationIdentity` が公開するのは中立 JSON と account 認証機能だけ。未選択の旧 hash は保つ。Controller は選択済み Dispatch に Runtime tuple がない場合と不一致を拒否する。選択済み start・resume・send は SDK 操作前に現在 Task・保存 instruction の承認と実 account を検査する。認証後、新 session binding の transaction 内、instruction 送信前にも承認を再検査する。管理下の Git も更新 transaction 内で tuple・承認を確認する。

runtime CLI は start・resume・send・events・commit の固定設定を解決する。新 Dispatch は選択を自動保存し、選択なしの旧 session は新たに認可した Dispatch を要求する。共通 runtime・workflow factory は保存選択で adapter を構築する。全 workflow Task は発行設定承認に明示 ID が必要。`taxion.execution-config.v1` の任意 `additionalTaskIds` は追加 Task ID の完全一致 list で、pattern や全子 Task への暗黙委譲ではない。`flow`、`maxRepairs: 1` なら review は `flow-review-0`・`flow-review-1` で、修正 Coder は元 Task ID を保つ。省略時は単一 Task 承認を保つ。空・空白付き・pattern・重複・主 Task ID は拒否する。

生成 review Task は出典付き承認を変更せず継承する。設定・core は完全 allowed tuple を持つ主 Task または明示追加 Task だけを認める。未列挙 review は許可せず、Flow 独自の認可と上限も必要とする。list は発行前に記録し、既存 Dispatch を拡張しない。local 検査は Controller・実 Git で選択済み Coder→review→修正→再レビューを実行し、login 済み SDK model は使わない。

CLI safe stop は、変更した prompt・tool・schema を再生成せず、保存 session ID、role、worktree、config hash に対応付けた取消専用 adapter を使う。現在の固定設定でなく保存選択の credential 参照を使い、認証照会は要求しない。Start・Send・通常 Resume は拒否する。停止接続は pending work の続行、resume event、transcript 修復、discovery を無効にし、tool を公開せず全 permission 要求を拒否する。local busy 状態を観測していなくても切断前に abort を送る。旧実行契約も、新契約での実行権限を得ずに停止できる。接続・取消・保存の失敗は明示し、この経路で model instruction は送らない。設定なしの local・proc は従来動作を保つ。credential 解決、repository actor 選択、契約資格、login 済み受入れは別の要件とする。

`runtime commit` は lock 済み index snapshot と message を scan してから正確な tree を更新する host 操作で、stage・Task 完了は行わない。`runtime start --managed-commit`・`workflow run --managed-commit` が Coder 用 `taxion_commit` を明示的に有効にする。SDK 非依存の `runtime.ManagedCommit` binding は Dispatch ID、scanner 参照、host callback を持ち、SDK tool 型は Copilot adapter 内に残す。

callback は呼出し取消、session ID、message だけを受ける。登録 tool は repository・scope・scanner を選べない。未知 field、message 欠落、未知・idle・停止済み session、sub-agent に帰属する呼出しは拒否する。host 中断と Flow deadline は Git 操作も取り消す。permission 検査は有効のまま保つ。scanner 失敗や部分更新の明示 error を含め、Controller error は SDK tool call の失敗とする。成功出力には SHA と、レビュー・受入れがまだ必要であることを含める。

作成・resume は同じ tool を登録する。有効時は Dispatch、scanner 絶対 path、操作版を config hash に含める。option なしでは従来の tool 構成と hash を保つ。既存 session は binding 不一致なしに resume で tool を追加・削除できない。共通 runtime・workflow 構築は同じ callback を使い、Reviewer に commit tool を登録しない。

commit は stage 済み内容だけを使う。追加の `--managed-stage` は `--managed-commit` を要求し、作成・resume で `taxion_stage` を登録する。別の操作版で commit-only session の identity を保つ。SDK 非依存 binding の stage callback は session ID と明示 path array を受け、repository・scope・scanner は host が管理する。

stage は Controller の認可 transaction、呼出し検査、Flow 上限を共用する。正規化した相対 file または追跡済み削除だけを選び、directory 再帰、強制追加、Git metadata、worktree 外の親 symlink は選べない。literal path 指定で wildcard・pathspec 展開を防ぐ。全 tree の scope・path・内容検査までは非公開 index だけを変更する。検査済み copy を flush して実 index に atomic に反映し、HEAD・作業ファイルは保つ。失敗検査は反映せず、commit は再 scan する。Git clean filter は適用し、拒否後も到達不能 local blob が残る場合がある。信頼する repository 設定と悪意ある host process との競合は sandbox の対象外とする。

local 検査は登録 SDK stage・commit handler を Controller・実 Git で呼ぶが、login 済み model の tool 呼出しは証明しない。実接続の stage→commit→独立レビュー受入れは #72 で別に追跡する。

## event 購読の期間

adapter は、現在の instance が記録した event を再送してから、新しい event を購読する。Copilot は message、turn、failure、cancellation、usage、context-window、compaction、permission-decision の正規化 event を出す。usage は対応する Dispatch とともに保存する。Controller は各 usage の直後に `credit_status` を合成し、取得できた call 別 nano-AIU と完全性状態、session ceiling を報告する。Dispatch が account quota 読取りを承認している場合は、SDK から取得した `premium_interactions` の request 枠も別項目で報告する。これは AI Credit とは異なる単位であり、残高へ換算しない。AI Credit の account 残高・使用率・残り率は同単位の allowance が確認できない場合 `unavailable` とする。合成 event は保存済み provider usage を変更しない。context・compaction event は、取得できた場合に SDK の token 上限と圧縮結果を報告する。

turn が終わっても、同じ role の次の指示に使う session stream は残る。stop は stream を閉じる。購読 context の cancel は、その購読だけを閉じる。利用側は session の終了だけを待たず、自身の context も監視する必要がある。

process 再起動後、SDK は session ID で Copilot の会話を再開する。正規化 event stream は、現在の adapter instance が観測した event から始まる。

SDK 固有型を core から分離する理由は [ADR-0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md)、adapter 評価の計画は[ロードマップ](roadmap.md)に記録している。
