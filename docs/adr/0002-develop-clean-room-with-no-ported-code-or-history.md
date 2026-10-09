# 2. Develop clean-room with no ported code or history

Date: 2026-10-09

## Status

Accepted

## Context

Taxion follows a private prototype that validated Task Graph + Dispatch +
Evidence + Independent Review. Porting its code, git history, issues, or
identifiers would leak private provenance (names, paths, hosts) and carry over
prototype-specific assumptions.

## Decision

Develop clean-room: no code, history, issues, names, paths, or credentials are
ported. Only generalized requirements, guardrails, procedures, and failure
lessons — after privacy/provenance sanitization — may serve as input.

## Consequences

- Public tree stays free of private provenance; PRIV-001 is enforceable by CI.
- Slower start: every behavior is re-derived from sanitized specs with an ID.
- Prototype failure patterns (e.g. repo/target confusion) must be re-encoded as
  guardrails (DSP-001 / SHA-001) instead of copied fixes.
