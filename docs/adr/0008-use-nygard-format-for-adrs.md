# 8. Use Nygard format for ADRs

Date: 2026-10-09

## Status

Accepted

## Context

`adrs` supports Nygard (classic) and MADR 4.0.0. MADR adds decision-makers,
consulted/informed, considered options, and pros/cons sections, but Taxion has
a single decision-maker and rarely compares options, so those sections would
stay empty and raise the cost of writing records.

## Decision

Write all ADRs in Nygard format (`Status / Context / Decision /
Consequences`). If a future decision genuinely requires option comparison, a
single MADR record may be used as an exception (`adrs new --format madr`).

## Consequences

- Records stay cheap to write, which keeps them written.
- No decision-maker tracking; history of who decided lives in git/issues.
- Mixed formats remain possible per record without migration.
