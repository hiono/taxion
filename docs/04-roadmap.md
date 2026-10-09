# 04 — Roadmap (small dispatches, no big-bang)

1. `bootstrap` — repo, CI, charter, guardrails (this commit). ✅
2. `core-conformance` — extend core tests: checkpoint sequences, ledger persistence (sqlite TBD Day3).
3. `cli-ux` — `task list`, `dispatch list`, JSON output, `--repo` identity gate (anti-confusion).
4. `sdk-eval` — Copilot SDK for Go feasibility spike in a scratch branch; report only, no core change.
5. `server-design` — Day3 design doc: Task Graph / Dispatch Ledger / Evidence Ledger as SoR, HTTP + ACP surface.
6. `acp-spike` — minimal ACP server exposing `dispatch` / `review` to opencode-class clients.
7. `web-slice` — read-only Task Graph view (TS, separate dir when started).

Each step: small PR, `gofmt + vet + test`, sanitized docs, SHA-bound review.
