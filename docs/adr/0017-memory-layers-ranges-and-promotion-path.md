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

The following tables describe the intended model, not the current store:

| layer | contents | examples | intended enforcement |
| --- | --- | --- | --- |
| log | raw records of what happened | task/dispatch/evidence rows, audit JSONL | none; append-only |
| lesson / decision | distilled knowledge | failure lessons, ADRs | versioned and referenced |
| guardrail | checks that must pass | DSP-001, SHA-001, promoted guidelines | dispatch-time |

| range | intended location | associated with | contents |
| --- | --- | --- | --- |
| project | `.taxion/` | repository | task memory, repo-local guidelines |
| global | `.config`-shaped | operator | cross-repo lessons, personal rules |

## Consequences

- M6-2 implements the project-range store (SQLite in the initial implementation).
- M6-3 implements the promotion flow (counter → proposal → guardrail).
- M6-4 evaluates the retrieval layer (SQLite FTS vs dolt vs QMD-embedded).
- Sanitizer/export (self-protection) is out of M6 scope, deferred to M7+.
- The M6 planning also considered cost control as an M8 candidate and
  left real-agent wiring behind proc as an M3 remainder. These are planning
  context, not a current implementation inventory.

## Implementation status

The decision above records the intended model. The initial M6-3 implementation provided warning
counting and guideline-candidate creation at a fixed threshold of three
occurrences; it did not implement configurable thresholds or promotion
to enforced guardrails. The CLI stores both scopes in one `memory.db`,
rather than separate project and global directories. Current behavior
is documented in [the Memory reference](../08-memory-model.md);
implementation results are recorded in Issues #28–#34.

M6-3 was reopened to complete threshold configuration, explicit approval,
and dispatch enforcement. The accepted predicate boundary is recorded in
[ADR-0019](0019-structured-memory-approval-and-dispatch-checks.md);
current commands and limitations remain in the Memory reference. The
initial three-occurrence implementation described above is historical.

M10 Issue #66 adds source references, versioned revisions, revocation, and
lifecycle events to the SQLite Memory store. Task Graph `memoryRefs` point to
Memory IDs; dispatch rejects missing, superseded, revoked, and unapproved
guardrail references. Project and global remain scopes in the same
workspace-adjacent `memory.db`, not separate storage locations. Older warning
counters did not retain individual evidence, so migration labels their
aggregate source instead of reconstructing occurrence history.
