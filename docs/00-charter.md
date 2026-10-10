# 00 — Project Charter (sanitized, clean-room)

This charter records the founding Foundation → Local scope, not the
current feature inventory. See [README](../README.md) for entry points,
[roadmap](04-roadmap.md) for tracked work, and the reference documents
linked from README for current behavior.

## Origin

Taxion is a clean-room project.
**No code, git history, issues, names, paths, or credentials are ported**
from any private predecessor.
Only generalized requirements, guardrails, procedures, and failure lessons — after
privacy/provenance sanitization — may serve as input.

## Public scope (Foundation → Local)

- SDK-neutral governance core (`internal/core`): Task / Dispatch / Evidence / Checkpoint.
- Runtime contract (`internal/runtime`): `Start / Resume / Stop / Events` only.
- Local-first CLI: `init / task create / dispatch / review / resume`.
- Conformance tests for DSP-001 / SHA-001 / CHK-001 / ROLE-001.

## Non-goals (Foundation → Local)

- Server / ACP / Web UI (Server / Platform phases).
- Copilot SDK adoption (Adapter phase evaluation task, not decision).
- Multi-provider orchestration, fleet, workflows.
- Any private-repo-specific behavior (provider-specific assumptions, internal hosts).

## Roadmap (phase names — no DayN)

- **Foundation**: repo, CI, charter, guardrails. ✅ (this commit)
- **Core**: Go governance core + conformance tests.
- **Local**: local-first CLI UX + `--repo` identity gate.
- **Adapter**: runtime adapter feasibility evaluation as plugin (first candidate: Copilot SDK for Go).
- **Server**: persistence and API design (Task Graph / Ledgers as source of truth).
- **Platform**: ACP server + Web UI for multi-client use.
- **Future**: only if contracts stabilize — consider Rust core split
  (`taxion-server` / `taxion-acp` in Rust, `taxion-runtime-copilot` in Go, `taxion-web` in TS).

## Traceability

Every public requirement carries an ID (PRIV-xxx / ROLE-xxx / SHA-xxx / CHK-xxx / DSP-xxx / SDK-xxx).
See `01-guardrails.md`.
