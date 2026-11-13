# セキュリティ方針

正式配布と operation provenance の制約は [binary provenance](docs/userguide/binary-provenance.md)、trust boundary は [ADR-0044](docs/develop/adr/0044-binary-distribution-and-cooperative-provenance.md) を参照。product mode は承認済み manifest pin と実 executable/archive bytes を検証する。local administrator や検査を削除した旧binaryに対するOSアクセス隔離ではない。

## 対応するバージョン

修正は `main` に反映する。過去の公開版へのバックポートは保証しない。

## 脆弱性の報告

公開 Issue より、GitHub の非公開 security advisory を優先する。個人情報、credential、内部 host を含む PoC データは添付しない。

## 実装している対策

- `internal/core` はネットワーク・SDK・provider に依存しない（SDK-001）。
- 全 `serve` route に Bearer-token 認証を設ける。`--token`・`TAXION_TOKEN` を使い、constant-time 比較と同一の 401 応答を適用する。token なしの loopback 外 bind は起動時に拒否する。token は server 側だけで扱い、`gen-token` は暗号学的乱数32 bytes を16進文字列にする（ADR-0015）。
- 全変更の試行と拒否を[追記専用監査ログ](docs/userguide/audit-log.md)に記録する。token、body、query は含めない。Taxion は削除せず、操作者がローテーションする。
- 平文 HTTP は信頼するネットワーク（loopback、SSH tunnel、VPN、Docker bridge）内だけで使う。前段の TLS 終端なしで公開インターネットへ公開することは対象外。
- リポジトリに秘密を含めない。CI の hygiene と公開前の履歴 scan を行う。現在の CI の検査範囲は後述する。
- 各 PR に `go vet`、`gofmt -l`、`go test ./...` を要求する。
- Reviewer の入力は Coder の出力 SHA と完全一致させる（SHA-001）。

## 既知の残るリスク

- 単一の共有 token を使う。ローテーションで旧値を無効にするが、利用者別 identity はまだない。
- Taxion 自体に TLS はない。信頼するネットワーク外では前段で終端する。
- 監査ログは操作者がローテーションするまで増え続ける。

## 現在の実装の補足

既定の `--mode loopback` は、token があっても loopback 外 bind を拒否する。`--mode remote` は token を要求する。認証失敗には IP 別の rate limiting と lockout を適用する。監査 entry には操作者の自己申告ラベルと hash chain を含める。

操作者ラベルは identity の認証ではない。chain は末尾の切詰めや、hash を再計算して全体を書き換えたログを検出しない。[監査ログ](docs/userguide/audit-log.md)の制約を参照する。

上記 SHA 条件はレビュー入力への要件である。現在の CLI・API の review 操作は、空でない SHA を記録するだけで、Reviewer の実行や Git の revision 検証は行わない。CI hygiene は token パターンと秘密鍵 header を検査し、任意の内部 host や個人情報は検査しない。
