# Taxion

**Taxion** — Evidence-governed control plane for coding agents.

> Order / Formation / Taxis → Taxion

Task Graph + Dispatch + Evidence + Independent Review form the core value proposition.
Taxion is developed **clean-room**: no code, history, or internal identifiers ported
from any private predecessor; only generalized requirements, guardrails,
procedures, and failure lessons are carried over as sanitized docs.

## What it is

採択した製品要件は [Taxion の要件と到達条件](docs/09-orchestration-requirements.md)
を参照する。階層化した Memory と Go Copilot SDK の役割別 Session による
自動実装・独立レビューは、現在の部品から実装する次の到達条件である。
以下は現在の構成であり、この到達条件の達成を意味しない。

```
Taxion Core (SDK-neutral)
 ├─ Server (Task Graph / Dispatch Ledger / Evidence Ledger / Policy / Checkpoint)
 ├─ CLI (human interface: init / task / dispatch / review / resume)
 ├─ Web UI (task/dispatch/review forms + HTTP API proxy)
 ├─ ACP Server (stdio agent with dispatch/review methods)
 └─ Runtime adapters (local and process-backed; Go Copilot SDK adapter not yet implemented)
```

## Non-goals (Foundation → Local)

- Not a Copilot SDK wrapper — SDK types never appear in `internal/core`.
- No Git history / Issues / code ported from private predecessors.
- No personal data, internal hostnames, or org-specific repo names in public artifacts (PRIV-001).

## Quickstart

```bash
mise install
go build ./...
go test ./...
go run ./cmd/taxion --help
```

Serve the API with a token (required beyond local use; see `docs/06-remote-access.md`):

```bash
go run ./cmd/taxion gen-token                          # once: store it outside the repo
TAXION_TOKEN=<tok> go run ./cmd/taxion serve --addr 127.0.0.1:3938
```

## Docs

- [ADR-0020](docs/adr/0020-layered-memory-and-role-sessions-for-orchestration.md) —
  採択した到達条件、階層化した Memory、Go Copilot SDK の役割別 Session。
  現在の実装状況とは区別する。
- [ADR-0021](docs/adr/0021-durable-task-graph-and-dispatch-change-control.md) —
  Task Graph の情報、依存と完了条件、Dispatch の変更管理。
- `CONTRIBUTING.md` — development checks, document ownership, and comment rules
- `docs/00-charter.md` — founding scope and project constraints
- `docs/01-guardrails.md` — behavioral invariants with IDs
- `docs/02-privacy-provenance.md` — sanitization policy
- `docs/03-runtime-contract.md` — SDK-neutral Runtime interface
- `docs/04-roadmap.md` — task breakdown
- `docs/05-ledger-schema.md` — frozen ledger wire format + HTTP surface
- `docs/06-remote-access.md` — SSH-tunnel-first remote access (SSH + docker),
  loopback-by-default policy. **Start here before exposing any port.**
- `docs/07-audit-log.md` — append-only audit log (mutations + denials) and
  retention policy.
- `docs/08-memory-model.md` — current Memory storage, scopes, promotion, search, and export.
- [docs/09-orchestration-requirements.md](docs/09-orchestration-requirements.md) —
  製品要件、段階別の完了条件、現在の実装との差分。
- [docs/10-task-graph.md](docs/10-task-graph.md) —
  版付き Task Graph の登録・遷移、scope out の保持、dispatch 検査と制約。
- `docs/adr/` — design decisions and rationale; Issues record implementation results.

## CLI (Local phase, minimal)

```bash
taxion init
taxion task create --title "…" --repo <repo>
taxion dispatch --task <id> --repo <repo>
taxion review --task <id> --sha <coder-sha> --repo <repo>
taxion resume --session <id>
```

The CLI is local-first and deterministic. `taxion serve` provides the HTTP
API, `taxion acp` provides ACP over stdio, and `web/` provides the browser
console.
