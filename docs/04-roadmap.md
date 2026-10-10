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
- [x] M2 / `http-read-api` (#9) — read-only HTTP API over the ledger (GET only, localhost).
- [x] M2 / `web-api-client` (#10) — web reads via `taxion serve`, 502 fail-closed.
- [x] M2 / `wire-freeze` (#11) — schema doc (05) + CI key pinning + ADR-0012 freeze note.
- [x] M3 / `proc-runtime` (#12) — process-backed Runtime, real worker supervision (stdlib only).
- [x] M3 / `acp-methods` (#13) — explicit taxion/dispatch + taxion/review, convention deprecated.
- [x] M3 / `split-reeval-2` (#14) — ADR-0013: DEFER again, new triggers recorded.
- [x] M4 / `write-api` (#15) — POST tasks/dispatches/reviews, CLI-identical guards.
- [x] M4 / `web-counter` (#16) — order forms + POST passthrough, 502 fail-closed.
- [x] M4 / `adr-revisits` (#17) — ADR-0011 amended, ADR-0014 stay-split-free, schema write section.
- [ ] Future / `browser-terminal` (#21) — someday: in-browser terminal editing via a
  terminal-emulator library (libghostty-class) behind the web counter, over the
  SSH tunnel to the work container. Needs M5 (auth) first; revisit triggers on
  ADR-0013/0014 when touched.

Each step: small PR, `gofmt + vet + test`, sanitized docs, SHA-bound review.
