# 契約・実行・公開の gate

v0.7.0 は、作業の節目を3つの gate として観測する。gate は判定だけを行い、承認、Dispatch の発行、状態遷移を代行しない。gate が失敗しても保存状態は変わらない。

## 3つの gate

| gate | 読むもの | 通過の意味 |
| --- | --- | --- |
| contract | 保存済み Task、承認 bundle、承認 schema | Task が実行に入れる |
| execution | 保存済み Flow、Dispatch 結果、評価証拠 | 次の role または公開へ進める |
| publication | 実行 gate の結果、Task の状態 | 外部公開の要求を作れる |

`pass` は確認済みの条件がすべて満たされた状態、`blocked` は入力または承認が不足して人間の判断を待つ状態、`fail` は保存記録が矛盾しているか前提を満たさない状態を表す。未検証の項目を `pass` として報告しない。

## 実行方法

```bash
taxion gate contract --task <task-id>
taxion gate execution --flow <flow-id>
taxion gate publication --flow <flow-id>
taxion gate contract --task <task-id> --json
```

`--json` を付けた場合だけ、version 1 の機械可読 report を出力する。既定の出力は人が読む形式で、既存の CLI 出力を変えない。

## report の内容

| field | 意味 |
| --- | --- |
| `version` | report schema の版。v0.7.0 は1 |
| `gate` | `contract`・`execution`・`publication` |
| `taskId`・`flowId` | 対象の識別子 |
| `verdict` | `pass`・`fail`・`blocked` |
| `hashes` | task、proposal、record、bundle、evidence の hash |
| `blockingConditions` | 通過を妨げる条件の code と説明 |
| `drifts` | 承認 basis との差分。値は含まず field path だけを持つ |
| `authorizationImpact` | `reapproval-required`・`invalid`（`none` は予約値） |
| `nextRequiredApproval` | 次に必要な承認 |
| `nextAuthorizedTransition` | 次に認可された遷移 |
| `stateChanged` | 常に `false` |
| `observedAt` | 観測時刻 |

report は観測であり、承認、公開、完了の証拠ではない。report を保存しても Task の状態は変わらない。

## 終了 code

| code | 意味 |
| --- | --- |
| 0 | `pass` |
| 1 | `fail` |
| 2 | `blocked` |

不明な verdict は1として扱う。script は終了 code だけに依存できる。

## 実行前の read-only lane

運用者は実行前の確認として、次の4つの read-only lane をモデル session なしで並行実行できる。

```bash
taxion workflow lanes --task <task-id> --timeout 2s --root <repository-root>
```

| lane | 確認内容 |
| --- | --- |
| `repository-identity` | Task、Dispatch、instruction の repository が一致し、埋め込み credential がない |
| `baseline-worktree` | baseline SHA、branch、worktree が完全で、worktree が repository root の内側に実在する |
| `contract-consistency` | Task と最新 Dispatch instruction の objective、scope、acceptance、requiredEvidence が一致する |
| `handoff-preparation` | 有効な Dispatch があり、return format と instruction hash が揃い、running flow が handoff を保持していない |

lane は Task Graph、Dispatch、Flow、worktree、runtime を変更しない。一つでも失敗、取消、timeout になれば `executionGateOpen` は `false` になり、JSON を出したうえで非zero終了する。診断は credential を伏せる。v0.7.0 ではこの値は運用者向けの advisory で、gate や実行入口が lane report を自動で読むことはない。

lane report は gate report とは別の report である。v0.7.0 では両者を別々に読む。

## 失敗したとき

`blocked` の場合は `nextRequiredApproval` に従い、承認または handoff の release を行う。`fail` の場合は、drift、停止理由、入口拒否の code を確認し、契約を修正するか新しい Task を承認する。gate の失敗だけで Task を完了・取消にしない。

失敗の分類と復旧手順は[失敗からの復旧](failure-recovery.md)を参照する。
