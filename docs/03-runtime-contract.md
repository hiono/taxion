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
- Selected Dispatches require `runtime.ConfigurationIdentity`: the saved tuple,
  effective runtime authentication and repository API access checks. The controller
  calls both checks before start/resume/send and rechecks approval afterward.
  The GitHub.com adapter selects `gh:active` or `env:NAME`, confirms the expected
  repository account and exact target, and requires read permission (plus write
  permission for Coder). Unknown access and unsupported providers stop execution.
  Successful checks are appended to `configuration.accessChecks` with the operation,
  runtime hash, observation interval, role permission and unverified contract.
  Recording rechecks approval transactionally and does not claim session creation.
  The first observation fixes the checked runtime identity even before a session
  exists. Earlier checks are not reused after restart; absent legacy observations
  imply no confirmation. These checks are not AI contract verification or Git push
  authentication. After session creation/attachment, and before each model send,
  selected Dispatches also call the adapter's SDK-neutral `CheckServiceAccess`.
  The Copilot adapter queries `session.quota.get`, requires successfully acquired
  account snapshots with explicit service access and capacity, and rejects missing,
  denied, exhausted or unknown observations without consulting numeric balances.
  A prior turn's admission observation is not an account snapshot. Approval is
  rechecked after this query; a rejected newly created session is stopped without
  binding it. These observations are not added to the preceding access receipts
  and do not identify the payer or guarantee future admission. Safe stop bypasses
  all three external checks. Configuration-less legacy controllers are unchanged.
  The same sourced Human approval must cover the selected account and contract
  reference in the current Task and issued instruction. Its reason records the
  approved use; checking that record does not verify the legal truth of its text.
- Saved-record side questions use a separate, tool-free Orchestrator runtime.
  They require sourced Task configuration approval, fresh authentication,
  repository read access, provider service checks and explicit time/token limits.
  Answers cite existing fields in the supplied observation and separate recorded
  facts, assumptions and unknowns. The observation is never a live-state proof.
  No Dispatch is bound, no workspace history or approval is changed, and returned
  session/usage information belongs to this question, not the main flow.
  Unknown usage and invalid citations stop the query. Safe cleanup uses its own
  bounded context after cancellation; errors are not suppressed.
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

This probe does not resolve credential references, select the repository actor
or establish contract eligibility. Execution-time checks use the same
authentication method separately; local protocol fixtures do not demonstrate
signed-in acceptance.

`config bind --dispatch` records a complete approved configuration selection,
decision reference, field origins and recording time in the Dispatch. The
controller uses one workspace transaction to check current Task and issued
instruction approvals before saving it. The core stores SDK-neutral JSON and
validates its identity, origins and membership in the saved approval; the
configuration layer validates the complete tuple schema.

Once selected, Dispatch configuration checks and the explicit authentication
probe require that tuple. Repeating identical values keeps the initial origins
and time. Binding different values or adding a first selection to an existing
session requires a newly authorized Dispatch. Missing selections on legacy
records remain readable. The selection record alone is not evidence of
effective authentication or contract eligibility.

The Copilot adapter's optional selected tuple is validated, canonicalized,
owned and included in its configuration hash. `runtime.ConfigurationIdentity`
exposes only neutral JSON and an account-authentication capability. Unselected
legacy hashes remain unchanged. The controller rejects a selected Dispatch
with a missing or different runtime tuple; selected start/resume/send check
current Task and saved instruction approval and the effective account before
SDK work. Approval is rechecked after authentication, in the new session
binding transaction and before sending an instruction. Managed Git checks the
tuple and approval inside its own publication transaction.

The runtime CLI resolves fixed configuration for start/resume/send/events/commit.
Fresh Dispatches save their selection automatically; legacy sessions without a
selection require a new authorized Dispatch. Shared runtime/workflow factories
construct the adapter from the saved selection. Every workflow Task must be
explicitly named in its issued configuration approval. The optional
`additionalTaskIds` on `taxion.execution-config.v1` lists exact additional
Task IDs; it is not a pattern or implicit delegation to all children.
For flow ID `flow` and `maxRepairs: 1`, reviews are `flow-review-0` and
`flow-review-1`; repair Coders retain the original Task ID. An omitted list
preserves single-Task approval. Empty, whitespace-bearing, pattern, duplicate
and primary IDs are rejected.

Generated review Tasks inherit the sourced approval record unchanged.
Configuration/core checks accept only the primary or explicitly listed Task
with a complete allowed tuple. Unlisted reviews fail closed; the flow's
independent authorization and ceilings remain required. The list must be
recorded before issuance and cannot widen an existing Dispatch. Local checks
exercise selected Coder→review→repair→re-review through controller and actual
Git, not a signed-in SDK model.

Safe stop reconstructs the saved session hash without current settings or an
authentication probe. It still verifies role, worktree and session identity,
then attempts SDK attachment and cancellation. Provider attachment can fail,
and those errors are returned. Unconfigured local/proc usage retains existing
behavior. Credential resolution, repository actor selection, contract
eligibility and signed-in acceptance remain separate requirements.

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
