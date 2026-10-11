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
- Adapters return errors for unknown or stopped sessions, out-of-order steps,
  and empty task IDs. They advance state and report readiness only for valid requests.
- `runtime.Identity` exposes the role, worktree, and configuration hash needed
  to bind a runtime session to one authorized Dispatch.
## Adapters

- `internal/runtime/local`: in-process reference adapter for state transitions.
  Runs a `core.Sequence` per session (`CompleteStep` enforces CHK-002 order),
  records accepted instructions as neutral `input` events, and emits
  `checkpoint`/`done` events.
- `internal/runtime/proc`: starts a configured external command, streams stdout
  and stderr as `output` events, writes instructions to stdin, and reports
  `done` or `failed` at process exit. `Resume` validates the existing session;
  process launch belongs to `Start`. Checkpoint sequencing remains separate.
- `internal/runtime/copilot`: wraps the official Go Copilot SDK. Configure one
  adapter per role and worktree; sessions use Auto and an explicit tool
  allowlist. Infinite-session compaction is enabled. The CLI appends the saved
  Dispatch snapshot to the role-specific system prompt so the authorization
  contract remains in system context across turns and compaction. Resume
  re-supplies the same system prompt, worktree and tools. The SDK permission
  handler grants Coder file writes inside the worktree and one-time use of
  allowlisted SDK tools; Reviewer receives read access inside its worktree.
  The handler rejects shell requests and file paths outside the assigned
  worktree. It governs SDK permission requests; host and external tools use
  their own security controls.
- `internal/runtime/controller`: checks the Dispatch role, assigned worktree,
  active status, and configuration hash before creating, resuming, sending,
  stopping, or recording usage for its bound session. Session IDs, hashes,
  model and token/cache usage are stored with the Dispatch in SQLite.

`taxion runtime start|send|resume|events|stop` is the explicit Copilot CLI
execution path. `start` creates a session with the saved Dispatch snapshot in
system context and sends a short execution request; follow-up instructions
come from a prompt file. The CLI validates the final structured response
against the saved Dispatch contract. An operator submits the result through
the Dispatch transition, which validates its required fields before changing
state. The ACP server uses `internal/runtime/local`; Copilot sessions start
through the explicit runtime commands.

## Event subscription lifetime

Adapters replay events recorded by the current adapter instance before
following live ones. Copilot emits normalized message, turn, failure,
cancellation, usage, context-window, compaction, and permission-decision
events; usage is persisted with the bound Dispatch. Context and compaction
events report SDK-provided token limits and compaction outcomes when available.
A finished turn leaves the session stream available for the same role's next
instruction. Stop closes the stream.
Cancelling the subscription context closes only that subscription; consumers
must still observe their context rather than waiting only for session closure.
After a process restart, the SDK resumes Copilot's conversation by session ID.
The normalized event stream starts with events observed by the current adapter instance.

The SDK-neutral boundary is justified in
[ADR-0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md).
Planned adapter evaluation belongs in [the roadmap](04-roadmap.md).
