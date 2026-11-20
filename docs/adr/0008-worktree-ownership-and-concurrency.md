# ADR-0008: Worktree所有権とRoleExecutionの安全な移転

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` のAccepted ADR-0082を根拠として、新TaxionのJobAggregate、Provider operationおよび認可境界へ適用する形で再構成した。

## Context

複数Roleが同じ作業領域を同時に変更すると、競合や成果の帰属不明が発生する。Providerがまだ書き込み可能な状態で次のRoleへ所有権を移すと、停止後の遅延書込みや成果物の混在を防止できない。

## Decision

開発作業のWorktree所有権はTask単位で永続的・排他的に管理する。同一Worktreeを使うmutating RoleExecutionは逐次実行する。

### 1. Git repositoryとWorktree

- Jobのcoding executionはGit repository内で行う。
- 最初に対応する開発実行環境はGit Worktreeとする。
- Taxion独自のVCSや汎用Workspace engineを実装しない。
- TaskごとにWorktreeを割り当て、Job-level Taskless PlannerはTask Worktreeを取得しない。
- 同じTaskのRoleExecutionは同じWorktreeを逐次引き継ぐ。
- 独立性が明確な別Taskだけを別Worktreeで並行実行する。独立性が不明なTaskは直列化する。

### 2. 所有権と排他

- 一つのWorktreeに対して同時に実行するmutating RoleExecutionは最大一つとする。
- 所有権はJobAggregate transactionで永続化し、process-local mutexだけに依存しない。
- Repository共通Git metadataを変更する操作はRepository単位で直列化する。
- Agentに任意のsibling Worktreeやbranchを作らせて並行性を自己管理させない。

### 3. 所有権移転

Handoff時は受信Task scopeを明示する。送信元Providerの停止証明、最終出力・Usageの照合、成果物identityの固定、所有権解放を永続化してから受信RoleExecutionをrunnableにする。WAITINGやRoleExecution terminal stateだけで解放しない。

### 4. 不明状態と隔離

Provider停止やWorktree状態が不明な場合は所有権を解放せず、Worktreeをquarantineする。古いownerの状態更新や成果確定はfencingとrevision検査で拒否する。fencing tokenだけでProviderの直接filesystem writeを防げると仮定しない。隔離解除はProvider状態と成果物の照合後に限る。

### 5. Reviewer

ReviewerはCoderの停止後に固定された成果物を独立Context / sessionで検証する。通常レビューのために物理的な別Worktreeを常時要求しない。read-only境界と対象digestを強制し、レビュー対象が変化した場合は結果を流用しない。

### 6. 並行実行の範囲

- Task依存関係が解決し、独立Worktreeに分離されたTaskだけを並行実行する。
- Worktree間の意味的競合やmerge conflictは、成果identityを固定した後の統合作業で明示的に解決する。
- 同じWorktree内のmutating Roleを並行させない。
- 全filesystem accessを監視する汎用lock graphやresource lock managerを先行実装しない。

## Consequences

同一Worktreeの二重所有を防ぎ、Role間の安全な逐次引継ぎと独立Task間の限定並行を実現する。任意の同一Worktree同時編集を前提にしない。

## Security and operational impact

Provider停止が確認できないWorktreeを再利用しない。Worktree path、repository identity、owner、Job / Task scope、target digestを照合する。Worktreeの分離やSkillの指示はJobの権限を増やさない。

## Alternatives considered

- RoleごとにWorktreeを常に分離する方式は、不要な複製と統合作業を増やすため採用しない。
- 同一Worktreeの複数Roleを同時実行する方式は、物理的な競合と成果帰属の曖昧さを招くため採用しない。
- WAITINGを停止証明とみなす方式は、Providerが動作を継続している可能性を排除できないため採用しない。
- 汎用lock graphを導入する方式は、実際のGit開発シナリオに対して過剰な抽象化となるため採用しない。

## Relationship to other ADRs

ADR-0003のTaskとRoleExecution、ADR-0004の認可、ADR-0005の全経路不変条件、ADR-0006のHandoff、ADR-0007のProvider外部境界を前提とする。
