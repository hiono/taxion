# 01 — Guardrails (behavioral invariants)

| ID | Statement | Enforced in |
|----|-----------|-------------|
| PRIV-001 | No personal name, email, user ID, home path, internal hostname, or org-specific repo name may appear in a public artifact. | docs review + CI hygiene job |
| ROLE-001 | Coder and Reviewer must use independently isolated execution contexts. | `runtime.Runtime` contract doc + future server |
| SHA-001 | Reviewer execution must be bound to the exact retained Coder output SHA. | `core.BindEvidence`, CLI `review --sha` required |
| CHK-001 | An incomplete checkpoint sequence must never report readiness. | `core.CheckReady` |
| CHK-002 | Checkpoints must complete in declaration order; out-of-order, unknown, or post-completion advances are rejected. | `core.Sequence.Advance` |
| LED-001 | Ledger (tasks/dispatches/evidence/checkpoints) must survive a save/load round-trip; sqlite backend deferred to Server phase must preserve this shape. | `core.Ledger.Save/LoadLedger` |
| DSP-001 | An active Dispatch must never be silently replaced. | `core.GuardActiveDispatch` |
| SDK-001 | SDK-specific types must not appear in the governance core. | `internal/core` imports stdlib only (CI `go list` check) |
| REP-001 | Task creation, dispatch, and evidence binding require an explicit `--repo`; the target is never inferred from cwd. | `core.CheckRepo`, CLI `task create`/`dispatch`/`review` |

These statements define the intended constraints. Current `review`
operations bind evidence only: they require a non-empty SHA but do not
verify its format, existence in Git, or reviewer execution. ROLE-001
is an adapter-isolation requirement, not a claim that the CLI/API
starts independent coder and reviewer processes.

## Failure lessons (generalized, no provenance)

- **Repository confusion**: implementation repo vs validation target must be distinguished
  by an explicit identity gate. Taxion CLI MUST require `--task` / `--sha` explicitly
  and never infer the target from cwd alone (future server: `repo identity gate` task).
- **Silent replacement**: dispatch overwrite without error caused lost evidence → DSP-001.
- **Ambiguous review input**: reviewing "latest" instead of a pinned SHA → SHA-001.
