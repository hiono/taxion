# 08 — Memory model (layers × ranges × promotion)

Frozen by ADR-0017 (M6-1, issue #27). Origin: 2026-10-09 wall log turns
[127][129][131][141][143][155][157].

## Layers

| layer | contents | examples | enforced? |
| ----- | -------- | -------- | --------- |
| log | raw records of what happened | task/dispatch/evidence rows, audit JSONL | no (append-only) |
| lesson / decision | distilled knowledge | failure→lesson notes, **ADRs** | no (versioned, referenced) |
| guardrail | checks that must pass | DSP-001, SHA-001, promoted guidelines | **yes (dispatch-time)** |

[129] answered: an ADR is a lesson/decision — distilled enough to cite,
not raw enough to be a log, not yet a gate.

## Ranges

| range | lives in | travels with | holds |
| ----- | -------- | ------------ | ----- |
| project | `.taxion/` | the repo | task memory, repo-local guidelines |
| global | `.config`-shaped | the operator | cross-repo lessons, personal rules |

## Promotion path

```
repeated warning (counted, threshold N configurable)
  → guideline (proposed, human-approved)
    → guardrail (checked at dispatch beside DSP-001 et al.)
```

Private knowledge graduates to shared through issues/MRs ([155]); the
standalone private layout sketch ([157]) is the project-range template.

## Out of M6 scope

Sanitizer / `export --profile` (M7 self-protection), cost control (M8
candidate), real-agent wiring behind proc (M3 remainder).
