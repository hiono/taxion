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

- `missing-approval-bundle`: `taxion task authorize` で bundle を追記する。
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

停止した Flow は `workflow show --id <flow-id>` で状態、停止 code、理由、Dispatch を確認し、`workflow evaluate --id <flow-id>` で approved/consumed/remaining と usage の完全性を確認する。`BUDGET_STOP` は ceiling 到達または usage 不明を意味する。不明な nano-AIU remaining は0として扱い、既存 bundle で新しい session/Dispatch を再開しない。部分実行の checkpoint と audit は保持し、新しい Task 契約・予算が承認されてから続行する。`workflow terminate` は未確認・未開始の Coder Dispatch に限る。

`workflow recover` は runner の所有権を解放する操作であり、停止条件を解除して同じ作業を再実行する操作ではない。続行には新しい Dispatch と、必要なら新しい予算の承認が要る。

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
