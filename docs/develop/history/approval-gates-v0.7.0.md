# Approval gates v0.7.0 implementation record

Date: 2026-10-21

## Scope

Epic #107 delivers three approval gates, an ApprovalBundle, controller read-only lanes, machine-readable reports, immutable role handoff packets, and Task-level accounting with recontract. The v0.7.0 baseline already includes #108 and #109 on main at `5a66460008ba1cd4f4034022548d591fcc9d8407`.

## Wave 1

- #110 / PR #117 adds four model-free read-only lanes with bounded concurrency, per-lane timeouts, deterministic aggregation, fail-closed reporting, and redacted diagnostics.
- #112 / PR #118 adds versioned Contract, Execution, and Publication gate reports. JSON is opt-in; human-readable output remains the default.
- #114 / PR #119 records ADR-0043 and the user/developer documentation for the shipped gates, approval bundles, lanes, and reports.

At the time this record was written, these PRs were open. Their previously reported CI checks passed; documentation additions in this PR still require CI on the updated head. No release has been created.

## Deferred

Immutable role handoff packets (#111) and Task-level usage accounting and recontract (#113) remain outstanding. They must not be described as available in v0.7.0 until implemented and validated. Release notes are published through the GitHub Release, as decided in ADR-0037.
