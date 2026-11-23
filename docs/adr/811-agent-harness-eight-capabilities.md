# ADR-811: Agent Harnessの能力要求を製品契約として維持する

Date: 2026-11-20

## Status

Accepted

## Context

Taxionは特定のAgent Harnessを再実装することを目的としませんが、隔離、委譲、Context管理、継続、Guardrails、検証、観測に必要な製品能力を失ってはなりません。

## Decision

**Agent Harnessに必要な能力群を製品要求として維持し、Taxion内部または外部Runtimeへの委譲を問わず、実証可能な形で充足させます。**

能力の実装場所は固定しません。外部SDK、CLI、OS、Providerまたは実行環境へ委譲する場合も、対象構成で能力が成立する証拠を要求します。

## Consequences

ハーネスの再実装そのものを目的化せず、製品として必要な能力の欠落を防止します。能力一覧、要求の詳細、受入シナリオはProduct ContractおよびProduct Acceptanceで管理します。

## Relationship to other ADRs

ADR-100は製品目的、ADR-201はJobと再開情報、ADR-300はCollaborationの基本方針、ADR-408はWorktree境界、ADR-500は権限・Guardrails、ADR-800は品質と証拠の基本方針を定義します。
