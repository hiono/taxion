# Architecture Decision Records

このディレクトリは、現在のTaxionで採用する設計判断の正本です。ADRの管理・改訂・来歴規約は [ADR-0000](0000-adr-governance.md) を参照してください。ADRの管理・改訂・来歴規約は [ADR-0000](0000-adr-governance.md) を参照してください。

## 決定の階層

- **ADR-0001 — 製品目的:** Taxionが利用者に提供する価値と完成評価の基準を定義します。
- **ADR-0002 — 再構築原則:** 製品目的を実現するために、旧設計の制約を温存せず再構築する方法を定義します。
- **ADR-0003 — JobAggregate:** Jobを作業・協調・成果の唯一のドメイン正本とし、Planning、Task、RoleExecution、JobMessage、Provider、Worktree、成果・証拠、Command境界を定義します。
- **ADR-0004 — Job authority:** Job承認境界、禁止副作用、PlanningAuthorization、Observation / Conformance / Admission / Execution Permissionの分離、不完全情報の協調を定義します。
- **ADR-0005 — Domain invariants:** 不変条件を生成・変更・保存・復元の全経路で強制する規範を定義します。
- **ADR-0006 — Internal communication:** Taxion内部の6 Actor通信、JobMessage、Envelope、MessageKind、配信観測を定義します。
- **ADR-0007 — External Agent protocol:** 外部Agent相互運用の必須protocolをA2Aに一本化し、内部JobMessageとの境界を定義します。
- **ADR-0008以降:** Worktree並行性、証拠管理、その他の具体的な設計判断を定義します。ADR-0001〜0007に適合させます。

ADR-0001は「何を実現するか」、ADR-0002は「どの原則で作り直すか」、ADR-0003は「Jobをどのようなドメイン正本として扱うか」、ADR-0004は「誰がどの境界内で何を実行できるか」、ADR-0005は「すべての状態変更経路でどう不変条件を守るか」、ADR-0006は「Taxion内部でどう協調するか」、ADR-0007は「外部Agentとどう相互運用するか」を定義します。下位ADRはこれらを上書きせず、具体化します。決定の変更は既存判断を暗黙に書き換えず、新しいADRと明示的なSupersedes関係で記録します。

## 状態

| Status | 意味 |
| --- | --- |
| Proposed | 判断案。実装の規範として扱いません。 |
| Accepted | 現行設計の規範。実装・テストはこれに適合させます。 |
| Superseded | 指定された後続ADRに置き換えられています。 |
| Deprecated | 新規利用を停止した判断。履歴として保持します。 |
| Rejected | 検討したが採用していません。 |

ADRがAcceptedであることは、実装や受入れが完了したことを意味しません。

## ADR一覧

- [ADR-0000: Architecture Decision Recordsの管理規約](0000-adr-governance.md) — Accepted
- [ADR-0000: Architecture Decision Recordsの管理規約](0000-adr-governance.md) — Accepted
- [ADR-0001: 製品目的 — 人間の曖昧な思考を継続可能な協調作業へ昇華する](0001-product-purpose.md) — Accepted
- [ADR-0002: 旧設計の制約を温存せず、製品目的から再構築する](0002-rebuild-over-legacy-constraints.md) — Accepted
- [ADR-0003: JobAggregateを作業・協調・成果の唯一のドメイン正本とする](0003-job-aggregate-and-command-boundary.md) — Accepted
- [ADR-0004: Job承認境界と禁止副作用を中心に実行権限を強制する](0004-job-authority-and-prohibited-effects.md) — Accepted
- [ADR-0005: ドメイン不変条件を生成・変更・保存・復元の全経路で強制する](0005-domain-invariants-across-state-paths.md) — Accepted
- [ADR-0006: Taxion内部の6 Actor通信をJobMessageへ統一する](0006-jobmessage-six-actor-communication.md) — Accepted
- [ADR-0007: 外部Agent相互運用の必須protocolをA2Aに一本化する](0007-a2a-external-agent-interoperability.md) — Accepted
- [ADR-0008: Worktree所有権とRoleExecutionの安全な移転](0008-worktree-ownership-and-concurrency.md) — Accepted
- [ADR-0009: 検証証拠の水準とJob完遂の受入れ](0009-evidence-and-product-acceptance.md) — Accepted

## 原典と来歴

ADR-0001は旧ADR-0088、ADR-0002は旧ADR-0081、ADR-0003は旧ADR-0083と旧ADR-0077を原典として再構成しました。ADR-0004は旧ADR-0080、0073、0066、0067のAccepted decisionを確認して再確立しました。ADR-0005は旧ADR-0079、ADR-0006は旧ADR-0087、ADR-0007は旧ADR-0108を原典として再確立しました。ADR-0008は旧ADR-0082のWorktree ownership / concurrency decisionを、ADR-0009は旧ADR-0107とADR-0085の候補実装比較・証拠区分・品質ゲートを新Taxion向けに再構成しました。

旧Taxionと旧Taxion-PoCのGit履歴、Commit、Issue / PR履歴は移植していません。旧リポジトリの原文は読み取り専用の根拠資料であり、旧番号は原典を識別する参照としてのみ使用します。旧Taxion-PoCの初期READMEは実験専用と記載していましたが、後続の旧Taxion ADR-0107が候補実装ベースラインとして位置付けを変更した経緯を保持します。旧コードの採用はADR-0000およびADR-0002のprovenance・設計適合性・安全性・受入れ条件に従って別途判断します。
