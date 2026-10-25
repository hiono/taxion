# 51. 申告した課金条件と予算方式の判断を集約する

Date: 2026-10-26

## Status

Accepted

## Context

提供元で追加利用が停止する契約でも、残量照会の一時障害が作業を停止させていた。
追加課金の防止と月内配分を同じ必須監視へまとめると、利用者ごとの契約の違いを扱えない。
課金条件を実行経路ごとの真偽値で増やすと、開始・監視・再開で判断が分かれる。

## Decision

`internal/billing` を課金条件の共通 API とする。契約形態、超過課金、自動チャージ、
有料切替えの値、既定の解決、提供元の停止に任せられる条件、承認済み条件との照合、
予算方式と利用額上限の整合性をここで扱う。提供元固有の残量 API 呼出しは既存 adapter に残す。
外部の課金設定を変更する API や汎用ルールエンジンは作らない。

`billing` の4条件は補足指示方式と独立して保存できる。
`provider` は自動残量照会なしで提供元の停止に任せる方式であり、
`account` は既存の厳格な監視を明示的に選ぶ方式とする。
既定は人間が承認する上限停止・追加課金なしの提案とし、提供元による検証とは表示しない。
異なる設定や未確認を申告した場合、既定の省略で提供元の停止に任せる方式を選ばない。

解決済み条件を承認済み Task・Dispatch に複写し、設定を変更しても再解釈しない。
保存済みの `accountQuotaOnly` と省略フィールドの JSON は維持し、
新方式だけ `budgetSource: provider-stop` を保存する。
予算制御は共通の判定を使い、期限・回数・権限を課金条件から分離して維持する。

利用者向けの仕様と操作は[課金ガイド](../../userguide/billing.md)を正本にする。
設定と CLI 文書は項目索引と参照にとどめ、契約条件の説明を重複させない。
ADR-0029・ADR-0049 は当時の判断として残し、新規既定値はこの決定で置き換える。

## Consequences

提供元の停止に任せる契約では残量 API の障害を実行条件から外せる。
一方、この方式は月内の残量を温存せず、利用枠を使い切ると外側の操作も止まり得る。
設定と申告の正確さは利用者が確認し、Taxion は請求額の保証をしない。
旧契約の再承認・停止回収と新形式を読めない版への復帰制約は残る。

## Validation

開発版で `go build ./...`、`go vet ./...`、`go test ./...`、
公開文書の repository 検査、`git diff --check` が成功した。
独立した差分レビューの判定は APPROVE。次の回帰検証を追加・拡張した。

| 検証 | 確認した動作 |
| --- | --- |
| `TestSettingsResolveAndProviderStopConditions` | 既定値と従量課金・未確認・超過課金・自動チャージ・有料切替えの条件 |
| `TestBudgetSourcesPreserveLegacyAndExplicitCaps` | 旧方式と新方式の区別、正の利用額上限、無効な数値の拒否 |
| `TestAllBillingFieldsFollowConfigurationPrecedence` | 4設定の環境変数による上書き、設定元、無効値の拒否 |
| `TestLegacyConfigJSONRemainsByteCompatible` | endpoint の有無を含む旧設定の JSON バイト列の維持 |
| `TestBillingSetupAndStandaloneConfigReport` | 設定保存、独立した JSON 表示、申告表示、workspace を開かない検査 |
| `TestProviderBudgetNeverReadsQuotaForDispatchOrIsolatedTurns` | 残量 RPC 不使用、課金条件変更時の承認再利用拒否 |
| `TestIsolatedNonAccountModesDoNotReadQuota` | 相談・検索の最終結果まで残量を照会しないこと |
| `TestProviderOnlyFlowRetainsLimitsAndUnknownUsage` | 利用量不明を保持し、正の利用額・期限・実行数の上限を維持 |
| `TestRequestProviderDefaultOmitsNativeCeilingAndResumes` | 残量照会能力のない合成 runtime で計画・実装・レビューを実行し、保存済み成果から再開 |

実行は合成 runtime を使った検証であり、実契約の追加課金防止、提供元による上限停止、
製品開発の実モデルによる完了を確認したものではない。正式リリースも別の手続きとする。

## References

- [Issue #182](https://github.com/hiono/taxion/issues/182)
- [ADR-0029](0029-separate-budget-guidance-from-provider-quota.md)
- [ADR-0049](0049-monitor-approved-account-quota-during-execution.md)
