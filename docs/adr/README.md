# Architecture Decision Records

このディレクトリは、現在のTaxionで採用する設計判断の正本です。管理規約は [ADR-0000](0000-adr-governance.md) を参照してください。

## 決定の階層

- **ADR-0001 — 製品目的:** 人間の曖昧な思考を対話で整理し、Job完遂を支援する製品価値を定義します。
- **ADR-0002 — 再構築原則:** 旧設計の制約を温存せず、新しい不変条件から再構築する原則を定義します。
- **ADR-0003 — JobAggregate:** Jobを作業・協調・成果の唯一のドメイン正本とし、Planning、Task、RoleExecution、JobMessage、Provider、Worktree、成果・証拠、Command境界を定義します。
- **ADR-0004〜0009 — 実行ガードレール:** 認可、不変条件、内部通信、外部A2A、Worktree所有権、証拠と製品受入れを定義します。

ADR-0001は「何を実現するか」、ADR-0002は「どの原則で作り直すか」、ADR-0003は「Jobをどのようなドメイン正本として扱うか」を定義します。ADR-0004以降はそれらを具体化し、上位の安全境界を弱めません。番号の大小は優先順位を意味しません。決定の変更は既存判断を暗黙に書き換えず、置換対象と維持する範囲を明示した後続ADRで記録します。

## Status

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
- [ADR-0001: 製品目的 — 人間の曖昧な思考を継続可能な協調作業へ昇華する](0001-product-purpose.md) — Accepted
- [ADR-0002: 旧設計の制約を温存せず、製品目的から再構築する](0002-rebuild-over-legacy-constraints.md) — Accepted
- [ADR-0003: JobAggregateを作業・協調・成果の唯一のドメイン正本とする](0003-job-aggregate-and-command-boundary.md) — Accepted
- [ADR-0004: Job承認境界と禁止副作用によって認可する](0004-job-authorization-and-effect-boundaries.md) — Accepted
- [ADR-0005: ドメイン不変条件をすべての状態変更経路で強制する](0005-domain-invariants-all-state-paths.md) — Accepted
- [ADR-0006: 内部6 Actor通信をJobMessageとして定義する](0006-internal-six-actor-jobmessage.md) — Accepted
- [ADR-0007: 外部Agent相互運用の必須プロトコルをA2Aに一本化する](0007-external-agent-a2a.md) — Accepted
- [ADR-0008: Worktree所有権とRoleExecutionの安全な移転](0008-worktree-ownership-and-concurrency.md) — Accepted
- [ADR-0009: 検証証拠の水準とJob完遂の受入れ](0009-evidence-and-product-acceptance.md) — Accepted

## 原典と来歴

ADR-0001〜0003は、アーカイブ済み旧Taxionの製品目的、再構築原則、JobAggregateのAccepted decisionを確認し、新しいリポジトリ向けに再構成しました。ADR-0004〜0008は、旧TaxionのJob認可、不変条件、Job中心協調、6 Actor通信、A2A、WorktreeのAccepted decisionを適用範囲ごとに確認して再構成しました。ADR-0009は旧PoCと旧Taxionの比較方針、検証証拠の区分、開発品質ゲートを新Taxion向けに再構成しました。

旧Taxionと旧Taxion-PoCのGit履歴、Commit、Issue / PR履歴は移植していません。旧リポジトリの原文は読み取り専用の根拠資料であり、旧番号は原典を識別する参照としてのみ使用します。旧コードの採用はADR-0000およびADR-0002のprovenance・設計適合性・安全性・受入れ条件に従って別途判断します。
