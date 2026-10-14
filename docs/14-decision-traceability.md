# Decision and milestone traceability

This document connects recorded decisions, GitHub milestones and issues, and
the current reference documents. An accepted ADR records a decision; it does
not prove that the implementation or its acceptance tests are complete.
Current behavior belongs in the linked reference documents, while issues and
execution records hold implementation evidence.

## Milestone numbering

GitHub milestone numbers and project phase labels are different identifiers.
The first seven GitHub milestones use names such as Foundation and Platform.
The first numbered project phase in the roadmap is M2 at GitHub milestone #8;
M10 through M15 are GitHub milestones #16 through #21. The current roadmap and
milestone names do not define an M1 label. The tables below retain both names
instead of inventing a one-to-one numbering.

No source record confirms that Foundation or another early milestone was M1.
The M1-to-GitHub-milestone correspondence is therefore unverified.

## Milestone, decision, and current-document map

| GitHub milestone | Project phase | Main decision records | Issues | Current reference | GitHub state |
| --- | --- | --- | --- | --- | --- |
| [#1 Foundation 基盤整備](https://github.com/hiono/taxion/milestone/1) | Foundation | ADR-0001, 0002, 0003 | #1 | [00-charter.md](00-charter.md), [01-guardrails.md](01-guardrails.md) | closed |
| [#2 Core コア実装](https://github.com/hiono/taxion/milestone/2) | Core | ADR-0003, 0004, 0005 | #2 | [01-guardrails.md](01-guardrails.md), [03-runtime-contract.md](03-runtime-contract.md) | closed |
| [#3 Local CLI 操作](https://github.com/hiono/taxion/milestone/3) | Local | No separate architecture decision | #3 | [cli-reference.md](cli-reference.md) | closed |
| [#4 Adapter 実行環境の評価](https://github.com/hiono/taxion/milestone/4) | Adapter | ADR-0003 | #4 | [03-runtime-contract.md](03-runtime-contract.md) | closed |
| [#5 Server 永続化と API 設計](https://github.com/hiono/taxion/milestone/5) | Server | ADR-0009 | #5 | [05-ledger-schema.md](05-ledger-schema.md) | closed |
| [#6 Platform ACP・Web](https://github.com/hiono/taxion/milestone/6) | ACP and Web | ADR-0010, 0011 | #6, #7 | [03-runtime-contract.md](03-runtime-contract.md), [web/README.md](../web/README.md) | closed |
| [#7 Future 将来検討](https://github.com/hiono/taxion/milestone/7) | Future | ADR-0005, 0012, 0018 | #8, #21, #26 | [04-roadmap.md](04-roadmap.md), [06-remote-access.md](06-remote-access.md) | closed |
| [#8 M2 HTTP 読み取り API](https://github.com/hiono/taxion/milestone/8) | M2 | ADR-0012 | #9–#11 | [05-ledger-schema.md](05-ledger-schema.md), [06-remote-access.md](06-remote-access.md) | closed |
| [#9 M3 プロセス実行](https://github.com/hiono/taxion/milestone/9) | M3 | ADR-0010, 0013 | #12–#14 | [03-runtime-contract.md](03-runtime-contract.md) | closed |
| [#10 M4 ブラウザ操作画面](https://github.com/hiono/taxion/milestone/10) | M4 | ADR-0011 amendment, 0014 | #15–#20 | [05-ledger-schema.md](05-ledger-schema.md), [06-remote-access.md](06-remote-access.md), [web/README.md](../web/README.md) | closed |
| [#11 M5 公開準備](https://github.com/hiono/taxion/milestone/11) | M5 | ADR-0015, 0016 | #22–#25 | [06-remote-access.md](06-remote-access.md), [07-audit-log.md](07-audit-log.md), [SECURITY.md](../SECURITY.md) | closed |
| [#12 M6 Memory](https://github.com/hiono/taxion/milestone/12) | M6 | ADR-0017, 0019 | #27–#31 | [08-memory-model.md](08-memory-model.md) | closed |
| [#13 M7 Self-Protection](https://github.com/hiono/taxion/milestone/13) | M7 | ADR-0017; sanitizer and export were deferred beyond M6 | #32–#34, #38–#40 | [02-privacy-provenance.md](02-privacy-provenance.md), [08-memory-model.md](08-memory-model.md) | closed |
| [#14 M8 Server Hardening](https://github.com/hiono/taxion/milestone/14) | M8 | ADR-0016 | #35–#37, #41–#43 | [06-remote-access.md](06-remote-access.md), [07-audit-log.md](07-audit-log.md), [SECURITY.md](../SECURITY.md) | closed |
| [#15 M9 文書整備](https://github.com/hiono/taxion/milestone/15) | M9 | ADR-0006, 0007, 0008 | #44, #45, #68 | [README.md](../README.md), [CONTRIBUTING.md](../CONTRIBUTING.md), [04-roadmap.md](04-roadmap.md) | closed |
| [#16 M10 階層化した Memory・Task Graph・実行契約](https://github.com/hiono/taxion/milestone/16) | M10 | ADR-0020, 0021, 0022 | #46–#48, #66 | [08-memory-model.md](08-memory-model.md), [10-task-graph.md](10-task-graph.md) | closed |
| [#17 M11 Copilot SDK と役割別セッション](https://github.com/hiono/taxion/milestone/17) | M11 | ADR-0020 | #49–#51 | [03-runtime-contract.md](03-runtime-contract.md), [09-orchestration-requirements.md](09-orchestration-requirements.md) | closed |
| [#18 M12 Memory を用いた自動実装・独立レビュー](https://github.com/hiono/taxion/milestone/18) | M12 | ADR-0020, 0023, 0024 | #52–#54, #67, #69, #72–#74 | [02-privacy-provenance.md](02-privacy-provenance.md), [09-orchestration-requirements.md](09-orchestration-requirements.md), [10-task-graph.md](10-task-graph.md), [configuration.md](configuration.md) | closed |
| [#19 M13 実行の復旧と永続的な作業記録](https://github.com/hiono/taxion/milestone/19) | M13 | ADR-0021–0024 | #55–#57, #70 | [11-execution-records.md](11-execution-records.md), [12-forge-capabilities.md](12-forge-capabilities.md) | closed |
| [#20 M14 仕様書検索・Memory 移行・tools 連携](https://github.com/hiono/taxion/milestone/20) | M14 | ADR-0020, 0025 | #58–#61 | [08-memory-model.md](08-memory-model.md), [13-specification-input.md](13-specification-input.md) | closed |
| [#21 M15 CLI・ACP 利用受入れ](https://github.com/hiono/taxion/milestone/21) | M15 | ADR-0010, 0020–0025 | [#62](https://github.com/hiono/taxion/issues/62) closed; [#79](https://github.com/hiono/taxion/issues/79) tracks the remaining end-to-end acceptance | [03-runtime-contract.md](03-runtime-contract.md), [09-orchestration-requirements.md](09-orchestration-requirements.md), [10-task-graph.md](10-task-graph.md), [13-specification-input.md](13-specification-input.md) | open |
| [#22 M16 Planner と Rust の後続検討](https://github.com/hiono/taxion/milestone/22) | M16 | ADR-0020 | #64, #65 | [09-orchestration-requirements.md](09-orchestration-requirements.md) | open |
| [#23 Release 0.1.0 — CLI first-use and documentation](https://github.com/hiono/taxion/milestone/23) | Release | ADR-0026–0028 record decisions needed after first-use and low-budget recovery findings | #78 | [README.md](../README.md), [user-guide.md](user-guide.md), [configuration.md](configuration.md), [cli-reference.md](cli-reference.md), [CONTRIBUTING.md](../CONTRIBUTING.md) | open |

Milestone state is not a substitute for checking issue evidence. In
particular, [#62](https://github.com/hiono/taxion/issues/62) records the
narrower M15 ACP execution acceptance, while docs/09 defines a broader
end-to-end condition. [#79](https://github.com/hiono/taxion/issues/79) tracks
that remaining condition; [#63](https://github.com/hiono/taxion/issues/63) is
an operational follow-up and does not replace it. The M15 milestone description
excludes additional forge support ([#75](https://github.com/hiono/taxion/issues/75)
and [#76](https://github.com/hiono/taxion/issues/76)) from its completion
criteria, consistent with the scope in [04-roadmap.md](04-roadmap.md).

Milestone names and states in the table were read from the
[GitHub milestones API](https://api.github.com/repos/hiono/taxion/milestones?state=all&per_page=100)
at 2026-10-14 02:28:28 UTC. Issue assignments and states were cross-checked
against the [GitHub issues API](https://api.github.com/repos/hiono/taxion/issues?state=all&per_page=100)
at the same time, excluding pull requests and issues without a milestone.
That snapshot showed aggregate `open_issues` counts of 1, 1, 2, and 3 for
closed milestones #7 and #12–#14, respectively, while their milestone-assigned
issue lists contained only closed issues. The API aggregates and issue list
are recorded separately; neither aggregate counts nor milestone state alone
prove completion. These GitHub values can change after the observation time.

## ADR status and current owner

All ADRs 0001–0028 record **Accepted** decisions. Acceptance means the choice
was made, not that every consequence has been implemented. Later amendments
and implementation clarifications are listed separately so the original
decision is not mistaken for current behavior.

| ADR | Decision | Current owner or applicability |
| --- | --- | --- |
| [0001](adr/0001-record-architecture-decisions.md) | Record architecture decisions | `docs/adr/` |
| [0002](adr/0002-develop-clean-room-with-no-ported-code-or-history.md) | Develop from a clean room | `00-charter.md`, `02-privacy-provenance.md` |
| [0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md) | Keep the governance core SDK-neutral behind Runtime | `03-runtime-contract.md` |
| [0004](adr/0004-use-phase-names-instead-of-dayn-milestones.md) | Use phase names rather than day-numbered milestones | `04-roadmap.md` |
| [0005](adr/0005-stay-on-go-until-contracts-stabilize-defer-language-split.md) | Keep Go until contracts stabilize; defer a language split | `03-runtime-contract.md`, `CONTRIBUTING.md` |
| [0006](adr/0006-never-mention-private-predecessor-names-in-public-artifacts.md) | Exclude private predecessor names from public artifacts | `02-privacy-provenance.md` |
| [0007](adr/0007-track-history-in-issues-prs-milestones-and-decisions-in-adrs.md) | Separate decision records from implementation history | `04-roadmap.md`, `11-execution-records.md` |
| [0008](adr/0008-use-nygard-format-for-adrs.md) | Use the Nygard ADR format | `docs/adr/` |
| [0009](adr/0009-sqlite-backend-is-pure-go-modernc-preserving-ledger-shape.md) | Use pure-Go SQLite while preserving the Ledger shape | `05-ledger-schema.md` |
| [0010](adr/0010-acp-v1-spike-stdio-agent-with-prompt-convention-dispatch-review.md) | Start with an ACP v1 stdio adapter | `03-runtime-contract.md`, `cli-reference.md`; M15 implementation update is recorded in the ADR |
| [0011](adr/0011-web-slice-stays-read-only-ts-with-zero-dependencies.md) | Keep the initial Web slice read-only and dependency-free | Amended in M4; current proxy behavior is in `web/README.md` and `06-remote-access.md` |
| [0012](adr/0012-defer-language-split-and-full-protocol-conformity-until-core-contracts-freeze.md) | Defer language split and full protocol conformity until contracts freeze | `04-roadmap.md`, `05-ledger-schema.md`; reevaluation results are in ADR-0013 and ADR-0014 |
| [0013](adr/0013-reevaluate-language-split-with-real-execution-behind-acp.md) | Reevaluate the language split at M3 and defer it again; the ADR's implementation clarification corrects the original ACP execution premise | The current M15 execution path is recorded in the ADR and `03-runtime-contract.md` |
| [0014](adr/0014-record-split-revisit-verdict-with-a-write-capable-web-ui.md) | Record the split decision after the write-capable Web UI | `04-roadmap.md`, `06-remote-access.md` |
| [0015](adr/0015-token-auth-and-audit-complete-the-pre-public-security-model.md) | Require token authentication and audit controls before public exposure | `06-remote-access.md`, `07-audit-log.md`, `SECURITY.md` |
| [0016](adr/0016-serve-modes-loopback-remote-with-token-opt-in-idp-delegation-deferred.md) | Separate loopback and remote serving modes; defer IdP delegation | `06-remote-access.md`, `07-audit-log.md` |
| [0017](adr/0017-memory-layers-ranges-and-promotion-path.md) | Define Memory layers, scopes, and promotion | `08-memory-model.md`; ADR-0019 and ADR-0020 refine approval and orchestration |
| [0018](adr/0018-browser-terminal-evaluation-and-shell-access-boundary.md) | Evaluate browser-terminal access without implementing it | `04-roadmap.md`, `06-remote-access.md`; terminal implementation remains deferred |
| [0019](adr/0019-structured-memory-approval-and-dispatch-checks.md) | Require explicit Memory approval and structured Dispatch checks | `08-memory-model.md` |
| [0020](adr/0020-layered-memory-and-role-sessions-for-orchestration.md) | Adopt layered Memory and role-specific sessions for orchestration | `03-runtime-contract.md`, `08-memory-model.md`, `09-orchestration-requirements.md` |
| [0021](adr/0021-durable-task-graph-and-dispatch-change-control.md) | Persist the Task Graph and control Dispatch changes | `10-task-graph.md` |
| [0022](adr/0022-sqlite-atomic-workspace-state.md) | Persist Ledger, Task Graph, and Dispatch state atomically in SQLite | `05-ledger-schema.md`, `10-task-graph.md`, `11-execution-records.md` |
| [0023](adr/0023-independent-project-forge-runtime-and-configuration-contracts.md) | Separate project, forge, runtime, and configuration contracts | `configuration.md`, `12-forge-capabilities.md` |
| [0024](adr/0024-human-handoff-control-and-side-questions.md) | Separate handoff control from questions that do not change active work | `09-orchestration-requirements.md`, `10-task-graph.md`, `11-execution-records.md` |
| [0025](adr/0025-approved-local-specification-search-and-tools.md) | Use approved local specification search and role-specific tools | `09-orchestration-requirements.md`, `13-specification-input.md` |
| [0026](adr/0026-bind-file-access-levels-to-task-approval.md) | Bind exact, committed and worktree-wide file access to Task approval | `03-runtime-contract.md`, `10-task-graph.md`, `user-guide.md` |
| [0027](adr/0027-review-retained-worktree-trees-without-commits.md) | Review a retained worktree tree without creating a commit | `03-runtime-contract.md`, `10-task-graph.md`, `11-execution-records.md` |
| [0028](adr/0028-cancel-saved-sessions-without-execution-reconfiguration.md) | Separate saved-session cancellation from execution settings; preserve result history and preflight future review approvals | `03-runtime-contract.md`, `11-execution-records.md`, `cli-reference.md`, `user-guide.md` |

ADRs 0026–0028 were added after the first-use and recovery investigation.
Local real-Git/fake-runtime tests establish permission boundaries, same-tree
handoffs, and successor checks. A cancellation-only operation also ended the
legacy suspended Dispatch and retained its partial Git evidence and both
result receipts without adding model instructions or usage. Neither result
closes the broader signed-in coding/review acceptance tracked by #79, nor
changes the observed GitHub milestone states above.
