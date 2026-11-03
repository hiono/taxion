# バックエンド別の確認記録と再開

Issue #231 の診断・合成確認と、実サービスの受入れを分けて記録する。
判断は [ADR-0068](adr/0068-preserve-safe-acp-failure-signals.md)、
接続・隔離・利用量の契約は [Runtime 契約](runtime-contract.md#opencode-acp-adapter)が正本である。

## 既存証拠と現在への適用範囲

| 経路 | 保存済みの根拠 | 確認範囲と残る条件 |
|---|---|---|
| OpenCode 初期対応 | `d368d71`、ADR-0038 | 匿名の `opencode/big-pickle` による最小応答を記録済み。「実証なし」ではない。現在の構成の全経路を保証しない |
| OpenCode 文書作業 | `07fabd1`、`cmd/taxion/opencode_live_test.go` | 正式成功・独立承認された文書を採用した VCS 記録。現行 adapter の新しい実受入れではない |
| OpenCode 接続互換性 | ADR-0038 の2026-10-17追補 | 1.19.3 の ACP v1・モデル・agent の一致と最小応答を記録済み。接続・最小応答だけで成果保存や独立レビューの確認としない |
| 応答前停止 | Issue #231、`809b88e` | 直接実行の無料制限と ACP 終了の因果は未確定。非同期 turn の context の寿命の不具合と合成修正確認は別に記録済み |
| Copilot の外部側作業 | `artifact-review-validation.md` の Issue #221 確認記録 | 実応答・既存成果再利用・独立レビュー・元作業の統合は操作者報告。旧契約の品質ゲート未達を維持する。OpenCode の成功証拠へ転用しない |

上表は VCS と既存記録の参照であり、今回それらの実サービス実験を再実行した記録ではない。
過去の成功対象・版・利用枠を現在へ無条件で流用しない。外部側の個人パスや session 識別子、
秘密・生ログを公開文書へ載せない。

## 今回のオフライン回帰

`go test ./internal/runtime/openacp` はローカルの合成 ACP peer を使い、
`go test -race ./internal/runtime/openacp` は同じ境界の競合を検査する。
正常テストに OpenCode 実機・ネットワーク・有料実行は要求しない。

| 確認 | 試験と期待する結果 |
|---|---|
| RPC の無料制限報告、直後の process 終了、`data` 内の報告 | `TestProviderFailureSignalsWithholdDetailsAndPreserveUnknownUsage`。取得経路と固定信号を保持し、原文・秘密を出さない |
| 標準エラーだけの報告、未知の認証文面、応答なしの終了 | 同試験。既知の報告と `unknown` を区別し、無料制限・認証障害を推測しない |
| 分割された書込みと長いログ | `TestDiagnosticStderrBoundsAndSplitWrites`。末尾4096バイトに制限し、失われた古い報告を保持し続けない |
| Controller の利用者出力と保存記録 | `TestPlannedControllerReceivesAsynchronousACPResponse`。RPC・標準エラーの固定信号を失敗記録へ保存し、利用量不明と原文秘匿を維持する |
| 正常応答・権限拒否・接続不一致・未知の利用量 | 同 Controller 試験と `TestFakePeerFailuresDeniedClientOperationsAndUnknownUsage`。成功と失敗を混同せず、許可していない操作を追加しない |
| 期限・取消と部分成果 | `TestExplicitDeadlineRequiredAndExpiredTurnIsReaped`、`TestParentCancellationReportsCauseAndPreservesPartialOutput`。原因、process 終了、部分成果、利用量不明を保持する |

## 未達の実受入れと再開条件

今回の合成確認は、現在の実 OpenCode の応答・ツールと権限判断・成果保存・
Controller 検証・同一対象の独立レビューの代用ではない。これらの操作、取消・停止、
既知／未知の利用量を、Copilot と OpenCode の両経路で対象版・モデル・アカウント・
利用枠・権限とともに対応付ける実受入れは、Issue #231 の未達条件として残す。

再開時は最初に上表の証拠と保存契約を読み、版・モデル・利用枠・権限・対象の差を記録する。
既存の停止 Flow や終端 session を再送せず、追加実行が必要な範囲と構成だけを承認する。
`TestOpenCodeLiveDocumentFlow` は明示 opt-in の既存の実機用試験であり、
環境変数の名前だけを埋めて承認とみなさない。過去の成果や利用量は保持する。
無料枠の実際の枯渇試験、有料 fallback、暗黙の再試行は不要であり許可しない。

バックエンドへ影響する更新では、両 adapter と Controller のオフライン回帰を実施し、
追加実受入れが必要かを保存構成との差から判断する。
更新していない条件の確認を繰り返さず、証拠がない項目は未実施のまま記録する。
