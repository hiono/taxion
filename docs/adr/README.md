# Architecture Decision Records

このディレクトリは、現在のTaxionで採用する設計判断の正本です。

## 決定の階層

- **ADR-0001 — 製品目的:** Taxionが利用者に提供する価値と完成評価の基準を定義します。
- **ADR-0002 — 再構築原則:** 製品目的を実現するために、旧設計の制約を温存せず再構築する方法を定義します。
- **ADR-0003 — JobAggregate:** Jobを作業・協調・成果の唯一のドメイン正本とし、Planning、Task、RoleExecution、JobMessage、Provider、Worktree、成果・証拠、Command境界を定義します。
- **ADR-0004 — Job authority:** Job承認境界、禁止副作用、PlanningAuthorization、観測・受入れ・実行許可の分離、不完全情報の協調を定義します。
- **ADR-0005 — Domain invariants:** 不変条件を生成・変更・保存・復元の全経路で強制する規範を定義します。
- **ADR-0006 — Internal communication:** Taxion内部の6 Actor通信、JobMessage、Envelope、MessageKind、配信観測を定義します。
- **ADR-0007 — External Agent protocol:** 外部Agent相互運用の必須protocolをA2Aに一本化し、内部JobMessageとの境界を定義します。
- **ADR-0008 — Worktree concurrency:** Task単位のWorktree分離、同一Worktreeの逐次所有、独立Taskの並行性を定義します。
- **ADR-0009 — Information provenance:** 現行仕様、Dropbox計画、GitHub履歴、Issue / PR、CI証拠の正本を分離します。

ADR-0001は「何を実現するか」、ADR-0002は「どの原則で作り直すか」、ADR-0003は「Jobをどのようなドメイン正本として扱うか」、ADR-0004は「誰がどの境界内で何を実行できるか」、ADR-0005は「すべての状態変更経路でどう不変条件を守るか」、ADR-0006は「Taxion内部でどう協調するか」、ADR-0007は「外部Agentとどう相互運用するか」、ADR-0008は「開発作業をどう分離・並行させるか」、ADR-0009は「設計・計画・履歴・検証証拠をどこに保存するか」を定義します。下位ADRはこれらを上書きせず、具体化します。決定の変更は既存判断を暗黙に書き換えず、新しいADRと明示的なSupersedes関係で記録します。

## 状態

| Status | 意味 |
| --- | --- |
| Proposed | 判断案。実装の規範として扱いません。 |
| Accepted | 現行設計の規範。実装・テストはこれに適合させます。 |
| Superseded | 指定された後続ADRに置き換えられています。 |
| Rejected | 検討したが採用していません。 |

ADRがAcceptedであることは、実装や受入れが完了したことを意味しません。

## ADR一覧

- [ADR-0001: 製品目的 — 人間の曖昧な思考を継続可能な協調作業へ昇華する](0001-product-purpose.md)
- [ADR-0002: 旧設計の制約を温存せず、製品目的から再構築する](0002-rebuild-over-legacy-constraints.md)
- [ADR-0003: JobAggregateを作業・協調・成果の唯一のドメイン正本とする](0003-job-aggregate-and-command-boundary.md)
- [ADR-0004: Job承認境界と禁止副作用を中心に実行権限を強制する](0004-job-authority-and-prohibited-effects.md)
- [ADR-0005: ドメイン不変条件を生成・変更・保存・復元の全経路で強制する](0005-domain-invariants-across-state-paths.md)
- [ADR-0006: Taxion内部の6 Actor通信をJobMessageへ統一する](0006-jobmessage-six-actor-communication.md)
- [ADR-0007: 外部Agent相互運用の必須protocolをA2Aに一本化する](0007-a2a-external-agent-interoperability.md)
- [ADR-0008: Jobの開発作業をGit Worktree単位で分離し、Roleを逐次引き継ぐ](0008-worktree-concurrency-and-ownership.md)
- [ADR-0009: 現行仕様・開発計画・履歴・検証証拠の正本を分離する](0009-specification-plan-history-and-evidence.md)

## 原典と来歴

ADR-0001〜0008は、アーカイブ済みhiono/obsolete-taxionのAccepted decisionを原典として、必要な意味を確認し新しい履歴へ再確立しました。各ADRのSource decisionに原典のpathとblob SHAを記録しています。ADR-0009は旧ADR-0085（blob bd25f6aee937ecd3a61cf7a2298aceefec281055）を原典とし、Dropboxを計画正本とする現在のプロジェクト運用指示を追加しています。

これらは旧Git履歴の移植ではありません。原典の判断内容を確認したうえで、新リポジトリの設計判断として新たに記録したものです。原典の後続改訂や関連ADRとの整合性を確認せず、原文を無条件に複製してはなりません。
