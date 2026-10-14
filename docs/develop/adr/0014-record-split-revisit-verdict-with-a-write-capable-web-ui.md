# 14. 書込み可能 Web UI を踏まえて言語分割の判断を記録する

Date: 2026-10-10

## Status

Accepted

## Context

Issue #17 は M4 で ADR-0013 の再検討条件2を満たす。Web は write API（M4-1）と form・POST 転送（M4-2）を持つ browser console になった。この code で分割を再判断する。

M4 の実装から分かったことは次のとおり。

- 書込みは browser、Web proxy、Go API、CLI 所有 Ledger の順で処理する。検査条件は複製せず、400・404・409 は `internal/server` が生成して転送する。Ledger key は増えず M2-3 の固定を保つ。
- TS に増えたのは form 表示と fetch だけで、依存なし・build なしを保つ。監督・順序制御・認証など Runtime 責務は持たない。
- 分割で解決するはずの契約不一致や TS Runtime の必要は生じなかった。固定 wire format を保ち、backpressure と失敗時に許可しない動作は検査を持つ Go に残る。

## Decision

STAY SPLIT-FREE。Go は core・runtime・ACP・store・API、TS は browser console を担当する。現時点では言語分割しない。

## Consequences

write UI について ADR-0013 の条件2を解決する。残る条件は同 ADR の MCP 転送・認証要求（M5）と、Go が監督できない worker Runtime。将来、検査 logic を TS へ移す提案には、本 ADR と ADR-0011 の両方を再検討する。
