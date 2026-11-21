# ADR-300: Collaborationの基本設計方針

Date: 2026-11-21

## Status

Accepted

## Context

複数のActor / Roleが同じJobを進めるには、情報交換と作業責任の移転を区別し、協調の経過をJobの状態と結び付ける必要があります。

## Decision

**CollaborationはJobを中心に構成し、情報交換と実行責任の移転を明示的に区別します。**

- Actor identityとRoleExecutionを区別し、各Roleの実行をJobに結び付けます。
- メッセージ、質問、回答、依頼、通知はJobに属する協調事実として記録します。
- 情報交換だけではTaskや実行責任を移転しません。実行責任の移転は、対象と受信Roleを明示するHandoffとして確定します。
- 内部通信のEnvelope、MessageKind、配信・応答観測はADR-301で定義します。Job内の保存境界はADR-201、Worktreeを伴う実行引継ぎはADR-408で定義します。

## Consequences

Job単位で協調の経過と実行責任を追跡でき、情報交換を権限や担当変更と誤認しません。

## Relationship to other ADRs

ADR-201は協調事実の保存境界、ADR-301は内部通信契約、ADR-408は実行責任とWorktreeの引継ぎを定義します。
