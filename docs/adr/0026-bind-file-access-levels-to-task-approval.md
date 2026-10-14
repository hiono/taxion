# 26. Task 承認に結び付けたファイルアクセスレベル

Date: 2026-10-14

## Status

Accepted

## Context

手動運用では、SDK の tool 名を許可することと、その tool が扱うファイルを許可することを
別々に指定する必要があった。新規ファイルを作る Task と、既存の追跡済みファイルだけを
直す Task の違いを、操作者が path ごとの設定から判断しなければならなかった。
tool allowlist を広げるだけでは、Task の承認範囲とファイル操作の境界が一致しない。

Human は、明示 path、Git 追跡済みファイル、割当 worktree 全体をレベルで選ぶ方式を採択した。
既存 Task の許可を暗黙に変えず、新規 Task には制限された既定値を与える必要がある。

## Decision

Task 契約に `fileAccess: exact | committed | all` を保存し、承認 hash、Dispatch の
immutable instruction、SDK permission、Controller の変更 path 検査に同じ policy を使う。
新規 Task の既定は `committed` とし、追跡対象は Dispatch の expected start commit で固定する。
`exact` は正規化した相対ファイル path に限り、新規ファイルも指定できる。
`all` は割当 worktree 内を対象とする。

全レベルで worktree 外と `.git` へのアクセスを拒否し、Reviewer は read-only とする。
Task scope、禁止範囲、SDK tool allowlist は別の条件として維持する。
ファイルアクセスレベルを選んでも、shell や任意の tool の実行は許可されない。

旧 Task・Dispatch の policy 省略値は旧動作と旧 hash を維持する。
既存 Task の policy 変更は契約変更として再承認を要求する。

## Consequences

既存ファイルだけを直す作業と、新規ファイルを作る作業の許可を、Task の承認時に区別できる。
開始後に新たに追跡されたファイルが、`committed` の許可へ自動追加されることはない。
設定を簡単にするために全 tool を既定許可する方式は採らない。

現在の permission 契約は [Runtime contract](../03-runtime-contract.md)、
Task の field と更新条件は [Task Graph](../10-task-graph.md)、
手動設定例は [利用者ガイド](../user-guide.md) が所有する。
回帰は `internal/core/fileaccess_test.go`、`internal/gitproof/fileaccess_test.go`、
`internal/runtime/copilot/fileaccess_test.go` に置く。
