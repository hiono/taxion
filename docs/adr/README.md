# Architecture Decision Records

このディレクトリは、現在のTaxionで採用する設計判断の正本です。ADRの管理・改訂・来歴規約は [ADR-000](000-adr-governance.md) を参照してください。

## 決定の階層

- **ADR-100 — 製品目的:** Taxionが利用者に提供する価値と完成評価の基準を定義します。
- **ADR-101 — 再構築原則:** 製品目的を実現するために、旧設計の制約を温存せず再構築する方法を定義します。
- **ADR-106 — 設計判断の比較方法:** 複数候補の責務・制約・証拠を比較し、必須条件をハード制約として維持する方法を定義します。
- **ADR-201 — JobAggregate:** Jobを作業・協調・成果の唯一のドメイン正本とし、Planning、Task、RoleExecution、JobMessage、Provider、Worktree、成果・証拠、Command境界を定義します。
- **ADR-202 — Domain invariants:** 不変条件を生成・変更・保存・復元の全経路で強制する規範を定義します。
- **ADR-300 — Internal communication:** Taxion内部の6 Actor通信、JobMessage、Envelope、MessageKind、配信観測を定義します。
- **ADR-408 — Execution & concurrency:** Git Worktree、Task単位の所有権、Roleの逐次引継ぎ、独立Taskの並行性を定義します。
- **ADR-500 — Job authority:** Job承認境界、禁止副作用、PlanningAuthorization、Observation / Conformance / Admission / Execution Permissionの分離、不完全情報の協調を定義します。
- **ADR-700 — External Agent protocol:** 外部Agent相互運用の必須protocolをA2Aに一本化し、内部JobMessageとの境界を定義します。
- **ADR-800 — Quality & evidence:** 検証証拠の水準とJob完遂の製品受入れを定義します。
- **ADR-811 — Agent Harness:** 製品能力として維持する8要素と、その検証条件を定義します。
- **ADR-000 / ADR-004:** ADR統治と、仕様・計画・履歴・検証証拠の情報責務を定義します。

番号帯は分類であり、優先順位・重要度・規範性を表しません。下位ADRは上位の製品目的・安全境界を上書きせず、具体化します。決定の変更は既存判断を暗黙に書き換えず、新しいADRと明示的なSupersedes関係で記録します。

## 状態

| Status | 意味 |
| --- | --- |
| Proposed | 判断案。実装の規範として扱いません。 |
| Accepted | 現行設計の規範。実装・テストはこれに適合させます。 |
| Superseded | 指定された後続ADRに置き換えられています。 |
| Deprecated | 新規利用を停止した判断。履歴として保持します。 |
| Rejected | 検討したが採用していません。 |

ADRがAcceptedであることは、実装や受入れが完了したことを意味しません。

## カテゴリ別ADR一覧

### 000–099 ADR Governance
- [ADR-000: Architecture Decision Recordsの管理規約](000-adr-governance.md) — Accepted
- [ADR-004: 現行仕様・開発計画・履歴・検証証拠の正本を分離する](004-specification-plan-history-and-evidence.md) — Accepted

### 100–199 Product & Governance
- [ADR-100: 製品目的 — 人間の曖昧な思考を継続可能な協調作業へ昇華する](100-product-purpose.md) — Accepted
- [ADR-101: 旧設計の制約を温存せず、製品目的から再構築する](101-rebuild-over-legacy-constraints.md) — Accepted
- [ADR-106: 制約付き最適化による設計判断の比較方法](106-constrained-design-decision-method.md) — Accepted

### 200–299 Domain Model
- [ADR-201: JobAggregateを作業・協調・成果の唯一のドメイン正本とする](201-job-aggregate-and-command-boundary.md) — Accepted
- [ADR-202: ドメイン不変条件を生成・変更・保存・復元の全経路で強制する](202-domain-invariants-across-state-paths.md) — Accepted

### 300–399 Collaboration
- [ADR-300: Taxion内部の6 Actor通信をJobMessageへ統一する](300-jobmessage-six-actor-communication.md) — Accepted

### 400–499 Execution & Runtime
- [ADR-408: Jobの開発作業をGit Worktree単位で分離し、Roleを逐次引き継ぐ](408-worktree-concurrency-and-ownership.md) — Accepted

### 500–599 Authorization & Security
- [ADR-500: Job承認境界と禁止副作用を中心に実行権限を強制する](500-job-authority-and-prohibited-effects.md) — Accepted

### 600–699 Persistence & Recovery
- 現時点で独立したADRはありません。永続化・復旧判断はJobAggregateと不変条件の責務に沿って必要時に追加します。

### 700–799 Interface & Integration
- [ADR-700: 外部Agent相互運用の必須protocolをA2Aに一本化する](700-a2a-external-agent-interoperability.md) — Accepted

### 800–899 Quality & Evidence
- [ADR-800: 検証証拠の水準とJob完遂の受入れを分離する](800-evidence-levels-and-product-acceptance.md) — Accepted
- [ADR-811: Agent Harnessの8要素を製品能力要求として維持する](811-agent-harness-eight-capabilities.md) — Accepted

### 900–999 Infrastructure & Operations
- 現時点で独立したADRはありません。運用手順は開発・運用文書で管理します。

## 原典と来歴

各ADRは、アーカイブ済みリポジトリの設計資料から現行Taxionに必要な判断だけを確認し、新しい製品目的・安全境界に照らして再構成したものです。旧Git履歴、Issue / PR履歴、旧識別子の対応表は新リポジトリへ移植・保存しません。旧資料は読み取り専用の根拠資料として扱い、現在の判断を理解するために必要な理由・制約・適用範囲は各ADR本文に記載します。

開発時のOS別品質ゲートとCommit前検査は [Development Quality Gates](../development/quality-gates.md) で管理します。
