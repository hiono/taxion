# Task Requirements

Version: 1.3
Date: 2026-11-23
Governing ADRs: ADR-200, ADR-201, ADR-202, ADR-400, ADR-408, ADR-500
Governing contract: [Product Contract](product-contract.md)

## 1. Purpose

TaskはJob内の任意の作業分解です。JobそのものやRoleExecutionとは別のimmutable factとしてJobAggregateへ保存します。Taskの作成は実行権限やProvider operationを開始しません。

## 2. Creation authority

- TaskはJobApproval後のACTIVE Jobに対してのみ作成できます。
- Taskは現在の最新JobContractRevisionとJobApprovalへ束縛します。
- DRAFT Job、JobApprovalの欠落、古いapproval、canonical hash不一致、deadline超過では作成を拒否します。
- Task creationはJobAggregate Commandであり、expected revisionとCommandIDを要求します。
- 同一CommandID・同一payloadの再送は元のTaskとreceiptへ収束します。異なるpayloadの再利用は拒否します。
- Task creationはRoleExecution、ExecutionPermission、Provider operation、Worktreeを作成しません。

## 3. Task identity and decomposition

Taskは次を保持します。
- JobIDとCommandIDから導出する不変TaskID
- TitleとPurpose
- 任意のParentTaskID
- 明示的なDependsOnTaskIDs
- approved JobContract revision / JobApproval identity
- effective target repositories / resources / workspaces
- additional acceptance criteria / required evidence
- effective prohibited effects / approval-required effects
- effective turn / token budget、concurrency ceiling、deadline
- CreatedAtとCreatedRevision

Parent-child包含とTask dependencyは異なる関係です。SchedulerはDependsOnTaskIDsだけをdependencyとして扱います。JobMessageやRoleExecutionの関係からdependencyを推定しません。

ParentTaskIDおよびDependsOnTaskIDsは同一Job・同一ContractRevisionの既存Taskだけを参照します。Self-reference、重複、存在しないTask、他Job / 他revisionのTask、cycleを拒否します。

## 4. Scope narrowing

TaskのscopeはJobContractまたはParentTaskのeffective scopeを継承します。requestでtarget repository / resource / workspaceを省略（nil）した場合は親scopeを継承します。明示した値は親scopeの完全一致subsetでなければなりません。明示的な空配列はそのresource dimensionへのアクセスを許可しない指定として扱います。TargetRepositories / TargetResources / Workspacesの全てを空にするTaskは拒否します。prefix / substring / host-only matchは認めません。

TaskのBudget、ConcurrencyCeiling、Deadlineはrequestで省略した次元を親から継承します。明示値は親の上限以下でなければなりません。BudgetはMaxTurns / MaxTokensともに正数とします。Deadlineは現在時刻より後で、JobContract / ParentTaskのdeadline以下とします。

TaskのProhibitedEffectsとApprovalRequiredEffectsはJobContract / ParentTaskの値を継承し、追加だけを許可します。既存の禁止や追加承認要件を削除できません。

AcceptanceCriteriaとRequiredEvidenceはTask固有の追加条件です。Task creationはJobContractの受入条件を変更・削除しません。

## 5. Immutability and validation

- Taskはappend-only factです。既存Taskのscope、dependency、budget、deadlineをin-place変更しません。
- JobAggregate validationは復元時もTaskID / CommandIDの一意性、approval provenance、parent / dependency参照、scope subset、budget / deadline上限、dependency DAGを検証します。
- Task作成の拒否はJob revision、Tasks、receiptを変更しません。
- Task creation後もJobApprovalとJobContractRevisionがauthorityの正本です。TaskはJob scopeを拡張できません。
- Task state / progressはこのTask definition unitでは定義しません。RoleExecution、Provider observations、Output、Evidence等の事実と混同しません。

## 6. Task execution and Worktree

Task creationはWorktree、ExecutionPermission、RoleExecution、Provider operationを作成しません。実行開始は別Commandとします。

- Task-bound executionはExecutionPermissionをTaskID、effective resource scope、budget、deadline、Provider identityへ束縛します。
- TaskExecutionStartはtrusted TaskWorktreeManagerを必須とします。Managerはtrusted Repository registryからTaskのapproved target repositoryを解決し、Job / Taskごとのdetached Git Worktreeを作成または再検証します。
- 現在の実装は単一repository Taskだけを許可します。複数repository Task、未登録Repository、canonical pathでないWorktreeは拒否します。
- WorktreeAssignmentはrepository identity / path、Worktree path、baseline commit、owner RoleExecutionを保持します。WorktreeAssignment、RoleExecution、ProviderStartIntentは同一JobAggregate transactionで保存します。
- RoleExecution.WorktreeIDとProviderStartIntent.WorktreeIDは同じWorktreeAssignmentを参照します。exact replayでは保存済みWorktreeを再検証し、同一Taskへの別RoleExecutionによる再取得はhandoffが成立するまで拒否します。
- Provider dispatchはtrusted Worktree verifierでassignment owner、Repository identity、Worktree path、baseline ancestry、detached stateを再検証します。
- Provider StartRequestにはRepositoryID、WorktreePath、baseline commitを含めます。Worktree付きStartはTask Worktree capabilityの証拠がE4以上の場合だけ許可します。Start idempotency / lookup capabilityは別途検証します。
- Worktree lifecycle enforcement開始前に作成された既存RoleExecutionは読み戻せます。ただしWorktree assignmentを持たない既存ExecutionからProvider dispatchは許可しません。
- Reviewer dispatchはimmutable commit / tree snapshotとread-only enforcementが成立するまで拒否します。
- Provider停止、event、final result、Usage、review target identityを照合できないWorktreeは保持します。handoff、quarantine、release、orphan cleanupは別の実装単位とし、未解決Worktreeを自動削除しません。

## 7. Task-bound Coder Turn

- Task CoderのTurn intent / observationはPlanningTurn factsと分離し、PlanningAuthorizationを権限根拠として使用しません。
- Turn intentはTask、Coder RoleExecution、ExecutionPermission、accepted Start Session、Provider identity、prompt digest、turn number、token reservationへ束縛します。
- intentとreceiptは同一JobAggregate transactionで保存し、Provider Sendより前にcommitします。
- 現在のJobApproval、ExecutionPermission、scope、budget、deadlineをTurn開始前に再検証します。
- Session identityをRoleExecution内で固定し、先行Turnが未解決の場合は次Turnを拒否します。
- UNKNOWNは同一OperationID / TurnIDで照合し、新しいTurn identityによるblind replayを禁止します。
- COMPLETED / FAILED observationはfinal ResultとComplete Usageの保存後にのみ追加します。Result欠落、partial Usage、identity mismatchではUNKNOWNを維持します。
- Reviewer Turnはimmutable read-only snapshot境界が成立するまで拒否します。

## 8. Task-bound Coder Provider Stop

- Stopは同一ACTIVE JobのTask-bound Coder RoleExecutionとaccepted Provider Sessionに限定します。権限根拠は当該ExecutionPermissionとJobApprovalです。
- Stop intentとCommand receiptを外部Provider呼出しより先にJobAggregateへappendします。
- Stop直前にPrincipal、JobApproval、Task、ExecutionPermission、Provider identity、accepted Session、Worktree assignment owner、および物理Worktreeを再検証します。
- Provider / SDK / configuration identityに束縛されたStop proof capabilityのE4以上の証拠を要求します。
- UNKNOWNまたはObservation欠落後は同じStop operationをblind replayしません。既存intentを照合し、未確定状態をUNKNOWNとして保持します。
- terminal Stop observationはProvider固有の証拠がStoppedかつQuiescentを示す場合だけ記録します。Stop intent後の追加Coder Turnを拒否します。
- 同じSessionに先行Resume intentがある場合、latest Resume observationがATTACHED / FAILEDになるまでStop intentを拒否します。
- StopはTaskTurnのResult / Usageを代替しません。Stopが確定しても未解決TurnやWorktreeを解放しません。

## 9. Task-bound Coder Provider Resume

- Resumeは同じACTIVE JobのTask-bound Coder RoleExecution、同じExecutionPermission / JobApproval、同じaccepted Provider Sessionに限定します。新しいSessionを同じRoleExecutionへ付け替えません。
- Resume intent / Command receiptをRuntime.Resumeより先にJobAggregateへappendします。Resume intentはTaskID / ExecutionPermissionID / Provider identity / Session / capability evidenceへ束縛します。
- Provider / SDK / configuration identityに束縛されたcross-process Session reattachment capabilityのE5証拠を要求します。未対応・未検証の場合はResumeを拒否します。
- Runtime.Resume直前にJobApproval、ExecutionPermission、Task、Provider identity、accepted Session、Worktree assignment owner、物理Worktreeを再検証します。
- UNKNOWNまたはObservation欠落時は同じResume OperationID / Sessionを照合し、別OperationIDや新Sessionによるblind retryを禁止します。
- latest Resume observationがATTACHEDでない場合、次のTaskTurnを拒否します。UNKNOWN / FAILEDを成功へ推測変換しません。
- Provider Stop intentが存在するSessionはResumeできません。Stop proof、final Result、Usage、Worktree ownershipを照合するまで同じJobの安全な継続を許可しません。

## 10. References

- ADR-201: JobAggregate / Task / RoleExecution boundary
- ADR-202: invariants across create / persist / restore
- ADR-408: Worktree ownership and Task concurrency
- ADR-500: JobApproval and prohibited effects
- Product Contract §4.4
- Product Acceptance AT-009
