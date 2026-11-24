# Provider Adapter 詳細設計書

Date: 2026-11-21
Status: Draft
対象Issue: #29
要求仕様: [Provider Adapter 実行境界 要求仕様書](../specifications/provider-adapter-requirements.md)
規範: ADR-201 / ADR-202 / ADR-400 / ADR-408 / ADR-409 / ADR-500 / ADR-507 / ADR-801

## 1. 設計方針

1. Provider固有SDKをDomain Modelへ持ち込まない。
2. JobAggregateとControllerを実行権限・実行状態の正本とする。
3. Provider Adapterは外部APIの変換と実行事実の取得だけを担う。
4. RuntimeはCapability gate、credential lease、request / response validation、error sanitizationを担う。
5. Controllerはauthority再検証、intentの先行永続化、外部呼出し、Observation保存を順序制御する。
6. Capabilityの証拠水準は能力ごとに評価する。無関係な能力の未検証を理由に、検証済み能力まで一律に停止しない。
7. UNKNOWNを通常の失敗へ変換しない。

## 2. コンポーネント構成

| Component | Package | 責務 |
|---|---|---|
| Provider Adapter | internal/provider | Provider APIを共通契約へ変換 |
| Capability Catalog / Verifier | internal/provider | identityとEvidence artifactを照合し、利用可能能力を確定 |
| Credential Broker | internal/provider | Provider / accountに対応するsecretを解決 |
| Credential Lease | internal/provider | secretの短時間参照、Close、best-effort zeroize |
| Provider Runtime | internal/provider | Adapter呼出し、Capability gate、secret検査、応答検証 |
| Provider Controller | internal/controller | Job authority、intent、Runtime呼出し、Observation保存を制御 |
| Domain | internal/domain | Provider identity / intent / Observation / Usageの不変条件 |
| Store | internal/store | JobAggregateとreceiptをexpected revisionで原子的に保存 |

Provider SDKのClient、Session、Event型はAdapter package内に閉じ込める。

## 3. 依存関係

```text
CLI / API
   |
   v
Controller -----> Job Store -----> JobAggregate
   |
   +---- authority validation
   |
   v
Provider Runtime
   |        | 
   |        +---- Capability Verifier / Catalog
   |
   +---- Credential Broker
   |
   v
Provider Adapter
   |
   v
External Provider
```

Role Runtime / Model / Tool / SandboxからCredentialBrokerまたはProvider SDKへ直接アクセスする経路を設けない。

## 4. Provider identity

`provider.Identity`は次の値を持つ。

- ProviderID
- AccountID
- SDKVersionまたはProtocolVersion
- ConfigurationSHA256

identityの完全一致をCapability、credential、Session、operationに適用する。PlanningAuthorizationはProvider ID、account、configuration digestを固定し、configuration digestにはSDK / protocol versionを含める。trusted Capability Evidence catalogはSDK / protocol versionを含むIdentity全体を照合する。Provider名だけ、accountだけ、configurationだけによる流用を許可しない。

Domainへ保存する場合はSDK固有型を使用せず、文字列identityとconfiguration digestを保存する。

## 5. Capability設計

### 5.1 DeclarationとVerificationの分離

Adapterが返すCapability declarationは、Adapterが実装していると主張する能力の一覧である。Declaration自体を検証証拠として扱わない。

Capability Verifierは次を照合する。

- Provider identityの完全一致
- Capability名
- Evidence Level
- TestID / verification profile
- Evidence artifactのdigest
- 検証日時と対象SDK / protocol version
- Evidence artifactの信頼元と改ざん有無

検証済みCapabilityだけをRuntimeへ渡す。Adapterが返した任意のEvidenceLevelやdigestをRuntimeがそのまま信頼する構造にしない。

### 5.2 操作別gate

| Runtime operation | 必須Capability | 最低Evidence |
|---|---|---|
| Start | Start idempotencyまたはStart lookup | E4 |
| Reviewer Start | Reviewer read-only snapshot enforcement | E4 |
| Send | Send idempotencyまたはSend lookup | E4 |
| Stop | Stop proof | E4 |
| Stop recovery lookup | Stop lookup（同一OperationID / SessionID） | E4 |
| Stop recovery after DispatchStart + NOT_FOUND Claim reacquisition | Stop dispatch fencing | E4 |
| Resume | Cross-process reattachment | E5 |
| Lookup | 対象がStartかSendかに対応するlookup | E4 |
| Result | Final output retrieval | E4 |
| Usage | Usage identityとUsage completeness | E4 |
| ReplayEvents | Event replay / cursor continuity | E5 |

Capabilityの検証は操作単位で行う。たとえばEvent replayがE2の段階でも、Start idempotencyがE4であればStartは許可できる。Event replayは拒否する。

### 5.3 Evidence Level

- E2: fake Adapterによるsynthetic integration
- E3: SDK / protocol compatibility
- E4: 実Provider接続と実応答の検証
- E5: process restart / crash後のdurable recovery検証

E2テストでE4 / E5を模擬した証拠を生成しない。Runtimeの本番constructorはE4 / E5の信頼済みEvidenceだけを受け付ける。E2 integrationはRuntime interfaceを使ったtest-only構成で実行し、本番経路へ切替可能なフラグを公開しない。

## 6. Adapter contract

Adapterは次の操作を提供する。

| Method | Request | Result | 意味 |
|---|---|---|---|
| Start | StartRequest | StartResult | Provider Sessionを開始 |
| Send | TurnRequest | TurnResult | 指定SessionでTurnを送信 |
| Stop | StopRequest | StopResult | Session / operationを停止 |
| Resume | ResumeRequest | ResumeResult | 同一Sessionへ再接続 |
| Lookup | LookupRequest | LookupResult | 同一OperationID / TurnIDを照会 |
| Result | ResultRequest | Result | terminal outputを取得 |
| Usage | UsageRequest | Usage | 確定利用量を取得 |
| ReplayEvents | ReplayEventsRequest | EventPage | 保存cursor以降のeventを再取得 |

### 6.1 Request共通条件

- JobID / RoleExecutionID / OperationID / TurnID / SessionIDを必要な操作に含める。
- request digestとprompt digestを検証する。
- deadlineを必須とし、Controllerのgrant deadlineを超えない。
- Provider / account / SDK / configuration identityはRuntimeが選択済みAdapter identityへ束縛する。
- Context documentはID、content digest、実contentの一致を検証する。
- Snapshot modeはReferenceと一致する単一documentを要求する。
- Documents modeはID昇順で「ID + LF + SHA256 + LF」を連結したSHA-256を境界digestと照合する。

### 6.2 Result共通条件

- Requestで指定したOperationID / TurnID / SessionIDとの一致を検証する。
- Provider receipt、event ID、usage IDはProvider identityの範囲で一意とする。
- ResultはFinal=trueを必須とし、contentは有効なUTF-8で1 MiB以下とする。
- terminal resultはcontent digestとevidence digestを持つ。
- raw Provider errorは返さず、許可されたFailureClass / OperationCodeへ変換する。
- credential検出または応答identity不一致は成功として採用せずUNKNOWNへ分類する。

### 6.3 Usage

Usage resultは次を持つ。

- OperationID / SessionID / optional TurnID
- UsageID
- token数
- Complete（確定値であることを示す）
- EvidenceSHA256

Complete=falseの値は途中ObservationとしてJobへ保存できるが、最終Usageとして確定しない。後続の照合で同一UsageIDの確定値へ更新する場合は、既存Observationを書き換えずComplete=trueの新しいObservationを追加する。Complete=trueの後に同じUsageIDのObservationを追加しない。EvidenceSHA256は途中値・確定値の両方で必須とする。

### 6.4 Stop

Stop resultは次を持つ。

- OperationID / SessionID
- Stopped
- Quiescent（Providerが後続書込みを継続しないことを確認済み）
- EvidenceSHA256

Stopped=trueだけではWorktree解放やJob完了を許可しない。Quiescent=trueとterminal result、Usageを照合する。

### 6.5 Event replay

EventPageはSessionID、FromCursor、NextCursor、Events、HasMore、EvidenceSHA256を持つ。各EventはEventID、OperationID、optional TurnID、Kind、PreviousCursor、Cursor、ObservedAt、EvidenceSHA256を持つ。

- page内のcursor chainを検証する。
- 保存済みcursorとFromCursorを一致させる。
- page内およびJob内でEventID / cursorの重複を拒否する。
- cursorの逆行、欠落、分岐を拒否する。
- eventのOperationID / TurnIDがJob内のintentと一致することをController / Domainで検証する。

## 7. Credential設計

### 7.1 Broker

CredentialBrokerはProvider identityを入力としてCredentialLeaseを返す。Brokerは認証情報の永続化先を隠蔽し、Adapter以外へraw secretを返さない。

初期E2実装ではmemory-backed brokerをテスト専用に使用する。Live Provider向けのBrokerはProvider選定とsecret storage環境が確定するまで実装完了とみなさない。

### 7.2 Lease

CredentialLeaseは次を保証する。

- Acquire時にsecretをコピーしてlease所有とする。
- WithSecretはcallbackへ短命コピーを渡す。
- callback終了時にコピーをbest-effort zeroizeする。
- Closeは新規callbackを拒否し、lease所有secretをzeroizeする。
- Closeは複数回呼び出しても安全とする。
- in-flight callbackは独立コピーを使用し、Close後にcallbackが戻った時点でそのコピーをzeroizeする。
- RuntimeはdeferでCloseを保証し、Adapter panic時もleaseを残さない。

secret bufferのzeroizeはGo runtime / OSの複製まで保証するものではない。ログ・永続化・外部公開を防ぐことを主たる境界とする。

### 7.3 Redaction

- Adapter errorのraw messageを外側へ返さない。
- Errorは固定されたcodeとclassだけを返す。
- Adapter resultのidentity、content、evidence、event metadataにcredential bytesが含まれる場合は結果を破棄する。
- secret検査用の一時bufferもbest-effort zeroizeする。
- Provider responseをログへ出力しない。

## 8. Controller実行フロー

### 8.1 Start（DRAFT Planning実装済み）

1. Controllerが現在のJobAggregateを取得する。
2. Principal、PlanningAuthorization、Job state、Provider identity、budget、deadlineを検証する。
3. immutable Start intentとCommand receiptを同一transactionで保存する。
4. commit結果がUNKNOWNの場合はCommand IDとOperationIDをread-backする。確定できない場合はProviderを呼び出さない。
5. 外部呼出し直前に最新revisionとauthorityを再検証する。
6. RuntimeがCapabilityとcredential leaseを検証してAdapter.Startを呼び出す。
7. receipt / Session identityを検証し、Provider operation observationを追加する。
8. ambiguous errorはUNKNOWN observationとして追加する。
9. 同一OperationIDの再実行ではStart idempotencyまたはLookupを必須とし、UNKNOWN後にblind replayしない。
10. grant期限後は既存OperationIDのLookupによる照合だけを許可し、NOT_FOUND後の新規Startを拒否する。

Start observationが永続化されるとき、JobはProviderLifecycleEnforcedFromRevisionを記録する。既存Jobの読み取り互換性を維持し、このrevision以降のTurn / Resume intentにaccepted Start observationとSessionIDの一致を要求する。


### 8.1.1 Task-bound Coder Start（Controller統合対象）

Task-bound StartはDRAFT Planning Startと別のauthority pathとする。Task-bound ResumeはE5 Session ReattachmentとE4 Resume Idempotencyの両方を必須とし、同じOperationID / SessionIDの重複要求をProvider側で一つの再接続へ収束させる。Planner用PlanningAuthorizationを流用せず、現在のJobApproval・Task・ExecutionPermission・RoleExecution・ProviderStartIntentを検証する。

1. ProviderControllerは、trusted ExecutionControllerが注入されていない場合、Task-bound dispatchを拒否する。
2. JobAggregateから対象Task、Coder RoleExecution、ProviderStartIntent、ExecutionPermissionを解決し、各IDとTask / Role / Operation / request digestの一致を検証する。
3. ExecutionController.ValidatePermissionへ実際のExecutionPermissionRequestを渡し、最新JobApproval、Contract revision / hash、Task scope、Resource、Effect、Provider configuration、budget、deadline、trusted assessment、permission expiryをdispatch直前に再検証する。
4. Runtime.IdentityはExecutionPermissionのProviderID / AccountID / ConfigurationSHA256と完全一致しなければならない。
5. PromptとContextは呼出し側から受け取らない。Jobの原文依頼とimmutable TaskのTitle / Purpose / AcceptanceCriteria / RequiredEvidence / target scopeから決定的に構築し、PromptSHA256を計算する。OperationID、RoleExecutionID、request digest、budget、deadlineは保存済みProviderStartIntent / ExecutionPermissionから取得する。
6. Start intentは既にJobAggregateへ永続化されていることを必須とする。Task-bound StartではE4 Start Idempotencyを必須とし、Start Lookup単独では新規Startをdispatchしない。RuntimeはStartRequest.RequireIdempotencyを検証し、E4証拠がない場合はcredential取得前に拒否する。
7. Lookup capabilityがある場合は同一OperationIDを先に照会する。既存operationが見つかった場合はその証拠を観測し、NOT_FOUNDが検証された場合だけ、permissionを再検証してRequireIdempotency付きで同じOperationIDのStartを発行する。Lookupが失敗・不明の場合はUNKNOWNを保存し、Startを呼び出さない。Lookupがない場合もE4 Start Idempotencyがある場合だけ同一OperationIDでStartする。
8. Start成功、definitive failure、ambiguous / unclassified failureをそれぞれACCEPTED、FAILED、UNKNOWNとしてJobAggregateへappend-onlyで保存する。Observation保存に失敗した場合は成功扱いせず、次回はLookup / idempotencyで同一OperationIDを照合する。
9. UNKNOWNは同じOperationIDのLookupまたは実証済みidempotencyでのみ解消する。新しいOperationIDで再送しない。permission失効後は新規Startを禁止し、既存OperationIDのLookupだけを許可する。
10. Task-bound Observationの作成はJobApprovalに紐付くExecutionPermissionとTaskをDomainで検証する。Planner Observationは従来どおりPlanningAuthorization ownerのPrincipalを検証する。Execution AgentをHUMAN Principalとして偽装しない。

Reviewer Startは、ADR-408 / ADR-507に従うimmutable Worktree snapshotとread-only boundaryを実行環境で強制できるまで拒否する。StartRequestにはJobOutput identity、commit SHA、tree SHAを束縛する。RuntimeはProvider / SDK / configuration identityに結び付いたE4 `reviewer.read_only_snapshot` capabilityをcredential取得前に検証する。TaskのRoleがREVIEWERであること、Worktreeがdetachedであること、またはEffect assessmentのMutating=falseだけではread-only executionの証明にならない。能力証拠がない場合はProvider呼出しを拒否する。Worktree / Reviewer handoffはPhase 7で扱う。

### 8.2 Send（実装済み）

1. 最新Job、grant、RoleExecution、accepted Session、turn budget、deadlineを検証する。
2. E4 Send idempotencyまたはSend lookup capabilityを確認する。
3. immutable Turn intentとCommand receiptを同一transactionで保存する。
4. commit確認後に最新Jobとauthorityを再検証する。
5. 同一OperationID / TurnIDを使い、Lookupで未実行を確認した場合またはSend idempotencyが検証済みの場合だけ送信する。
6. terminal responseはPlanningTurnObservationへ保存し、terminal resultとUsageは別々のObservationとして取得・保存する。
7. UNKNOWN中は同じTurnIDで照合し、新しいTurnIDで再送しない。Lookupでterminal statusが見つかった場合もUsage Complete=trueを確認するまでturnを確定しない。
8. Runtime.SendがFailureDefinitiveを返した場合は、当該Dispatch ClaimだけをEvidence digest付きで解放し、UNKNOWN observationを追加しない。同じimmutable Turn intent / OperationIDは未dispatchとして再試行できる。FailureAmbiguousまたは分類不能なエラーではUNKNOWN observationとClaimを保持する。

### 8.3 Stop / Resume（Controller実装済み）

- Stop intentを先に保存し、Runtime.Stopの後にstop proof、quiescence、terminal result、Usageを照合する。
- 既存Stop intentの再送ではE4 Stop lookupを先に実行する。STOPPEDかつStopped / Quiescent=trueなら同じintentのObservationを追加する。
- active ClaimにDispatchStartがない場合、検証済みNOT_FOUNDで旧Claimを未dispatchとして解放し、新しいClaimを作成する。
- DispatchStart済みClaimは、E4 Stop LookupのNOT_FOUNDとE4 Stop Dispatch Fencingが揃う場合だけ、旧Claimをfenced_not_foundとして解放し、新しいJob revisionをfencing tokenとして同じOperationID / SessionIDで再dispatchする。Providerは古いtokenを拒否し、同一operationの重複要求を一つへ収束させる。
- fencing capabilityがない、またはLookupがUNKNOWN / 不整合の場合はClaimを保持し、Runtime.Stopを再実行しない。ClaimのTTL・経過時間だけで解放しない。
- Resume intentとCommand receiptをRuntime.Resumeより先に保存する。
- E5 SessionReattach capability evidence、Principal / grant / Provider identity、既存accepted Session、configuration identityを検証する。
- Resumeは同一OperationID / SessionIDで再照合し、Attached=trueとevidenceを得た場合だけATTACHED observationを追加する。曖昧な結果はUNKNOWNとして保存し、UNKNOWN解消時は同じOperationIDのterminal evidenceだけを許可する。
- Resume ATTACHED observationが確定するまで次Turnを許可しない。
- Resume不能の場合、同じRoleExecutionに新Sessionを付け替えない。

### 8.4 Lookup / Result / Usage / ReplayEvents（Start Lookup / Event replay / Result・Usage Controller実装済み）

これらは既存intentの照合操作であり、新しいProvider execution identityを生成しない。Controllerはaccepted Start Sessionとoptional Turn intentを検証し、操作に対応するE4 Capabilityを確認してRuntimeへ照会する。取得結果は元のOperationID / TurnIDへ束縛し、append-only Observationとして保存する。Usage Complete=falseは途中Observationとして保存し、Complete=trueだけを確定利用量として扱う。Lookup NOT_FOUNDはProviderが同一identityの不存在を証明した場合だけ記録する。Result / UsageのController orchestrationは実装済み。

## 9. Domain / Store設計

### 9.1 Domain facts

Providerの外部事実をJobAggregateへappend-onlyで保持する。現時点で実装済みの事実と後続実装を分離する。

- ProviderStartObservation（実装済み）: StartのACCEPTED / FAILED / UNKNOWN、SessionID、receipt、evidence
- ProviderEventObservation（実装済み）: EventID、Session-scoped Command ID、cursor、event kind、OperationID / TurnID
- ProviderResultObservation（Domain / Store実装済み）: OperationID / optional TurnID / SessionIDへ束縛したFinal=trueのUTF-8 result content（最大1 MiB）、content digest、evidence digest
- ProviderUsageObservation（Domain / Store実装済み）: OperationID / optional TurnID / SessionIDへ束縛したUsageID、token数、Complete、evidence digest。Complete=falseからComplete=trueへの更新は新しいObservationを追加する
- ProviderStopIntent（Domain / Store / Controller実装済み）: OperationID、SessionID、Provider identity、Reason。Stop前に保存し、後続Turn / Resumeを禁止
- ProviderStopObservation（Domain / Store / Controller実装済み）: Stopped、Quiescent、evidence、UNKNOWN reconciliation
- JobOutput（Domain / Store / ExecutionController / Worktree Manager実装済み）: Coderのcompleted Turn、final Result、complete Usage、Stopped / Quiescent evidenceに束縛したGit commit SHA / tree SHAをappend-only保存
- PlanningResumeIntent / PlanningResumeObservation（Domain / Store / Controller実装済み）: E5 SessionReattach evidence、同一OperationID / SessionID、ATTACHED / FAILED / UNKNOWN、UNKNOWN reconciliation

既存PlanningTurnObservation / PlanningResumeObservationはPlanning lifecycleの状態を表す。Start observationはProviderStartIntentのOperationIDへ束縛し、accepted SessionIDなしに新しいTurn / Resume intentを作成できない。Provider eventはaccepted SessionとOperationID / TurnIDへ束縛し、cursor chainを検証してJobAggregateへappend-onlyで保存する。Stop intentはRuntime.Stopより先に保存し、Stop observationは同一OperationIDの証拠でUNKNOWNを解消する。SendはTurn intentを先行保存し、同じOperationID / TurnIDでLookupまたは実証済みidempotencyを使って照合する。Provider固有のResult / Usageはaccepted Start / Turnへ束縛したappend-only factsとして保存し、Complete=trueのUsageだけを確定利用量として扱う。

### 9.2 Append-only invariant

- Fact IDとCommand IDは一意とする。
- 同じFact IDで異なる内容を保存しない。
- 既存Observationを更新・削除しない。
- UNKNOWNの解消は同一identityを参照する新Observationで記録する。
- Fact CreatedRevisionはintentより後であり、Job revision順序と一致する。
- Provider identityとRoleExecution / grant scopeが一致する。
- Stop intent以降の同一Sessionに対するTurn / Resume intentを拒否する。
- Stop UNKNOWNは同一OperationIDの新しいObservationでのみ解消し、Quiescent proofなしにreleaseしない。
- Result / Usage / Stop evidenceは対応するOperationID / TurnIDへ束縛する。
- Resultはaccepted Startまたは既存Turn intentに一致し、同じOperationID / TurnIDでFinal=trueを一度だけ記録する。
- Usageはaccepted Startまたは既存Turn intentに一致し、同一Session内のUsageIDを一つのOperationID / optional TurnIDへ束縛する。Complete=true後の追加を拒否する。

### 9.3 Store

既存JobAggregate保存方式とCommand receipt transactionを再利用する。Provider factだけの独立した可変storeを追加しない。Start / Event / Stop / Resume intent・observationは同一transactionでappendする。Result / Usage observationも同じexpected revision、idempotency、rollback、read-back recoveryの共通transactionで保存する。

## 10. Error classification

| Class | 例 | Controllerの扱い |
|---|---|---|
| Definitive | invalid request、unauthorized、unsupported capability | 外部操作未成立を確認できる場合だけ確定拒否 |
| Ambiguous | timeout、connection reset、panic、receipt missing、response mismatch | UNKNOWNを記録し、同一identityで照合 |
| Credential unavailable | Broker未取得、lease失効 | Providerを呼ばず拒否 |
| Unsupported capability | Evidence不足、identity mismatch | Credential取得・Provider呼出し前に拒否 |

Providerが操作を受理した可能性を排除できないerrorはAmbiguousとする。raw errorを保持しない。

## 11. 実装状況と残作業

実装済み:
- Adapter contract、Identity、Capability evidence、trusted registry verifier
- CredentialLease、secret isolation、panic-safe close
- Runtimeの全Adapter method、per-capability gate、response validation、error sanitization
- ProviderStartObservation、Start / Turn / ResumeのSession binding
- StoreのStart / Event / Stop / Result / Usage intent・observation transaction
- DRAFT Planning Start / Sendのintent-first Controllerと同一identity Lookup recovery
- Stop Controllerのintent-first永続化、UNKNOWN保持、blind replay防止
- Resume ControllerのE5 capability gate、intent-first永続化、同一OperationID / SessionIDのUNKNOWN reconciliation
- accepted Sessionに束縛したEvent replay Controllerとcursor/idempotency検証
- Result / Usage ControllerのE4 capability gate、identity再検証、append-only保存
- 要求仕様、詳細設計、内部設計、テスト仕様

未実装:
- JobApproval-backed execution path（JobApproval domain contract未実装のためfail-closed）
- Live Provider Adapter、Live CredentialBroker、E4 / E5 evidence catalog backend
- E2 Controller / Store / Runtime integrationの全操作網羅
- Linux CIはコードHEADでformat / vet / test / race / build PASS。専用secret scanと独立レビューは未完了。

## 12. 実装順序

1. 要求仕様・本書・内部設計・テスト仕様の整合を確認する。
2. Provider contract / identity / capability verificationを確定する。
3. CredentialLeaseのpanic-safe closeとredactionを確定する。
4. Runtime request / result validationを確定する。
5. Domain Provider factsと不変条件を追加する。
6. Store transaction / idempotency / recoveryを追加する。
7. Controllerのintent-first operation flowを追加する。
8. fake AdapterによるE2 integrationを実行する。
9. Secret scanとLinux必須CIを実行する。
10. 独立レビュー後、PRをDraftで提出する。Mergeは行わない。

## 13. 未決事項

- 初期Live ProviderとSDK / protocol version
- Live credentialの保存・取得方式
- Capability Evidence artifactの信頼済みcatalogの配布・更新方法
- ProviderごとのStop proof / Usage completenessの実証方法

これらが未決の間はE4 / E5 capabilityを有効化しない。
