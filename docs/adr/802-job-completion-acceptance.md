# ADR-802: Job完了を証拠に基づく明示的なAggregate判断とする

Date: 2026-11-23

## Status

Accepted

## Context

Roleの終了、Providerの成功応答、ReviewerのPASSだけでは、成果の受入れ、外部実行の停止、資源の解放まで完了したことを証明できません。

## Decision

**Job完了は、承認済み受入条件と検証済みEvidenceを確認したうえで、JobAggregateが明示的に確定する判断とします。**

未解決のProvider operation、未完了のRoleExecution、停止未確認のProvider、未解放のWorktree、認証・認可・データ整合性上の未解決問題がある場合は完了を認めません。

## Consequences

個別RoleやProviderの状態をJob完了と誤認せず、成果・実行・証拠・資源の整合性を一つのJob境界で判定します。具体的な受入条件と検証シナリオはProduct ContractおよびProduct Acceptanceで定義します。

## Relationship to other ADRs

ADR-800はQuality & Evidenceの基本方針、ADR-801は証拠水準、ADR-201はJobAggregate、ADR-409はProvider operationの照合、ADR-408はWorktree所有権を定義します。
