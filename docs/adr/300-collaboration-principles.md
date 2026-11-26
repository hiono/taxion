# ADR-300: Collaborationの基本設計方針

Date: 2026-11-21

## Status

Accepted

## Context

複数のActor / Roleが同じJobを進めるには、情報交換と作業責任の移転を区別し、協調の経過をJobの状態と結び付ける必要があります。Agent間の情報交換は、手動の自然言語コピー&ペーストで成立する作業をTaxion上で再現することを基準とします。

## Decision

**CollaborationはJobを中心に構成し、自然言語による情報交換と実行責任の移転を明示的に区別します。**

- Agent間の通常のメッセージは自然言語本文をそのまま渡します。情報交換・保存・参照に通信専用grantやMessageKind別認可を要求しません。
- Jobと送受信Actorを特定するための最小限の識別情報はControllerが確定します。本文の自己申告をidentityや宛先の根拠にしません。
- 情報交換だけではTaskや実行責任を移転しません。実行責任の移転は、対象と受信Roleを明示する独立したHandoff操作として確定します。
- Providerの新規実行や外部副作用は情報交換とは別の操作です。必要な実行認可はADR-506 / ADR-507等の適用範囲で検証します。
- 内部通信の保存境界はADR-201、最小メッセージ契約はADR-301、Worktreeを伴う実行引継ぎはADR-408で定義します。

## Consequences

Agent間の会話を過剰な通信プロトコルや認可で複雑化せず、Job単位で情報交換と実行責任を追跡できます。Provider実行や外部副作用の安全境界は独立して維持します。

## Relationship to other ADRs

ADR-201は協調事実の保存境界、ADR-301は内部メッセージ契約、ADR-408は実行責任とWorktreeの引継ぎ、ADR-506 / ADR-507はProvider実行権限を定義します。
