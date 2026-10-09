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
