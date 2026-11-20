# ADR-0003: JobAggregateを作業・協調・成果の唯一のドメイン正本とする

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の旧ADR-0083（blob f1250773ff5c8ddb650c9d8af79af3fe61ecaafd）、旧ADR-0077（blob fdca26027bcc69a530c7751e807a6f135514a300）、旧ADR-0086（blob 41870b82399f169a04073ec625942bcaf18694b7）を原典とする。旧ADR-0083のAccepted resolutionと2026-11-18 deletion boundary clarification、旧ADR-0077のADR-0080 / 0082による適用範囲の置換、旧ADR-0086の独立Memory subsystem退役判断を確認し、新しいリポジトリ向けに再構成した。旧Git履歴や旧コードは移植しない。

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
- JobContractRevision、JobApproval、PlanningAuthorization、JobQuestionAuthorization、JobProgressNote、JobMessage、Provider operation intent / observation、Worktree ownership、JobOutput、JobEvidence、JobUsageおよびEffect decisionはJobの責務に属します。
- JobProgressNoteはJob内の進捗・継続に必要な要点を出所と時刻に結び付けて保持するappend-only factです。可変計画の正本や独立Memory storeの代替にはしません。
- GoalはPlannerが提案するJobContractRevisionに含め、JobApprovalを作成する前に非空であることを検証します。Goal未設定のDRAFT Jobは、実装Taskや実装RoleExecutionを許可しません。
- WorkspaceはJobAggregateの集合を保持する保存境界です。全Jobを一つの可変TaskGraphへ集約しません。

### 2. Task依存関係とRole協調関係を分離する

- Taskの親子包含とTask間の前提条件は別の関係として扱います。
- Role間の協調はJob内のappend-onlyなJobMessageと実行事実で表します。
- JobMessage、Reply、HandoffはTask依存関係を生成・解決しません。
- RoleExecutionの親子・起動元を別々の重複edgeとして永続化せず、Handoff JobMessage / eventを因果関係のcanonical representationとし、recipient executionはTargetExecutionIDで関連付けます。ParentExecutionID等の参照ビューはcanonical messageから導出します。別indexを保持する場合はsource / target / messageの同一性と整合性を検証します。
- Taskの親子包含と前提条件は別のtyped relationshipです。完了準備判定は両方を考慮し、Schedulerは明示的な前提条件だけを使います。
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
- 承認済みJob内でRoleからTaskless PLANNERへ質問し、限定的なread-only context acquisitionを伴う個別回答実行が必要な場合は、別のimmutable JobQuestionAuthorizationを発行できます。このgrantはJobApproval、planning RoleExecution、元Question JobMessage、Provider / account / configuration、read-only context、budget、deadlineに束縛します。QUESTION本文やMessageKindだけから自動発行せず、JobApprovalやPlanningAuthorizationを拡張しません。
- Providerがread-onlyを強制できない場合は、immutable snapshotまたは明示的context文書だけを渡します。いずれも強制できない場合、repository readを許可しません。
- 実装は完全なJobContractをimmutable revisionとして提案し、利用者がその正確なcanonical hashを明示承認したJobApprovalだけが実装権限を与えます。

### 5. Providerの論理状態と外部事実を分離する

- RoleExecution stateはTaxion内部の論理的な作業状態を表します。
- Provider session / process state、turn completion、Provider stop、event / usage persistence、result capture、output identity、evidence verificationは別々の事実です。
- 外部Provider operationは、呼出し前にTaxion生成のimmutable OperationIDとintentをJobAggregateへ永続化します。
- Provider observationsはOperationIDに束縛したappend-only factsとします。
- SQLite transactionの結果とProvider operationの結果を別の状態軸として扱います。
- operationの結果が不明な場合はUNKNOWNとして保持し、Providerが同じOperationIDの冪等性または照合可能性を証明しない限りblind resendしません。

#### Operation intentと必要なProvider observation

外部呼出しの前に、Taxion生成のimmutable OperationIDとintentをJobAggregateへ永続化します。Worktree予約が必要なStartでは、Worktree予約とStart intentを同じJob transactionで確定します。Provider呼出しはtransactionのcommit確認後に行い、commit結果がUNKNOWNの場合はOperationIDと予約を読み戻してから判断します。

| Operation | 呼出し前に固定するintent | 次へ進むために必要な観測 |
|---|---|---|
| Start | Job / RoleExecution / Provider / configuration identity、start request hash | Provider session identityと受理、または未解決Start |
| Resume | 保存済みの同一session identityとRoleExecution binding | 同一sessionへの再接続確認。replacement sessionを旧Executionへ偽装しない |
| Send | session、stable turn identity、prompt hash | Provider受理後のterminal turn observation。受理だけでは完了しない |
| Stop | session、停止理由、terminal intent | Adapter固有のstop evidenceとassurance level。cancel通知だけでは停止証明にならない |
| Effect | 承認済みJobEffect、canonical target/resource、originating turn | Provider / hostが実際のeffectと結果を観測した証拠 |

Sendのterminal event、Usage、最終出力は別のfactsとして保存し、turn identityとsession bindingを照合します。Providerへ到達した可能性がありreceiptやterminal eventが未保存の場合、自動再送しません。Stop intentは呼出し前に保存し、RoleExecutionのterminal status、Stop APIの成功戻り値、cancel通知だけで停止を確定しません。Providerが書込みを継続できない証拠と最終結果・Usageの照合が永続化されるまでWorktreeを保持します。

各Provider Adapterは、少なくとも次の能力を個別に宣言・検証します: OperationIDによるStart idempotency / lookup、process再起動後のsession reattachment、turn identityによるSend idempotency / lookup、event replay / cursor continuity、stop proof、lost event後のfinal output取得、Usage identity / completeness / deduplication。未対応能力は未対応として明示し、楽観的に補完しません。

Model Call / Tool CallはRoleExecution、turn、Provider operationまたはEffect observationへ相関し、観測可能な呼出しの追跡をJob内に保持します。Providerが報告しない呼出しや欠落eventがある場合はcoverage gapを記録し、全呼出しを追跡済みと宣言しません。

Recoveryはdurable intentとobservationを先に照合します。snapshot / restoreは、未解決Provider operation、稼働可能性のあるProvider writer、未照合turn result / Usage、保持中またはquarantine中のWorktree ownerがない場合に限ります。
- RoleExecutionのWAITING、SUSPENDED、CANCEL_REQUESTEDまたはterminal stateをProvider停止の証拠とみなしません。

### 6. Worktree ownershipをTask単位で管理する

- 開発TaskにはGit Worktreeを割り当てます。Taskless Job-level PlannerはWorktreeを持ちません。
- 同一Taskのmutating RoleExecutionは同じWorktreeを逐次引き継ぎます。
- Worktreeの所有権は永続化し、プロセス内mutexだけで排他を保証しません。
- Provider停止の証拠、結果・利用量の照合、成果物identityの確定が永続化されるまで所有権を解放しません。
- 停止状態または結果が不明なWorktreeはquarantineし、再利用しません。
- 独立性が確認された別Taskだけを別Worktreeで並行実行します。詳細な並行性契約は下位ADRで定義します。

### 7. すべてのJob変更を明示的Command境界へ集約する

- Job、Task、RoleExecution、JobProgressNote、JobMessage、Provider operation、Worktree ownership、JobOutput、JobEvidence、JobUsage、Effect decisionの変更はJobAggregateへの明示的Commandとして実行します。
- Commandはexpected aggregate revisionと安定したidempotency identityを持ちます。
- 一つのJobに対するCommandと関連レコードは一つのtransactionで原子的に確定します。
- 同じCommand IDと同じ内容の再送は同じreceiptへ収束させます。同じCommand IDで異なる内容を送信した場合は拒否します。
- 任意のWorkspace callbackや直接slice変更を、productionの代替write APIとして残しません。
- 旧GraphTask / TaskGraph、履歴projection、旧Task / Dispatch / ReviewFlow workspace format、decoders、validators、migration readers、fixturesをread-only archiveやcompatibility readerとして残しません。具体的な現行Job-native use caseが後から必要になった場合は、必要なfacts、consumer、retention、Job-native schemaを別ADRで定義します。旧approvalやexecution receiptを新Job authorityへ変換しません。
- 旧Memory subsystemの独立DB / schema、CLI / API、migration、Snapshot / Inspect / Restore、旧Task / Dispatch参照を新Taxionへ持ち込みません。旧MemoryデータをJob権限、JobApproval、PlanningAuthorization、JobContract、RoleExecutionまたはGraph edgeへ自動変換・復元しません。必要なJobの目的、計画、質問・回答、判断、成果、証拠、利用量、Provider観測はJob-native recordsとしてJobAggregateに保持します。
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

## Alternatives considered

- **旧TaskGraphへJob情報を追加して維持する:** 異なる関係とmutation境界を混在させ、旧実行経路を温存するため採用しません。
- **Task dependency、Role lineage、Message、Resource ownershipを一つの汎用Graph edgeへ統合する:** 関係の意味が異なり、Schedulerが誤ったedgeを解釈するため採用しません。
- **JobQuestionAuthorizationをQUESTION種別から自動生成する:** 通信intentが実行権限を作成するため採用しません。
- **RoleExecution statusをProvider stop/resultの証拠として扱う:** 論理状態と外部実行事実が異なるため採用しません。
- **旧Task-era形式をread-only decoder / migration readerとして残す:** 新しい正本に旧モデルの責務と保守負担を残すため採用しません。

## Acceptance criteria

- Jobのすべての変更がexpected revisionとidempotency identityを持つJobAggregate Commandを通ります。
- Task dependencyとRole collaborationが別の関係として保存・評価されます。
- PlanningAuthorizationとJobQuestionAuthorizationが異なる用途・scopeで発行され、JobApprovalへ昇格しません。
- Provider operation intentが外部副作用より前に永続化され、UNKNOWNを推測で解消しません。
- Provider capabilityが明示され、stop proof / result / usage照合前にWorktreeを解放しません。
- 旧TaskGraph等の実行・履歴projection・decoder・migration経路を新Taxionへ持ち込みません。

## Consequences

- 実装・テストはJobAggregateと明示的Commandを正本として構築します。
- PlannerからCoder、Reviewer、修正、人間の介入、Job完了までを同じJobで追跡します。
- 合成テストはドメイン契約の証拠に限定し、実Provider・再起動復旧・実Job完遂の証拠と区別します。
- 物理schemaや保存技術の詳細は、実際の製品要件を満たす最小の実装を選択します。

## Relationship to other ADRs

- ADR-0001は製品目的を定義します。
- ADR-0002は旧設計の制約を温存せず再構築する原則を定義します。
- 認可、全経路の不変条件、6 Actor通信、Worktree並行性、外部Agent protocol、仕様・履歴・証拠の保存先は下位ADRで具体化します。
