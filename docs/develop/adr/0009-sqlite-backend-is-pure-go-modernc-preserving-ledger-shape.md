# 9. Ledger の形を保ち pure-Go modernc SQLite を使う

Date: 2026-10-09

## Status

Accepted

## Context

Server 段階には永続 SQLite 保存と、将来の relay 用 outbox が必要となる。一般的な mattn/go-sqlite3 driver は cgo toolchain を要求し、最小 host と CI の build を複雑にする。

## Decision

`internal/store` に pure-Go の `modernc.org/sqlite` v1.60.1 を使う。schema は `core.Ledger` の tasks・dispatches・evidence・checkpoints と1対1にし、JSON file と SQLite を Ledger 単位で交換可能に保つ（LED-001）。将来の server relay 用に outbox table（enqueue・claim・mark-sent）を含める。

## Consequences

- cgo toolchain は不要で、`go build`・`go test` だけで使える。
- cgo driver より binary が大きく初回 compile が遅いが、初期の build 移植性のため受け入れる。
- 性能が要求する場合は core を変えず `internal/store` 内の driver を替えられる。
