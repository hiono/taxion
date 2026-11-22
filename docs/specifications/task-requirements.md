# Task Requirements

Version: 1.0
Date: 2026-11-22
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

TaskのscopeはJobContractまたはParentTaskのeffective scopeを継承します。requestでtarget repository / resource / workspaceを省略（nil）した場合は親scopeを継承します。明示した値は親scopeの完全一致subsetでなければなりません。明示的な空配列はそのresource dimensionへのアクセスを許可しない指定として扱います。prefix / substring / host-only matchは認めません。

TaskのBudget、ConcurrencyCeiling、Deadlineはrequestで省略した次元を親から継承します。明示値は親の上限以下でなければなりません。BudgetはMaxTurns / MaxTokensともに正数とします。Deadlineは現在時刻より後で、JobContract / ParentTaskのdeadline以下とします。

TaskのProhibitedEffectsとApprovalRequiredEffectsはJobContract / ParentTaskの値を継承し、追加だけを許可します。既存の禁止や追加承認要件を削除できません。

AcceptanceCriteriaとRequiredEvidenceはTask固有の追加条件です。Task creationはJobContractの受入条件を変更・削除しません。

## 5. Immutability and validation

- Taskはappend-only factです。既存Taskのscope、dependency、budget、deadlineをin-place変更しません。
- JobAggregate validationは復元時もTaskID / CommandIDの一意性、approval provenance、parent / dependency参照、scope subset、budget / deadline上限、dependency DAGを検証します。
- Task作成の拒否はJob revision、Tasks、receiptを変更しません。
- Task creation後もJobApprovalとJobContractRevisionがauthorityの正本です。TaskはJob scopeを拡張できません。
- Task state / progressはこのTask definition unitでは定義しません。RoleExecution、Provider observations、Output、Evidence等の事実と混同しません。

## 6. References

- ADR-201: JobAggregate / Task / RoleExecution boundary
- ADR-202: invariants across create / persist / restore
- ADR-408: Worktree ownership and Task concurrency
- ADR-500: JobApproval and prohibited effects
- Product Contract §4.4
- Product Acceptance AT-009
