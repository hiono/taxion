# Architecture Decision Records

このディレクトリは、現在のTaxionで採用する設計判断の正本です。ADRの管理・改訂・来歴規約は [ADR-000](000-adr-governance.md) を参照してください。

## 決定の階層

- **ADR-100 — 製品目的:** Taxionが利用者に提供する価値と完成評価の基準を定義します。
- **ADR-101 — 再構築原則:** 製品目的を実現するために、旧設計の制約を温存せず再構築する方法を定義します。
- **ADR-201 — JobAggregate:** Jobを作業・協調・成果の唯一のドメイン正本とし、Planning、Task、RoleExecution、JobMessage、Provider、Worktree、成果・証拠、Command境界を定義します。
- **ADR-500 — Job authority:** Job承認境界、禁止副作用、PlanningAuthorization、Observation / Conformance / Admission / Execution Permissionの分離、不完全情報の協調を定義します。
- **ADR-202 — Domain invariants:** 不変条件を生成・変更・保存・復元の全経路で強制する規範を定義します。
- **ADR-300 — Internal communication:** Taxion内部の6 Actor通信、JobMessage、Envelope、MessageKind、配信観測を定義します。
- **ADR-700 — External Agent protocol:** 外部Agent相互運用の必須protocolをA2Aに一本化し、内部JobMessageとの境界を定義します。
- **ADR-408以降:** Worktree並行性、情報の正本、証拠管理、Agent Harness能力などの具体的な設計判断を定義します。ADR-100〜0007に適合させます。

ADR-100は「何を実現するか」、ADR-101は「どの原則で作り直すか」、ADR-201は「Jobをどのようなドメイン正本として扱うか」、ADR-500は「誰がどの境界内で何を実行できるか」、ADR-202は「すべての状態変更経路でどう不変条件を守るか」、ADR-300は「Taxion内部でどう協調するか」、ADR-700は「外部Agentとどう相互運用するか」を定義します。下位ADRはこれらを上書きせず、具体化します。決定の変更は既存判断を暗黙に書き換えず、新しいADRと明示的なSupersedes関係で記録します。

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
- 現時点で新Taxionに独立したADRはありません。必要な永続化・復旧判断は、JobAggregateと不変条件の責務に沿って追加します。

### 700–799 Interface & Integration
- [ADR-700: 外部Agent相互運用の必須protocolをA2Aに一本化する](700-a2a-external-agent-interoperability.md) — Accepted

### 800–899 Quality & Evidence
- [ADR-800: 検証証拠の水準とJob完遂の受入れを分離する](800-evidence-levels-and-product-acceptance.md) — Accepted
- [ADR-811: Agent Harnessの8要素を製品能力要求として維持する](811-agent-harness-eight-capabilities.md) — Accepted

### 900–999 Infrastructure & Operations
- 現時点で新Taxionに独立したADRはありません。運用手順は開発・運用文書で管理します。
## 原典と来歴

ADR-100は旧ADR-0088、ADR-101は旧ADR-0081、ADR-201は旧ADR-0083と旧ADR-0077を原典として再構成しました。ADR-500は旧ADR-0080、0073、0066、0067のAccepted decisionを確認して再確立しました。ADR-202は旧ADR-0079、ADR-300は旧ADR-0087、ADR-700は旧ADR-0108を原典として再確立しました。ADR-408は旧ADR-0082のWorktree ownership / concurrency decisionを、ADR-004は旧ADR-0085の仕様・計画・履歴・検証証拠の分離を新Taxion向けに再構成しました。ADR-800は検証証拠の水準とJob完遂の製品受入れを定義します。旧PoC候補実装の採用評価はADR-101で扱います。ADR-811は旧ADR-0062のH1〜H8を製品能力要求として保持します。開発時のOS別品質ゲートとCommit前検査は [Development Quality Gates](../development/quality-gates.md) で管理します。

旧Taxionと旧Taxion-PoCのGit履歴、Commit、Issue / PR履歴は移植していません。旧リポジトリの原文は読み取り専用の根拠資料であり、旧番号は原典を識別する参照としてのみ使用します。旧ADRから新Taxionへの判断対応は [Historical ADR Source Decision Map](source-decision-map.md) で追跡します。旧Taxion-PoCの初期READMEは実験専用と記載していましたが、後続の旧Taxion ADR-0107が候補実装ベースラインとして位置付けを変更した経緯を保持します。旧コードの採用はADR-000およびADR-101のprovenance・設計適合性・安全性・受入れ条件に従って別途判断します。
