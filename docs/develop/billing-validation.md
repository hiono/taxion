# 保存契約を起点にする課金判断の確認記録

Issue #234 の判断と操作経路の対応を記録する。
[ADR-0063](adr/0063-apply-runtime-specific-billing-contracts.md) の契約を維持し、
保存方式の表示は [ADR-0069](adr/0069-show-saved-task-billing-policy.md) と
[課金ガイド](../userguide/billing.md)に従う。

## 契約・操作・回帰確認

| 条件 | 利用者の操作と期待する結果 | 確認 |
|---|---|---|
| 保存した Copilot の数値必須契約 | `config check --billing --dispatch ID` で保存方式を確認し、承認済みなら `runtime quota --dispatch ID` で非推論観測を使う。新規提案の既定値へ戻さない | `TestBillingReportUsesSavedTaskAndDispatchPolicyWithoutMutation`、`TestObserveAccountQuotaWithoutSessionOrWorkspaceMutation` |
| 観測可能・取得不能・鮮度切れ・枯渇 | 成功とそれぞれの拒否を区別し、取得元・対象・単位・時刻・残量を保持する。利用量不明をゼロにしない | 同 quota 観測試験、`TestRuntimeQuotaDoesNotInitializeWorkspace` |
| 確認済み OpenCode Free | 保存契約が `provider` なら Go 残量を要求しない。参照名だけで無料資格を保証せず、4条件の申告と別途指定した上限を維持する | 保存表示の Free 構成と `TestProviderBudgetNeverReadsQuotaForDispatchOrIsolatedTurns` |
| OpenCode Go の Task 別選択 | `account` と `provider` を別の保存ポリシーとして表示する。前者の未対応観測を成功にせず、後者へ無断で切り替えない | 保存表示の Go 2構成、`TestExplicitDeadlineRequiredAndExpiredTurnIsReaped`。OpenCode adapter の account 拒否は `New` の実装を静的照合したもので、実接続確認ではない |
| 未知の有料 fallback・契約不一致 | 保存承認との不一致を非ゼロ終了し、成功表示・契約更新・課金先変更をしない | 保存表示の不一致試験、`TestSettingsResolveAndProviderStopConditions`、提供元停止の既存試験 |
| 方式のない旧契約 | 欠落を返し、新規 `provider` の既定値を補わない | 保存表示の legacy 構成 |
| 利用額上限・期限・実行数・品質 | `provider` でも正の上限と明示制約を維持し、事後利用量だけで事前超過を防げるとは説明しない | `TestProviderOnlyFlowRetainsLimitsAndUnknownUsage`、Issue #221 の品質確認表 |
| 外側の操作担当の利用枠 | 許可済みなら既存 `runtime quota --account ACCOUNT --approve-account-read` を使い、worker の方式と分離する | ADR-0063 の既存非推論接続記録、今回の作業開始前にも同じ観測経路を利用 |

保存表示の CLI 試験はローカルの設定・保存グラフを使い、OpenCode の実行ファイルや
設定ファイルを読むことなく成功する。DB・WAL・SHM を含む workspace の bytes を比較し、
元ファイルの不変を確認する。Task の `request` 由来の方式と現在の新規提案用方式を分け、
保存方式の欠落や不一致を合格にしない。

## 実施した範囲と未達

保存表示、既存設定照合、quota CLI、管理スキル、文書の対象試験と、
`go vet ./...`、文書規約検査を実施した。
保存表示の承認不一致・元ファイル不変の回帰試験は `go test -race` でも実施したが、
この試験は逐次実行であり、同時操作時の競合を検証したものではない。
最終候補では全体の Go 試験とビルド、必須 CI、独立レビューも同じ変更へ対応付ける。
追加モデル、提供元の課金設定変更、有料 fallback、無料枠の実際の枯渇試験は行わない。

Copilot の非推論 quota 観測と、実モデルを含む成果作成の受入れは別である。
過去の Copilot／OpenCode 実証と現在の未達は
[バックエンド別の確認記録](backend-acceptance.md)を参照する。
今回の Free／Go 構成は合成試験であり、実契約での提供元停止や
現在の実モデルを含む成果・同一対象の独立レビューを実証したものではない。
必要な実受入れと未知の経路は Issue #234／#231 の未達として保持し、
追加実行は構成・費用・期限の別承認を得る。API や接続の存在だけで完了として閉じない。
