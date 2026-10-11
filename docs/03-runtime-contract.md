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
  Managed staging and commits additionally require an active Task, the invoking session
  for tool calls, and a running review flow within its saved deadline and
  observed known-usage ceiling when the Dispatch belongs to a flow.

`taxion runtime start|send|resume|events|stop` is the explicit Copilot CLI
execution path. `start` creates a session with the saved Dispatch snapshot in
system context and sends a short execution request; follow-up instructions
come from a prompt file. The CLI validates the final structured response
against the saved Dispatch contract. An operator submits the result through
the Dispatch transition, which validates its required fields before changing
state. The ACP server uses `internal/runtime/local`; Copilot sessions start
through the explicit runtime commands.

`runtime auth` is an explicit preflight, not an execution action. Fixed
configuration and current/saved Dispatch approvals are checked before the
Copilot adapter calls `auth.getStatus`. `CheckAuthentication` verifies an
authenticated account matching the requested account and an explicit
`github.com` host. It creates no SDK session, does not cache the result, and
does not alter the adapter's configuration hash. Provider status messages
and raw RPC errors are withheld from its returned errors; cancellation
remains identifiable.

This does not resolve credential references, select the repository actor,
establish contract eligibility or bind the configuration durably. Runtime
start/resume/send and safe stop keep their existing behavior. Those execution
and reauthorization gates remain in #69; local protocol fixtures do not
demonstrate signed-in acceptance.

`runtime commit` is a host operation that scans a locked index snapshot and
message before publishing its exact tree. It does not stage files or complete
the Task. `runtime start --managed-commit` and `workflow run --managed-commit` explicitly
enable `taxion_commit` for Coder sessions. The neutral `runtime.ManagedCommit`
binding carries a Dispatch ID, scanner reference and host callback; SDK tool
types remain inside the Copilot adapter.

The callback receives invocation cancellation, session ID and message only.
The registered tool cannot choose its repository, scope or scanner. It rejects
unknown fields, missing messages, unknown/idle/stopped sessions and attributed
sub-agent invocations. Host interruption and flow deadlines also cancel Git
work. Permission checks remain enabled. Controller errors are failed SDK tool
calls, including scanner failures and explicit partial-publication errors.
Successful output contains the SHA and reminds the agent that review and
acceptance are still required.

Creation and resume register the same tool. Dispatch, absolute scanner path
and operation version are part of the configuration hash when enabled.
Without the option, the previous tool surface and configuration hash are
preserved. Existing sessions cannot gain or lose the tool on resume without a
binding mismatch. Shared runtime/workflow construction uses the same callback
and does not register commit tools for Reviewers.

Commit uses already-staged content. The additional `--managed-stage` option
requires `--managed-commit` and registers `taxion_stage` on creation and resume.
Its separate operation version preserves commit-only session identities.
The neutral binding's staging callback receives session ID and an explicit
paths array; repository, scope and scanner still belong to the host.

Staging shares the controller's authorization transaction, invocation checks
and flow ceilings. It selects canonical relative files or tracked deletions,
not recursive directories, force adds, Git metadata or parent symlinks outside
the worktree. Literal path selection prevents wildcard/pathspec expansion.
Only a private index is changed before full-tree scope, path and content checks.
The checked copy is flushed and atomically published as the real index, while
HEAD and working files remain unchanged. Failed checks do not publish it.
Commit scans again. Git clean filters apply and local unreachable blobs can
remain after rejected staging; trusted repository configuration and hostile
host-process races are not sandboxed by these operations.

Local checks invoke registered SDK stage and commit handlers through controller
and real Git, but do not demonstrate a signed-in model's tool invocation. Live
stage→commit→independent-review acceptance remains separately tracked in #54/#72.

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
