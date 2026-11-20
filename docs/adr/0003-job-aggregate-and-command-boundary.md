# ADR-0003: JobAggregateを作業・協調・成果の唯一のドメイン正本とする

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の旧ADR-0083（JobAggregate / Command境界）と旧ADR-0077（Job中心協調、JobApproval、成果・証拠の所有関係）を原典とする。旧ADR-0083のAccepted resolutionと2026-11-18 deletion boundary clarification、旧ADR-0077のADR-0080 / 0082による適用範囲の置換を確認し、新しいリポジトリ向けに再構成した。旧Git履歴や旧コードは移植しない。

## Context

Job、Task、RoleExecution、Role間通信、Provider実行、Worktree所有権、成果、証拠を単一の汎用Graphや可変Workspaceへ混在させると、異なる関係の意味が曖昧になり、状態変更・認可・永続化の迂回路が生じます。

Taxionでは、利用者の意図と成果を保持するJobを継続単位とし、作業分解、担当の実行、協調、外部Providerの事実、成果と証拠を明確な責務で関連付ける必要があります。

## Decision

**JobAggregateを、Jobの意図、承認、協調、実行事実、成果および証拠の唯一のドメイン正本とします。**

### 1. JobとTaskとRoleExecution

- Jobは利用者の依頼、目的、制約、承認、協調履歴、実行状態、成果および受入れ証拠を保持する永続的な作業単位です。
- Jobは最初のPlanner呼出しより前に作成します。初期状態はDRAFTとし、利用者の原文依頼を保存します。Goalは未設定でもよく、Plannerが提案する契約revisionに含めます。
- TaskはJob内の任意の作業分解です。Taskは必須のJobIDを持ちます。Job側にTaskIDsやRootTaskIDの逆方向所有indexを持たせません。
- RoleExecutionはRoleによる個々の実行です。JobIDを必須とし、TaskIDは任意とします。Job全体のPlanningを行うTaskless PlannerはTaskIDを持ちません。
- JobContractRevision、JobApproval、PlanningAuthorization、JobMessage、Provider operation intent / observation、Worktree ownership、JobOutput、JobEvidence、JobUsageおよびEffect decisionはJobの責務に属します。
- WorkspaceはJobAggregateの集合を保持する保存境界です。全Jobを一つの可変TaskGraphへ集約しません。

### 2. Task依存関係とRole協調関係を分離する

- Taskの親子包含とTask間の前提条件は別の関係として扱います。
- Role間の協調はJob内のappend-onlyなJobMessageと実行事実で表します。
- JobMessage、Reply、HandoffはTask依存関係を生成・解決しません。
- RoleExecutionの親子・起動元を別々の重複edgeとして永続化せず、必要な因果関係はcanonical JobMessage / eventから導出します。別indexを保持する場合は同一性と整合性を検証します。
- SchedulerはTaskの明示的な依存関係だけを用います。メッセージやRoleExecutionの親子関係をTask依存関係として解釈しません。

### 3. Handoffは受信側のTask scopeを明示する

- Handoffは受信Roleと受信Task scopeを明示します。
- 送信元RoleExecutionのTaskIDを受信側へ暗黙継承しません。
- Taskless PlannerへのJob-level相談と、同一Job内のTask-bound作業を区別します。
- 受信Taskは同じJobに属し、Job承認範囲を拡張しません。
- 情報不足や任意metadata欠落だけを理由に協調を拒否しません。identity、認可、scope、重複実行、禁止副作用の検査は維持します。

### 4. PlanningAuthorizationとJobApprovalを分離する

- 明示的な利用者起点のPlanningAuthorizationは、DRAFT JobにおけるTaskless Planner一回の限定的な計画・読取り専用実行を許可する独立したimmutable grantです。
- PlanningAuthorizationはJobApprovalではなく、JobをACTIVEにせず、実装TaskやCoder / Reviewer RoleExecutionを作成・実行する権限を持ちません。
- PlanningAuthorizationはJobID、原文依頼hash、認証済み利用者とintake provenance、Provider / account / configuration identity、repository identity、実際に強制できるread-only context境界、予算、期限、許可されたeffectおよびrecord hashに束縛します。
- Plannerの提案や利用者の回答はgrantを拡張しません。開始、各turn、各effect、resumeの前に再検証します。
- Providerがread-onlyを強制できない場合は、immutable snapshotまたは明示的context文書だけを渡します。いずれも強制できない場合、repository readを許可しません。
- 実装は完全なJobContractをimmutable revisionとして提案し、利用者がその正確なcanonical hashを明示承認したJobApprovalだけが実装権限を与えます。

### 5. Providerの論理状態と外部事実を分離する

- RoleExecution stateはTaxion内部の論理的な作業状態を表します。
- Provider session / process state、turn completion、Provider stop、event / usage persistence、result capture、output identity、evidence verificationは別々の事実です。
- 外部Provider operationは、呼出し前にTaxion生成のimmutable OperationIDとintentをJobAggregateへ永続化します。
- Provider observationsはOperationIDに束縛したappend-only factsとします。
- SQLite transactionの結果とProvider operationの結果を別の状態軸として扱います。
- operationの結果が不明な場合はUNKNOWNとして保持し、Providerが同じOperationIDの冪等性または照合可能性を証明しない限りblind resendしません。
- RoleExecutionのWAITING、SUSPENDED、CANCEL_REQUESTEDまたはterminal stateをProvider停止の証拠とみなしません。

### 6. Worktree ownershipをTask単位で管理する

- 開発TaskにはGit Worktreeを割り当てます。Taskless Job-level PlannerはWorktreeを持ちません。
- 同一Taskのmutating RoleExecutionは同じWorktreeを逐次引き継ぎます。
- Worktreeの所有権は永続化し、プロセス内mutexだけで排他を保証しません。
- Provider停止の証拠、結果・利用量の照合、成果物identityの確定が永続化されるまで所有権を解放しません。
- 停止状態または結果が不明なWorktreeはquarantineし、再利用しません。
- 独立性が確認された別Taskだけを別Worktreeで並行実行します。詳細な並行性契約は下位ADRで定義します。

### 7. すべてのJob変更を明示的Command境界へ集約する

- Job、Task、RoleExecution、JobMessage、Provider operation、Worktree ownership、JobOutput、JobEvidence、JobUsage、Effect decisionの変更はJobAggregateへの明示的Commandとして実行します。
- Commandはexpected aggregate revisionと安定したidempotency identityを持ちます。
- 一つのJobに対するCommandと関連レコードは一つのtransactionで原子的に確定します。
- 同じCommand IDと同じ内容の再送は同じreceiptへ収束させます。同じCommand IDで異なる内容を送信した場合は拒否します。
- 任意のWorkspace callbackや直接slice変更を、productionの代替write APIとして残しません。
- Coreは不変条件と内部整合性を検証し、Controllerは認証・承認・Providerの外部事実を検証します。ドメイン検証を外部実行の証拠と取り違えません。

### 8. 成果・証拠とJob完了

- JobOutputは成果物のimmutable identityとJob / Task / producing RoleExecutionのprovenanceを保持します。
- JobEvidenceは受入条件、証拠identity、producer、独立したverifier、検証状態を保持します。
- 成果の作成者自身の申告だけで成果を受入れ済みとしません。
- JobUsageはProvider operation / turnと照合できるidentity、完全性および利用量を保持します。
- Job完了は、必須受入条件を満たすprovenance-valid evidence、usage finality、Provider quiescence、Worktree releaseおよび未解決の安全・実行状態がないことを確認するCommandとしてのみ成立します。

## Implementation boundaries

- SQLiteの物理schema、table名、codec、Command型名は実装判断とします。ただし、JobAggregate単位のatomic transaction、expected revision、idempotent command identityおよび本ADRの不変条件を弱めてはなりません。
- 汎用分散transaction coordinator、汎用lock graph、全Provider共通のsession manager、推測的retry、旧TaskGraphの再導入は行いません。
- 旧Task / Dispatch / ReviewFlowの実行・認可経路やデータ形式を新しい実行経路へ持ち込みません。

## Consequences

- 実装・テストはJobAggregateと明示的Commandを正本として構築します。
- PlannerからCoder、Reviewer、修正、人間の介入、Job完了までを同じJobで追跡します。
- 合成テストはドメイン契約の証拠に限定し、実Provider・再起動復旧・実Job完遂の証拠と区別します。
- 物理schemaや保存技術の詳細は、実際の製品要件を満たす最小の実装を選択します。

## Relationship to other ADRs

- ADR-0001は製品目的を定義します。
- ADR-0002は旧設計の制約を温存せず再構築する原則を定義します。
- 認可、全経路の不変条件、6 Actor通信、Worktree並行性、外部Agent protocol、仕様・履歴・証拠の保存先は下位ADRで具体化します。
