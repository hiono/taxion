# ADR-700: Interface & Integrationの基本設計方針

Date: 2026-11-21

## Status

Accepted

## Context

TaxionはCLI、Web UI、Provider、外部Agent等と連携します。外部のprotocolやidentityを内部のJob・Actor・権限モデルへ直接持ち込むと、信頼境界とドメイン責務が曖昧になります。

## Decision

**外部との連携は明示的なAdapter境界を通し、内部のJob契約と権限モデルを正本として扱います。**

- 外部protocolのmessage、task、identity、artifactは、Taxion内部のJob・Actor・承認・認可とは別の情報として扱います。
- 外部入力と結果は、Controllerが認証、provenance、scope、不変条件を検証してからJobへ反映します。
- 外部Agentとの必須相互運用protocolはA2Aとし、その適用範囲と内部JobMessageとの境界をADR-701で定義します。

## Consequences

外部protocolの変更や相手側のidentity体系が、内部のJob状態・認可・承認の意味を直接変更しません。

## Relationship to other ADRs

ADR-201はJob内の正本、ADR-301は内部Actor通信、ADR-501はHUMAN Principalの信頼源、ADR-701は外部AgentとのA2A連携を定義します。
