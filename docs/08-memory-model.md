# 08 — Memory model (layers × ranges × promotion)

Frozen by ADR-0017 (M6-1, issue #27). Origin: 2026-10-09 design discussion entries
[127][129][131][141][143][155][157].

## Layers

| layer | contents | examples | enforced? |
| ----- | -------- | -------- | --------- |
| log | raw records of what happened | task/dispatch/evidence rows, audit JSONL | no (append-only) |
| lesson / decision | distilled knowledge | failure→lesson notes, **ADRs** | no (versioned, referenced) |
| guardrail | checks that must pass | DSP-001, SHA-001, promoted guidelines | **yes (dispatch-time)** |

[129] answered: an ADR belongs to lesson/decision. It records a decision
and rationale for reference, rather than an event or an enforced check.

## Ranges

| range | location | associated with | contents |
| ----- | -------- | ------------ | ----- |
| project | `.taxion/` | the repo | task memory, repo-local guidelines |
| global | `.config`-shaped | the operator | cross-repo lessons, personal rules |

## Promotion path (design)

```
repeated warning (counted, threshold N configurable)
  → guideline (proposed, human-approved)
    → guardrail (checked at dispatch beside DSP-001 et al.)
```

Private knowledge is shared through issues/MRs ([155]); the
standalone private layout sketch ([157]) is the project-range template.

## Current implementation

`memory warn` counts identical `(body, scope)` pairs. On the third occurrence,
it creates one lesson with status `proposed` as a guideline candidate. The
threshold is fixed at `PromoteAfter = 3`; it is not a CLI setting. Promotion
to a guardrail and dispatch-time enforcement of Memory entries are not
implemented. Existing core checks such as DSP-001 remain separate.

The CLI stores both `project` and `global` entries in `memory.db` next to
the selected ledger, distinguished by the `scope` column. The locations
in the range table describe the design, not separate storage directories
implemented by the current CLI.

`memory export [--scope] [--sanitize]` prints a preview to stdout only.
Sanitizing defaults on. It does not transmit data, and there are no
`--profile` or `--dry-run` flags.

## Out of M6 scope

The M6 design deferred sanitizer/export to M7, considered cost control
as an M8 candidate, and left real-agent wiring behind proc as an M3
remainder. The implemented export command is described above.
