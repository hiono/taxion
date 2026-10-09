# 00 — Project Charter (sanitized, clean-room)

## Origin

Taxion is a clean-room project.
**No code, git history, issues, names, paths, or credentials are ported**
from any private predecessor.
Only generalized requirements, guardrails, procedures, and failure lessons — after
privacy/provenance sanitization — may serve as input.

## Public scope (Day2)

- SDK-neutral governance core (`internal/core`): Task / Dispatch / Evidence / Checkpoint.
- Runtime contract (`internal/runtime`): `Start / Resume / Stop / Events` only.
- Local-first CLI: `init / task create / dispatch / review / resume`.
- Conformance tests for DSP-001 / SHA-001 / CHK-001 / ROLE-001.

## Non-goals (Day2)

- Server / ACP / Web UI (Day3+).
- Copilot SDK adoption (evaluation task, not decision).
- Multi-provider orchestration, fleet, workflows.
- Any private-repo-specific behavior (GitLab-only assumptions, internal hosts).

## Roadmap

- **Day2**: Go core + CLI stub + SDK feasibility evaluation (Go has SDK → fastest validation).
- **Day3**: Server-ization (Task Graph / Ledgers as source of truth), ACP server, Web UI.
- **Day4+**: only if contracts stabilize — consider Rust core split
  (`taxion-server` / `taxion-acp` in Rust, `taxion-runtime-copilot` in Go, `taxion-web` in TS).

## Traceability

Every public requirement carries an ID (PRIV-xxx / ROLE-xxx / SHA-xxx / CHK-xxx / DSP-xxx / SDK-xxx).
See `01-guardrails.md`.
