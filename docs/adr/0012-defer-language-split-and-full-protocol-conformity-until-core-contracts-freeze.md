# 12. defer language split and full protocol conformity until core contracts freeze

Date: 2026-10-09

## Status

Accepted

## Context

Issue #8 (Future phase, follows ADR-0005) asks to revisit the server/ACP/Web
language split and protocol conformity once core contracts stabilize. Status
at evaluation time:

- Core rules CHK-001/002, DSP-001, SHA-001, REP-001, ROLE-001, LED-001 are
  implemented and tested (16 Go + 4 web tests green).
- BUT the ledger wire format churned as late as #7 (lowercase JSON tags), so
  contracts are stabilizing, not frozen.
- ACP conformity gaps (spike-grade, ADR-0010): convention-based prompt
  routing (`review <sha>`), no session/load, no MCP passthrough, no auth, no
  real streaming or agent execution.
- TS is confined to the read-only view (ADR-0011); there is no runtime
  pressure for a split.

## Decision

DEFER. Keep Go-only core/runtime/ACP/store plus the read-only TS view. No
language split, no full-conformity push now.

## Consequences

Revisit triggers (any one reopens this ADR):

1. Another ledger wire-format change is proposed (contracts not yet frozen).
2. Real agent execution is put behind the ACP server (needs streaming,
   cancellation depth, MCP — conformity stops being cosmetic).
3. A write-capable web UI is proposed (revisits ADR-0011 first).

## Freeze note (2026-10-10, milestone M2)

The ledger wire format was frozen at the close of M2 (issues #9–#11):
schema pinned in `docs/05-ledger-schema.md`, key sets enforced by
`internal/core/wire_test.go` in CI, `web/` reads exclusively via the
`taxion serve` HTTP API. Trigger 1 is therefore considered satisfied going
forward — any future wire change must arrive as an ADR amending the schema
doc. Triggers 2 (M3) and 3 (M4) remain open.
