# 11. Web を依存のない read-only TypeScript 表示に限定する

Date: 2026-10-09

## Status

Accepted

## Context

Platform 段階には人が読める Task Graph 表示が必要となる。判断の元は CLI が所有する Ledger のままとし、Web を第2の書込み元にしない。管理を分裂させ、混同による失敗を再導入するためである。

## Decision

`web/` は依存のない独立 TypeScript directory とする。Node ≥22.18 の型除去を使い、build は不要。単一 HTML page と `GET /api/ledger` だけを提供する。GET 以外は405とし、Ledger file は read-only で開く。全変更操作は Go CLI に残す。

## Consequences

- Task・Dispatch・Evidence の表示には Node と Ledger path だけが必要となる。
- 将来の書込み可能 UI は、この ADR を明示的に再検討する。

## 改訂（2026-10-10、M4、Issue #15–#17）

上記の条件に従って再検討した。`web/` は localhost console とし、3つの form が同一 origin の `/api/*` に POST し、変更せず `taxion serve` の write API に転送する。第2の書込み元を作らない方針は保つ。browser は Ledger を直接書かず、Go server を唯一の書込み元とし、DSP-001・SHA-001・REP-001 の検査条件は一か所で定義する。範囲は localhost 限定、認証なしのまま（認証は M5）で、loopback 外への公開には新 ADR が必要。依存なし・build なしの方針は変えない。
