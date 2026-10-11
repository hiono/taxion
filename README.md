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
以下は現在の構成を示す。到達条件の進捗は Roadmap と対応 Issue の完了条件で確認する。

```
Taxion Core (SDK-neutral)
 ├─ Server (Task Graph / Dispatch Ledger / Evidence Ledger / Policy / Checkpoint)
 ├─ CLI (human interface: init / task / dispatch / runtime / review)
 ├─ Web UI (task/dispatch/review forms + HTTP API proxy)
 ├─ ACP Server (stdio agent with dispatch/review methods)
 └─ Runtime adapters (local, process-backed, and Go Copilot SDK)
```

## Non-goals (Foundation → Local)

- The governance core is not coupled to a Copilot SDK; SDK types stay outside `internal/core`.
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

## CLI

```bash
taxion init
taxion task create --title "…" --repo <repo>
taxion task plan --repo <repo>
taxion dispatch --task <id> --repo <repo>
taxion runtime start --dispatch <id> --system-prompt-file <path>
taxion runtime send --dispatch <id> --system-prompt-file <path> --prompt-file <path>
taxion runtime resume --dispatch <id> --system-prompt-file <path>
taxion runtime events --dispatch <id> --system-prompt-file <path>
taxion runtime stop --dispatch <id> --system-prompt-file <path>
taxion review --task <id> --sha <coder-sha> --repo <repo>
```

Runtime commands require an active Task Graph Dispatch and a role-specific
system prompt file. The Task's worktree and Dispatch recipient select the
runtime role and workspace. The saved Dispatch snapshot is appended to the
role prompt as the session's durable contract, so it remains part of the system
context across turns and SDK compaction. SDK tools require an explicit
`--tools` allowlist. The SDK permission handler grants Coder file writes within
the assigned worktree and one-time use of allowlisted SDK tools; Reviewer
sessions receive read access to that worktree. The handler applies to SDK
permission requests, while host and external tools use their own security
controls. Resume verifies that the prompt, CLI, worktree, and tool configuration
match the persisted hash.

`task plan --repo <repo>` reconstructs follow-up actions from the SQLite Task
Graph and referenced Memory. It retains deferred Tasks, their sources and resume
conditions, existing decisions, and each Dispatch's issued contract and result.
Actions distinguish unresolved Human decisions, deferred-work reassessment,
evidence collection, review reconciliation, and execution candidates. Generating
a plan preserves the saved Task states and authorization. The same output is
available through `GET /api/tasks/plan?repo=<repo>` and ACP `taxion/plan` with
`{"repository":"<repo>"}`, including after a session is recreated.

For a Reviewer Dispatch, the Task's worktree must be a new absolute path.
Start it with `--review-source-worktree <existing-repository-worktree>` to
create an independent detached worktree at the saved target SHA. Finalization
closes the SDK connection and removes the clean worktree before saving review
success. Dirty, locked, or unreleased worktrees leave the review incomplete
with an explicit error.

Usage events record the selected model and available token/cache counts in
SQLite; missing SDK measurements remain unknown. Infinite-session compaction
is enabled, and its context and outcome events are reported.

The Task's `validationCommands` authorize the checks run by the controller.
At the end of a runtime turn, the controller checks the structured response,
verifies repository/worktree/SHA/scope against Git, executes those commands in
the assigned worktree, and saves the observed output and completion in SQLite.
Validation commands run as `sh -c` with the controller's host permissions;
approve their effects when authorizing the Task. The explicit
`dispatch transition --result-file` path performs the same verification and
validation, with a ten-minute default `--validation-timeout`.

The CLI is local-first. `taxion serve` provides the HTTP API, `taxion acp`
provides ACP over stdio, and `web/` provides the browser console.
