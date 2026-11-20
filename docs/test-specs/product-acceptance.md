# Product Acceptance Test Specification

Version: 1.0
Date: 2026-11-20
Governing contract: [Product Contract](../specifications/product-contract.md)
Governing ADRs: ADR-000, ADR-004, ADR-100, ADR-101, ADR-106, ADR-200, ADR-201, ADR-202, ADR-300, ADR-301, ADR-400, ADR-408, ADR-500, ADR-501, ADR-700, ADR-701, ADR-800, ADR-811

## 1. Purpose

本書は、Taxionが人間の意図を保ちながらJobを完遂できることを検証する受入れ仕様を定義する。個々のunit testの実装方法は固定しない。各テスト結果は対象HEAD SHA、実行環境、Provider / SDK version、条件、Run ID、観測結果に対応付ける。

## 2. Evidence levels

| Level | Evidence | 証明範囲 | 証明しないこと |
| --- | --- | --- | --- |
| E1 | Static / unit | 局所ロジック・型・不変条件 | ProviderやJob E2E |
| E2 | Synthetic integration | fake Provider / in-memory契約・順序・異常系 | 実Providerの挙動 |
| E3 | SDK compatibility | 実SDK API / 型 / handshake | 実モデル実行・実Job完遂 |
| E4 | Live Provider | 実Provider callback、identity、turn、usage、stop | process crash後のdurable recovery |
| E5 | Durable recovery | crash / restart後のJob facts・Provider operation・Worktree照合 | 人間のJob完遂能力全体 |
| E6 | Product E2E | 曖昧な依頼から成果確認・Job完了までの実シナリオ | 未試験Provider / OS / configuration |

上位Levelは下位Levelの成功だけから推定しない。未実施のLevelを完了扱いしない。

## 3. Acceptance scenarios

### AT-001: Natural-language intake

**Given** 認証済み利用者が不完全な自然言語依頼を提示する。
**When** Taxionが依頼を受け付ける。
**Then**
- 原文依頼を保持したDRAFT Jobを作成する。
- Goalや受入条件が未確定でもJobを作成できる。
- Job作成だけでJobApprovalや実装権限を付与しない。
- 同一Commandの再送は同じreceiptへ収束する。
- 同じCommand IDで異なるpayloadを送ると拒否し、保存状態を変更しない。

### AT-002: Bounded planning authorization

**Given** DRAFT Jobと明示的な利用者起動がある。
**When** Taskless PlannerへPlanningAuthorizationを発行する。
**Then**
- GrantがJob、原文依頼hash、利用者principal、Provider / account / configuration、repository、read-only boundary、permitted effects、budget、deadlineに束縛される。
- JobはDRAFTのままである。
- repository write、validation、commit、publish、credential、network、external mutationが拒否される。
- Provider側read-onlyを強制できない場合はsnapshot / explicit documentsだけを使用し、それもできない場合はrepository readを拒否する。
- grant scopeの変更は既存grantの変更ではなく新しいgrantとして記録される。
- start、各turn、各effect、resumeの前に再検証される。

### AT-003: Incomplete-information collaboration

**Given** Roleが任意metadataや一部情報を欠く自然言語依頼を受け取る。
**When** 既存Contextと成果物から判断する。
**Then**
- 情報不足だけを理由にJob / Handoff / Messageを拒否しない。
- 判断可能な範囲は仮定と不確実性を明示して進める。
- 不可欠な情報だけを質問し、既存Contextから分かることを聞き直さない。
- 認証、認可、identity、provenance、秘密情報、scope、実行不明の不確実性はfail-closedとする。

### AT-004: Exact JobApproval binding

**Given** PlannerがJobContract revisionを提案する。
**When** 利用者が承認する。
**Then**
- Approvalが正確なJobID、revision、canonical hash、approver identity / source / reason / timeに束縛される。
- 対象、scope、budget、deadline、禁止事項、configuration referenceの変更で旧Approvalは無効になる。
- TaskはJob scopeを狭められるが拡張できない。
- RoleExecutionのprivileged operation直前にApprovalと現在のscope / budget / deadlineを再検証する。

### AT-005: JobMessage semantics and actor policy

**Given** 6 Actorの認証済みendpoint間でJobMessageを送る。
**When** MESSAGE / QUESTION / ANSWER / REQUEST / NOTIFICATION / HANDOFFを処理する。
**Then**
- EnvelopeのSender / Recipient / Job / Task / RoleExecution identityはtrusted contextから確定する。
- Bodyは自然言語であり、利用者へrouting metadataや固定JSONを要求しない。
- 全30の非自己宛てActor関係を表現できるが、共通policyが許可を制御する。
- ANSWERは既存QUESTIONにReplyToで相関する。
- Question / Answer / Request / Notificationは実行権限を移転しない。
- 承認済みJob内のTaskless PLANNERへの個別質問実行はJobQuestionAuthorizationに束縛し、QUESTION本文やMessageKindだけでgrantを作成しない。
- BatchIDはfan-out全体で不変であり、Delivery状態はRecipientごとに分離する。
- UNKNOWNを未配信と推測せずblind resendしない。

### AT-006: Handoff safety

**Given** 送信RoleExecutionがWorktreeを所有している。
**When** 別RoleへHANDOFFする。
**Then**
- 受信Roleと受信Task scopeが明示される。
- 送信側Providerの停止証拠、terminal result / usage照合、成果identity固定、Worktree releaseが永続化されるまで受信側はrunnableにならない。
- WAITING、SUSPENDED、terminal status、Handoff messageの存在だけで停止を証明しない。
- 停止や結果がUNKNOWNの場合はWorktreeを保持またはquarantineする。

### AT-007: External Agent A2A boundary

**Given** Taxionが外部Agentと協調する。
**When** A2A Agent discovery、Task、Message、Artifact、stateを受信・送信する。
**Then**
- 外部Agent相互運用の標準必須経路はA2Aである。
- 内部6 Actor通信はJobMessageのまま維持する。
- Agent Cardや外部自己申告だけで内部Actor identity / authorityを付与しない。
- A2A terminal stateやArtifactだけでJob完了やExecution Permissionを成立させない。
- 外部入力はObservation / Conformance / Admissionの境界を通り、JobAggregateの不変条件を満たした場合のみ記録する。
- 切断、重複、遅延、UNKNOWNを成功と推測しない。

### AT-008: Provider operation idempotency and UNKNOWN

**Given** Provider operationを開始する。
**When** 外部呼出しの前後でprocess crash、timeout、receipt欠落が発生する。
**Then**
- 呼出し前にTaxion生成OperationIDとimmutable intentが永続化される。
- Provider observationsはOperationIDへ相関するappend-only factsとなる。
- DB transaction resultとProvider operation resultを別々に保持する。
- ambiguous resultはUNKNOWNとなる。
- Provider照会または実証されたidempotencyがない限りblind replayしない。
- restart後にin-process completionを復元したことにしない。
- Adapter capabilityはProvider / SDK versionとconfiguration identityに対応付け、Start / Sendのidempotencyまたはlookup、cross-process Session reattachment、event replay / cursor continuity、Stop proof、final output retrieval、Usage identity / completenessを個別に宣言・検証する。
- 同じRoleExecutionのSession再接続は、保存済みの正確なSession / Provider / account / configuration identityが一致し、Adapterがcross-process reattachmentを明示的にサポートする場合だけ成功する。
- Start / SendのUNKNOWNを、新しいOperationID / turn identityで再送しない。照合または同じidentityに対する実証済みidempotencyを要求する。
- Stop notificationだけでは停止を認定しない。Stop proof、terminal result、Usageを照合する。

### AT-009: Worktree ownership and concurrency

**Given** 複数Taskと複数RoleExecutionが存在する。
**When** coding executionを開始・引継ぎ・並行実行する。
**Then**
- coding executionはGit repository内で行う。
- 独立Taskごとに別Worktreeを割り当てる。
- 同一Worktreeのmutating RoleExecutionは最大一つとする。
- 独立性が不明なTaskは直列化する。
- Repository共通Git metadataの変更はRepository単位で直列化する。
- Reviewerは固定成果を独立Context / sessionとread-only boundaryで検証する。
- merge conflictは明示的な統合Taskで解決し、暗黙に上書きしない。

### AT-010: Observation / Conformance / Admission / Execution Permission separation

**Given** 外部入力、Provider result、成果、Evidenceが到着する。
**When** Controllerが判定する。
**Then**
- Observationは対象、provenance、時刻、scopeを記録する。
- Conformanceは固定契約との適合を副作用なしで評価する。
- AdmissionはJobへの入力・履歴として登録可能かを判定する。
- Execution PermissionはJobApproval等に基づき特定effectを許可する。
- いずれの成功も他の段階を暗黙に成立させない。
- UNKNOWNやidentity / provenance不明は安全側で保留・隔離・拒否する。

### AT-011: Independent review and evidence

**Given** Coderが成果を生成しReviewerが評価する。
**When** Reviewerが結果を記録する。
**Then**
- review targetのimmutable digestが固定される。
- ReviewerはCoderと異なるRoleExecution / Context / sessionを使用する。
- Evidenceは受入条件、対象digest、producer、verifier、結果へ束縛される。
- Coderの自己申告やMessage本文だけでEvidenceをPASSにしない。
- 対象digestが変わった場合は古いreviewを無効にする。

### AT-012: Durable recovery

**Given** Job、Provider operation、Worktree ownershipが永続化されている。
**When** process restart / crash後にJobを再開する。
**Then**
- 保存済みJob factsと現在のProvider / filesystem状態を照合する。
- 破損、schema不明、provenance不一致、矛盾は安全側で隔離・拒否する。
- 未確定のProvider resultやstop stateを成功・失敗へ推測変換しない。
- Worktree ownershipはstop proofとresult / usage reconciliation前に解放しない。
- Adapterがcross-process reattachmentをサポートする場合だけ、同一RoleExecutionを保存済みの正確なProvider Sessionへ再接続する。
- reattachmentが未対応・未検証の場合、同じRoleExecutionへ新しいSessionを作成しない。旧operationを未解決として保持し、Worktreeを保持またはquarantineする。
- 新しいRoleExecutionによる継続は、旧Providerのquiescence、terminal result、Usage、Worktree releaseを確認した後に限り、現在のJob authorityを再検証して開始する。
- 古いRoleExecutionやownerの遅延更新はrevision / fencingで拒否する。

### AT-013: End-to-end Job completion

**Given** 人間が曖昧な自然言語依頼を提示する。
**When** TaxionがPlanner、Coder、Reviewer、人間、必要な外部A2A Agentと協調する。
**Then**
- 目的・制約・受入条件を対話で整理できる。
- 質問・回答・判断・計画変更が同じJobに関連付く。
- JobContractの正確なrevisionが人間に承認される。
- Coderの成果を独立Reviewerが固定対象で検証する。
- 必要な修正と再レビューを同じJobで継続できる。
- Evidence、Usage、Provider quiescence、Worktree release、未解決operationの不存在を確認してJobを完了する。
- 中断・再起動後も同じJobを安全に再開できる。

### AT-014: Agent / Tool execution loop (H1)

**Given** Job内のRoleExecutionがModel Contextと承認済みTool能力を持つ。
**When** ModelがTool実行を要求し、Toolが結果を返す。
**Then**
- Contextが対象RoleExecutionとJob scopeに束縛されます。
- Tool結果が次のModel turnへ返されます。
- Task終了判定が必須条件と実行結果に基づきます。
- Tool submission acknowledgementだけでTask完了にしません。

### AT-015: Computer sandbox isolation (H2)

**Given** 複数Taskが異なる実行環境で開始されます。
**When** 各TaskがTerminal、File、Browser、接続Applicationを必要な範囲で利用します。
**Then**
- 各TaskのComputer isolationがfilesystem、process、credential、network、host accessの境界で検証されます。
- Git Worktree、別Session、別Processだけを独立Computerの証拠にしません。
- 不適切なCommandの影響がSandbox内に封じ込められます。
- 承認されていないTool能力や外部副作用は拒否または承認待ちになります。

### AT-016: Independent sub-agent delegation (H3)

**Given** Main RoleExecutionが独立して実行可能なSide Taskを持ちます。
**When** Sub-agentへ委譲し、並行実行します。
**Then**
- Sub-agentは独立Context / Sessionで開始されます。
- 親Jobのbudget、deadline、scope、concurrency ceilingを超えません。
- Sub-agentは短いSummaryと成果identityを親Jobへ返します。
- 重複実行や依存Taskの先行実行を許可しません。

### AT-017: Context selection and compaction (H4)

**Given** RoleExecutionが複数turnを継続し、Context Windowの上限へ近づきます。
**When** Contextを選択し、Skill / Fileを読み込み、履歴を圧縮します。
**Then**
- 各turnで必要なContextだけを選択します。
- Skill / FileはTaskが必要とする場合にのみ読み込みます。
- 圧縮前後でJob Contract、承認、重要な判断、未解決事項、実行UNKNOWN、provenanceを保持します。
- Context不足や圧縮を理由に権限・承認を推定しません。

### AT-018: Durable memory and same-Job resume (H5)

**Given** Jobの途中でProcessまたはSessionが中断します。
**When** 新しいSessionから同じJobを再開します。
**Then**
- Progress Note、Git履歴、Skill等の必要情報を再取得できます。
- JobAggregateの確定済み事実と現在のProvider / filesystem状態を照合します。
- 未確定情報やUNKNOWNを成功・失敗へ推測変換しません。
- 独立したMemory storeをJobAggregateの代替正本にしません。

### AT-019: Guardrails and credential isolation (H6)

**Given** RoleExecutionがEmail、Payment、Delete等の高リスク操作や秘密情報へ接触し得ます。
**When** Prompt Injectionを含むuntrusted inputを処理し、操作を要求します。
**Then**
- 高リスク操作は明示的なJob authorityと必要な人間承認を要求します。
- 実認証情報はModel / Sandboxから分離し、必要な操作はController / Adapter経由で仲介します。
- Prompt InjectionがCredential取得や外部送信を誘発しても実行境界で拒否します。
- Skill / Promptの指示だけを安全境界としません。

### AT-020: Verification before completion (H7)

**Given** AgentがTaskの成功Summaryを返します。
**When** ControllerがTask / Jobの完了可否を判定します。
**Then**
- Summaryだけでは完了を許可しません。
- 必須Test、Browser Checkまたは独立評価の証拠を確認します。
- 対象成果物のimmutable identityとEvidence provenanceを照合します。
- 必須Checkが未実施・失敗・UNKNOWNの場合は完了を許可しません。

### AT-021: Model / Tool observability and stagnation (H8)

**Given** Model CallとTool Callが複数turnにわたって実行されます。
**When** Callの追跡と失敗反復の検知を行います。
**Then**
- 観測可能なすべてのModel / Tool CallをJob、RoleExecution、Operation、Turnへ相関します。
- Providerが報告しないCallや欠落eventは未観測として明示し、完全追跡済みと表示しません。
- 同じ失敗した編集の反復を検知し、別Approachを提案できます。
- 停滞検知は権限、budget、deadline、retry permissionを拡張しません。

### AT-022: Trusted HUMAN identity and intake provenance

**Given** 利用者がWeb / CLI / APIからJob intakeまたはJobApprovalを要求する。
**When** Trusted Ingressが認証し、Controllerへrequestを渡す。
**Then**
- 設定済みOIDC issuerのAuthorization Code Flow with PKCEだけを人間認証の標準経路とする。
- 未登録issuer、署名不正、audience不一致、期限切れ、nonce / state / PKCE不一致を拒否し、JobやApproval等の副作用を発生させない。
- PrincipalIDは検証済みissuer / subjectの組に安定して束縛され、email / display name変更や一致で自動統合しない。
- request body、query、任意headerに含まれるprincipal identityを無視または拒否する。
- A2A Agent identityとservice identityをHUMAN approverとして扱わない。
- IntakeProvenanceはPrincipal、server-derived ingress source、認証時刻、受付時刻、request identity、原文依頼digestをJobAggregateと同じtransactionで保存する。
- token、cookie、secretをJob、provenance、ログへ保存しない。
- 未認証のローカルCLI DRAFT作成は製品trusted intakeとみなさず、productionのPlanningAuthorization / JobApprovalへ接続しない。

## 4. Evidence recording

各受入れ結果は次の情報を持つ。

- 対象HEADの完全なSHA
- test / scenario ID
- OS / architecture / runtime
- Provider / SDK / protocol version（該当時）
- 実行条件とfixture / configuration identity
- GitHub Actions Run IDまたは再現可能な実行記録
- PASS / FAIL / INCONCLUSIVE / NOT RUN
- 観測した事実、未確認事項、失敗理由

未実施のシナリオをPASSにしない。synthetic evidenceをlive Provider evidenceとして扱わない。CI結果を後続HEADへ流用しない。

## 5. Product acceptance rule

Taxionの製品受入れはAT-013の実Job完遂を中心に判定する。AT-001〜AT-012およびAT-014〜AT-022は必要な下位契約・能力検証であり、個別に合格してもAT-013の代替にはならない。


