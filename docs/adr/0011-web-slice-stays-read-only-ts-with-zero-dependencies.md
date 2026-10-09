# 11. web slice stays read-only TS with zero dependencies

Date: 2026-10-09

## Status

Accepted

## Context

Platform phase wants a human-readable Task Graph view. The SoR stays the
CLI-owned ledger; a web UI must not become a second writer (that would split
governance and reintroduce the confusion failure class).

## Decision

`web/` is a separate zero-dependency TypeScript dir (Node ≥22.18 type
stripping, no build step): one HTML page + `GET /api/ledger`. Only GET routes
exist (405 otherwise); the ledger file is opened read-only. All mutations
stay in the Go CLI.

## Consequences

- Viewing tasks/dispatches/evidence needs only node + the ledger path.
- Any future write-capable UI must revisit this ADR explicitly.

## Amendment (2026-10-10, milestone M4, issues #15–#17)

Revisited as required above. `web/` is now a localhost order counter: three
forms POST to same-origin `/api/*`, proxied untouched to the `taxion serve`
write API. The no-second-writer rule is preserved in a new form — the browser
never writes the ledger itself; the Go server remains the single writer and
guard semantics (DSP-001/SHA-001/REP-001) live in exactly one place. Scope
stays localhost-only with no auth (auth is M5); exposing beyond loopback
requires a new ADR. Zero-dependency, no-build-step policy unchanged.
