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
- effective Budget / ConcurrencyCeiling / Deadline
- CreatedAt / CreatedRevision

## 2. Effective scope

Task作成時に最新JobContractまたはParentTaskからeffective scopeを解決してTaskへ保存します。nil / omittedのtarget listは親scopeの継承を意味します。明示listはcanonicalized exact subsetとして保存します。明示的な空配列はそのdimensionを空に固定し、親scopeの値を継承しません。TargetRepositories / TargetResources / Workspacesの全てが空になるTaskは拒否します。

BudgetのMaxTurns / MaxTokens、ConcurrencyCeiling、Deadlineは各次元で0 / zero timeをinherit sentinelとし、保存時には親から解決したeffective valueを記録します。明示値が親の上限を超える場合は拒否します。

ProhibitedEffects / ApprovalRequiredEffectsは親のeffective valuesとrequestの追加valuesをcanonicalized unionとして保存します。AcceptanceCriteria / RequiredEvidenceはTask固有の追加条件として保存します。

## 3. Parent and dependency validation

ParentTaskIDは同一Job・同一ContractRevisionの既存Taskを参照します。ParentTaskが存在する場合、子Taskのeffective target / budget / concurrency / deadlineは親の範囲内でなければなりません。

DependsOnTaskIDsは同一Job・同一ContractRevisionの既存Taskを参照します。ParentTaskIDとdependency edgesは別フィールドです。Job validationではdependency graphを構築し、DFSでcycleを拒否します。Task作成は既存Taskだけを参照するため、新規edgeは既存nodeから新nodeへ逆向きに戻れませんが、restore / tamper検査としてcycle detectionを維持します。

## 4. Command and Store

TaskController.CreateはJobを読み込み、ACTIVE / latest JobApproval / deadlineをpreflightで検証します。Domain.NewTaskでeffective scopeとimmutable factを構築し、Store.AddTaskへexpected Job revisionを渡します。

Storeは同一transaction内でJobAggregateを再読込し、Domain.NewTaskとJob.Validateを再実行してTaskとCommandReceiptをappendします。Expected revision mismatch、idempotency conflict、validation failureでは部分変更を残しません。

## 5. Execution boundary

Task creationは実行開始ではありません。ExecutionPermissionはTaskIDとeffective scope / budgetへ束縛します。

TaskExecutionStartはtrusted TaskWorktreeManagerを必須とします。ControllerはTaskのapproved target repositoryを照合し、WorktreeManagerが信頼済みRepository registryからdetached Git Worktreeを作成します。Worktree pathはJob / Taskから決定的に導出し、repository identity、canonical path、baseline commit、linked Worktree metadataを検証します。現在の実装は単一repository Taskだけを許可し、複数repository Taskはfail closedとします。

WorktreeAssignment、Coder / Reviewer RoleExecution、ProviderStartIntentは同一JobAggregate transactionでappendします。WorktreeAssignmentはrepository identity / path、Worktree path、baseline commit、owner RoleExecutionを保持します。RoleExecution.WorktreeIDとProviderStartIntent.WorktreeIDを同じassignmentへ束縛します。Worktree lifecycle enforcement開始前の既存RoleExecutionは復元できますが、assignmentを持たない既存ExecutionからProvider dispatchは許可しません。

ExecutionControllerは同一Taskの別RoleExecutionによるWorktree再取得を拒否します。exact replayでは保存済みassignmentを再検証します。handoffはProvider停止、event、final result、Usage、review target identityを照合する別Commandが成立するまで許可しません。

ProviderControllerはtrusted Worktree verifierでassignment ownerと物理Worktreeを再検証します。Task-bound Provider StartRequestにはRepositoryID、WorktreePath、baseline commitを含めます。Provider runtimeはTask Worktree capabilityの検証証拠がruntime minimum以上の場合だけWorktree付きStartを許可します。Task dispatchではE4以上のTask Worktree evidenceを要求します。Startのidempotency / lookup capabilityも別途検証します。

Reviewer dispatchはread-only snapshot境界が未実装のためfail closedとします。Worktreeの解放、quarantine、owner handoff、orphan reconciliationも未実装であり、未解決Worktreeを自動削除しません。

## 6. Task-bound Coder Turn

Task CoderのTurnはPlanningTurnと別のappend-only factsとして管理します。権限正本はTaskに束縛されたExecutionPermissionとし、DRAFT PlannerのPlanningAuthorizationを流用しません。

- TaskTurnIntentはJob / Task / Coder RoleExecution / ExecutionPermission / Provider Start / accepted Session / Provider identity / prompt digest / turn number / token reservationへ束縛します。
- intentとCommand receiptは同一JobAggregate transactionで保存し、Provider Sendより先にcommitします。
- 作成時に現在のJobApproval、ExecutionPermission、scope、deadline、accepted Start Session、累積turn / token budgetを再検証します。
- 同一RoleExecution内のSession identityを固定します。先行Turnが未観測またはUNKNOWNの場合、同じTurnの照合が完了するまで次Turnを拒否します。
- TaskTurnObservationはUNKNOWN / COMPLETED / FAILEDをappend-onlyで記録します。UNKNOWNは同一Turnのterminal evidenceだけで解消します。
- Terminal observationはfinal ResultとComplete Usageの保存後にのみ追加します。Provider Sendの受理だけでTurn完了としません。

## 7. Non-goals

- Task status / progress state machine
- Worktree ownership handoff / quarantine / release
- Reviewer用immutable commit / tree snapshotとread-only enforcement
- Worktree orphan reconciliation / cleanup
- Task editing / supersession
- Schedulerによる独立Task並列化
