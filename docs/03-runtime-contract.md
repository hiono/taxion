# 03 — Runtime Contract (SDK-neutral)

## Interface

```go
type Runtime interface {
    Start(ctx context.Context, taskID string) (sessionID string, err error)
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
## Adapters

- `internal/runtime/local`: in-process reference adapter. Runs a `core.Sequence`
  per session (`CompleteStep` enforces CHK-002 order), emits neutral
  `checkpoint`/`done` events, one instance per role (ROLE-001 isolation).
- `internal/runtime/proc`: starts a configured external command, streams stdout
  and stderr as `output` events, and reports `done` or `failed` at process exit.
  `Resume` validates an existing session; it does not restart a process.
  Checkpoint sequencing remains separate from this adapter.

The current ACP server uses `internal/runtime/local`, not `proc`. Its
dispatch/review methods record ledger operations; they do not run an LLM
or an independent reviewer. The CLI `resume` command is a local stub,
separate from the Runtime interface.

## Event subscription lifetime

Both adapters replay recorded events before following live ones. Stop or
completion closes the channel. Cancelling the subscription context removes
the live watcher but does not close its channel; consumers must also observe
their context rather than waiting only for channel closure.

The SDK-neutral boundary is justified in
[ADR-0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md).
Planned adapter evaluation belongs in [the roadmap](04-roadmap.md).
