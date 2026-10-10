# 4. Use phase names instead of DayN milestones

Date: 2026-10-09

## Status

Accepted

## Context

Day1/Day2/… numbering came from a prototype style that assumed one-day
delivery. Taxion phases span indefinite time; day numbers mislead about pace
and tie public planning to a private cadence.

## Decision

Name milestones by phase: Foundation → Core → Local → Adapter → Server →
Platform → Future. No DayN references in code, docs, or GitHub milestones.

## Consequences

- Roadmap reads as scope ordering, not a schedule promise.
- GitHub milestones retain these phase identifiers, with descriptive labels
  where needed (see ADR-0007).
- Old DayN terms must be translated when importing prototype lessons.
