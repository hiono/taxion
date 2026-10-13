# 13. reevaluate language split with real execution behind ACP

Date: 2026-10-10

## Status

Accepted

## Context

Issue #14 (M3, fires ADR-0012 revisit trigger 2): real agent execution now
sits behind the ACP server via `internal/runtime/proc` (M3-1), and prompt
routing is replaced by explicit `taxion/dispatch` / `taxion/review` methods
(M3-2). The question ADR-0012 deferred — split Go/TS and push full protocol
conformity — must be re-answered against this code, not speculatively.

Findings from the M3 implementation:

- The proc adapter supervises workers with stdlib `os/exec` only; event
  streaming is in-process channels with backpressure, no network boundary.
  Nothing about real execution demands a second runtime language.
- ACP conformity improved (explicit methods, same guards) but gaps remain:
  no session/load, no MCP passthrough, no auth, notifications stay coarse
  (plan + message chunks, no token streaming).
- The TS slice is still read-only and zero-dep; it gained no runtime duties.
- The ledger wire format is frozen (M2-3), so cross-language contract risk
  is lower than at ADR-0012 time — but there is still no consumer that
  needs TS outside the view.

## Decision

DEFER again. Keep Go-only core/runtime/ACP/store plus the read-only TS
view. No split, no full-conformity push now — for a new reason: M3 proved
the Go runtime can supervise real workers without language pressure.

## Consequences

Supersedes the trigger-2 portion of ADR-0012 (trigger 3, write-capable web
for M4, remains open). Revisit triggers from here:

1. An MCP passthrough or auth requirement lands (M5) — a second language
   runtime or a conformant ACP library may be preferable to a custom implementation.
2. A write-capable web UI is proposed (revisits ADR-0011 first, then this).
3. A worker runtime appears that Go cannot supervise (e.g. browser-only
   agent harness) — genuine multi-language pressure.

## Implementation clarification

At the time of this evaluation, proc-backed execution was treated as an ACP
integration. The code then provided `internal/runtime/proc` separately, while
ACP constructed a `local.Adapter`, recorded ledger operations and emitted
notifications without starting the configured worker process. The decision to
defer a language split remains unchanged.

## M15 implementation update

ACP accepts a fresh CLI-issued Dispatch and executes one turn through the
shared runtime controller. It can reconnect to an already-running Dispatch to
observe the saved turn without resending work. It does not create Tasks or
Dispatches. The Go-only language decision remains unchanged.
