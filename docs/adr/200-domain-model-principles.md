# ADR-200: Domain Modelの基本設計方針

Date: 2026-11-21

## Status

Accepted

## Context

Taxionの作業、協調、実行、成果を複数の独立した可変モデルへ分散すると、Jobとの関係や状態変更の責務が不明確になります。Domain Modelには、利用者の依頼からJob完了までを一貫して表現する共通境界が必要です。

## Decision

**Domain ModelはJobを唯一の作業単位とし、Jobに属する状態と事実をJobAggregateへ集約します。**

- TaskとRoleExecutionはJobに属する作業・実行単位として扱います。
- Jobの状態変更は明示的なCommandを通じて行い、ドメイン不変条件をすべての状態変更経路で強制します。
- 協調、Provider operation、Worktree、成果、証拠などの事実は、Jobとの関係を失わない形で保持します。
- AggregateとCommandの具体的な境界はADR-201、不変条件の強制方針はADR-202で定義します。

## Consequences

Domain Modelの責務とJob境界が統一され、個別の状態・事実を追加するときもJobとの整合性を維持できます。

## Relationship to other ADRs

ADR-100は製品目的、ADR-201はJobAggregateとCommand境界、ADR-202はドメイン不変条件を定義します。
