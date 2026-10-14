# 13. ACP の実行機能を踏まえて言語分割を再評価する

Date: 2026-10-10

## Status

Accepted

## Context

Issue #14 は M3 で ADR-0012 の再検討条件2を満たす。M3-1 の `internal/runtime/proc` による実 agent 実行を ACP server の背後に置き、M3-2 で慣例 routing を明示的な `taxion/dispatch`・`taxion/review` に替えたものとして評価した。ADR-0012 で延期した Go・TS の分割と protocol 全面準拠を、推測でなくこの code に基づいて再判断する。実際の ACP 接続状況は後述の補足に記録する。

M3 の実装から分かったことは次のとおり。

- proc adapter は標準の `os/exec` だけで worker を監督する。event streaming は backpressure 付き process 内 channel で、network の分離はない。実実行は第2の Runtime 言語を要求しない。
- ACP は明示 method と共通検査で準拠が進んだが、session/load、MCP 転送、認証はない。通知は計画と message chunk にとどまり、token streaming はない。
- TS は依存なしの read-only 表示のままで、Runtime の責務は増えていない。
- Ledger wire format は M2-3 で固定され、ADR-0012 時点より言語間の契約 risk は小さい。しかし表示外の TS を必要とする利用先はまだない。

## Decision

再び DEFER。Go の core・runtime・ACP・store と read-only TS 表示を維持し、現時点で分割・全面準拠は進めない。今回は M3 によって、Go Runtime が別言語を要求せず実 worker を監督できると分かったことを理由とする。

## Consequences

ADR-0012 の条件2に関する判断を置き換える。条件3（M4 の write Web）は未解決のまま残す。以後は次で再検討する。

1. MCP 転送・認証が要求される（M5）。独自実装より別 Runtime 言語や準拠済み ACP library が適する場合がある。
2. write Web が提案される（ADR-0011 を先に再検討し、次に本 ADR）。
3. Go が監督できない worker Runtime が現れる。browser 専用 agent harness など、実際に複数言語を要求するものを想定する。

## 実装状況の訂正

評価時は proc 実行を ACP 統合として扱った。しかし当時の code は `internal/runtime/proc` を別に提供し、ACP は `local.Adapter` を作って Ledger 操作と通知を行うだけで、設定 worker process は起動しなかった。言語分割を延期する判断は変えない。

## M15 の実装補足

ACP は CLI 発行の新 Dispatch を受け、共通 Runtime Controller で1 turn を実行する。実行中 Dispatch への再接続は保存 turn の観測だけで、作業を再送しない。Task・Dispatch は作成しない。Go を維持する言語の判断は変えない。
