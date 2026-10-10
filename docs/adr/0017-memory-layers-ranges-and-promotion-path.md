# 17. memory layers, ranges, and promotion path

Date: 2026-10-10

## Status

Accepted

## Context

Issue #27 (M6 Memory). The 2026-10-09 design discussion identified
requirements for memory: logs should inform subsequent tasks ([127]),
repeated warnings should escalate to guidelines and then guardrails
([127]), failures/lessons/guardrails are shared via issues/MRs ([155]),
and open questions remained about which memory layer contains ADRs
([129]), how global and project memory split ([131]), and where each
lives ([141] project under `.taxion/`, [143] global as `.config`-shaped).

## Decision

Memory has three layers and two ranges:

```
log (raw: what happened)
  → lesson / decision (distilled: failures→lessons, judgments→ADRs)
    → guardrail (enforced: checked at dispatch time)
```

- ADRs live in the **lesson/decision layer**: distilled, versioned, not raw
  logs and not yet enforced checks. This answers [129].
- Ranges: **project** memory is confined under `.taxion/` ([141]);
  **global** memory is `.config`-shaped ([143]) and travels with the
  operator, not the repo.
- Promotion path ([127][155]): a repeated warning (Nth occurrence,
  threshold configurable) is proposed for guideline promotion; an
  approved guideline becomes a guardrail checked beside DSP-001 and
  other checks at dispatch time. Private knowledge is shared via
  issues/MRs ([155]) and the standalone layout sketch ([157]).

Details and the layer×range table: `docs/08-memory-model.md`.

## Consequences

- M6-2 implements the project-range store (SQLite in the initial implementation).
- M6-3 implements the promotion flow (counter → proposal → guardrail).
- M6-4 evaluates the retrieval layer (SQLite FTS vs dolt vs QMD-embedded).
- Sanitizer/export (self-protection) is out of M6 scope, deferred to M7+.

## Implementation status

The decision above records the intended model. M6-3 implemented warning
counting and guideline-candidate creation at a fixed threshold of three
occurrences; it did not implement configurable thresholds or promotion
to enforced guardrails. The CLI stores both scopes in one `memory.db`,
rather than separate project and global directories. See
`docs/08-memory-model.md` for the current implementation and export flags.
