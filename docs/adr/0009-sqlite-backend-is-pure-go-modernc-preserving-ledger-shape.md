# 9. sqlite backend is pure-Go modernc, preserving Ledger shape

Date: 2026-10-09

## Status

Accepted

## Context

The Server phase needs durable sqlite persistence plus an outbox for future
relay delivery. The classic driver (mattn/go-sqlite3) requires a cgo
toolchain, which complicates builds on minimal hosts and CI.

## Decision

Use pure-Go `modernc.org/sqlite` (v1.60.1) under `internal/store`.
Schema mirrors `core.Ledger` 1:1 (tasks/dispatches/evidence/checkpoints) so
the JSON file backend and sqlite stay interchangeable at the Ledger level
(LED-001). An outbox table (enqueue/claim/mark-sent) is included for the
future server relay.

## Consequences

- No cgo toolchain needed anywhere; `go build`/`go test` suffice.
- Larger binary and slower first compile vs cgo driver; accepted for initial build portability.
- If performance ever demands it, the driver can be swapped behind
  `internal/store` without touching `core`.
