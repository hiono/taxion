# 6. Never mention private predecessor names in public artifacts

Date: 2026-10-09

## Status

Accepted

## Context

The private prototype is not public. Naming it (or its repos, hosts, people)
in README, docs, ADRs, issues, or commits would disclose its existence and
invite provenance linkage.

## Decision

No private predecessor/project/repo/host/personal names in any public
artifact. Refer only to generic roles (`private predecessor`,
`implementation-repo`, `validation-target`). Recorded in
`docs/02-privacy-provenance.md`; hygiene CI scans for secret shapes.

## Consequences

- Sanitization review required before merging imported knowledge.
- Failure lessons must be rewritten generically (see ADR-0002).
- Stricter wording discipline in issues and ADRs.
