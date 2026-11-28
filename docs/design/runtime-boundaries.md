# Runtime Boundaries and Data Flow

Date: 2026-11-20
Normative contract: [Product Contract](../specifications/product-contract.md)
Acceptance contract: [Product Acceptance Test Specification](../test-specs/product-acceptance.md)
Provider Adapter requirements: [Provider Adapter 実行境界 要求仕様書](../specifications/provider-adapter-requirements.md)
Provider Adapter design: [Provider Adapter 詳細設計書](provider-adapter-detailed-design.md) / [内部設計書](provider-adapter-internal-design.md)

## 1. Purpose

本書は、Taxionの論理コンポーネント、信頼境界、データ所有、外部副作用の経路を定義する。物理package、DB table、UI framework、Provider SDKの具体的な型は固定しない。各責務は対応するADRに従う。

## 2. Logical components

| Component | Responsibility | Must not own |
| --- | --- | --- |
| User Interface / CLI | 利用者入力、Job表示、承認確認、成果・Evidence表示 | Job authorityや独自の状態正本 |
| Trusted Ingress | 認証済み利用者・Agent endpointのidentityとrequest provenanceを確定 | 自然言語本文からのidentity推定 |
| Controller | Job command、authorization、scope、Effect、Provider operation、recovery、Job completionを制御 | Roleの意味的判断や成果品質の代行 |
| JobAggregate | Job、Task、RoleExecution、JobMessage、Approval、Provider facts、Worktree、Output、Evidenceの正本 | 外部Providerの実行状態を推測したmutable status |
| Role Runtime | Planner / Coder / Reviewerの個別実行とContextを提供 | JobApprovalやresource boundaryの発行 |
| Computer Sandbox Manager | Task単位の独立Computer、filesystem / process / credential / network / host境界を強制 | Job authorityやJob scopeの発行 |
| Context Manager | TurnごとのContext選択、Skill / Fileのon-demand load、履歴圧縮を実施 | Job facts、承認、provenanceの正本 |
| Execution Telemetry | Model / Tool Callとturn / operationの相関、coverage gapと停滞兆候を記録 | 実行権限、deadlineの拡張 |
| Provider Adapter | Provider固有APIを呼び出し、receipt / event / result / stop observationを返す | TaxionのJob identity、承認、認可の正本 |
| A2A Gateway | 外部AgentとのA2A discovery / task / message / artifact相互運用 | 内部JobMessage、JobAggregate、JobApprovalの置換 |
| Persistence | JobAggregate Commandと関連factを原子的に保存・復元 | 別の独立したJob authority store |
| Worktree Manager | Git Worktree作成・割当て・排他・停止後の解放・quarantine | Agentによる任意の所有権変更 |
| Evidence Verifier | 固定対象のEvidenceを検証し、結果とprovenanceを記録 | JobApprovalやExecution Permissionの発行 |

## 3. Trust boundaries

### 3.1 Human ingress

利用者principalは、管理者が明示設定したOIDC issuerによるAuthorization Code Flow with PKCEを検証したTrusted Ingressからのみ確定します。Principalはissuer / subjectに束縛し、email、body、query、任意headerから推定しません。Ingress adapterは検証済みAuthenticatedPrincipalとserver-derived sourceをControllerへ渡し、Controllerは原文依頼digest、request identity、受信時刻とともにIntakeProvenanceをJobAggregateへ保存します。生tokenやcookieは保存しません。自然言語Bodyは意図と情報の入力であり、本人性、承認、実行権限を含む信頼済みmetadataではありません。A2A Agentとservice identityはHUMAN Principalと分離します。認証の詳細はADR-501に従います。ControllerはJobと送受信Actorを信頼できるContextから確定し、JobMessageには必要最小限の識別情報だけを記録します。ReplyToは元の会話へ戻る必要がある場合だけ保持します。

### 3.2 Role execution

Planner / Coder / Reviewerの実行主体はRoleExecutionIDに結び付ける。Role名やProvider sessionだけでidentityを確定しない。Role RuntimeはJobApproval、Task scope、Effect boundaryを受け取り、権限の拡張を行わない。

### 3.3 Provider boundary

Providerは外部実行主体である。Providerが返すevent、tool list、session state、terminal statusはTaxionの承認・Job完了の正本ではない。Provider credentialはController / Provider Adapterが管理し、Model / Sandboxへ露出しません。Provider API通信はAdapterが仲介し、現在のgrantとProvider / account / configurationに束縛します。RoleのToolへ任意のnetwork accessやcredential accessを継承しません。ControllerはOperationIDとJob revisionで相関し、外部結果をJobAggregateへ明示的に記録します。

Provider AdapterのCapabilityはProvider / account / SDK・protocol version / configuration identityへ束縛し、証拠水準を操作単位で検証します。CredentialBroker / CredentialLeaseはAdapter境界内でsecretを仲介し、Runtimeはraw error・responseへのsecret混入を検査してsafe errorへ変換します。詳細はProvider Adapter要求仕様・詳細設計・内部設計を参照します。

### 3.4 External Agent boundary

A2A Gatewayは外部Agentとの唯一の必須相互運用protocolを実装する。外部Agent identity、Agent Card、Message、Artifactは信頼境界を越える入力であり、内部Actor identityや権限へ自動変換しない。A2AのTask stateをJob stateとして直接保存しない。

### 3.5 Persistence boundary

保存データの読込み、復元、migration、recoveryはすべて信頼境界として扱う。schema version、identity、provenance、aggregate invariant、idempotency、revisionを検証する。未知・破損・矛盾したデータを現在のJobへ無条件に取り込まない。

### 3.6 Computer isolation boundary

Taskごとに独立したComputer Sandboxを割り当て、filesystem、process、credential、network、host accessを隔離します。Git Worktree、別Session、別ProcessだけをComputer isolationの証拠にしません。実行環境が必要な境界を強制できない場合は、対象Taskを開始せず未対応条件を明示します。JobApprovalとTask scopeはSandboxの外側でControllerが検証します。

### 3.7 Context and observability boundary

Context Managerは各turnで必要なContextだけを選択し、Skill / Fileをon-demandで読み込み、Context Window上限前に履歴を圧縮します。Job Contract、承認、重要な判断、未解決事項、UNKNOWN、provenanceを圧縮で失いません。Execution TelemetryはModel / Tool CallをJob / RoleExecution / turn / operationへ相関し、Providerが報告しない呼出しやevent欠落をcoverage gapとして残します。

## 4. Main execution paths

### 4.1 Intake and planning

1. Trusted Ingressが利用者principalと原文依頼のprovenanceを確定する。
2. ControllerがDRAFT Jobを作成し、原文依頼をJobAggregateへ保存する。
3. 明示的な利用者起動がある場合、Controllerがimmutable PlanningAuthorizationを発行する。
4. Taskless Plannerは許可されたread-only contextとJob情報から計画・JobContract revisionを提案する。
5. ControllerはObservation / Conformance / Admissionを分離して記録し、Planner提案をJobへ関連付ける。
6. 利用者が正確なJobContract revision hashを承認するまで、Jobは実装実行へ進まない。

### 4.2 Approved implementation

1. Controllerが現在のJobApproval、Task scope、deadline、Runtime configurationを検証する。
2. ExecutionControllerがeffect / resource / Provider identity / request digestを評価し、JobApprovalと最新JobContractに束縛した単一OperationのExecutionPermissionをJobAggregateへ保存する。
3. TaskIDに束縛されたExecutionPermissionをsingle-useで消費し、Coder / Reviewer RoleExecutionとProviderStartIntentをJobAggregate Commandで同時にappendする。ProviderStartIntentは同じPermission ID / OperationIDを参照する。
4. Coding TaskにWorktreeを割り当て、repository identity、Worktree path、baseline commit、ownerを永続化する。
5. Provider呼出し前にOperationIDとimmutable operation intentをJobAggregateへ保存する。
6. Controllerが同じExecutionPermissionを外部副作用の直前に再検証する。
7. Provider Adapterが外部呼出しを行い、受理・event・result・stop observationをOperationIDへ返す。
8. ControllerがObservationを記録し、Conformance / Admissionを別々に判定する。
9. JobOutputとJobEvidenceをimmutable content identityとproducer / verifierへ結び付ける。

### 4.3 Internal communication

1. Sender identityはTrusted Ingressまたは現在のRoleExecutionから確定する。
2. Controllerは同一Job内のRecipientを信頼できるContextから確定する。曖昧な場合だけ確認する。
3. 自然言語BodyをJobMessageとしてJobAggregateへappend-onlyで保存する。MessageKind、BatchID、通信専用grantは要求しない。
4. Orchestratorは保存済みメッセージをRecipientの既存RoleExecution / Sessionへ渡し、受信側は元のJob / Contextで回答する。配送専用の状態機械は設けない。
5. 情報交換はTask assignment、Execution Permission、JobApproval、Worktree ownershipを変更しない。
6. Provider Send / Turnや外部副作用はメッセージ受け渡しと別のoperationとして扱い、必要な認可をADR-505 / ADR-507に従って検証する。
7. 実行責任とWorktree ownershipの移転は通常メッセージと分離し、既存のHandoff条件を検証する。

### 4.4 External A2A communication

1. A2A Gatewayが外部Agentとの接続・identity・protocol状態を確認する。
2. A2A request / response / artifactを外部Observationとして受け取る。
3. ControllerがJobID、承認revision、scope、provenance、content identityを照合する。
4. 必要なConformanceとAdmissionを実施してからJobAggregateへ記録する。
5. 外部Agentの完了状態はJob完了やExecution Permissionを成立させない。
6. 外部通信のUNKNOWNはProvider operationとして照合し、blind replayしない。

## 5. Operation and recovery boundaries

### 5.1 Before external side effect

- OperationIDをTaxion側で生成する。
- JobID、RoleExecutionID、TaskID、operation kind、Provider identity、configuration digest、request hash、authority revisionをintentへ固定する。
- 必要なWorktree reservationを同じJob transactionで確定する。
- intentの永続化に失敗した場合、外部副作用を開始しない。

### 5.2 After external side effect

- Provider receipt、event、terminal result、stop evidenceをappend-only observationとして保存する。
- Provider quiescenceが確定するまで、完了・資源解放を許可しない。Usageの未報告は妨げとしない。
- DB commitとProvider operation resultを同一の成功状態へまとめない。
- timeout、connection loss、receipt欠落はUNKNOWNとして保持する。
- 同じOperationIDで照会できない、またはProvider idempotencyを証明できない場合はblind replayしない。

### 5.3 Recovery

- JobAggregateをrevisionとschema versionを検証して復元する。
- 未解決OperationIDをProviderへ照会し、観測結果をappend-onlyで追加する。
- AdapterがProvider / SDK versionとconfiguration identityに対応して宣言したcapabilityを確認する。Session reattachmentは保存済みの正確なSession / Provider / account / configuration identityに一致する場合だけ許可する。
- Adapterがcross-process reattachmentをサポートしない場合、同じRoleExecutionへ新しいSessionを作成しない。旧operationの停止・resultを照合するまでWorktreeを保持またはquarantineする。
- Worktree owner、Provider stop proof、result、artifact identityを照合する。
- 停止・結果が不明なWorktreeはquarantineし、所有権を解放しない。
- 旧RoleExecutionを解決した後の継続は、新しいRoleExecutionとしてJobApproval、Task scope、deadlineを再検証して開始する。
- 復旧不能な破損・矛盾は隔離し、成功へ推測変換しない。

## 6. Completion boundary

Job完了CommandはControllerだけが実行する。必須受入条件、JobEvidence、未解決Provider operationの不存在、Provider quiescence、Worktree release、未完了RoleExecutionの不存在、認可・安全上の阻害事項を確認する。Usageの未報告は妨げとしない。UI、Role、Provider、A2A GatewayはJob完了状態を直接設定しない。

## 7. Implementation constraints

- JobAggregate以外にJobの独立した正本を作らない。
- Controllerの外側に認可やJob state mutationの迂回路を作らない。
- Provider SDKの型やsession modelをDomain Modelへ漏らさない。
- 内部Actor通信をA2Aで実装しない。
- MCP / ACP / Copilot SDK等を外部Agent連携の必須経路にしない。
- 同一Worktreeのmutating Roleを並行実行しない。
- Git Worktree / Session / Processの分離だけを独立Computer Sandboxの代替として扱わない。
- Model / Tool Callの観測欠落を完全追跡済みとして扱わない。
- Observation、Conformance、Admission、Execution Permissionを一つのbooleanへまとめない。
- 旧Task / Dispatch / ReviewFlow runtime、旧権限モデル、旧fallbackを新しいJob-native経路として残さない。

Provider SDK / credential / capability / operation / recoveryの境界実装は[Provider Adapter 詳細設計書](provider-adapter-detailed-design.md)と[Provider Adapter 内部設計書](provider-adapter-internal-design.md)に従う。要求と受入れ条件は[Provider Adapter 要求仕様書](../specifications/provider-adapter-requirements.md)および[Provider Adapter テスト仕様書](../test-specs/provider-adapter-test-specification.md)を正本とする。

## 8. Traceability

ADR-200 Domain Model、ADR-201 JobAggregate / Command、ADR-202 invariant enforcement、ADR-300 Collaboration、ADR-301 JobMessage、ADR-400 Execution & Runtime、ADR-408 Worktree、ADR-409 Provider operation、ADR-501 Trusted Ingress / HUMAN Principal、ADR-504 JobApproval、ADR-505 PlanningAuthorization、ADR-507 Execution Permission、ADR-700 Interface & Integration、ADR-701 A2A、ADR-801 Evidence、ADR-802 Job completionを具体化する。
