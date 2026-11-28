# ADR-201: JobAggregateを作業・協調・成果の唯一のドメイン正本とする

Date: 2026-11-20

## Status

Accepted

## Context

Job、Task、RoleExecution、通信、Provider実行、Worktree、成果・証拠を別々の可変モデルへ分散すると、関係の意味と状態変更の責務が曖昧になります。Taxionには、利用者の意図からJob完了までを一貫して保持するドメイン正本が必要です。

## Decision

**JobAggregateを、Jobの意図、承認、協調、実行事実、成果および証拠の唯一のドメイン正本とします。**

### Job model

- Jobは利用者の依頼、目的、制約、承認、協調履歴、実行状態、成果、証拠を保持する永続的な作業単位です。最初のPlanner呼出し前にDRAFTとして作成し、原文依頼を保存します。
- TaskはJob内の任意の作業分解であり、必ずJobIDを持ちます。
- RoleExecutionはRoleによる個々の実行であり、JobIDを持ちます。Taskless PlannerはTaskIDを持ちません。
- Taskの親子包含、Task間の前提条件、Role間の協調は別の関係として扱います。Role間協調はappend-only JobMessageで記録します。
- IntakeProvenance、JobContractRevision、JobApproval、PlanningAuthorization、JobProgressNote、JobMessage、Provider operation / observation、Worktree ownership、JobOutput、JobEvidence、JobUsage、Effect decisionはJobAggregateに属します。
- IntakeProvenanceは認証済みPrincipal、server-derived ingress source、受信時刻、request identity、原文依頼digestを保持するappend-only factです。認証境界はADR-501に従います。
- GoalはJobContractRevisionに含め、承認前に確定します。Goal未設定のDRAFT Jobは実装Taskを開始しません。

### Command boundary

- JobAggregateの変更はexpected revisionと安定したidempotency identityを持つ明示的Commandで実行します。
- 一つのJobに属するCommandと関連factsは一つのtransactionで確定します。同じCommand ID・同じ内容の再送は同じreceiptへ収束させ、異なる内容の再利用は拒否します。
- SQLiteの保存形式は現在のJobAggregate契約に合わせます。未対応のschema versionは変更前に拒否します。旧processとの並行稼働、旧schemaとの互換性維持、旧データ移行のためのtrigger・fallbackは設けません。
- Job、Task、RoleExecution、Message、Provider operation、Worktree ownership、Output、Evidence、Usage、Effect decisionに独立したproduction write pathを設けません。
- Coreはドメイン整合性を検証し、Controllerは認証・承認・Provider等の外部事実を検証します。

### Authorization and collaboration

- JobApprovalとPlanningAuthorizationは異なるimmutable factsとして保持します。発行条件と権限はADR-504 / ADR-505に従います。
- Question / Answerは情報交換であり、Taskや実行責任を移転しません。Handoffは受信RoleとTask scopeを明示するJobAggregate Commandとします。
- Handoffは送信側Providerの停止、terminal result / usage、成果identity、Worktree ownership releaseを確認してから受信側実行を許可します。

### Provider operations

- Provider operationのintentと観測事実はJobAggregateに属し、RoleExecutionの状態とは区別します。
- 外部呼出し前にintentを確定し、Provider result、usage、stop等の観測を対応するoperation identityへ束縛します。
- 不明な結果を成功・失敗と推定せず、照合されるまで実行資源の所有権を維持します。Provider operationの設計判断はADR-409に従います。

### Outputs and completion

成果と証拠はimmutable identityおよびJob / Task / producing RoleExecution / verifierのprovenanceへ束縛します。Job完了は、必須条件の確認結果を伴う明示的Commandとして成立させます。受入条件はADR-802に従います。

## Consequences

Planner、Coder、Reviewer、修正、人間の介入、Job完了を同じJobで追跡できます。物理schema、table名、codec、Command型名は実装要件に応じて選択します。

## Relationship to other ADRs

ADR-200はDomain Modelの基本方針、ADR-202は不変条件、ADR-300はCollaborationの基本方針、ADR-301は内部通信、ADR-400はExecution & Runtimeの基本方針、ADR-408はWorktree、ADR-409はProvider operation、ADR-501はintake identity、ADR-504 / ADR-505 / ADR-507は権限、ADR-801は証拠水準、ADR-802はJob完了を定義します。
