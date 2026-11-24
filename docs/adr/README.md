# Architecture Decision Records

このディレクトリは、現在のTaxionで採用する設計判断の正本です。ADRの管理・改訂・来歴規約は [ADR-000](000-adr-governance.md) を参照してください。

各カテゴリの00番ADRは基本設計方針と責務を定義します。個別ADRは00番の有無にかかわらず、カテゴリ内の最初の利用可能な非00番へ採番します。00番がないカテゴリでは、個別ADRがカテゴリの責務に従って独立した判断を記録します。後から00番を追加しても既存ADRは再採番しません。たとえばADR-600がない場合、最初の個別判断はADR-601とします。

## 決定の階層

- **ADR-000 — ADR Governance:** ADRの管理規約とカテゴリ00番の役割を定義します。
- **ADR-100 — Product & Governance:** Taxionの製品目的を定義します。
- **ADR-200 — Domain Model:** Jobを中心とするドメインモデルの基本方針を定義します。
- **ADR-300 — Collaboration:** Jobを中心とする協調と、情報交換・実行責任移転の区別を定義します。
- **ADR-400 — Execution & Runtime:** Controllerが管理する実行ライフサイクルと、外部実行事実の扱いを定義します。
- **ADR-500 — Authorization & Security:** Jobの承認範囲と禁止副作用を中心に権限を強制する基本方針を定義します。
- **ADR-700 — Interface & Integration:** 外部連携のAdapter境界と内部Job契約との関係を定義します。
- **ADR-800 — Quality & Evidence:** 検証証拠と製品受入れを区別する基本方針を定義します。
- 個別の設計判断は各カテゴリの下位ADRに記録します。カテゴリ00番ADRは一覧や下位ADRの要約ではなく、カテゴリに共通する基本設計方針を定義します。

番号帯は分類であり、優先順位・重要度・規範性を表しません。下位ADRは上位の製品目的・安全境界を上書きせず、具体化します。Accepted ADRは現在有効な判断を示し、方針変更時は同じ設計課題を扱う既存ADRを直接改訂します。独立した設計課題を扱う場合に限り、新しいADRを作成します。

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
- [ADR-001: 現行仕様・開発計画・履歴・検証証拠の正本を分離する](001-specification-plan-history-and-evidence.md) — Accepted

### 100–199 Product & Governance
- [ADR-100: 製品目的 — 人間の曖昧な思考を継続可能な協調作業へ昇華する](100-product-purpose.md) — Accepted
- [ADR-101: 旧設計の制約を温存せず、製品目的から再構築する](101-rebuild-over-legacy-constraints.md) — Accepted
- [ADR-106: 制約付き最適化による設計判断の比較方法](106-constrained-design-decision-method.md) — Accepted

### 200–299 Domain Model
- [ADR-200: Domain Modelの基本設計方針](200-domain-model-principles.md) — Accepted
- [ADR-201: JobAggregateを作業・協調・成果の唯一のドメイン正本とする](201-job-aggregate-and-command-boundary.md) — Accepted
- [ADR-202: ドメイン不変条件を生成・変更・保存・復元の全経路で強制する](202-domain-invariants-across-state-paths.md) — Accepted

### 300–399 Collaboration
- [ADR-300: Collaborationの基本設計方針](300-collaboration-principles.md) — Accepted
- [ADR-301: Taxion内部の6 Actor通信をJobMessageへ統一する](301-jobmessage-six-actor-communication.md) — Accepted

### 400–499 Execution & Runtime
- [ADR-400: Execution & Runtimeの基本設計方針](400-execution-runtime-principles.md) — Accepted
- [ADR-408: Jobの開発作業をGit Worktree単位で分離し、Roleを逐次引き継ぐ](408-worktree-concurrency-and-ownership.md) — Accepted
- [ADR-409: Provider operationの意図と観測をJob内で分離して管理する](409-provider-operation-intent-and-observation.md) — Accepted

### 500–599 Authorization & Security
- [ADR-500: Jobの承認範囲と禁止副作用を中心に権限を強制する](500-job-authority-and-prohibited-effects.md) — Accepted
- [ADR-501: Trusted Ingressで認証済みHUMAN Principalを確定する](501-trusted-ingress-and-human-principal.md) — Accepted
- [ADR-502: 人間起点の依頼とAgent実行先を分離する](502-human-originated-intake-and-agent-routing.md) — Accepted
- [ADR-503: ACTIVE Jobの契約変更は独立したHUMAN authorityで行う](503-active-job-contract-amendment-authority.md) — Accepted
- [ADR-504: JobApprovalを承認済みJobContract revisionへ束縛する](504-job-approval-contract-binding.md) — Accepted
- [ADR-505: DRAFT planningを限定的なread-only authorityとして分離する](505-draft-planning-authorization.md) — Accepted
- [ADR-506: Job内の質問実行権限を独立したgrantとして扱う](506-job-question-authorization.md) — Accepted
- [ADR-507: 実行権限をoperation単位に限定し、観測・適合・登録と分離する](507-operation-scoped-execution-permission.md) — Accepted

### 600–699 Persistence & Recovery
- 現時点で独立したADRはありません。永続化・復旧に関する判断はDomain ModelおよびExecution & Runtimeの責務と整合させ、必要時に追加します。

### 700–799 Interface & Integration
- [ADR-700: Interface & Integrationの基本設計方針](700-interface-integration-principles.md) — Accepted
- [ADR-701: 外部Agent相互運用の必須protocolをA2Aに一本化する](701-a2a-external-agent-interoperability.md) — Accepted

### 800–899 Quality & Evidence
- [ADR-800: 検証証拠と製品受入れを区別する](800-evidence-levels-and-product-acceptance.md) — Accepted
- [ADR-801: 検証証拠の主張を実証済みの境界に限定する](801-verification-evidence-boundaries.md) — Accepted
- [ADR-802: Job完了を証拠に基づく明示的なAggregate判断とする](802-job-completion-acceptance.md) — Accepted
- [ADR-811: Agent Harnessの能力要求を製品契約として維持する](811-agent-harness-eight-capabilities.md) — Accepted

### 900–999 Infrastructure & Operations
- 現時点で独立したADRはありません。共通方針または個別の設計判断が必要になった時点で追加します。

## 原典と来歴

各ADRは、アーカイブ済みリポジトリの設計資料から現行Taxionに必要な判断だけを確認し、新しい製品目的・安全境界に照らして再構成したものです。旧Git履歴、Issue / PR履歴、旧識別子の対応表は新リポジトリへ移植・保存しません。旧資料は読み取り専用の根拠資料として扱い、現在の判断を理解するために必要な理由・制約・適用範囲は各ADR本文に記載します。

開発時のOS別品質ゲートとCommit前検査は [Development Quality Gates](../development/quality-gates.md) で管理します。
