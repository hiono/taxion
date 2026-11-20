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
- IntakeProvenance、JobContractRevision、JobApproval、PlanningAuthorization、JobQuestionAuthorization、JobProgressNote、JobMessage、Provider operation / observation、Worktree ownership、JobOutput、JobEvidence、JobUsage、Effect decisionはJobAggregateに属します。
- IntakeProvenanceは認証済みPrincipal、server-derived ingress source、受信時刻、request identity、原文依頼digestを保持するappend-only factです。認証境界はADR-501に従います。
- GoalはJobContractRevisionに含め、承認前に確定します。Goal未設定のDRAFT Jobは実装Taskを開始しません。

### Command boundary

- JobAggregateの変更はexpected revisionと安定したidempotency identityを持つ明示的Commandで実行します。
- 一つのJobに属するCommandと関連factsは一つのtransactionで確定します。同じCommand ID・同じ内容の再送は同じreceiptへ収束させ、異なる内容の再利用は拒否します。
- Job、Task、RoleExecution、Message、Provider operation、Worktree ownership、Output、Evidence、Usage、Effect decisionに独立したproduction write pathを設けません。
- Coreはドメイン整合性を検証し、Controllerは認証・承認・Provider等の外部事実を検証します。

### Authorization and collaboration

- JobApproval、PlanningAuthorization、JobQuestionAuthorizationは異なるimmutable factsとして保持します。発行条件と権限はADR-500に従います。
- Question / Answerは情報交換であり、Taskや実行責任を移転しません。Handoffは受信RoleとTask scopeを明示するJobAggregate Commandとします。
- Handoffは送信側Providerの停止、terminal result / usage、成果identity、Worktree ownership releaseを確認してから受信側実行を許可します。

### Provider operations

- 外部Provider呼出し前にTaxion生成のimmutable OperationIDとintentをJobAggregateへ保存します。
- Provider observationsはOperationIDに束縛したappend-only factsとします。RoleExecutionの状態とProviderのsession、turn、stop、result、usageは別の事実として扱います。
- operation結果がUNKNOWNの場合はそのまま保持し、Provider側の冪等性または照合可能性が確認できるまで再送しません。
- Worktree予約を伴うStartは、予約とStart intentを同じJob transactionで確定してからProviderを呼び出します。
- Provider停止証拠、最終結果、Usageが照合されるまでWorktreeを保持します。

### Outputs and completion

成果と証拠はimmutable identityおよびJob / Task / producing RoleExecution / verifierのprovenanceへ束縛します。Job完了は、必須条件の確認結果を伴う明示的Commandとして成立させます。受入条件はADR-800に従います。

## Consequences

Planner、Coder、Reviewer、修正、人間の介入、Job完了を同じJobで追跡できます。物理schema、table名、codec、Command型名は実装要件に応じて選択します。

## Relationship to other ADRs

ADR-202は不変条件、ADR-300は内部通信、ADR-408はWorktree、ADR-500は権限、ADR-501はintake identity、ADR-800は証拠と完了条件を定義します。
