# Taxion

**Taxion** — Evidence-governed control plane for coding agents.

> Order / Formation / Taxis → Taxion

Task Graph + Dispatch + Evidence + Independent Review form the core value proposition.
Taxion is developed **clean-room**: no code, history, or internal identifiers ported
from any private predecessor; only generalized requirements, guardrails,
procedures, and failure lessons are carried over as sanitized docs.

## What it is

```
Taxion Core (SDK-neutral)
 ├─ Server (Task Graph / Dispatch Ledger / Evidence Ledger / Policy / Checkpoint)
 ├─ CLI (human interface: init / task / dispatch / review / resume)
 ├─ Web UI (future)
 ├─ ACP Server (future: for opencode / cursor / copilot-cli / custom tools)
 └─ Runtime adapters (plugins, e.g. Copilot SDK for Go as first candidate)
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

- `docs/00-charter.md` — scope, non-goals, phase roadmap
- `docs/01-guardrails.md` — behavioral invariants with IDs
- `docs/02-privacy-provenance.md` — sanitization policy
- `docs/03-runtime-contract.md` — SDK-neutral Runtime interface
- `docs/04-roadmap.md` — task breakdown
- `docs/05-ledger-schema.md` — frozen ledger wire format + HTTP surface
- `docs/06-remote-access.md` — SSH-tunnel-first remote access (SSH + docker),
  loopback-by-default policy. **Start here before exposing any port.**
- `docs/07-audit-log.md` — append-only audit log (mutations + denials) and
  retention policy.

## CLI (Local phase, minimal)

```bash
taxion init
taxion task create --title "…"
taxion dispatch --task <id>
taxion review --sha <coder-sha>
taxion resume --session <id>
```

Local-phase CLI is local-first and deterministic. Server / ACP / Web arrive in Server / Platform phases.
