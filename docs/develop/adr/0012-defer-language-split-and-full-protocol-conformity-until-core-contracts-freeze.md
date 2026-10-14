# 12. core 契約の固定まで言語分割と protocol 全面準拠を延期する

Date: 2026-10-09

## Status

Accepted

## Context

Issue #8 は Future 段階で ADR-0005 を受け、core 契約が安定したら server・ACP・Web の言語分割と protocol 準拠を再検討するよう求める。評価時の状態は次のとおり。

- CHK-001/002、DSP-001、SHA-001、REP-001、ROLE-001、LED-001 は実装・検証済み（Go 16件、Web 4件が成功）。
- ただし Ledger wire format は #7 でも lowercase JSON tag へ変更され、安定化中であって固定済みではない。
- ACP は試作段階（ADR-0010）で、`review <sha>` の慣例 routing を使う。session/load、MCP 転送、認証、実 streaming・agent 実行はない。
- TS は read-only 表示に限定し（ADR-0011）、実行上の言語分割の必要はない。

## Decision

DEFER。Go の core・runtime・ACP・store と read-only TS 表示を維持する。現時点で言語分割や全面準拠は進めない。

## Consequences

いずれか一つを満たしたら再検討する。

1. Ledger wire format の変更が再提案される（まだ契約が固定していない）。
2. ACP server で実 agent を実行する（streaming、詳細な取消対応、MCP が必要となり、準拠が形式だけの問題ではなくなる）。
3. 書込み可能な Web UI が提案される（先に ADR-0011 を再検討する）。

## 固定時の補足（2026-10-10、M2）

M2 完了時（Issue #9–#11）に Ledger wire format を固定した。`docs/develop/05-ledger-schema.md` に schema を記録し、CI の `internal/core/wire_test.go` で key 集合を強制する。`web/` は `taxion serve` HTTP API だけで読む。今後は条件1を満たしたものとし、wire 変更には schema 文書を改訂する ADR を必須とする。条件2（M3）と3（M4）は未解決のまま残す。
