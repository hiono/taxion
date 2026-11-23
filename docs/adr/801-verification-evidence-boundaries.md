# ADR-801: 検証証拠の主張を実証済みの境界に限定する

Date: 2026-11-23

## Status

Accepted

## Context

局所テスト、合成統合、SDK互換性、実Provider実行、再起動復旧、製品E2Eは、それぞれ証明できる対象が異なります。低い検証段階の成功から、上位の能力を推定すると製品の実力を誤認します。

## Decision

**検証結果は、実際に観測・確認した証拠水準の範囲内でのみ主張します。**

E1からE6までの証拠水準を区別し、上位水準の成立を下位水準の成功だけから推定しません。検証結果は対象HEAD、環境、Provider構成および実行条件に結び付けます。

## Consequences

実装済み、合成検証済み、実Provider検証済み、耐久復旧検証済み、製品E2E検証済みを混同しません。各水準の定義と受入シナリオはProduct Acceptanceで管理します。

## Relationship to other ADRs

ADR-800はQuality & Evidenceの基本方針、ADR-201は成果・証拠のJob内保存、ADR-409はProvider operationの観測、ADR-802はJob完了の受入れ判断を定義します。
