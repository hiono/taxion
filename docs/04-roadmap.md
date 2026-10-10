# 04 — Roadmap

Phases: Foundation → Core → Local → Adapter → Server → Platform → Future.

Checked items record completed work at that phase; they are not a current
feature inventory. In particular, the original read-only Web and
localhost-only API scopes were extended later. Issues hold detailed
implementation results; the reference documents linked from
[README](../README.md) describe current behavior.

- [x] Foundation / `bootstrap` — repo, CI, charter, guardrails.
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
- [x] M3 / `proc-runtime` (#12) — process-backed Runtime, worker process supervision (stdlib only).
- [x] M3 / `acp-methods` (#13) — explicit taxion/dispatch + taxion/review, convention deprecated.
- [x] M3 / `split-reeval-2` (#14) — ADR-0013: DEFER again, new triggers recorded.
- [x] M4 / `write-api` (#15) — POST tasks/dispatches/reviews, CLI-identical guards.
- [x] M4 / `web-console` (#16) — task/dispatch/review forms + POST passthrough, 502 fail-closed.
- [x] M4 / `adr-revisits` (#17) — ADR-0011 amended, ADR-0014 stay-split-free, schema write section.
- [x] M5 / `token-auth` (#22) — Bearer gate, gen-token, web forwarding, loopback refusal.
- [x] M5 / `audit-trail` (#23) — append-only JSONL of mutations + denials, operator rotates.
- [x] M5 / `public-prep` (#24) — ADR-0015 security model, history scan clean, public release.
- [x] M5 / `mode-switch` (#25) — F-lite: --mode loopback|remote, ADR-0016, IdP deferred.
- [x] M6 / `memory-model` (#27) — ADR-0017 layers×ranges×promotion, docs/08.
- [x] M6 / `guideline-promotion` (#29) — configurable warning threshold,
  explicit structured approval, and shared dispatch checks (MEM-001, ADR-0019).
- [x] M7 / `memory-export-and-scope` (#39–#40) — sanitized stdout-only
  export and explicit global writes; current commands are in docs/08.
- [x] M8 / `server-hardening` (#41–#43) — self-asserted operator labels,
  default five-failure lockout, and audit-chain verification with documented limits.
- [x] Future / `browser-terminal-eval` (#21) — approach and shell-access threat
  model recorded in ADR-0018. Implementation remains deferred; use SSH/editor
  access in docs/06 Pattern D. A terminal implementation needs its own Issue
  and browser-user authentication, not only the upstream M5 token.
- [ ] Adapter / `copilot-sdk-eval` — evaluate session start/resume fidelity,
  event mapping cost, authentication requirements, and version pinning
  before adding an SDK adapter. There is no vendored
  `internal/runtime/copilot/`; a failed evaluation leaves core untouched.

Each step: small PR, `gofmt + vet + test`, sanitized docs, SHA-bound review.
