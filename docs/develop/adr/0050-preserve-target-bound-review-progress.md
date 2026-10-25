# ADR-0050: 中断レビューの確認記録を対象に結び付けて保存する

## Context

成功した実装を保存しても、独立レビューが期限に達すると途中の調査内容が残らず、
後続レビューで再調査する場合がある。既存のレビュー専用継続は成功済み Coder の
再実行を避けるが、Reviewer の途中確認記録は保存していなかった。

## Decision

Copilot Reviewer に、明示選択する専用 tool で確認範囲、未確認範囲、指摘と根拠を送らせる。
Controller は session、指示 hash、成功済み Coder の commit・tree と現物を照合し、
redaction 後の記録を append-only receipt として保存する。
1件64 KiB、32件までとし、各記録を累積した自己申告として扱う。

新しい Task と予算が承認されたレビュー専用継続では、最後の有効な記録を指示に含める。
停止・所有権解放・引継ぎ保留・成果物照合の条件は維持する。
記録が存在する場合は停止 receipt も検証する。記録のない旧実行は欠落を明示する。
自己申告を検証済みの範囲や承認に昇格させず、後続 Reviewer に独立検証を求める。

時間、利用量、レビュー指摘による停止原因は、停止時に省略可能な `stopCause` へ保存する。
期限到達と修正回数上限も共通の停止 code を使うため、code だけで原因を分類しない。
旧記録の観測では保存理由を使い、現在時刻を使って過去の停止原因を変えない。

## Alternatives considered

会話全体の再送は入力を増やし、対象の照合も曖昧になる。終了時だけの保存は、
期限到達や取消前に呼ばれない場合に備えられない。内部の思考状態や消費済み token を
復元する機構は採用しない。

## Consequences

成功済み Coder と受理済みの確認記録を再利用できる。保存前に中断した内容は復元できず、
後続レビューの実際の調査量や課金がどれだけ減るかは未測定である。
synthetic test によるデータの引継ぎと、実作業での費用対効果は別の検証とする。

## Security and operational impact

Reviewer の作業ファイルは引き続き読み取り専用とする。専用 tool は記録保存だけを許可し、
実行許可、予算延長、成功結果を作らない。session 不一致、対象変更、取消失敗、
破損した記録は拒否する。停止後や期限後の追加保存は許可しない。
資格情報の redaction は未知の機密内容をすべて識別する保証ではない。

## Compatibility

`--review-progress` 未指定時の tool 集合と設定 hash は維持する。
既存 session へ再接続するときは元と同じ option を使う。
OpenCode の専用 tool は未対応であり、option の指定を拒否する。
rollback 前には session の停止を確認し、確認記録や停止履歴は削除しない。
旧版での接続失敗を避けるため、新しい tool を持つ session は旧版で再開しない。

## Status

2026-10-25 時点の実装判断。関連 Issue は #171。
`TestReviewProgress*` は、保存後の期限停止、取消失敗、欠落・破損、対象変更、
redaction と上限、承認した後続 Task での Reviewer のみの発行を合成検証する。
実モデルを使った再実行や費用削減率の測定は、この変更の受入れ証拠に含めない。

同日のローカル検証では `go generate ./cmd/taxion`、`go build ./...`、
`go vet ./...`、`go test ./...` と変更 package の対象を絞った `go test -race` を実行した。
quota の実 account 検証は `TestAccountQuotaSignedInAcceptance` の読取りだけであり、
Coder・Reviewer の実モデル実行を意味しない。公開 CI と配布物の検証は別に扱う。
