# Taxion Product Contract

Version: 1.0
Date: 2026-11-20
Normative decisions: ADR-001, ADR-100, ADR-101, ADR-200, ADR-201, ADR-202, ADR-300, ADR-301, ADR-400, ADR-408, ADR-500, ADR-501, ADR-700, ADR-701, ADR-800, ADR-811

## 1. Purpose and scope

Taxionは、人間の曖昧な思考を対話で整理し、実行可能な計画へ昇華し、複数Agent / Roleの協調とガードレールによって、人間が意図したJobの完遂を支援する。

本書は、利用者からJob完了までの外部から観測可能な製品契約を定義する。内部の物理schema、DB table、SDK固有型、UI frameworkは固定しない。具体的な設計判断は対応するADRを正本とする。

## 2. Normative principles

1. 利用者は最初から完全な要求仕様や実装手順を提示する必要はない。
2. 目的、制約、受入条件、確定事項、仮定、未解決事項を区別する。
3. Jobを利用者の意図、承認、協調、実行事実、成果、証拠の継続単位とする。
4. 計画、承認、実行許可、観測、受入れ、Job完了を同一視しない。
5. 情報不足だけを理由にRole間の協調を拒否しない。安全・権限・同一性・provenance・整合性の不明確さはfail-closedとする。
6. 内部6 Actor通信はJobMessage、外部Agent相互運用はA2Aとする。
7. 状態変更はJobAggregateの明示的Commandを通し、全経路で不変条件を強制する。
8. 実行状態不明を成功・失敗へ推測変換しない。安全な照合なしにblind resendしない。
9. 製品受入れは検証可能な成果と実Job完遂で判断する。
10. Agent Harnessの能力要求H1〜H8を満たす。能力をProvider / Runtimeへ委譲する場合も、対応構成と実動作の証拠を確認する。
11. 承認済みJob内のTaskless PLANNERへの個別質問実行にはJobQuestionAuthorizationを使用できます。JobMessageやQUESTION種別だけでは認可を発行しません。

## 3. Actors and responsibilities

| Actor / Role | 責務 | 権限上の制約 |
| --- | --- | --- |
| HUMAN | 意図の提示、質問への回答、JobContractの承認、成果確認、介入 | 承認主体は認証済みprincipalで特定する |
| CONTROLLER | ingress、identity、Job scope、認可、Command、永続化、Provider事実、復旧を強制 | 意味的判断を代行せず、会話から権限を推定しない |
| ORCHESTRATOR | Job全体の進行・担当調整・連絡 | Actorであること自体は承認・実行権限を持たない |
| PLANNER | 目的・制約・受入条件の整理、計画・Task候補の提案、必要な質問 | DRAFTではPlanningAuthorizationのread-only範囲のみ |
| CODER | 承認済みJob scope内で成果を作成・修正 | JobApproval、Task scope、budget、deadline、Effect境界に従う |
| REVIEWER | 固定された成果と証拠の独立評価 | Coderと異なるRoleExecution / Contextを用い、read-only境界を強制する |

Roleは作業責務であり、Role名だけで認証済み実行主体やoperation ACLを表現しない。Planner、Coder、Reviewerの実行回数・順序・session寿命をJobの寿命と同一視しません。

### 3.1 HUMAN identity and authentication

- HUMAN Principalは管理者が明示設定したOIDC issuerによるAuthorization Code Flow with PKCEの検証結果からTrusted Ingressが生成します。
- Principalの外部identityはissuer / subjectの組で確定し、email、display name、request body、query、任意header、Role名から推定しません。
- 認証検証とPrincipal生成の詳細はADR-501に従います。外部A2A Agentおよびservice identityはHUMAN Principalではなく、JobApprovalを行えません。
- 認証成功はJobApprovalやExecution Permissionを付与しません。認証とJob authorityは別の判定です。

## 4. User journey and Job lifecycle

### 4.1 Intake

- Trusted IngressがOIDC認証済みHUMAN Principalを確定した後にJobを作成します。
- 利用者の原文依頼を受け付けた時点でJobを作成します。
- 初期状態はDRAFTとし、原文依頼とintake provenanceを保存します。IntakeProvenanceはPrincipal、server-derived source、受信時刻、request identity、原文依頼digestへ束縛します。
- Goal、制約、受入条件は初期時点で未確定でもよい。
- Job作成は実装権限やJobApprovalを付与しない。

### 4.2 Bounded planning

- 明示的な利用者起動に基づき、Taskless PLANNER RoleExecutionへimmutable PlanningAuthorizationを発行できる。
- PlanningAuthorizationはJob、原文依頼hash、認証済み利用者、Provider / account / configuration、repository、read-only境界、permitted effects、budget、deadlineに束縛する。
- 許可範囲はread-only context acquisitionとbounded planningに限定する。
- JobをACTIVEにせず、実装Task、Coder / Reviewer、write、validation、commit、publish、Role / Model / Tool / Sandboxからのcredential access、任意宛先へのnetwork access、external mutationを許可しない。
- Provider Start / Turn / Resumeに必要な認証情報と通信はController / Provider AdapterがModel / Sandboxから分離して仲介し、grantに束縛されたProvider / account / configurationに限定する。
- Provider側でread-onlyを強制できない場合はimmutable snapshotまたは明示文書だけを渡す。それも強制できなければrepository readを許可しない。
- Plannerの提案、回答、会話によってPlanningAuthorizationを拡張しない。

### 4.3 Contract proposal and approval

- Plannerは目的、制約、対象、受入条件、必要Evidence、resource boundary、budget、deadline、禁止事項、承認が必要な副作用、秘密を含まないconfiguration referenceを含むJobContract revisionを提案する。
- 利用者はその正確なimmutable revision / canonical hashを確認して承認する。
- JobApprovalはJobID、revision、proposal hash、canonicalization version、approver identity / source / reason / timeを持つappend-only factとする。
- 承認対象が変わる場合は新revisionと明示的な再承認を要求する。
- TaskはJobの対象・resource・budget・deadlineを狭められるが、拡張できない。
- 会話、Planner proposal、Task、RoleExecution、Providerの自己申告はJobApprovalの代替にならない。

### 4.3.1 JobQuestionAuthorization

承認済みJob内でRoleからTaskless PLANNERへ質問し、個別のbounded read-only executionが必要な場合は、immutable JobQuestionAuthorizationを発行できます。grantはJobApproval、Planner RoleExecution、元Question JobMessage、Provider / account / configuration、read-only context、budget、deadlineへ束縛します。QUESTION本文やMessageKindだけから自動発行しません。DRAFT Jobの初期PlanningAuthorizationとは用途と適用条件を分離します。


### 4.4 Execution and collaboration

- JobはTaskを必須としない。TaskはJobに所属する任意の作業分解である。
- RoleExecutionはJobに所属し、TaskIDは任意とする。Taskless Plannerを許容する。
- Taskの親子包含、Task dependency、Role間協調を別の関係として扱う。
- SchedulerはTaskの明示的dependencyだけを使う。JobMessageやRoleExecutionの親子関係をTask dependencyとして解釈しない。
- Roleは承認済みJob scope内の通常操作を選択できる。通常操作名の静的allowlistやRole-to-operation ACLを権限根拠にしない。
- ControllerはJobApproval、Task scope、resource identity、禁止副作用、実行基盤のcontainment、budget、deadline、現在のProvider / Runtime状態を検証する。
- 可逆で影響の小さい不足情報は仮定と不確実性を明示して進めてよい。不可欠な情報だけを質問する。

### 4.5 Review and correction

- ReviewerはCoderと異なるRoleExecution / Context / sessionで評価する。
- 対象commit / tree / snapshotのimmutable identityを固定する。
- Reviewerのread-only boundaryをProvider / adapter / execution environmentで強制する。
- read-onlyを強制できない場合はimmutable snapshotを使う。用意できない場合はReviewerを開始しない。
- ReviewerのPASSはJobApproval、Execution Permission、Job完了の代替にならない。
- 指摘があれば同じJobへ結び付け、修正・再レビューを継続する。

### 4.6 Completion

Job完了は明示的なJobAggregate Commandでのみ成立する。少なくとも次を確認する。

- 必須受入条件を満たす成果とprovenance-valid Evidenceがある。
- UsageとProvider operation結果が確定している。
- 未解決Provider operation、実行状態UNKNOWN、未完了RoleExecutionがない。
- Provider quiescenceとWorktree releaseが確認されている。
- 未解決の認可・安全・データ整合性問題がない。

### 4.7 Interruption and recovery

- 中断後は同じJobの保存済み事実と現在の外部状態を照合して再開する。
- UNKNOWNを成功・失敗・未送信へ推測変換しない。
- 同じOperationIDでのProvider照会または実証されたidempotencyがない限りblind replayしない。
- Provider停止・Worktree状態が不明な場合は所有権を保持し、Worktreeをquarantineする。
- Provider AdapterはProvider / SDK versionとconfiguration identityに対応するcapabilityを宣言し、Start / Sendのidempotencyまたはlookup、cross-process Session reattachment、event replay / cursor continuity、Stop proof、final output retrieval、Usage identity / completenessを個別に検証します。未対応・未検証の能力を推定で補いません。
- 同じRoleExecutionの再開では、保存済みの正確なProvider Session identity、Provider / account identity、configuration digestが一致し、Adapterがcross-process reattachmentを明示的にサポートする場合だけ再接続します。新しいSessionを同じRoleExecutionへ付け替えてResumeしたことにしません。
- reattachmentできない場合は元のoperationをUNKNOWN / unresolvedのまま保持し、Stop proof、final result、Usageを照合します。新しいRoleExecutionによる継続は、旧Providerのquiescence、result / usage reconciliation、Worktree release後に限り、現在のJobApproval・Task scope・budget・deadlineを再検証して別実行として開始します。
- Start / Sendの結果が不明な場合は、同じOperationID / turn identityによるlookup、またはそのidentityに対して実証されたidempotencyがある場合だけ再試行します。
- 旧データや旧承認を現行Jobの権限へ暗黙変換しない。

## 5. Communication contract

### 5.1 Internal JobMessage

内部6 Actor間通信はJobMessageを使用する。EnvelopeはController / UI / Runtimeが生成・検証し、Bodyは利用者とAgentに共通の自然言語とする。本文中の自己申告でSender、Recipient、Job identity、認可、承認、Handoffを確定しない。

MessageKindは次の意味を持つ。

| Kind | 意味 | 権限効果 |
| --- | --- | --- |
| MESSAGE | 情報共有 | なし |
| QUESTION | 情報照会 | なし |
| ANSWER | 既存QUESTIONへの相関応答 | なし |
| REQUEST | 情報・作業依頼 | なし |
| NOTIFICATION | 通知 | なし |
| HANDOFF | 責任移転intent | 明示的Commandと停止・結果・Usage・Worktree gateを要求 |

基本単位は1 Message・1 Recipientとする。Fan-outは不変BatchIDで相関する。Delivery状態はMessage本体と分離し、REQUESTED / ACCEPTED / FAILED / UNKNOWNを区別する。Exactly-once deliveryを保証せず、MessageIDによる重複排除と追跡可能性を提供する。

### 5.2 External Agent A2A

外部Agentとの相互運用の唯一の必須protocolはA2Aとする。MCP、ACP系方式、Copilot SDK、Provider固有protocolは初期必須構成に含めない。

A2A Task / Message / Artifact / Agent Card / stateはTaxionのJob、RoleExecution、Actor identity、JobApproval、Execution Permission、Job完了状態と同一視しない。外部入力はuntrusted inputとしてControllerが認証、scope、provenance、Admission、不変条件を検証する。

## 6. Data ownership and integrity

| Fact / Model | 正本と責務 |
| --- | --- |
| Job / JobContractRevision | Job目的、原文依頼、制約、受入条件、revision |
| IntakeProvenance | 認証済みPrincipal、Ingress source、受信時刻、request identity、原文依頼digest |
| JobApproval | 承認者と正確なJobContract revisionの束縛 |
| PlanningAuthorization | DRAFT planningの限定的read-only grant |
| JobQuestionAuthorization | 承認済みJob内でTaskless PLANNERへ質問する個別のbounded read-only grant |
| JobProgressNote | Job内の進捗・再開に必要な要点とprovenance |
| Task | Job内の作業分解と明示的dependency |
| RoleExecution | Roleの個々の実行とprovenance |
| JobMessage | Job内協調のappend-only facts |
| DeliveryObservation | Recipient単位の配信結果 |
| ProviderOperation | 外部副作用のintentとOperationID |
| ProviderObservation | Providerの受理、turn、result、usage、stop等の観測事実 |
| WorktreeOwnership | Task単位の排他的所有状態 |
| JobOutput | immutable成果identityとproducer provenance |
| JobEvidence | 受入条件、証拠identity、producer、独立verifier、判定 |
| JobUsage | Provider operation / turnに相関する利用量 |

JobAggregateの変更はexpected revisionとstable idempotency identityを持つCommandを通す。同じCommand IDと同じ内容は同じreceiptへ収束し、同じIDで異なる内容は拒否する。関連レコードはJob単位のtransactionで原子的に確定する。

Observation、Conformance、Admission、Execution Permissionは別の責務とする。観測や受入れの成功から実行権限を推論しない。

## 7. Worktree and concurrency

- Coding executionはGit repository内で行う。
- TaskごとにGit Worktreeを割り当てる。
- 同一Worktreeのmutating RoleExecutionは最大一つとし、Roleを逐次引き継ぐ。
- 独立性が明確なTaskだけを別Worktreeで並行実行する。
- 独立性が不明なTaskは直列化する。
- Repository共通Git metadataの変更はRepository単位で直列化する。
- Provider停止・結果・成果identityが不明なWorktreeは解放・再利用しない。

## 8. Agent Harness capability contract

Agent Harnessの必須能力H1〜H8と各判定項目は [ADR-811](../adr/811-agent-harness-eight-capabilities.md) に従います。製品実装はModel / Tool loop、Computer Sandbox、独立Sub-agent、Context選択・圧縮、Job再開、Credential / Effect guardrails、独立Verification、Model / Tool observabilityを満たします。

Provider / Runtimeへの委譲は許容しますが、APIの存在やProviderの自己申告だけで能力充足と判定しません。対応構成、実行証拠、失敗時挙動を確認します。Worktree、Session、Processの分離だけをComputer Sandboxの証拠にしません。
## 9. Explicit non-goals

- 内部6 Actor通信をA2Aへ置き換えること。
- 複数の外部Agent protocol / SDKを初期必須化すること。
- 固定されたPlanner → Coder → Reviewerの工程順序を強制すること。
- 通常操作の完全な静的allowlistやRole-to-operation ACLを権限の正本にすること。
- 情報不足や任意metadataの欠落だけでRole協調を拒否すること。
- 同一Worktreeのmutating Roleを並行実行すること。
- Taxion独自VCS、汎用Workspace engine、汎用lock graphを先行実装すること。
- fake Provider / in-memory testの成功を実Provider・Job完遂の証拠と扱うこと。
- 旧Task / Dispatch / ReviewFlow等の旧実行・認可経路を新Runtimeとして温存すること。

## 10. Traceability

| Contract area | Governing ADR |
| --- | --- |
| 仕様・計画・履歴・CI証拠の正本 | ADR-001 |
| 製品目的・利用者価値 | ADR-100 |
| 再構築・provenance | ADR-101 |
| JobAggregate・Command・Provider facts | ADR-201 |
| JobApproval・禁止副作用・PlanningAuthorization・Admission | ADR-500 |
| 全経路の不変条件 | ADR-202 |
| 内部6 Actor・JobMessage | ADR-300 / ADR-301 |
| 外部Agent A2A | ADR-700 / ADR-701 |
| Git Worktree・並行性 | ADR-408 |
| 検証水準・Job完遂受入れ | ADR-800 |
| Agent Harness H1〜H8 | ADR-811 |
