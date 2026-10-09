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

- Core (`internal/core`) depends on this interface only — never on SDK structs.
- Each `Runtime` implementation MUST isolate Coder vs Reviewer sessions (ROLE-001).
- Events are neutral `{Type, Payload}` — no SDK event types leak into core.
- First candidate adapter: **Copilot SDK for Go** — as a *plugin* under
  `internal/runtime/copilot/` (evaluation task; not yet vendored).
  - Evaluation criteria: session start/resume fidelity, event mapping cost,
    auth story (no secret in repo), version pinning story.
  - If evaluation fails, core remains untouched.

## Anti-pattern (must avoid)

```
Taxion → Go → Copilot SDK types everywhere → Copilot SDK Wrapper
```

This collapses Taxion into a wrapper. The interface above is the firewall.
