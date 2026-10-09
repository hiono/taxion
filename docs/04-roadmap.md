# 04 — Roadmap (phase names, small dispatches, no big-bang)

Phases: Foundation → Core → Local → Adapter → Server → Platform → Future.

- [x] Foundation / `bootstrap` — repo, CI, charter, guardrails (this commit).
- [x] Core / `core-conformance` — ordered Sequence (CHK-002), JSON Ledger (LED-001).
- [x] Local / `cli-ux` — `task list`, `dispatch list`, JSON output, `--repo` gate (REP-001).
- [x] Adapter / `runtime-wiring` — in-process local Runtime, fail-closed.
- [x] Server / `sqlite-ledger` — sqlite store + outbox, pure-Go (ADR-0009).
- [x] Platform / `acp-spike` — minimal ACP v1 server over stdio (dispatch/review).
- [x] Platform / `web-slice` — read-only Task Graph view (`web/`, TS, zero-dep).
- [x] Future / `language-split-eval` — evaluated, deferred until core freeze (ADR-0012).

Each step: small PR, `gofmt + vet + test`, sanitized docs, SHA-bound review.
