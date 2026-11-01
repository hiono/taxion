# 60. Use Remaining Plan Quota on the Final Workday

Date: 2026-10-30

## Status

Accepted

## Context

Near the end of a billing period, preserving unused Plan quota can prevent
approved, finishable work from being completed even though the quota will not
carry forward. A visible remaining balance is actionable information; a
cumulative Session AIC figure is usage, not by itself a configured spending
limit. ADR-0036 rejects stopping approved work at an unobservable token
threshold, while ADR-0051 distinguishes provider-enforced limits from
account-quota monitoring. The Plan balance and a model provider's allowance
are separate limits. This decision applies to the displayed Plan balance; it
does not redefine either policy or treat Session AIC usage as a limit.

## Decision

On the final workday before a Plan quota reset, prioritize useful, approved,
bounded work that can consume the remaining quota. Do not stop solely to
preserve that balance for the next period.

The displayed remaining Plan quota is the maximum additional Plan usage
authorized by this decision. Apply any separately configured hard ceiling
according to its existing contract. Do not infer a new session ceiling from
cumulative Session AIC, estimate an unknown remaining balance, or bypass a
known ceiling through delegation. If an explicitly configured hard ceiling
could be crossed and its remaining amount cannot be verified, stop only work
that could cross that ceiling and record the blocker.

Use the remaining balance for approved work that can reach a safe stopping
point within it. Preserve incomplete work and its evidence for the next
period.

OpenCode lists Big Pickle among its free models and describes Go separately as
a paid subscription with rolling-window and monthly usage limits
(https://opencode.ai/ja/go). Do not count use of a confirmed free model against
the Go monthly allowance or stop solely because the Go balance is unknown.
Free-model availability may still impose its own limit; if reached, stop
without switching to a paid model or subscription. When a paid monthly
allowance is selected, verify its remaining amount before work that could
exhaust it. If the selected route or fallback behavior is unknown, do not
assume it is free or switch to a paid route.

## Consequences

The operator should choose bounded, ready work and use the visible Plan
balance rather than impose an additional conservation reserve. Work still
requires the task's existing approvals, acceptance criteria, and validation.
This ADR does not authorize speculative work, exceed an explicitly approved
hard ceiling, consume a paid monthly allowance without checking its headroom,
or claim a precise conversion between Plan quota and Session AIC.
