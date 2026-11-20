# ADR-500: Job承認境界と禁止副作用を中心に実行権限を強制する

Date: 2026-11-20

## Status

Accepted

## Context

通常操作の静的allowlistは承認済みJob内の作業まで不必要に停止させます。一方、Agentの判断だけに権限を委ねると、対象境界、人間承認、禁止副作用を維持できません。

## Decision

**承認済みJobの範囲内でAgentが通常操作の手段を選択し、Controllerと実行基盤がJob境界および禁止副作用を強制します。**

### JobApproval

JobApprovalはimmutable JobContract revisionのcanonical hashへ束縛し、approver identity / source / reason / timeを記録します。契約には目的、制約、受入条件、対象repository / resource、作業領域、budget、concurrency ceiling、deadline、禁止事項、人間承認が必要な副作用を含めます。

TaskはJobの境界を狭められますが拡張できません。契約の対象、権限、予算、期限を実質的に変更する場合は新revisionと再承認を要求します。

### Normal operations and prohibited effects

承認済みJobの対象・資源・作業領域内にある可逆的な通常操作は、個別allowlistへの登録なしで実行できます。Role-to-operation ACLやTool allowlistをJob authorityの正本にしません。

ControllerはJob境界外アクセス、他Jobへの不正アクセス、秘密情報の不正取得・送信、権限昇格、sandbox bypass、未承認の外部変更・publish、provenance改変、二重所有、実行状態不明の再送などの禁止副作用を強制します。不可逆操作や承認範囲を変える操作は人間承認へ送ります。

未知のOperationは名称だけで拒否せず、副作用、封じ込め可能性、承認境界で判断します。影響を判定できず実行基盤でも封じ込められない場合は承認要求または拒否とします。

### PlanningAuthorization

JobApproval前のDRAFT Jobでは、明示的に開始されたrequest intakeに対してTaskless PLANNERのbounded read-only executionを許可できます。PlanningAuthorizationはJobID、原文依頼hash、認証済みHUMAN Principal、Provider / account / configuration、repository、read-only boundary、budget、deadlineへ束縛したimmutable grantです。

PlanningAuthorizationはJobをACTIVEにせず、実装Task、Coder / Reviewer、repository write、validation、commit、publication、外部mutationを許可しません。Providerがread-onlyを強制できない場合はimmutable snapshotまたは明示的context documentsを使用し、それも強制できなければrepository contextを渡しません。

Provider credentialと通信はController / Provider Adapterが仲介し、Model / Sandboxへ露出させません。grantはProvider start、turn、effect、resume前に再検証します。

### JobQuestionAuthorization

承認済みJob内でRoleからTaskless PLANNERへread-only context acquisitionを伴う質問を行う場合、JobApproval、Planner RoleExecution、元Question、Provider / account / configuration、context、budget、deadlineに束縛したJobQuestionAuthorizationを別途発行できます。Question本文やMessageKindだけでは発行せず、他のgrantやscopeを拡張しません。

### Observation, Conformance, Admission, Execution Permission

- Observationは対象の状態とprovenanceを記録します。
- Conformanceは観測事実を契約・受入条件に照らして評価します。
- Admissionは対象と証拠が次の仕事の入力として利用可能かを判断します。
- Execution Permissionは現在のJobApproval、scope、budget、deadline、Provider、effectを検証して実行可否を判断します。

各段階の判定を相互に代替しません。Admissionのacceptは実行許可を意味しません。

### Collaboration

情報不足は質問、仮定の明示、部分作業によって解消します。identity、認可、provenance、承認、禁止副作用、データ整合性、実行状態が不明な場合はfail-closedとします。Reviewerは固定された成果物を独立したContext / sessionとread-only boundaryで評価します。

## Consequences

Agentの作業上の自由と人間の承認境界を両立します。通常操作の網羅的な許可集合ではなく、Job scope、禁止副作用、実行基盤の強制境界を中心に認可します。

## Relationship to other ADRs

ADR-201はJob内のauthority facts、ADR-202は全状態経路の不変条件、ADR-408はWorktree境界、ADR-501はHUMAN Principal、ADR-800は受入れ証拠を定義します。
