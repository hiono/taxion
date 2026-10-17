# 判断・Milestone 対応表

この文書は、記録した判断、GitHub の Milestone・Issue、現在の仕様文書を対応付ける。ADR の採択は判断の記録であり、実装や受入れテストの完了を証明しない。現在の動作は参照先の仕様文書、実装の証拠は Issue と実行記録に記載する。

## Milestone の番号

GitHub の Milestone 番号とプロジェクトの段階名は異なる識別子である。最初の七つの GitHub Milestone は Foundation・Platform などの名前を使う。ロードマップで番号を付けた最初の段階は、GitHub Milestone #8 の M2。M10〜M15 は #16〜#21 に対応する。現在のロードマップと Milestone 名には M1 の定義がない。そのため、表では一対一の番号を創作せず、両方の名称を残す。

Foundation や初期の別の Milestone を M1 とした出典記録は確認できない。M1 と GitHub Milestone の対応は未確認である。

## Milestone・判断・現在の仕様

| GitHub Milestone | 段階 | 主な判断の記録 | Issues | 現在の仕様 | GitHub state |
| --- | --- | --- | --- | --- | --- |
| [#1 Foundation 基盤整備](https://github.com/hiono/taxion/milestone/1) | Foundation | ADR-0001, 0002, 0003 | #1 | [charter.md](charter.md), [guardrails.md](guardrails.md) | closed |
| [#2 Core コア実装](https://github.com/hiono/taxion/milestone/2) | Core | ADR-0003, 0004, 0005 | #2 | [guardrails.md](guardrails.md), [runtime-contract.md](runtime-contract.md) | closed |
| [#3 Local CLI 操作](https://github.com/hiono/taxion/milestone/3) | Local | 個別の設計判断なし | #3 | [cli-reference.md](../userguide/cli-reference.md) | closed |
| [#4 Adapter 実行環境の評価](https://github.com/hiono/taxion/milestone/4) | Adapter | ADR-0003 | #4 | [runtime-contract.md](runtime-contract.md) | closed |
| [#5 Server 永続化と API 設計](https://github.com/hiono/taxion/milestone/5) | Server | ADR-0009 | #5 | [ledger-schema.md](ledger-schema.md) | closed |
| [#6 Platform ACP・Web](https://github.com/hiono/taxion/milestone/6) | ACP・Web | ADR-0010, 0011 | #6, #7 | [runtime-contract.md](runtime-contract.md), [web/README.md](../../web/README.md) | closed |
| [#7 Future 将来検討](https://github.com/hiono/taxion/milestone/7) | Future | ADR-0005, 0012, 0018 | #8, #21, #26 | [roadmap.md](roadmap.md), [remote-access.md](../userguide/remote-access.md) | closed |
| [#8 M2 HTTP 読み取り API](https://github.com/hiono/taxion/milestone/8) | M2 | ADR-0012 | #9–#11 | [ledger-schema.md](ledger-schema.md), [remote-access.md](../userguide/remote-access.md) | closed |
| [#9 M3 プロセス実行](https://github.com/hiono/taxion/milestone/9) | M3 | ADR-0010, 0013 | #12–#14 | [runtime-contract.md](runtime-contract.md) | closed |
| [#10 M4 ブラウザ操作画面](https://github.com/hiono/taxion/milestone/10) | M4 | ADR-0011 の改訂、0014 | #15–#20 | [ledger-schema.md](ledger-schema.md), [remote-access.md](../userguide/remote-access.md), [web/README.md](../../web/README.md) | closed |
| [#11 M5 公開準備](https://github.com/hiono/taxion/milestone/11) | M5 | ADR-0015, 0016 | #22–#25 | [remote-access.md](../userguide/remote-access.md), [audit-log.md](../userguide/audit-log.md), [SECURITY.md](../../SECURITY.md) | closed |
| [#12 M6 Memory](https://github.com/hiono/taxion/milestone/12) | M6 | ADR-0017, 0019 | #27–#31 | [memory-model.md](../userguide/memory-model.md) | closed |
| [#13 M7 Self-Protection](https://github.com/hiono/taxion/milestone/13) | M7 | ADR-0017。sanitizer と export は M6 より後へ延期 | #32–#34, #38–#40 | [privacy-provenance.md](privacy-provenance.md), [memory-model.md](../userguide/memory-model.md) | closed |
| [#14 M8 Server Hardening](https://github.com/hiono/taxion/milestone/14) | M8 | ADR-0016 | #35–#37, #41–#43 | [remote-access.md](../userguide/remote-access.md), [audit-log.md](../userguide/audit-log.md), [SECURITY.md](../../SECURITY.md) | closed |
| [#15 M9 文書整備](https://github.com/hiono/taxion/milestone/15) | M9 | ADR-0006, 0007, 0008 | #44, #45, #68 | [README.md](../../README.md), [CONTRIBUTING.md](../../CONTRIBUTING.md), [roadmap.md](roadmap.md) | closed |
| [#16 M10 階層化した Memory・Task Graph・実行契約](https://github.com/hiono/taxion/milestone/16) | M10 | ADR-0020, 0021, 0022 | #46–#48, #66 | [memory-model.md](../userguide/memory-model.md), [task-graph.md](task-graph.md) | closed |
| [#17 M11 Copilot SDK と役割別セッション](https://github.com/hiono/taxion/milestone/17) | M11 | ADR-0020 | #49–#51 | [runtime-contract.md](runtime-contract.md), [orchestration-requirements.md](orchestration-requirements.md) | closed |
| [#18 M12 Memory を用いた自動実装・独立レビュー](https://github.com/hiono/taxion/milestone/18) | M12 | ADR-0020, 0023, 0024 | #52–#54, #67, #69, #72–#74 | [privacy-provenance.md](privacy-provenance.md), [orchestration-requirements.md](orchestration-requirements.md), [task-graph.md](task-graph.md), [configuration.md](../userguide/configuration.md) | closed |
| [#19 M13 実行の復旧と永続的な作業記録](https://github.com/hiono/taxion/milestone/19) | M13 | ADR-0021–0024 | #55–#57, #70 | [execution-records.md](../userguide/execution-records.md), [forge-capabilities.md](forge-capabilities.md) | closed |
| [#20 M14 仕様書検索・Memory 移行・tools 連携](https://github.com/hiono/taxion/milestone/20) | M14 | ADR-0020, 0025。会話 source の追加判断 ADR-0035 は Release の補足であり、M14 の完了範囲には遡及しない | #58–#61 | [memory-model.md](../userguide/memory-model.md), [specification-input.md](specification-input.md) | closed |
| [#21 M15 CLI・ACP 利用受入れ](https://github.com/hiono/taxion/milestone/21) | M15 | ADR-0010, 0020–0025 | [#62](https://github.com/hiono/taxion/issues/62) は closed。[#79](https://github.com/hiono/taxion/issues/79) で残る通し受入れを追跡 | [runtime-contract.md](runtime-contract.md), [orchestration-requirements.md](orchestration-requirements.md), [task-graph.md](task-graph.md), [specification-input.md](specification-input.md) | open |
| [#22 M16 Planner と Rust の後続検討](https://github.com/hiono/taxion/milestone/22) | M16 | ADR-0020 | #64, #65 | [orchestration-requirements.md](orchestration-requirements.md) | open |
| [#23 Release 0.1.0 — CLI first-use and documentation](https://github.com/hiono/taxion/milestone/23) | Release | ADR-0026〜0028 に初回利用と復旧、ADR-0029 に残予算指示、ADR-0030 に内部 Planner との相談、ADR-0031 に ready で今の coding agent に操作を任せる入口、ADR-0032〜0034 に既定 workspace・保存 record・portable snapshot、ADR-0035 に選択した会話 user 発言の仕様 source 化、ADR-0036 に token 閾値停止の廃止と実測利用量の報告、ADR-0037 に GitHub Release でのノート管理を記録。実装と無料回帰、account の読取り専用証拠を保持。実モデルの正式な通し受入れは未完了 | #78, #81 | [README.md](../../README.md), [user-guide.md](../userguide/user-guide.md), [configuration.md](../userguide/configuration.md), [cli-reference.md](../userguide/cli-reference.md), [CONTRIBUTING.md](../../CONTRIBUTING.md) | open |
| [#24 Release 0.3.0 — 初期設定・診断修復・CLI 操作の UX 改善](https://github.com/hiono/taxion/milestone/24) | UX | ADR-0039。設定準備、明示的な修復、bash 補完、help と ready の分類 | #82〜#85 | [user-guide.md](../userguide/user-guide.md), [configuration.md](../userguide/configuration.md), [cli-reference.md](../userguide/cli-reference.md) | 初期3件 closed。追加範囲は #85 |

#24 は最初の #82〜#84 の実装と検証を完了して一度 closed とした。この時点では v0.3.0 tag・Release の公開を含まない。
その後、目的別案内の評価・文書補正・公開 tag 導入と Release を [#85](https://github.com/hiono/taxion/issues/85) に追加し、milestone を再開した。追加範囲の受入れと公開結果は #85 で追跡する。
完了確認時の集計 API は open=3・closed=0 だったが、所属する3件の本文と状態を個別に再取得し、全件 closed と確認した。集計値と個別状態は区別して記録する。

Milestone の状態だけでは、Issue の証拠確認の代わりにならない。[#62](https://github.com/hiono/taxion/issues/62) は M15 の狭い範囲の ACP 実行受入れを記録するが、[要件と到達条件](orchestration-requirements.md)は、より広い通し受入れを定義する。その残件を [#79](https://github.com/hiono/taxion/issues/79) で追跡する。[#63](https://github.com/hiono/taxion/issues/63) は運用上の後続作業であり、代替にはならない。

M15 の説明では、開発基盤の追加対応（[#75](https://github.com/hiono/taxion/issues/75)、[#76](https://github.com/hiono/taxion/issues/76)）を完了条件から除いている。[ロードマップ](roadmap.md)の範囲とも一致する。

表の Milestone 名と状態は、2026-10-14 02:28:28 UTC に [GitHub milestones API](https://api.github.com/repos/hiono/taxion/milestones?state=all&per_page=100) から取得した。同時刻の [GitHub issues API](https://api.github.com/repos/hiono/taxion/issues?state=all&per_page=100) で割当てと状態を照合し、PR と Milestone なしの Issue は除いた。

2026-10-15 の再照合でも23 Milestone の名前・状態は一致した。
Milestone に割り当てられた75 Issue すべてについて、表の該当行に参照があることを検査した。
これは判断・実施結果・所有仕様の対応確認であり、未完了 Issue の受入れを証明するものではない。

この snapshot では、closed の Milestone #7、#12〜#14 の集計 `open_issues` が順に1、1、2、3だった一方、割当 Issue 一覧は closed の Issue だけだった。API の集計と Issue 一覧は別々に記録し、集計値や Milestone 状態だけで完了を証明しない。観測後に GitHub の値は変わる場合がある。

## ADR の状態と現在の参照先

ADR-0001〜0039 はすべて `Accepted` の判断を記録する。採択は選択肢を決めたことを意味し、必要な実装がすべて済んだことは意味しない。後の改訂と実装時の補足を別に示し、元の判断と現在の動作を区別する。

| ADR | 判断 | 現在の参照先・適用範囲 |
| --- | --- | --- |
| [0001](adr/0001-record-architecture-decisions.md) | 設計判断を記録する | `docs/develop/adr/` |
| [0002](adr/0002-develop-clean-room-with-no-ported-code-or-history.md) | 移植せず clean-room で開発する | `charter.md`、`privacy-provenance.md` |
| [0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md) | Runtime を介し、管理処理の core を SDK 共通に保つ | `runtime-contract.md` |
| [0004](adr/0004-use-phase-names-instead-of-dayn-milestones.md) | 日数番号ではなく段階名を使う | `roadmap.md` |
| [0005](adr/0005-stay-on-go-until-contracts-stabilize-defer-language-split.md) | 契約が安定するまで Go を維持し、言語分割を延期する | `runtime-contract.md`、`CONTRIBUTING.md` |
| [0006](adr/0006-never-mention-private-predecessor-names-in-public-artifacts.md) | 非公開の先行プロジェクト名を公開成果物に含めない | `privacy-provenance.md` |
| [0007](adr/0007-track-history-in-issues-prs-milestones-and-decisions-in-adrs.md) | 判断の記録と実装履歴を分ける | `roadmap.md`、`execution-records.md` |
| [0008](adr/0008-use-nygard-format-for-adrs.md) | Nygard 形式の ADR を使う | `docs/develop/adr/` |
| [0009](adr/0009-sqlite-backend-is-pure-go-modernc-preserving-ledger-shape.md) | Ledger 形式を保ち、pure-Go SQLite を使う | `ledger-schema.md` |
| [0010](adr/0010-acp-v1-spike-stdio-agent-with-prompt-convention-dispatch-review.md) | ACP v1 stdio adapter から始める | `runtime-contract.md`、`cli-reference.md`。M15 の実装更新は ADR に記録 |
| [0011](adr/0011-web-slice-stays-read-only-ts-with-zero-dependencies.md) | 最初の Web 実装を read-only・依存なしにする | M4 で改訂。現在の proxy は `web/README.md`、`remote-access.md` |
| [0012](adr/0012-defer-language-split-and-full-protocol-conformity-until-core-contracts-freeze.md) | 契約固定まで言語分割と完全な protocol 適合を延期する | `roadmap.md`、`ledger-schema.md`。再評価結果は ADR-0013・0014 |
| [0013](adr/0013-reevaluate-language-split-with-real-execution-behind-acp.md) | M3 で言語分割を再評価し、再度延期する。実装補足で元の ACP 実行の前提を訂正 | 現在の M15 実行は、この ADR と `runtime-contract.md` |
| [0014](adr/0014-record-split-revisit-verdict-with-a-write-capable-web-ui.md) | 書込み可能な Web UI 導入後の分割判断を記録する | `roadmap.md`、`remote-access.md` |
| [0015](adr/0015-token-auth-and-audit-complete-the-pre-public-security-model.md) | 外部公開前に token 認証と監査を要求する | `remote-access.md`、`audit-log.md`、`SECURITY.md` |
| [0016](adr/0016-serve-modes-loopback-remote-with-token-opt-in-idp-delegation-deferred.md) | loopback・remote mode を分け、IdP 委譲を延期する | `remote-access.md`、`audit-log.md` |
| [0017](adr/0017-memory-layers-ranges-and-promotion-path.md) | Memory の層、scope、昇格を定義する | `memory-model.md`。ADR-0019・0020 で承認と orchestration を具体化 |
| [0018](adr/0018-browser-terminal-evaluation-and-shell-access-boundary.md) | browser terminal を実装せず評価する | `roadmap.md`、`remote-access.md`。terminal 実装は延期中 |
| [0019](adr/0019-structured-memory-approval-and-dispatch-checks.md) | 明示的な Memory 承認と構造化 Dispatch 検査を要求する | `memory-model.md` |
| [0020](adr/0020-layered-memory-and-role-sessions-for-orchestration.md) | orchestration に階層化した Memory と role 別 session を採用する | `runtime-contract.md`、`memory-model.md`、`orchestration-requirements.md` |
| [0021](adr/0021-durable-task-graph-and-dispatch-change-control.md) | Task Graph を保存し、Dispatch の変更を管理する | `task-graph.md` |
| [0022](adr/0022-sqlite-atomic-workspace-state.md) | Ledger、Task Graph、Dispatch 状態を SQLite に atomic に保存する | `ledger-schema.md`、`task-graph.md`、`execution-records.md` |
| [0023](adr/0023-independent-project-forge-runtime-and-configuration-contracts.md) | project、開発基盤、Runtime、設定の契約を分ける | `configuration.md`、`forge-capabilities.md` |
| [0024](adr/0024-human-handoff-control-and-side-questions.md) | 引継ぎ制御と、active 作業を変えない質問を分ける | `orchestration-requirements.md`、`task-graph.md`、`execution-records.md` |
| [0025](adr/0025-approved-local-specification-search-and-tools.md) | 承認済みローカル仕様検索と role 別 tool を使う | `orchestration-requirements.md`、`specification-input.md` |
| [0026](adr/0026-bind-file-access-levels-to-task-approval.md) | exact・committed・worktree 全体の file access を Task 承認に結び付ける | `runtime-contract.md`、`task-graph.md`、`user-guide.md` |
| [0027](adr/0027-review-retained-worktree-trees-without-commits.md) | commit を作らず、保持した worktree tree をレビューする | `runtime-contract.md`、`task-graph.md`、`execution-records.md` |
| [0028](adr/0028-cancel-saved-sessions-without-execution-reconfiguration.md) | 保存 session の取消と実行設定を分ける。結果履歴を保ち、後続レビューの承認を事前確認する | `runtime-contract.md`、`execution-records.md`、`cli-reference.md`、`user-guide.md` |
| [0029](adr/0029-separate-budget-guidance-from-provider-quota.md) | 残予算案内の既定・指示別選択と、provider 固有の account quota 観測を分ける | `configuration.md`、`runtime-contract.md`、`user-guide.md`。token 閾値を使う停止 gate は ADR-0036 で改訂。実 account の読取り専用受入れは完了。実モデルでの効果は未確認 |
| [0030](adr/0030-refine-natural-language-requests-with-planner.md) | Planner と自然言語の依頼を改善し、JSON・role prompt をコピーせず既存 intake・review flow を構成する | `user-guide.md`、`cli-reference.md`、`runtime-contract.md`、`configuration.md`。オフライン通しテストの証拠を保持。実モデルの入口受入れは未完了 |
| [0031](adr/0031-bootstrap-the-current-coding-agent-with-ready.md) | ready で今の coding agent に既存 CLI の操作手順を渡し、内部 Planner の重複実行を避ける | `user-guide.md`、`cli-reference.md`、`runtime-contract.md`。無料の合成操作回帰を実装。実 agent の依頼改善・停止復旧・独立レビュー受入れは未完了 |
| [0032](adr/0032-resolve-default-workspace-from-git-worktree-root.md) | 通常の保存先を Git 作業ツリールートの `.taxion/` から解決し、保存ファイルの個別指定を例外用途にする | `user-guide.md`、`cli-reference.md`。linked worktree は分離し、旧状態の自動移動・統合は行わない |
| [0033](adr/0033-reference-results-and-persist-workspace-records-incrementally.md) | 不変の結果を ID 参照し、役割別入力・host の結果補完・SQLite の差分保存を使う | `runtime-contract.md`、`user-guide.md`、`cli-reference.md`。別マシン移送は後続の0034で実装 |
| [0034](adr/0034-portable-sqlite-workspace-snapshots.md) | 停止済み workspace・Memory・必要な Git object を一つの SQLite で移送し、新配置の継続は再承認する | `runtime-contract.md`、`user-guide.md`、`cli-reference.md`。元の契約・hash・usage は維持し、旧 SDK session は再開しない |
| [0035](adr/0035-use-selected-user-chat-messages-as-requirement-sources.md) | 会話 export は選択した user 発言だけを private な仕様 source として扱い、取込み・Copilot 送信・公開を個別承認する | `specification-input.md`、`orchestration-requirements.md`。synthetic 回帰は追加済み。実会話の Task 添付・Copilot 送信は未受入れ |
| [0036](adr/0036-prioritize-approved-goals-over-token-thresholds.md) | 予測不能な token 閾値で新しい承認済み実行を停止せず、call 別利用量を報告する。既存 `maxTokens` / `BUDGET_LIMIT` は履歴互換だけに残す | `task-graph.md`、`runtime-contract.md`、`execution-records.md`、`user-guide.md`。同単位の account allowance がない残高・割合は unavailable |
| [0037](adr/0037-publish-release-notes-on-github-only.md) | リリースノート本文は GitHub Release に保存し、未達条件は Issue、導入手順は利用者ガイドに分ける | `CONTRIBUTING.md`、`user-guide.md`、GitHub Releases。repository に release-note ファイルを置かない |
| [0038](adr/0038-run-opencode-acp-with-sealed-role-boundaries.md) | OpenCode ACP を role 別の Linux namespace で実行し、承認 path・read-only review・用途別送信許可を維持する | `runtime-contract.md`、`configuration.md`、`specification-input.md`。最小実 prompt の成功と実文書の通し受入れは区別する |
| [0039](adr/0039-prepare-project-configuration-with-explicit-repair.md) | 設定と DB の基点を揃え、init と doctor fix で不足を補い、実 CLI から bash 補完を生成する | `user-guide.md`、`configuration.md`、`cli-reference.md`。初期設定はモデル実行・契約・送信・Task・予算の承認を行わない |

ADR-0026〜0028 は、初回利用と復旧の調査後に追加した。ローカルの実 Git・fake Runtime テストで、許可範囲、同一 tree の引継ぎ、successor の照合を確認した。停止専用操作では、旧 suspended Dispatch を終了し、追加モデル指示・利用量なしで部分 Git 証拠と両方の結果 receipt を保持した。

これらの結果は、#79 の広い範囲の login 済みコード作成・レビュー受入れを完了させるものではなく、上記の観測済み GitHub Milestone 状態も変更しない。
