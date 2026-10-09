# 01 — Guardrails (behavioral invariants)

| ID | Statement | Enforced in |
|----|-----------|-------------|
| PRIV-001 | No personal name, email, user ID, home path, internal hostname, or org-specific repo name may appear in a public artifact. | docs review + CI hygiene job |
| ROLE-001 | Coder and Reviewer must use independently isolated execution contexts. | `runtime.Runtime` contract doc + future server |
| SHA-001 | Reviewer execution must be bound to the exact retained Coder output SHA. | `core.BindEvidence`, CLI `review --sha` required |
| CHK-001 | An incomplete checkpoint sequence must never report readiness. | `core.CheckReady` |
| DSP-001 | An active Dispatch must never be silently replaced. | `core.GuardActiveDispatch` |
| SDK-001 | SDK-specific types must not appear in the governance core. | `internal/core` imports stdlib only (CI `go list` check) |

## Failure lessons (generalized, no provenance)

- **Repository confusion**: implementation repo vs validation target must be distinguished
  by an explicit identity gate. Taxion CLI MUST require `--task` / `--sha` explicitly
  and never infer the target from cwd alone (future server: `repo identity gate` task).
- **Silent replacement**: dispatch overwrite without error caused lost evidence → DSP-001.
- **Ambiguous review input**: reviewing "latest" instead of a pinned SHA → SHA-001.
