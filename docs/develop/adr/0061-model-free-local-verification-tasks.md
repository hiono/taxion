# 61. Record Local Verification Tasks Without a Runtime

Date: 2026-10-30

## Status

Accepted

## Context

First-time users may need to record a local verification task before they have
configured a model runtime, credentials, or execution approval. Requiring those
references for a task that will not execute a model prevents this bookkeeping
and evidence workflow from starting.

This is narrower than artifact-first validation and failed-input successor
workflows. It does not execute validation commands or start an independent
model Reviewer.

## Decision

Provide an explicit `init --model-free` option that initializes a local
verification workspace without inventing runtime, credential, or execution
approval references. The option permits partial configuration only for this
setup path; normal model execution continues to require its complete
configuration and authorization.

Mark such tasks explicitly as model-free. The graph accepts this classification
only for USER-owned `VERIFICATION` tasks with at least one declared validation
command. Intake confirmation must match the exact proposed task hash and must
not add runtime configuration, execution authorization, an ApprovalBundle, or
a Dispatch.

Model-free tasks cannot start a Dispatch or Review Flow. A user may record the
task's evidence, acceptance results, reviewer attribution, and reviewed target
SHA through the existing task transition proof. Completion records those
assertions; it does not mean Taxion ran the declared commands, authenticated
the reviewer, or independently verified the evidence.

## Consequences

Users can create and complete a local verification record without configuring
a model runtime. The explicit task marker keeps this path distinct from normal
executable tasks and from USER-owned artifact tasks that may use a Reviewer
Dispatch.

The path is an evidence-recording workflow, not an automated verification
workflow. Operators must not describe a completed model-free task as proof
that Taxion executed its validation commands or performed an independent
review.

## Compatibility

The model-free field is optional and defaults to false when absent. Existing
tasks retain their current execution and approval behavior. No stored task,
approval, or result is migrated or rewritten.

## Security considerations

Model-free confirmation is not permission to execute commands or use a model.
Declared commands and recorded proof are user-provided data; they are not
executed or independently authenticated by this path. Dispatch and Review Flow
creation fail closed for model-free tasks.

## References

- Issue #217 — runtime-free local verification task lifecycle
- Issue #201 — artifact-first validation and independent review
- Issue #203 — recovery after failed artifact input validation
