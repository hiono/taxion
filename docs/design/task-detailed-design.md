# Task Detailed Design

## 1. Domain fact

TaskはJobAggregate内のappend-only factとします。TaskIDはCommandIDから決定的に導出します。Task stateやRoleExecutionをTaskへ埋め込まず、実行・成果・証拠は別factとして保持します。

主要フィールド:
- ID / CommandID / JobID
- ParentTaskID / DependsOnTaskIDs
- Title / Purpose
- JobApprovalID / ContractRevision / CanonicalizationVersion / CanonicalHash
- effective TargetRepositories / TargetResources / Workspaces
- additional AcceptanceCriteria / RequiredEvidence
- effective ProhibitedEffects / ApprovalRequiredEffects
- effective ConcurrencyCeiling / Deadline
- CreatedAt / CreatedRevision

## 2. Effective scope

Task作成時に最新JobContractまたはParentTaskからeffective scopeを解決してTaskへ保存します。nil / omittedのtarget listは親scopeの継承を意味します。明示listはcanonicalized exact subsetとして保存します。明示的な空配列はそのdimensionを空に固定し、親scopeの値を継承しません。TargetRepositories / TargetResources / Workspacesの全てが空になるTaskは拒否します。

ConcurrencyCeiling、Deadlineはそれぞれ0 / zero timeをinherit sentinelとし、保存時には親から解決したeffective valueを記録します。明示値が親の上限を超える場合は拒否します。

ProhibitedEffects / ApprovalRequiredEffectsは親のeffective valuesとrequestの追加valuesをcanonicalized unionとして保存します。AcceptanceCriteria / RequiredEvidenceはTask固有の追加条件として保存します。

## 3. Parent and dependency validation

ParentTaskIDは同一Job・同一ContractRevisionの既存Taskを参照します。ParentTaskが存在する場合、子Taskのeffective target / concurrency / deadlineは親の範囲内でなければなりません。

DependsOnTaskIDsは同一Job・同一ContractRevisionの既存Taskを参照します。ParentTaskIDとdependency edgesは別フィールドです。Job validationではdependency graphを構築し、DFSでcycleを拒否します。Task作成は既存Taskだけを参照するため、新規edgeは既存nodeから新nodeへ逆向きに戻れませんが、restore / tamper検査としてcycle detectionを維持します。

## 4. Command and Store

TaskController.CreateはJobを読み込み、ACTIVE / latest JobApproval / deadlineをpreflightで検証します。Domain.NewTaskでeffective scopeとimmutable factを構築し、Store.AddTaskへexpected Job revisionを渡します。

Storeは同一transaction内でJobAggregateを再読込し、Domain.NewTaskとJob.Validateを再実行してTaskとCommandReceiptをappendします。Expected revision mismatch、idempotency conflict、validation failureでは部分変更を残しません。

## 5. Execution boundary

Task creationは実行開始ではありません。ExecutionPermissionはTaskIDとeffective scopeへ束縛します。TaskExecutionStart CommandはPermissionをsingle-useで消費し、Coder / Reviewer RoleExecutionとProviderStartIntentを同一JobAggregate transactionでappendします。Provider dispatchは別のController境界で行います。

## 6. Non-goals

- Task status / progress state machine
- RoleExecution / Provider operation creation
- Worktree allocation / release
- Task editing / supersession
- Scheduler implementation
