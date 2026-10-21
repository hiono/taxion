# Approval gates v0.7.0 implementation record

Date: 2026-10-21

## Scope

Epic #107 delivers three approval gates, an ApprovalBundle, controller read-only lanes, machine-readable reports, immutable role handoff packets, and Task-level accounting with recontract. The v0.7.0 baseline already includes #108 and #109 on main at `5a66460008ba1cd4f4034022548d591fcc9d8407`.

## Wave 1

- #110 / PR #117 adds four model-free read-only lanes with bounded concurrency, per-lane timeouts, deterministic aggregation, fail-closed reporting, and redacted diagnostics.
- #112 / PR #118 adds versioned Contract, Execution, and Publication gate reports. JSON is opt-in; human-readable output remains the default.
- #114 / PR #119 records ADR-0043 and the user/developer documentation for the shipped gates, approval bundles, lanes, and reports.

Wave 1 merged to main in PRs #117, #118, and #119. The merged baseline is `9452a94cf279d9f90f9be289b2556b20d62026bc`.

## Wave 2

- #111 / PR #120 adds versioned, canonical immutable role handoff packets with hash verification, tamper detection, and credential redaction. Merge commit: `cceee3c48fc0a6ea9fcb043399ee471c3708d578`.
- #113 / PR #121 adds Task budget reporting, fail-closed hard stops, active-session stop/resume checks, append-only budget audit, and terminate-and-recontract for an untouched Dispatch. Merge commit: `17b33d054ed95c8be44adc22579ce42f08d48e72`.
- PR #121 required checks passed: build, core-purity, web, and hygiene. `go build ./...`, `go vet ./...`, `go test ./...`, controller/store race tests, and `git diff --check` passed on the integrated head.

The seven Epic issues #108–#114 are implemented and merged. This record documents implementation status only; v0.7.0 is not released until its release gates pass. Release notes are published through the GitHub Release, as decided in ADR-0037.

## Deferred

General DAG execution, dynamic lane plugins, retry scheduling, advanced caching, UI, and embedding lane results into gate reports remain deferred to v0.7.1. Provider-wide billing, account balance prediction, and currency conversion are not implemented.
