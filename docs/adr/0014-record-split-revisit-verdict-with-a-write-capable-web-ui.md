# 14. record split-revisit verdict with a write-capable web UI

Date: 2026-10-10

## Status

Accepted

## Context

Issue #17 (M4, fires ADR-0013 revisit trigger 2): the web slice is now a
write-capable browser console (M4-1 write API, M4-2 forms + POST passthrough).
The split question must be re-answered against this code.

Findings from the M4 implementation:

- Writes flow browser → web proxy → Go API → CLI-owned ledger. Guard
  semantics were not duplicated: 400/404/409 originate in `internal/server`
  and pass through. No ledger keys were added (M2-3 freeze intact).
- The TS slice gained form rendering and fetch calls only — still zero-dep,
  no build step, no runtime duties (no supervision, no sequencing, no auth).
- The failure modes a split would address (contract drift, TS runtime
  pressure) did not appear: the frozen wire format held, and backpressure /
  fail-closed behavior stayed in Go where the guards live.

## Decision

STAY SPLIT-FREE. Go keeps core/runtime/ACP/store/API; TS keeps the
browser console. No language split now.

## Consequences

ADR-0013 trigger 2 is discharged for the write-UI case. Remaining triggers
(from ADR-0013): MCP passthrough / authentication requirements (M5), or a worker runtime
Go cannot supervise. Any future proposal to move guard logic into TS must
revisit this ADR and ADR-0011 together.
