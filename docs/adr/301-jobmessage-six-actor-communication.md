# ADR-301: Taxion内部の6 Actor通信をJobMessageへ統一する

Date: 2026-11-20

## Status

Accepted

## Context

Taxion内部の協調は同じJobの文脈で追跡する必要があります。Role名だけの通信や本文にrouting情報を埋め込む方式では、送信主体、宛先、実行provenanceが曖昧になります。

## Decision

**HUMAN / CONTROLLER / ORCHESTRATOR / PLANNER / CODER / REVIEWER間の通信は、JobAggregate内のappend-only JobMessageへ統一します。**

### Actor identity and Envelope

Actor種別とActor実体を分離します。HUMANは認証済みPrincipal、CONTROLLERとORCHESTRATORはTaxion管理のcomponent identity、PLANNER / CODER / REVIEWERはRoleExecutionIDに結び付けます。SenderとRecipientは認証済みcontextとControllerが確定します。

JobMessageは機械処理用Envelopeと自然言語Bodyで構成します。EnvelopeはMessageID、JobID、Sender、Recipient、MessageKind、Thread / Reply相関、時刻、provenance、必要なTask / RoleExecution scopeを保持します。利用者とAgentは同じ自然言語Bodyを使用します。

Job、Thread、Recipient等を現在のcontextから一意に解決できない場合は利用者へ確認します。LLMによる分類は候補提示に使用できますが、identity、宛先、認可、承認、Handoffを確定しません。

### Message kinds

- MESSAGE: 情報共有
- QUESTION: 回答を求める照会
- ANSWER: 特定Questionへの回答
- REQUEST: 情報提供または作業の依頼
- NOTIFICATION: 応答を要求しない通知
- HANDOFF: 作業・実行責任の移転

Question / Answerは情報交換であり、Taskや実行責任を移転しません。REQUESTも担当責任を移転しません。HANDOFFはJobAggregate Commandとして記録し、Provider停止、result / usage照合、成果identity固定、Worktree releaseを確認してから受信側実行を許可します。

### Delivery and correlation

基本単位は1メッセージ・1宛先です。fan-outはBatchIDで相関した宛先別メッセージとして記録します。保存と配信を別の事実として扱い、宛先ごとの配信・応答観測をappend-onlyで保持します。

REQUESTED、ACCEPTED、FAILED、UNKNOWNを区別します。MessageID / BatchIDで重複を排除し、配信結果UNKNOWNは不明のまま保持します。Exactly-once deliveryは保証しません。

### Authority boundary

JobMessage、Question、Answer、Request、Notification、HandoffはJobApproval、PlanningAuthorization、JobQuestionAuthorization、Task scope、Effect approval、Provider authorityを作成・拡張しません。必要なJobQuestionAuthorizationはADR-500に従い別途発行します。

## Consequences

6 Actorのidentity、自然言語Body、配信、応答、転送、HandoffをJob単位で追跡できます。内部通信の正本はJobAggregateに集約されます。

## Relationship to other ADRs

ADR-200はDomain Modelの基本方針、ADR-300はCollaborationの基本方針、ADR-201はJobMessageの保存境界、ADR-202はidentity・scope・idempotencyの不変条件、ADR-408はWorktree handoff、ADR-500は権限、ADR-701は外部AgentとのA2A境界を定義します。
