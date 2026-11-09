# ADR: Reconcile advisory execution separately from answer acceptance and ownership

## Context

The request Planner can stop after its instruction was sent but before its answer,
usage, or checkpoint was durably adopted. Replaying a possibly delivered paid
request can duplicate work and cost. A cleanup result alone does not establish a
valid answer or prove that the native session became quiescent.

## Decision

Record a private append-only attempt binding before session creation, the native
session immediately after creation, and the exact sent-prompt hash before send.
Persist a schema-validated terminal answer, output hash, terminal event and
reported usage before quota rechecks or cleanup.
Reject credential-like content in executable proposals; redact advisory text
before persistence and retain explicit redaction provenance in captured sources.

`taxion request recover` is a read-only preview followed by an exact-hash apply.
It attaches without execution tools, send, continuation or abort; checks the
saved native input, unique main message and turn, completed on-time output,
source/configuration/task/worktree/deadline bindings and matching usage; then
atomically adopts the minimal answer, checkpoint, receipt
and deduplicated usage. Drift and ambiguous or incomplete evidence fail closed.
Questions remain unanswered and proposals remain unapproved. Original deadlines,
budgets, immutable history and normal follow-on authorization gates remain intact.

The shared question and search boundary requires a metadata-bound main
`assistant.turn_end` followed by SDK `session.idle`. The former ends answer
generation; the latter establishes the SDK completion boundary after its
serialized event callbacks. A temporarily empty queue, stream closure before idle,
an abort RPC response, or `AbortData` does not establish completion. Autopilot
idle is a continuation boundary; aborted idle is cancellation. Questions validate
and persist the answer before quota rechecks and cleanup; only successful
acceptance and Stop complete the advisory operation. Absent terminal persistence
is reconciled from unchanged native evidence, not replayed execution.

Stop waits for the idle callback after Abort before disconnecting. The callback
records the event and queues it for subscribers before waking Stop. `EventsAfter`
can replay recorded events only; it cannot recover an SDK event that has not yet
reached the callback.

For a v0.8.2 version-2 request without attempt records, explicit legacy association
binds a selected native session, exact user-input hash, original human request text,
planning Task and stopped-process evidence. It records a new decision and states
that historical configuration binding is unavailable; it does not invent a
historical session/start/send receipt. Version-1 checkpoints and unbound old direct
questions are not migrated.
Task identity must be in the native Planner context, not quoted elsewhere; a
quoted request digest is not a substitute for the original request text.

Continuation and cancellation have different authority. Routine shutdown and
valid advice permit ordinary checkpoint continuation within unchanged resources.
Explicit stopped-process and uncertainty consent permits cancellation only when
native aggregate usage/shutdown or answer validity is incomplete. Known usage
floors remain monotonic, unknown usage remains unknown, and invalid advice is
never execution approval. The original checkpoint and advice references are kept.
Expired deadlines can be reconciled and cancelled, not revived.

SDK usage checkpoints and shutdown snapshots preserve model and provider/model
aggregates separately from individual receipts. A checkpoint subsumes receipts
within its watermarks and is never added to them. Provider/model and model/global
aggregates are alternative views of the same usage; known-cost floors take their
maximum rather than summing them. A non-pointer checkpoint cost of zero is not
evidence of a reported zero. Exact accounting requires explicit complete status;
only an explicit complete zero with no covered calls is accepted as zero cost.

Apply acquires the exclusive process-lifetime operation lease, reobserves native
history, and stores the exact decision, retained checkpoint, advice and deduplicated
usage atomically. A stale persisted owner is not a live lease; confirmed stopped
ownership can be reclaimed only if the actual lease is available. Exact repeated
apply is idempotent. A cancelled advisory operation does not waive future Task,
configuration, execution or publication gates.

## Alternatives considered

- Resume or resend a session with an uncertain send: rejected because delivery,
  cost and model state would be unknown.
- Treat Stop success as answer acceptance: rejected because cleanup does not
  prove a valid terminal answer.
- Reconstruct missing legacy input or usage from a digest: rejected because a
  digest does not contain the original value or prove its association.

## Consequences

The same public primitive handles request Planner and durably bound direct
`conversation ask`, `plan` and `investigate` operations. Legacy association is
request-only and requires verifiable original input and main-turn identity.
Providers without a saved-turn reader, active execution, conflicting histories
and missing identity remain blocked. The original operational incident's first receipt
adoption failure is not established by these verified structural defects.

## Security and operational impact

Preview reports saved evidence and is not execution approval. Apply is bound to
the exact preview hash and an explicit actor/source/reason/evidence decision.
The native adapter disables pending-work continuation, transcript repair, tools,
permissions and resume-event writes, and closes without aborting an execution.
Only explicit cancellation consent can accept absent native shutdown/aggregate
usage after the original process stop. This is not proof of zero cost.

## Compatibility

The explicit Clarification adapter (#283) retains questions as planning receipts
and references the canonical Planner answer by request ID, sequence and hash.
It does not create another Planner answer, usage ledger, operation owner or
snapshot mechanism. A reference is only an observation: it does not establish
cleanup, answer acceptance, authorization or Dispatch. A conflicting concurrent
answer must not roll back an already observed canonical terminal receipt.
Controller acceptance and continuation integration remain #284 work.

The workspace schema and append-only request records are retained. Upgrading
does not rewrite old checkpoints or native histories. Downgrade is not a
migration: retain an intact private backup before opening a v0.8.3 workspace
with an older binary, and never delete new receipts to make them readable.

## Status

Accepted 2026-11-07. The original incident cause remains unconfirmed.
Implementation, regression tests, full validation and independent same-target
review are separate evidence; no release or validation success is implied here.
