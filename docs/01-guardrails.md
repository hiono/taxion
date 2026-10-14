# 01 — Guardrails (behavioral invariants)

| ID | Statement | Enforced in |
|----|-----------|-------------|
| PRIV-001 | No personal name, email, user ID, home path, internal hostname, or org-specific repo name may appear in a public artifact. | docs review + CI hygiene job |
| ROLE-001 | Coder and Reviewer must use independently isolated execution contexts. | `runtime.Identity`, Controller-managed independent Reviewer worktree and Git proof |
| SHA-001 | Reviewer execution must be bound to the exact retained Coder output commit, or the exact retained tree and unchanged baseline in explicitly approved snapshot mode. | `core.BindEvidence` for metadata; `gitproof.Verify`, snapshot/ref verification and Controller handoff for execution |
| CHK-001 | An incomplete checkpoint sequence must never report readiness. | `core.CheckReady` |
| CHK-002 | Checkpoints must complete in declaration order; out-of-order, unknown, or post-completion advances are rejected. | `core.Sequence.Advance` |
| LED-001 | Ledger (tasks/dispatches/evidence/checkpoints) must survive a save/load round-trip; sqlite backend deferred to Server phase must preserve this shape. | `core.Ledger.Save/LoadLedger` |
| DSP-001 | An active Dispatch must never be silently replaced. | `core.GuardActiveDispatch` |
| MEM-001 | Every approved structured Memory check in the selected workspace must match before dispatch; unreadable or malformed policy must fail. | `memory.CheckDispatch`, CLI/HTTP/ACP dispatch; Web uses the HTTP verdict |
| SDK-001 | SDK-specific types must not appear in the governance core. | `internal/core` imports stdlib only (CI `go list` check) |
| REP-001 | Task creation, dispatch, and evidence binding require an explicit `--repo`; the target is never inferred from cwd. | `core.CheckRepo`, CLI `task create`/`dispatch`/`review` |
| SPEC-001 | 仕様の出典・版・用途別承認を同じ snapshot として保持し、送信・公開時に許可とローカル原本を照合する。 | `core.CheckSpecificationUse`、controller の実行・質問・公開、役割間の snapshot 検査 |
| MEM-002 | 完全な移行は両 scope の本文・出典・版・承認・失効・履歴・設定を保持し、確認した hash のファイルを新しいローカル保存先に設置する。 | `memory.Snapshot`、`InspectSnapshot`、`RestoreSnapshot` |
| FILE-001 | Task 承認済み fileAccess と Dispatch 開始時点の追跡対象を permission・Git 変更検査に使い、全レベルで worktree 外と `.git` を拒否する。Reviewer は read-only とする。 | `FileAccessPolicy`、SDK permission、`gitproof.CheckChangedPaths` と fileaccess 回帰 |
| STOP-001 | 保存 session の停止専用接続は新規 session・指示送信・通常再開を拒否し、pending work と全 permission を無効にする。取消・保存の失敗を停止成功として扱わない。 | `copilot.NewForStop`、`ResumeForStop`、Controller.Stop、停止専用 SDK protocol 回帰 |
| LED-002 | 同じ Dispatch の suspension と cancellation の結果 receipt を追記保持し、最新結果・履歴の順序・内容 hash・利用量を照合する。 | `Controller.transition`、`validateResultReceipt`、suspension→cancellation 回帰 |
| BUD-001 | 停止済み Flow の上限変更で再開せず、別 worktree・新しい Task と予算の承認で継続する。既知の Reviewer 設定承認漏れは CLI の有料 turn 前に検出する。 | successor Git proof、intake confirmation、`CheckReviewFlowConfiguration` と継続・設定回帰 |

These statements define the intended constraints. Current `review`
operations bind evidence only: they require a non-empty SHA but do not
verify its format, existence in Git, or reviewer execution. The Controller's
runtime review flow separately creates an independent Reviewer worktree and
checks commit/tree identity. The metadata-only `review` command is not proof
that this execution took place. Decisions and limits for FILE-001, SHA-001,
STOP-001, LED-002 and BUD-001 are recorded in ADRs 0026–0028.

## Failure lessons (generalized, no provenance)

- **Repository confusion**: implementation repo vs validation target must be distinguished
  by an explicit identity gate. Taxion CLI MUST require `--task` / `--sha` explicitly
  and never infer the target from cwd alone (future server: `repo identity gate` task).
- **Silent replacement**: dispatch overwrite without error caused lost evidence → DSP-001.
- **Ambiguous review input**: reviewing "latest" instead of a pinned SHA → SHA-001.
