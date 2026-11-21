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
- 各Provider AdapterはProvider / SDK versionとconfiguration identityに対応するcapabilityを明示し、対応する検証証拠を保持します。少なくとも次を個別に宣言します。
  1. StartのOperationID単位のidempotencyまたはlookup
  2. process restart後の、同一Provider Sessionへの再接続と必要なSession identity
  3. Sendのturn identity単位のidempotencyまたはlookup
  4. disconnect / restart後のevent replayとcursor continuity
  5. Stop observationと、Provider processが書込みを継続しないことを示す証拠
  6. event stream欠落時のfinal output retrieval
  7. Usage identity、粒度、完全性、重複排除の意味
- 未対応または未検証のcapabilityは利用可能と推定しません。capabilityの宣言はProviderの実行能力を示すものであり、Job authorityやEffect permissionを付与しません。
- 同じRoleExecutionをprocess restart後に継続する場合、Controllerは保存済みの正確なSession identity、Provider / account identity、configuration digestを照合し、Adapterがその組合せでcross-process reattachmentを明示的にサポートするときだけ再接続します。
- reattachmentが未対応・未検証の場合、同じRoleExecutionに新しいSessionを作成してResumeしたことにしません。元のoperationとWorktree ownershipを未解決として保持し、Stop proof、terminal result、Usageを照合します。新しいRoleExecutionによる継続は、旧Providerのquiescence、result / usage reconciliation、Worktree release後に限り、Controllerが現在のJobApproval、Task scope、budget、deadlineを再検証して別の実行として発行します。
- Startの結果が不明な場合は同じOperationIDでlookupするか、当該OperationIDに対するidempotencyが実証された場合だけ再試行します。新しいOperationIDでStartを重複発行しません。
- Worktree予約を伴うStartは、予約とStart intentを同じJob transactionで確定してからProviderを呼び出します。transactionのcommit結果がUNKNOWNの場合は、OperationIDとWorktree reservationをread-backしてcommitを確認するまでProviderを呼び出しません。
- Sendは送信前にimmutable turn identityとprompt hashを保存します。Providerの受理応答はturn完了を意味しません。terminal eventまたは同一turn identityによるlookupで結果を照合し、結果不明のSendを自動再送しません。
- Stopは先にintentを保存し、Adapter固有のstop evidenceをappend-onlyで記録します。cancel notificationやRoleExecutionのterminal statusだけではProvider停止を証明しません。停止証拠が不明なWorktreeは保持またはquarantineします。
- final output、terminal turn、Usageをそれぞれのidentityへ束縛して照合します。event replay / final output retrievalが未対応の場合、欠落した結果を成功と推定しません。Provider停止証拠、最終結果、Usageが照合されるまでWorktreeを保持します。

### Outputs and completion

成果と証拠はimmutable identityおよびJob / Task / producing RoleExecution / verifierのprovenanceへ束縛します。Job完了は、必須条件の確認結果を伴う明示的Commandとして成立させます。受入条件はADR-800に従います。

## Consequences

Planner、Coder、Reviewer、修正、人間の介入、Job完了を同じJobで追跡できます。物理schema、table名、codec、Command型名は実装要件に応じて選択します。

## Relationship to other ADRs

ADR-202は不変条件、ADR-300は内部通信、ADR-408はWorktree、ADR-500は権限、ADR-501はintake identity、ADR-800は証拠と完了条件を定義します。
