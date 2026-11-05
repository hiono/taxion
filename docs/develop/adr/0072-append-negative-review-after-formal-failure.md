# ADR-0072: FAILED Reviewer の保存済み指摘を失敗履歴とは別に追記する

## Context

Reviewer のモデル回答が保存されても、通信切断により Controller が正式な `FAILED` を保存する場合がある。
最終回答・turn 終了・routine shutdown があることと、Controller が品質検証を完了したことは別である。
既存の `recover` は `RUNNING` かつ正式結果なしの確定、
`recover-review` は停止 receipt 付き `CANCELLED` の成果物レビュー後続、
`accept-saved-review` は特定の不具合で停止した正式成功済み `APPROVE` の受入れを扱う。
これらの対象条件を広げるだけでは、不変な失敗履歴と品質検証の契約を維持できない。

## Decision

`workflow record-failed-review` を追加し、元の実行に束縛された `REQUEST_CHANGES` だけを
`Records.FailedReviewSupplements` へ追記する。これは正式な指摘記録であり、
成功した `DispatchResult` への置換でも、品質結果の正式受理でもない。
回答中の `status:SUCCEEDED` はモデルの申告として保持し、Dispatch 自体は `FAILED` のままとする。

| 元 Dispatch と保存回答 | 必須条件 | 操作と効果 |
|---|---|---|
| `RUNNING`、正式結果なし | 元 runner の停止、期限内 native 完了、元実行との照合、host 検証権限 | 既存 `recover`。通常の結果確定へ合流する |
| `CANCELLED`、成果物 Reviewer | 元契約に一致する停止 receipt、検証済み候補、後続の承認 | 既存 `recover-review`。新たな Reviewer 実行を伴う |
| `FAILED`、`REQUEST_CHANGES` | 同じ session・設定・指示・実行観測、元期限内かつ正式失敗前の完了、停止確認、準備済み tree の一致 | 新操作。指摘だけを追記する |
| `FAILED`、`APPROVE` または `BLOCKED` | 完了した native 履歴があっても対象外 | 拒否する。今回の負の指摘記録から一般化しない |
| `FAILED`、原文・実行 anchor・期限・候補のいずれかが不明または不一致 | 回答ファイルや申告で不足を補わない | 拒否し、元の履歴を保存する |
| 同じ Dispatch の追記済み回答 | 二重処理しない | 既存記録を確認する |

preview は実行を再開せず元 Copilot session を観測する。pending work、tools、hooks、
transcript 修復を有効にしない。保存済みの実行 anchor と native 履歴を照合し、
曖昧な回答、別 session、後続実行、正常終了の欠落を拒否する。
Caller が作った回答 JSON の入力は提供しない。

apply は同じ要求、workspace 全体、元 Dispatch、native 履歴、保存回答を含む preview hash を要求する。
処理中の workspace 変更は transaction 内で再確認して拒否する。
追記記録の削除・入れ子の書換えと、元 Dispatch の指示・結果・利用量の変更は拒否する。
現在の Task・Flow・owner・operation・既存 receipt は変更しない。
別の稼働実行があれば拒否し、同じ旧 Flow の claim は停止確認と完全一致した場合だけ保持したまま観測する。

host 検証は開始済みでも未実施でも再実行しない。
回答中の検証申告は `NOT_REPLAYED_NOT_ACCEPTED` と区別し、検証 receipt に変換しない。
shutdown 集計は追加証拠として保持するが、旧利用量をリセット・加算・確定せず、
`accountingReconciled:false` とする。後続の実行には累計消費の別途照合が必要である。
修正、再レビュー、品質合格、公開、完了をこの操作から許可しない。

`.env.ci.example` の index 拒否は変更しない。秘密検査の前に全 prospective index の
`.env` と `.env.*` を拒否する既存方針は、変更していない例示ファイルにも適用される。
差分だけへの縮小、scanner の迂回、例示名の一括許可は今回の対応に含めない。

## Alternatives considered

`FAILED` を `RUNNING` に戻す方法は、正式結果と利用量の履歴を変えるため採用しない。
通常 finalizer への再投入は host 検証の再実行を伴うため、今回の非実行契約には使わない。
新 Reviewer への再送は、既存指摘の原本を処理する目的とは異なるため採用しない。
回答を品質合格として扱う方法は、`REQUEST_CHANGES` の意味とも独立検証とも一致しない。

## Consequences

保存された指摘を参照しながら、失敗理由と未確認の品質・利用量を区別できる。
旧 Flow を再開したり Task を自動で `REQUEST_CHANGES` へ遷移させたりはしない。
準備済み Reviewer worktree が解放・移動・変更された場合、commit mode、復元 workspace、
他 provider の native 履歴は対象外である。対象外を旧状態の手編集で解消してはならない。

## Security and operational impact

申請者・承認出所・停止証拠は既存と同様の協調的な記録であり、認証 credential ではない。
credential に見える回答や申請情報は出力・追記を拒否し、原本を秘匿して保持する。
実行権限は追加しない。既存の停止・品質・公開 gate と予算上限を維持する。
運用 store 操作、製品検証の再実行、運用機への導入は、この変更の開発検証とは別の承認範囲である。

## Compatibility

DB の table schema は変えず、任意の Records 配列を追加する。
追記前の既存 workspace はそのまま読める。追記後は新 field を理解する版を使用する。
旧版へ戻すために追記記録を削除する移行は提供しない。必要なら操作前の保全済み複製を使う。
既存の `recover`、`recover-review`、`accept-saved-review` の状態条件は維持する。

## Status

2026-11-06 の実装判断。合成 repository、合成回答、ローカル SDK protocol peer の検証と
個別事象の原本確認を区別する。運用原本未提供の事象を確認済みとは扱わない。
変更範囲と検証記録は [Issue #268](https://github.com/hiono/taxion/issues/268) を参照する。
