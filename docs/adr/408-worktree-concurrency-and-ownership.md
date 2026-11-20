# ADR-408: Jobの開発作業をGit Worktree単位で分離し、Roleを逐次引き継ぐ

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の設計資料に記録されたJob-native concurrency、Worktree ownership、Reviewer handoffおよびRepository共通Git metadataの制約を確認し、新しい履歴へ再確立しました。

## Context

Taxionは既存Git repository上の開発作業を支援します。同じファイルを複数Agentが任意のタイミングで同時編集する汎用競合問題を先に解決するのではなく、独立したTaskを分離されたWorktreeへ割り当て、同じWorktreeを担当するRoleを順番に実行することで、通常の開発作業の競合を実行前に制御します。

汎用lock graphや動的dependency engineを先行実装すると、実際の製品利用を超える複雑さを持ち込みます。Job Graphの明示的依存、Git Worktreeの分離、同一Worktreeの単一所有、Git統合時の明示的競合解決で必要な安全性を実現します。

## Decision

**Jobのcoding executionはGit repository内で行い、TaskごとにGit Worktreeを割り当てます。同じWorktreeを使うmutating RoleExecutionは逐次実行し、独立したTaskだけを別Worktreeで並行実行します。**

### 1. Git repositoryをcoding executionの前提とする

- Jobのcoding executionはGit repository内で行います。
- 最初のJob-native並行実行環境はGit Worktreeとします。
- standalone workspaceが存在することを理由にGit外のcoding executionをサポート対象にしません。
- GitButler、Jujutsu等は候補として評価できますが、作業分離、成果identity、統合、停止・復旧を実証するまで対応済みと扱いません。
- Taxion独自VCS、汎用Workspace engine、全環境共通lock abstractionは作りません。

JobのDRAFT intakeとPlannerのbounded read-only context取得は、coding Worktreeを必要としません。

### 2. Taskを独立作業単位としWorktreeを割り当てる

- Jobは目的と成果を保持し、TaskはJob内の具体的な作業単位とします。
- 並行させるTaskごとに別のGit Worktreeを割り当てます。同じTaskのRoleExecutionはそのWorktreeを順次引き継ぎます。
- Job-level Plannerなど開発作業領域を必要としないRoleExecutionはWorktreeを持たなくてもよいものとします。
- Task分割はPlannerがJob目的・制約・成果物を踏まえて決定します。細分化自体を目的にしません。
- Task間の独立性が明確でない場合は並行させず、逐次作業とします。
- 並行数はJob承認済みMaxConcurrentExecutionsを上限とし、上限まで実行することを要求しません。
- Worktreeの作成・割当て・識別・解放はControllerがGit native worktree操作で管理します。repository identity、Worktree path、baseline commitを保存し、RuntimeのWorkingDirectoryを割当先に固定します。
- Worktree add/remove、branch/ref更新などrepository共通Git metadataを変更する操作はRepository単位で直列化します。
- Task成果とimmutable identityを保存する前にWorktreeを削除しません。未保存変更は自動破棄せず、成果確定または明示的保全へ進めます。

### 3. 同一WorktreeのRoleは逐次実行する

- 一つのWorktreeに対して同時に実行するmutating RoleExecutionは最大一つとします。
- Coder、Reviewer、修正担当などは同じWorktreeを順次引き継ぎます。
- WAITING、SUSPENDED、CANCEL_REQUESTEDだけでWorktreeを解放しません。
- Handoffは旧Provider execution / processの終了を確認し、event・usage・resultを保存し、review targetのcommit / tree identityを固定した後に行います。
- Provider停止結果が不明な場合、Worktreeを使用中として保持します。新しいRoleを同じWorktreeで開始しません。
- 安全な停止または隔離を確認できないWorktreeはquarantineし、再利用しません。
- 所有状態と実行IDを永続化し、再起動後に推測で解放しません。
- fencing tokenだけでProviderの直接filesystem writeを防止できると仮定しません。物理的な停止はProvider / processの停止確認または実行環境の隔離で保証します。

### 4. Reviewerは同じWorktreeを順次引き継ぎ、固定成果を読む

- 通常のCoder → Reviewer → 修正では、ReviewerはCoderの終了後に同じWorktreeを引き継ぎます。
- ReviewerはCoderと異なるRoleExecution、session、Contextで判断し、固定commit / tree / snapshotを対象とします。
- Reviewerの作業境界はread-onlyとし、Provider / adapterの実行境界で強制します。
- read-onlyを強制できない場合は固定成果物から作ったread-only snapshotを使用します。用意できない場合はReviewerを開始しません。
- Reviewerの独立性のために、常に別の永続Worktreeを作ることは要求しません。
- review target identityが変わった場合、当該レビュー結果を無効にします。

### 5. 別Worktreeの並行性はJob Graphで制御する

- Job GraphはTask間の明示的依存関係を表します。依存先の成果が確定するまで後続Taskを開始しません。
- Plannerが独立性を確認したTaskを別Worktreeへ割り当て、Graph dependencyが解決済みの場合に限り並行実行します。
- 変更対象の重複、共通排他的資源、同一branch/refを同時変更する必要がある場合は直列化するか、先行Task確定後に統合作業へ送ります。
- Git Worktreeは作業ファイルを分離しますが、repository共通metadata、remote、外部サービス等すべての共有資源を分離しません。
- Worktree間のmerge conflictは、成果identity固定後の統合Taskで明示的に検出・解決します。上書き・破棄を自動で選択しません。
- Taskの独立性や資源関係が不明な場合は安全側に直列化します。
- 全filesystem accessを監視する動的dependency graph、汎用resource lock manager、実行中の任意アクセスによる自動Graph再構築は先行実装しません。
- Job Graphのedge追加でJob approvalのresource boundaryや権限を拡張しません。

### 6. 安全境界

Worktree分離、Task dependency、Role handoff、Skillの指示はJob権限を増やしません。JobApprovalのrepository / resource / file-access boundary、禁止副作用、承認が必要な副作用、budget、deadline、secret protection、duplicate execution preventionを維持します。実行状態が不明な場合は再送・所有権移転を行いません。

## Alternatives considered

- **同一Worktreeの複数mutating Roleを同時実行し、後からraceを検出する:** 実行前の逐次所有で防止できる競合を発生させるため採用しません。
- **すべてのfile / resource accessを監視する汎用lock graphを作る:** Git WorktreeとTask dependencyで制御できる範囲を超える汎用基盤となるため採用しません。
- **Job全体を一つのWorktreeで直列実行する:** 独立Taskの並行実行を失うため採用しません。
- **RoleExecutionごとにWorktreeを作る:** Coder / Reviewer / 修正担当間の成果物引継ぎと統合が増えるため採用しません。
- **Skillの指示やfencing tokenだけで物理書込み停止を保証する:** Provider / processの直接filesystem writeを防止できる証拠がないため採用しません。
## Consequences

- 最初に対応するJob-native開発環境はGit repository + Git Worktreeです。
- 同じWorktree内のRoleは逐次実行し、独立WorktreeのTaskのみ並行実行します。
- 依存が不明なTaskは直列化します。
- Provider停止を確認できないWorktreeは再利用せずquarantineします。
- Repository共通Git metadataの更新はrepository単位で直列化します。
- Job-native Worktree ownership、Scheduler、read-only Reviewer boundary、停止・復旧は独立した実装・受入れ対象です。ADR採択は実装完了を意味しません。

## Acceptance criteria

- coding executionがGit repository外で開始されません。
- 独立Taskは別Worktreeに割り当てられ、Graph dependencyとJob concurrency ceilingの範囲で並行実行できます。
- 独立性が不明なTaskは並行開始されません。
- 同じWorktreeのmutating RoleExecutionは最大一つであり、同時Start / Resume / Handoffはatomicに拒否されます。
- HandoffはProvider停止、結果保存、artifact identity固定後にのみ行われます。
- Reviewerは固定成果物を独立Context / sessionとread-only boundaryで検証します。
- Repository共通Git metadata更新はrepository単位で直列化されます。
- merge conflictは統合Taskで明示的に解決され、成果を暗黙に上書きしません。

## Relationship to other ADRs

- ADR-100は製品目的を定義します。
- ADR-101は旧設計の制約を温存しない再構築原則を定義します。
- ADR-201はJob、Task、RoleExecution、Provider operation、Worktree ownershipのaggregate境界を定義します。
- ADR-500はJob approvalと禁止副作用を定義します。
- ADR-202はWorktree ownership、RoleExecution、artifact identityの不変条件を全状態変更経路で強制します。
