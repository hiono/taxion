# 03 — Runtime Contract (SDK-neutral)

## Interface

```go
type Runtime interface {
    Start(ctx context.Context, taskID string) (sessionID string, err error)
    Send(ctx context.Context, sessionID, prompt string) error
    Resume(ctx context.Context, sessionID string) error
    Stop(ctx context.Context, sessionID string) error
    Events(ctx context.Context, sessionID string) (<-chan Event, error)
}
```

## Rules

- Core (`internal/core`) uses standard-library types, never SDK structs.
  Adapters implement the Runtime interface outside core.
- Each `Runtime` implementation MUST isolate Coder vs Reviewer sessions (ROLE-001).
- Events are neutral `{Type, Payload}` — no SDK event types leak into core.
- Adapters MUST be fail-closed: unknown/stopped sessions, out-of-order steps,
  and empty task IDs return errors and never advance state or report readiness.
- `runtime.Identity` exposes the role, worktree, and configuration hash needed
  to bind a runtime session to one authorized Dispatch.
## Adapters

- `internal/runtime/local`: in-process reference adapter. Runs a `core.Sequence`
  per session (`CompleteStep` enforces CHK-002 order), records accepted
  instructions as neutral `input` events, emits `checkpoint`/`done` events,
  and does not execute a worker.
- `internal/runtime/proc`: starts a configured external command, streams stdout
  and stderr as `output` events, writes instructions to stdin, and reports
  `done` or `failed` at process exit. `Resume` validates an existing session;
  it does not restart a process. Checkpoint sequencing remains separate.
- `internal/runtime/copilot`: wraps the official Go Copilot SDK. Configure one
  adapter per role and worktree; sessions use Auto and an explicit tool
  allowlist. Resume re-supplies the same system prompt, worktree and tools.
  Reviewer permission requests allow reads only inside its worktree and reject
  writes, shell, and other permission categories. Permission checks are not an
  OS sandbox.
- `internal/runtime/controller`: checks the Dispatch role, assigned worktree,
  active status, and configuration hash before creating, resuming, sending,
  stopping, or recording usage for its bound session. Session IDs, hashes,
  model and token/cache usage are stored with the Dispatch in SQLite.

`taxion runtime start|send|resume|events|stop` is the explicit Copilot CLI
execution path. `start` sends the saved Dispatch instruction; follow-up
instructions come from a prompt file. A model turn ending is not evidence that
the Dispatch succeeded; its result must pass the existing Dispatch transition
contract. The ACP server still uses `internal/runtime/local` and does not
automatically start a Copilot session.

## Event subscription lifetime

Adapters replay events recorded by the current adapter instance before
following live ones. Copilot emits normalized message, turn, failure,
cancellation, usage, context-window, compaction, and permission-decision
events; usage is persisted with the bound Dispatch. A finished turn does not
close the session stream because the same role session accepts another
instruction. Stop closes the stream.
Cancelling the subscription context closes only that subscription; consumers
must still observe their context rather than waiting only for session closure.
After a process restart, Copilot's conversation history is resumed by SDK
session ID, but this adapter does not replay the prior normalized event stream.

The SDK-neutral boundary is justified in
[ADR-0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md).
Planned adapter evaluation belongs in [the roadmap](04-roadmap.md).
