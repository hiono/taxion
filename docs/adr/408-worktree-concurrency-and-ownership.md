# ADR-408: Jobの開発作業をGit Worktree単位で分離し、Roleを逐次引き継ぐ

Date: 2026-11-20

## Status

Accepted

## Context

同じファイルを複数Agentが同時編集する競合を汎用lock graphで解決すると、製品利用を超える複雑さを持ち込みます。独立TaskをWorktreeで分離し、同じWorktreeのRoleを逐次実行することで通常の競合を制御します。

## Decision

**Coding executionはGit repository内で行い、TaskごとにGit Worktreeを割り当てます。同じWorktreeのmutating RoleExecutionは逐次実行し、独立Taskだけを別Worktreeで並行実行します。**

この判断はTrusted Jobの実行に適用します。ローカルの `taxion run` は利用者が指定した作業ディレクトリを直接使い、Worktreeの作成・所有権管理や他プロセスとの排他を行いません。

- 最初のJob-native開発環境はGit Worktreeとします。DRAFT intakeとPlannerのbounded read-only context取得にはWorktreeを要求しません。
- Task間の独立性と明示的依存関係を確認してWorktreeを割り当てます。並行実行数はJobApprovalで承認されたMaxConcurrentExecutionsを上限とし、独立性が不明な場合は逐次実行します。
- Worktreeの作成、割当て、識別、解放はControllerが管理し、repository identity、path、baseline commit、ownerを永続化します。
- 同一Worktreeのmutating RoleExecutionは最大一つとします。Coder、Reviewer、修正担当は同じWorktreeを順次引き継げます。
- Handoff前にProvider停止、event・result、review target identityを確認します。停止や結果が不明なWorktreeは保持またはquarantineします。
- Reviewerは独立したRoleExecution / Contextで固定commit・tree・snapshotをread-onlyで評価します。
- Repository共通Git metadataの変更はRepository単位で直列化し、Worktree間の競合は統合Taskで明示的に解決します。
- Worktree分離はJobApproval、resource boundary、Computer Sandboxを代替しません。

## Consequences

同一WorktreeのRoleは逐次実行し、独立Taskの並行性はWorktreeとJob Graphで制御します。停止状態が不明なWorktreeを推測で再利用しません。

## Relationship to other ADRs

ADR-400はExecution & Runtimeの基本方針、ADR-201はWorktree ownershipのJobAggregate上の正本、ADR-500はJob権限、ADR-202は所有権と実行状態の不変条件を定義します。
