# ADR-301: Taxion内部の6 Actor間メッセージをJobMessageへ統一する

Date: 2026-11-20

## Status

Accepted

## Context

Taxion内部の協調は同じJobの文脈で追跡する必要があります。Agent間の質問・回答・指摘・補足は、手動の自然言語コピー&ペーストで成立していました。Taxionの役割はその受け渡しと元の作業Contextへの復帰を自動化することであり、会話専用の複雑な通信プロトコルを導入することではありません。

## Decision

**HUMAN / CONTROLLER / ORCHESTRATOR / PLANNER / CODER / REVIEWER間の情報交換は、Jobに属する自然言語本文のJobMessageとして扱います。**

### Message content and identity

- Bodyは自然言語のまま保存・受け渡し、固定JSONやWorkflow DSLへ変換しません。
- 永続化に必要な最小情報はMessageID、JobID、認証済みSender、Recipient、作成時刻、provenanceとします。
- SenderはHUMANの認証済みPrincipal、CONTROLLER / ORCHESTRATORの管理identity、またはPLANNER / CODER / REVIEWERのRoleExecution identityに結び付けます。本文の自己申告でSenderやRecipientを確定しません。
- Thread、Reply、Task、RoleExecution等の参照は、元Contextへの復帰や対象特定に実際に必要な場合だけ保持します。任意metadataやMessageKind分類を通信の必須条件にしません。
- Jobや宛先を信頼できるContextから一意に特定できない場合だけ確認します。

### Information exchange and execution

- 質問・回答・依頼・通知・レビュー指摘は通常の自然言語メッセージです。種類ごとの通信認可、方向別ACL、専用grantを設けません。
- メッセージの保存・参照・受け渡しはProvider実行や外部副作用の許可を意味しません。一方で、情報交換そのものにProvider実行grantを要求しません。
- 回答のために新たなProvider実行やbounded read-only context acquisitionが必要な場合だけ、ADR-506等の該当する認可を別途検証します。
- 実行責任やWorktree所有権の移転は通常メッセージに含めず、停止・結果・成果・所有権の条件を検証する独立したHandoff操作とします。

### Persistence and delivery

- JobAggregateをJobMessageの永続化境界とし、メッセージ本文と送受信Actorの関係を保持します。
- 通常の情報交換にRecipient別状態機械、BatchID、MessageKind別終端状態を必須化しません。
- ProviderへのSend / Turnやその結果照合が必要な場合は、JobMessageとは別のProvider operationとして扱い、ADR-409および該当する認可判断に従います。Provider operationのUNKNOWNや再試行をメッセージ配送状態へ重複実装しません。

## Consequences

Agent間の自然言語交換を最小の構造で実現し、元のJobと作業Contextへ戻れます。情報交換とProvider実行・外部副作用・実行責任の移転を混同しません。

## Relationship to other ADRs

ADR-200はDomain Modelの基本方針、ADR-201はJobAggregateの保存境界、ADR-202はidentity・scope・idempotencyの不変条件、ADR-300はCollaborationの基本方針、ADR-408はWorktree handoff、ADR-409はProvider operation lifecycle、ADR-506は質問を契機とする追加Provider実行、ADR-507はoperation単位の実行権限、ADR-701は外部AgentとのA2A境界を定義します。
