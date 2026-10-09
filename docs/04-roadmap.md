# 04 — Roadmap (phase names, small dispatches, no big-bang)

Phases: Foundation → Core → Local → Adapter → Server → Platform → Future.

- [x] Foundation / `bootstrap` — repo, CI, charter, guardrails (this commit).
- [ ] Core / `core-conformance` — extend core tests: checkpoint sequences, ledger persistence (sqlite TBD in Server phase).
- [ ] Local / `cli-ux` — `task list`, `dispatch list`, JSON output, `--repo` identity gate (anti-confusion).
- [ ] Adapter / `sdk-eval` — runtime adapter feasibility spike in a scratch branch; report only, no core change.
- [ ] Server / `server-design` — design doc: Task Graph / Dispatch Ledger / Evidence Ledger as SoR, HTTP surface.
- [ ] Platform / `acp-spike` — minimal ACP server exposing `dispatch` / `review` to agent-client-class tools.
- [ ] Platform / `web-slice` — read-only Task Graph view (TS, separate dir when started).

Each step: small PR, `gofmt + vet + test`, sanitized docs, SHA-bound review.
