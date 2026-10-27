# 失敗からの復旧

実行入口と gate の拒否は、保存記録を変えずに理由を返す。復旧は、理由の分類を確認し、必要な承認または修正を行ってから再実行する。拒否を無視して Dispatch を発行し直さない。

## 実行入口の拒否

bundle-required の Task が実行入口で拒否された場合、code は次のいずれかである。

| code | 意味 | 次の操作 |
| --- | --- | --- |
| `unknown-stage` | 実行入口の stage が未知 | CLI と binary の版を確認する |
| `unknown-task` | Task が保存されていない | Task を承認する |
| `task-not-ready` | Task の状態が実行可能でない | 状態遷移を確認する |
| `no-matching-bundle` | 現在の basis を覆う bundle がない | 新しい manifest で再承認する |
| `invalid-bundle` | bundle の hash または版が一致しない | 保存記録を調査する |

拒否時は Dispatch を発行しない。Task、Flow、Dispatch の状態は変わらない。

## gate が blocked を返す

`blocked` は入力または承認の不足を表す。`blockingConditions` と `nextRequiredApproval` を確認する。

- `missing-approval-bundle`: 開発版では、元の正式な `conversation confirm --file confirmation.json` を先に照合・再実行する。未実行・未変更の READY Task に限り、確認履歴から不足 bundle を補完でき、同じ承認を取り直さない。元の要求や履歴がなければ推測で作らず、`taxion task authorize` で明示的に承認する。
- `reapproval-required`: drift を確認し、新しい token で再承認する。
- `quality-gate-unmet`: 同一 target の独立レビューと受入れ証拠を記録する。未検証のまま `pass` にしない。
- `handoff-held`: 次の role を一つだけ実行する場合は handoff を release する。release は実行中の role を取り消さない。
- `task-not-ready`・`task-not-approved`: Task の状態遷移または公開承認を確認する。

## gate が fail を返す

`fail` は保存記録の矛盾または前提の不成立を表す。自動で修復しない。

- `invalid-authorization-schema`: 未知の schema version か、矛盾した bundle がある。binary の版と保存記録を確認する。
- `approval-invalid`: bundle の hash または dimension が一致しない。再承認で上書きせず、まず記録を調査する。
- `flow-stopped`: 停止理由と保存済み findings を確認する。再開には新しい判断が必要である。
- `task-not-executable`: Task は既に完了、却下、取消のいずれかである。
- `evidence-unavailable`: 保存済み Flow の証拠を評価できない。Flow と Dispatch の結果を確認する。

## Flow が停止している

Coder が製品の実装をせず Taxion の管理 skill や管理 DB・session directory を調べて停止した場合は、役割と指示の混同を先に調べる。API 接続成功は、役割が正しく割り当てられた証拠ではない。管理用 shell や広いファイル権限の追加、同じ構成の再試行を第一の対処にしない。製品の目的・担当・必要なファイルへの既存の権限・controller による検証の分担を照合する。ログの `denied-interactively-by-user` だけで実際に利用者が拒否したと断定しない。

開発版（未配布）は Copilot Coder／Reviewer から同名の `taxion` 管理 skill を除外し、Dispatch の共通指示にも役割の境界を明記する。他の製品 skill を一括無効化せず、権限も増やさない。設定や指示を修正しても、終端 Flow の再送、旧 session の書換え、期限・予算の延長を許可するものではない。実装・検証・独立レビューの未実施を完了に変えず、元の停止・利用量と保存成果を保全して正式な後続を準備する。

停止した Flow は `workflow show --id <flow-id>` で状態、停止 code、理由、Dispatch を確認し、`workflow evaluate --id <flow-id>` で approved/consumed/remaining と usage の完全性を確認する。`BUDGET_STOP` は ceiling 到達または usage 不明を意味する。不明な nano-AIU remaining は0として扱い、既存 bundle で新しい session/Dispatch を再開しない。部分実行の checkpoint と audit は保持し、新しい Task 契約・予算が承認されてから続行する。`workflow terminate` は未確認・未開始の Coder Dispatch に限る。

`workflow recover` は runner の所有権を解放する操作であり、停止条件を解除して同じ作業を再実行する操作ではない。続行には新しい Dispatch と、必要なら新しい予算の承認が要る。

開発版の `workflow reconcile-stopped` は、`STOPPED / SAFETY_STOP` と `HOLD` が保存され、初回の snapshot 入力を持つ Coder Dispatch だけが `ISSUED` のまま残った場合を対象とする。ACK・実行開始・session・結果・runtime receipt がある場合や、他の実行が稼働中の場合には使えない。人間が継続中の実行がないことを確認し、その根拠と配送不確実性の受入れを明示してから、preview の完全ハッシュを承認する。コマンド自身が外部実行を停止・照会するわけではない。

適用すると Dispatch は `CANCELLED`、Task は `BLOCKED` となり、判断を追記保存する。元の停止理由・`HOLD`・承認・期限・予算は変更しない。未配信・未実行・無課金を証明せず、保持した tree はこの Dispatch の新成果ではなく、元の承認済み入力として扱う。後続 Task は別 worktree に同一入力を保全し、新提案と確認の両方に `reconciliationHash` を含める。新しい実行には別途承認が要る。通常取消だけではこの入力継承の根拠にならない。request と手順は [CLI リファレンス](cli-reference.md#停止済み初回指示の整合回復)を参照する。

開発版では、`workflow repair-status --id <flow-id>` の
`reviewContinuationCandidate` に成功済み Coder の Dispatch ID が表示される場合がある。
保存結果の receipt と停止・所有権・引継ぎ保留の条件を確認した、レビュー専用継続の候補であり、
実行許可ではない。新しい Task・レビュー予算の承認と対象 Git 成果物・設定の再照合を経て、
`continueFromDispatchId` に候補を指定する。成功済み実装を最初からやり直す必要はない。
対象が変わった場合や停止を確認できない場合は、候補を無条件で流用しない。
停止した `workflow run`・`step`・`resume` は、期限エラーでも stderr に状態と次の操作を表示する。

過去の結果や検証証拠を保ったまま、指示の入れ子だけを省く既存の
`--compact-context` も利用できる。ただし既存 session の option と設定 hash を
通知なしに変えて再接続しない。モデル内部の思考状態や消費済みトークンは引き継がない。

Copilot の `workflow run|step|resume` と `runtime` は、明示的な `--review-progress` で
Reviewer の確認記録を保存できる。Reviewer は専用 tool で、確認済み範囲、未確認範囲、
指摘と根拠を累積した記録として送る。Controller はその session、指示、成功済み Coder の
commit・tree と実際の作業場所を照合し、資格情報を除去して追記保存する。
作業ファイルへの書込みやレビュー承認は許可しない。既存 session への再接続では同じ option を指定する。
OpenCode はこの専用 tool に対応しておらず、指定すると拒否される。

新しい承認を得たレビュー専用継続では、最後の有効な確認記録を後続 Reviewer の指示へ渡す。
記録は Reviewer の自己申告であり、確認済み範囲を独立検証済みとは扱わない。
必須の受入れ条件は改めて独立に検証する。記録のない旧実行は `missing` と明示し、
未確認事項を完了に置き換えず、成功済み Coder の結果と検証証拠だけを引き継ぐ。
記録の破損、対象の変更、取消失敗、所有権未解放では確認記録を流用しない。
tool が呼ばれる前の中断や保存失敗では、それ以前に受理した記録までしか残らない。

`workflow repair-status` の `stopCause` は `time-limit`、`usage-limit`、
`review-findings`、`other` を区別する。保存された停止理由を使うため、
観測時に期限を過ぎたことだけで過去の停止原因を時間切れへ変更しない。
これらの変更によるトークン消費や費用の削減率は未測定である。

handoff が hold されている場合、hold は次の role を止めるだけで、実行中の role を止めない。release する前に、表示された instruction、draft、結果の対象を確認する。

## lane が失敗した

`taxion workflow lanes` は、いずれかの lane が失敗、取消、timeout になると execution gate を閉じる。診断には失敗した lane と redacted な理由が含まれる。

- `repository-identity`: repository の不一致、または埋め込み credential を除く。
- `baseline-worktree`: baseline SHA、branch、worktree の指定を直す。worktree は repository root の内側に置く。
- `contract-consistency`: Task と最新 instruction の契約を一致させる。Dispatch を黙って書き換えない。
- `handoff-preparation`: 有効な Dispatch、return format、instruction hash、handoff の状態を確認する。

lane は read-only であり、失敗しても保存状態は変わらない。

## rollback

v0.7.0 の保存形式は additive である。rollback は binary を v0.6.x に戻し、ledger を書き換えない。bundle は残るが、旧 binary は従来の検査だけを行う。再度 v0.7.0 を使う場合も bundle はそのまま読める。

gate report と lane report は観測であり、失敗しても状態を変更しない。復旧の判断は人間が行う。
