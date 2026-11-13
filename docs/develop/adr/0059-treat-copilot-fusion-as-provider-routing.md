# 59. Copilot Fusion を提供元側の model routing として扱う

Date: 2026-10-30

## Context

2026-10-30、利用者の Copilot CLI 1.0.105 の model picker に
`Copilot Fusion` が表示された。提示画面では Fusion Standard が選択され、
選択中の Claude Opus 5 と「Credits Per 1M Tokens」の表示も確認できた。
画面はこの選択を session only と説明し、将来の session には `/config model` を
使うよう案内している。また、Fusion の選択は Autopilot や Assisted Permissions を
変更しないと表示された。画面上の token 単価はその時点の観測値であり、
固定価格や全 session の実利用 model を示すものではない。

GitHub の現行文書では、Copilot Fusion は prompt ごとに model と実行方式を選ぶ
router/orchestrator とされる。方式には単一 model、draft を別 model が評価する
Cascade、複数 model が批評する Critique がある。Fusion Standard は各 task に
一つの model を選び、High / Max は品質や推論を優先し、必要に応じて追加の model
call を行う。利用中の model は応答後に確認できる。

これは Auto の一律な改称ではない。GitHub は Auto model selection も別機能として
文書化しており、利用 surface に Fusion が表示される場合は、その surface で
Fusion が利用可能であることを示すとしている。
CLI の `/model` は現在の session を変更し、その session の再開後も選択を保つ。
`/config model` は現在と将来の session に適用する。

Taxion の Copilot adapter は SDK v1.0.22 の `SessionConfig` と
`ResumeSessionConfig` に `Model: "auto"` を明示している。SDK には Fusion の phase・
aggregate usage event 型もあるが experimental とされる。現 adapter の
`normalizeEvent` は `AssistantUsageData` を通常の usage event に変換する一方、
Fusion 固有 event は変換しない。したがって、CLI picker の変更だけから
Taxion の SDK session が Fusion に切り替わった、または Fusion の全利用量が
Taxion の予算会計へ入るとは判断できない。

## Decision

Microsoft/GitHub は Copilot の利用者向け model-selection 方針を Fusion へ移行し、
task ごとの model と実行方式を提供元で選ぶ。Taxion はこの方針に追随し、
Copilot SDK を更新して Fusion を明示的にサポートする。対応作業は Issue #216 で追跡する。

判断の比較では、Copilot の製品方針に合わせるという Microsoft/GitHub の選択を採用する。
先の「Fusion は Auto の一律な改称ではない」という評価は、現在の SDK 動作と機能差の
説明としては妥当だが、Taxion の長期方針まで Auto 維持に寄せた点は適切でなかった。
Taxion のモデル選択の目標を Fusion とし、Auto は実装移行中の一時的な動作と位置付ける。

Fusion は SDK 更新と実装が完了するまでは有効化しない。実装では、全 phase の利用量と
aggregate usage を二重計上なく受け取り、native AI-credit ceiling と実行停止が
multi-model turn でも守られることを検証する。使用量の欠落や集計の不一致はゼロとして
扱わず、従来どおり fail-closed にする。Fusion の有効化はモデル実行設定の変更として扱い、
Task 承認と runtime configuration に結び付ける。

## Alternatives considered

Auto を長期的な Taxion の既定として維持する案は採らない。Microsoft/GitHub の製品方針と
利用者の Copilot CLI に示された選択を踏まえ、Taxion も Fusion を目標にする。一方、
SDK の契約と Fusion の usage accounting が Taxion の budget boundary で確認される前に
`"auto"` を即時に `"fusion"` へ置き換える案も採らない。製品方針の採用と、安全な実装の
完了は別の判断だからである。

## Consequences

利用者の interactive CLI で選んだ `/model` は、Taxion が別途生成する SDK session の
model を変更しない。Taxion は移行完了まで adapter の `"auto"` を使い、Fusion 対応を
検証したリリースで目標設定へ切り替える。

Fusion 対応では SDK/CLI の実行契約、per-phase usage、aggregate usage、budget stop の回帰を
同時に確認する。単価や利用可能 model は提供元の変更に追随するため、この ADR の観測値を
実装定数にしない。完了状況と未解決条件は Issue #216 に記録する。

## Security and operational impact

Fusion が選んだ model や追加呼出しは、Taxion の承認済み file access、tool policy、
期限、native AI-credit ceiling を緩和しない。model routing の利用量が照合できない
間は、Fusion を Taxion の承認済み実行方式として案内しない。

## Compatibility

保存済み Task、Dispatch、usage receipt、既存 SDK session の意味は変更しない。
この決定による database migration はない。将来 Fusion を有効にする場合は、
明示的な設定・承認と usage event の互換性を別途実装・検証する。

## Status

Accepted. 2026-10-30 時点の Copilot CLI UI、GitHub 文書、Taxion の SDK v1.0.22
設定と adapter 実装を根拠とする。Microsoft/GitHub の Fusion 方針を Taxion の目標として
採用する。Taxion 側の SDK 更新・Fusion 実行・会計受入れは未実施で、Issue #216 で追跡する。

## References

- [About GitHub Copilot Fusion](https://docs.github.com/en/copilot/concepts/models/copilot-fusion)
- [About GitHub Copilot auto model selection](https://docs.github.com/en/copilot/concepts/models/auto-model-selection)
- [Choosing a model in GitHub Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/choose-a-model)
- [Copilot SDK Go v1.0.22 session configuration](https://github.com/github/copilot-sdk/blob/go/v1.0.22/go/types.go)
- [Copilot SDK Go v1.0.22 session events](https://github.com/github/copilot-sdk/blob/go/v1.0.22/go/rpc/zsession_events.go)
- `go.mod`: Copilot SDK `v1.0.22`
- `internal/runtime/copilot/copilot.go`: explicit `"auto"` model on create and resume
- `internal/runtime/copilot/copilot.go`: `normalizeEvent` usage conversion
