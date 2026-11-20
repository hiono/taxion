# ADR-800: 検証証拠の水準とJob完遂の受入れを分離する

Date: 2026-11-20

## Status

Accepted

## Context

Unit test、synthetic harness、SDK互換性、実Provider実行、再起動復旧、実Job完遂は証明する対象が異なります。低い証拠水準の成功だけで製品能力を証明することはできません。

## Decision

検証結果は証明した境界を明示し、製品受入れは実際のJob完遂能力で判断します。

### 証拠水準

1. **E1 — Static / unit:** 局所ロジックと不変条件
2. **E2 — Synthetic integration:** fake Provider等による契約と異常系
3. **E3 — SDK compatibility:** 実SDKの型・API・protocol互換性
4. **E4 — Live Provider:** 実Provider接続、turn、usage、stop semantics
5. **E5 — Durable recovery:** 再起動・crash後のJob、Provider、成果、Usage、Worktreeの照合
6. **E6 — Product E2E:** 人間の曖昧な依頼から複数Role協調、レビュー、修正、証拠確認、Job完了まで

上位の証拠水準は下位の成功だけから推定しません。結果は対象HEAD、環境、Provider / SDK version、実行条件、観測値に対応付けます。

### Job完了

Job完了には、受入条件を満たす成果、provenance-valid Evidence、確定したUsageとProvider operation、未解決operation・未完了RoleExecutionの不存在、Provider quiescence、Worktree release、安全・認可・データ整合性上の未解決問題がないことを要求します。これらはJobAggregateの明示的な完了Commandで確認します。

### 品質判断

製品上の判断は成果、正確性・安全性、費用の順に評価します。必要な質問、検証、独立レビューを通信量や処理時間の削減だけを理由に省略しません。

## Consequences

実装済み、synthetic確認済み、SDK互換性確認済み、実Provider確認済み、復旧確認済み、実Job完遂確認済みを区別します。CI成功やAPIの存在だけで製品完成を宣言しません。

## Relationship to other ADRs

ADR-201は成果・証拠・Job完了の正本、ADR-202は不変条件、ADR-300とADR-700は通信境界、ADR-408はWorktree所有権、ADR-500は承認と実行権限を定義します。
