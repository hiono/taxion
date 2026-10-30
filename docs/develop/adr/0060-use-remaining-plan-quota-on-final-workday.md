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
account-quota monitoring. This decision applies to the displayed Plan balance;
it does not redefine either policy or treat Session AIC usage as a limit.

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

## Consequences

The operator should choose bounded, ready work and use the visible Plan
balance rather than impose an additional conservation reserve. Work still
requires the task's existing approvals, acceptance criteria, and validation.
This ADR does not authorize speculative work, exceed an explicitly approved
hard ceiling, or claim a precise conversion between Plan quota and Session
AIC.
