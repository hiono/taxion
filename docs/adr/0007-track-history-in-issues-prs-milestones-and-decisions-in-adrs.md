# 7. Track history in issues-PRs-milestones and decisions in ADRs

Date: 2026-10-09

## Status

Accepted

## Context

Without a stated split, work history (what was done, when) and rationale (why
it was decided) blur together across source comments, chat logs, and docs.

## Decision

- Source tree = current state only.
- GitHub issues / PRs / milestones = work history (per-phase milestones from
  ADR-0004; roadmap items become tracked issues).
- `docs/adr` (managed with `adrs`, installed via `mise use cargo:adrs`) =
  durable decisions. Every behavior-affecting change links its ADR.

## Consequences

- Reviews check: history in the issue, rationale in the ADR, state in the diff.
- ADR count grows steadily; superseded ADRs stay (status updated, never deleted).
- `adrs` becomes a required dev tool via `mise.toml`.
