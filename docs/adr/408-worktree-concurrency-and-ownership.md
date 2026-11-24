# ADR-408: Jobの開発作業をGit Worktree単位で分離し、Roleを逐次引き継ぐ

Date: 2026-11-20

## Status

Accepted

## Context

同じファイルを複数Agentが同時編集する競合を汎用lock graphで解決すると、製品利用を超える複雑さを持ち込みます。独立TaskをWorktreeで分離し、同じWorktreeのRoleを逐次実行することで通常の競合を制御します。

## Decision

**Coding executionはGit repository内で行い、TaskごとにGit Worktreeを割り当てます。同じWorktreeのmutating RoleExecutionは逐次実行し、独立Taskだけを別Worktreeで並行実行します。**

- 最初のJob-native開発環境はGit Worktreeとします。DRAFT intakeとPlannerのbounded read-only context取得にはWorktreeを要求しません。
- Task間の独立性と明示的依存関係を確認してWorktreeを割り当てます。並行実行数はJobApprovalで承認されたMaxConcurrentExecutionsを上限とし、独立性が不明な場合は逐次実行します。
- Worktreeの作成、割当て、識別、解放はControllerが管理し、repository identity、path、baseline commit、ownerを永続化します。
- 同一Worktreeのmutating RoleExecutionは最大一つとします。Coder、Reviewer、修正担当は同じWorktreeを順次引き継げます。
- Handoff前にProvider停止、event・usage・result、review target identityを確認します。停止や結果が不明なWorktreeは保持またはquarantineします。
- Reviewerは独立したRoleExecution / Contextで固定commit・tree・snapshotをread-onlyで評価します。
- Repository共通Git metadataの変更はRepository単位で直列化し、Worktree間の競合は統合Taskで明示的に解決します。
- Worktree分離はJobApproval、resource boundary、Computer Sandboxを代替しません。
- Worktreeの生成元は信頼性や利用可否を決める条件にしません。Taxionが生成したWorktreeと、利用者・外部ツールが事前に生成したWorktreeは、同じ検証・登録手続きを通過した後は同一のJob / Task管理対象として扱います。生成元は監査用のprovenanceとして保持します。
- 既存Worktreeの採用（Adopt）はController管理の明示的なCommandとします。Taskの承認済みRepository scope、trusted Repository identity、canonical repository / Worktree path、Git linked-worktree metadata、baseline commit、HEAD commit / tree、index・working tree・untracked filesを含む初期状態のidentityを検証し、採用事実をJobAggregateへ永続化します。検証不能、scope外、identity不一致、symlink経由、baselineとの履歴関係不明の場合はfail closedとします。
- Adoptは既存ファイルを変更しません。reset、checkout、clean、stashなどにより利用者の変更を破棄・移動してはなりません。staged / unstaged / untrackedの状態を完全に識別・保全できない場合は採用を拒否します。
- CreateとAdoptは登録方法だけを分け、登録後は同じWorktree lifecycle、Job / Task ownership、Provider検証、停止・成果Evidenceの契約を適用します。Worktreeは同時に一つのJob / Taskだけへ割り当てます。
- WorktreeAssignmentのownerを上書きしてRoleを切り替えてはなりません。Role間のmutable Worktree ownership移転は、旧ownerのProvider停止・Quiescent、event / final result / complete Usage、移転対象identityを照合した明示的Handoff Commandとappend-only transfer factで確定します。未確定・UNKNOWNの場合は旧ownerとWorktreeを保持またはquarantineします。
- Reviewerにはmutable Worktreeを渡しません。Coderの確定したcommit / treeから独立したimmutable snapshotを作成し、別RoleExecution / Context / SessionとProvider側read-only enforcementで評価します。Reviewer終了後にCoderが修正を続ける場合も、Reviewerの停止・結果・Usageを照合した後、明示的なHandoffでmutable ownershipを再取得します。
- Release、quarantine、orphan reconciliation / recoveryはControllerが管理します。停止・所有者・実行結果が不明なWorktreeを自動削除または別Roleへ再割当てしません。

## Consequences

同一WorktreeのRoleは逐次実行し、独立Taskの並行性はWorktreeとJob Graphで制御します。停止状態が不明なWorktreeを推測で再利用しません。

## Relationship to other ADRs

ADR-400はExecution & Runtimeの基本方針、ADR-201はWorktree ownershipのJobAggregate上の正本、ADR-500はJob権限、ADR-202は所有権と実行状態の不変条件を定義します。
