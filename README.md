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

## Non-goals (Day2)

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

## Docs

- `docs/00-charter.md` — scope, non-goals, Day2→Day4 roadmap
- `docs/01-guardrails.md` — behavioral invariants with IDs
- `docs/02-privacy-provenance.md` — sanitization policy
- `docs/03-runtime-contract.md` — SDK-neutral Runtime interface
- `docs/04-roadmap.md` — task breakdown

## CLI (Day2 minimal)

```bash
taxion init
taxion task create --title "…"
taxion dispatch --task <id>
taxion review --sha <coder-sha>
taxion resume --session <id>
```

Day2 CLI is local-first and deterministic. Server / ACP / Web arrive in Day3+.
