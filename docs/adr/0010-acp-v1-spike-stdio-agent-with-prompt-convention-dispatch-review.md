# 10. ACP v1 spike: stdio agent with prompt-convention dispatch/review

Date: 2026-10-09

## Status

Accepted

## Context

Platform phase needs a path for agent-client-class tools (editors) to reach
Taxion. ACP v1 (agentclientprotocol.com, JSON-RPC 2.0 over newline-delimited
stdio) is the open standard: initialize → session/new → session/prompt →
session/cancel, with session/update notifications and end_turn/cancelled
stop reasons.

## Decision

Spike `internal/acp`: Taxion acts as the agent. `session/new` creates a task
(cwd = explicit repo identity, REP-001) and starts a coder-adapter session;
`session/prompt` with plain text dispatches (DSP-001), with `review <sha>`
binds evidence (SHA-001); `session/cancel` stops fail-closed. No core change;
stdlib only; no LLM wired — replies are governance receipts plus plan updates.

## Consequences

- Editors speaking ACP v1 can drive dispatch/review through `taxion acp`.
- Prompt-convention routing (`review <sha>`) is spike-grade; a real turn
  engine or explicit methods replace it before any release.
- v2 is draft — stay on v1 until v2 stabilizes, then re-evaluate.

## M15 implementation update

The ledger-only spike behavior above is superseded for runtime execution.
`session/new` now requires a fresh Dispatch ID issued by the CLI; ACP cannot
create Tasks or Dispatches. `session/prompt` runs one turn through the shared
runtime controller, and `session/cancel` requests the controller's safe stop.
The saved Dispatch contract, role-specific system prompt, tool policy,
configuration, credit ceiling and timeout constrain execution. ACP session
IDs remain process-local. A reconnect names the saved running Dispatch and
observes its existing turn without sending another prompt; other recovery uses
the CLI runtime commands.
