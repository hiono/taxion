# Provider Adapter 内部設計書

Date: 2026-11-21
Status: Draft
対象Issue: #29
要求仕様: [Provider Adapter 実行境界 要求仕様書](../specifications/provider-adapter-requirements.md)
詳細設計: [Provider Adapter 詳細設計書](provider-adapter-detailed-design.md)

## 1. Package構成

| Package / File | 主な型・機能 |
|---|---|
| `internal/provider/adapter.go` | Identity、Capability、Evidence、Request / Result、Adapter interface、OperationError |
| `internal/provider/credential.go` | CredentialBroker、CredentialLease、memory lease |
| `internal/provider/runtime.go` | Runtime、Capability gate、Adapter invocation、応答検証、error sanitization |
| `internal/domain/provider_lifecycle.go` | ProviderStartObservation、Start状態遷移、Session binding |
| `internal/store/planning_lifecycle.go` | Provider Start observationのexpected revision / idempotency / transaction保存 |
| `internal/controller/provider.go` | DRAFT Planning Startのauthority再検証、intent-first実行、Lookup recovery、Observation保存 |
| `internal/provider/*_test.go` | Adapter contract、Runtime、CredentialLeaseのunit / E2 tests |
| `internal/controller/provider_test.go` | Controller / Runtime / StoreのE2 integration tests |
| `internal/store/provider_lifecycle_test.go` | transaction、replay、rollback、recovery tests |

Provider SDKのimportは将来追加するProvider固有package内だけに許可する。Domain / Store / ControllerはProvider SDKに依存しない。

## 2. Provider Adapter API

### 2.1 Identity

```go
type Identity struct {
    ProviderID          string
    AccountID           string
    SDKVersion          string
    ConfigurationSHA256 string
}

PlanningAuthorizationはProvider ID / account / configuration digestを固定する。ConfigurationSHA256はSDKVersionを含む正規化済みProvider configurationから生成する。CapabilityEvidenceRegistryはSDKVersionを含むIdentity全体を照合する。
```

Validateは全項目を必須とする。ConfigurationSHA256はlowercase SHA-256 hexとする。Identityはimmutable valueとして扱う。

### 2.2 Adapter interface

```go
type Adapter interface {
    Identity() Identity
    Capabilities(context.Context) (CapabilityDeclaration, error)
    Start(context.Context, StartRequest, CredentialLease) (StartResult, error)
    Send(context.Context, TurnRequest, CredentialLease) (TurnResult, error)
    Stop(context.Context, StopRequest, CredentialLease) (StopResult, error)
    Resume(context.Context, ResumeRequest, CredentialLease) (ResumeResult, error)
    Lookup(context.Context, LookupRequest, CredentialLease) (LookupResult, error)
    Result(context.Context, ResultRequest, CredentialLease) (Result, error)
    Usage(context.Context, UsageRequest, CredentialLease) (Usage, error)
    ReplayEvents(context.Context, ReplayEventsRequest, CredentialLease) (EventPage, error)
}
```

AdapterはProvider固有のHTTP / SDK request、response、session、eventを内部で変換する。Adapterの外側へraw Provider responseやerrorを返さない。

### 2.3 Request validation

各RequestはAdapter呼出し前にRuntimeでValidateする。

- IDは空でなく、長さ上限とcontrol character制約を満たす。
- SHA-256はlowercase hex 64文字とする。
- PromptSHA256とPrompt、ContextDocumentのdigestとContentを照合する。
- MaxTurns / MaxTokensは0を許可しない。
- Deadlineはゼロ値・期限切れを拒否する。
- RequestのProvider identityはRuntimeが保持するIdentityへ束縛する。

Runtimeはcontext deadlineとrequest deadlineの早い方を有効deadlineとする。

## 3. Capability内部モデル

### 3.1 Declaration

Adapterは実装能力を宣言する。宣言は検証済み能力ではない。

```go
type Capabilities struct {
    Identity                    Identity
    StartIdempotency            bool
    StartLookup                 bool
    SendIdempotency             bool
    SendLookup                  bool
    CrossProcessSessionReattach bool
    EventReplay                 bool
    StopProof                   bool
    FinalOutputRetrieval        bool
    UsageIdentity               bool
    UsageCompleteness           bool
    Evidence                    []CapabilityEvidence
}
```

Capabilityは次の固定値を使用する。

- start.idempotency / start.lookup
- send.idempotency / send.lookup
- session.cross_process_reattachment
- event.replay
- stop.proof
- result.final_output_retrieval
- usage.identity
- usage.completeness

未知値、重複値、Identity mismatchを拒否する。

### 3.2 Evidence verification

```go
type CapabilityEvidence struct {
    Capability     Capability
    Identity       Identity
    TestID         string
    EvidenceSHA256 string
    EvidenceLevel  EvidenceLevel
    VerifiedAt     time.Time
}

type CapabilityVerifier interface {
    Verify(context.Context, Identity, CapabilityEvidence) (bool, error)
}

type CapabilityEvidenceRegistry interface {
    Find(context.Context, Identity, Capability) (CapabilityEvidence, bool, error)
}
```

`RegistryCapabilityVerifier`はtrusted registryから取得したCapabilityEvidenceとAdapter宣言を完全一致で照合する。IdentityにはSDKVersionを含む。Adapterが返すEvidenceLevelだけで能力を有効化しない。Registry backendとartifact署名検証方式は未確定であり、未設定の環境ではE4 / E5を有効化しない。

### 3.3 Per-operation gate

Runtimeは各methodで必要Capabilityだけを検証する。

- Start: Start idempotencyまたはStart lookupのE4
- Send: Send idempotencyまたはSend lookupのE4
- Stop: Stop proofのE4
- Resume: cross-process reattachmentのE5
- Lookup: Start / Sendそれぞれに対応するlookupのE4
- Result: final output retrievalのE4
- Usage: Usage identityとUsage completenessのE4
- ReplayEvents: event replayのE5

全Capabilityの最低水準を一括検査してRuntime全体を停止させない。無関係なCapabilityの証拠不足は、当該Capabilityを必要とするmethodだけを拒否する。

E2 fake integrationはprovider package内のtest-only constructorとtest verifierで実行する。本番constructorにEvidenceLevelを引き下げる公開optionを設けない。

## 4. Credential内部モデル

### 4.1 CredentialBroker

```go
type CredentialBroker interface {
    Acquire(context.Context, Identity) (CredentialLease, error)
}
```

BrokerはIdentityを完全一致で解決する。Provider / account / configurationの不一致やsecret不在はCredentialUnavailableとして返す。raw errorは外側へ伝播しない。

### 4.2 CredentialLease

```go
type CredentialLease interface {
    WithSecret(func([]byte) error) error
    Close() error
}
```

memory leaseの内部状態:

- secret: leaseが所有するbyte slice
- closed: 新規callbackを拒否する状態
- mutex: CloseとWithSecretの競合を制御

WithSecretはlock中にsecretを独立bufferへコピーし、lock解放後にcallbackを実行する。callback終了時に独立bufferをzeroizeする。Closeはlease所有bufferをzeroizeし、closedを設定する。in-flight callbackは独立bufferを使用するため、Closeはcallback完了を待たずにleaseを閉じられる。

### 4.3 Runtime invocation

```go
func withCredential[T any](
    ctx context.Context,
    broker CredentialBroker,
    identity Identity,
    call func(CredentialLease) (T, error),
) (result T, err error) {
    lease, err := broker.Acquire(ctx, identity)
    if err != nil || lease == nil {
        return result, ErrCredentialUnavailable
    }
    defer func() {
        if closeErr := lease.Close(); closeErr != nil {
            var zero T
            result = zero
            err = ErrAmbiguousOperation
        }
    }()
    return call(lease)
}
```

Closeはdeferで保証する。Adapter panicは上位のoperation boundaryでAmbiguous / UNKNOWNへ変換し、panic valueをerror・logへ出力しない。deferされたCloseはpanic経路でも実行する。

## 5. Runtime内部設計

### 5.1 Runtime dependencies

```go
type Runtime struct {
    adapter         Adapter
    broker          CredentialBroker
    verifier        CapabilityVerifier
    identity        Identity
    minimumEvidence EvidenceLevel
    durableEvidence EvidenceLevel
}
```

NewRuntimeはAdapter、Broker、Verifierを必須とする。Adapter identityを一度取得して保持し、各operation前にもidentity driftを検出する。

### 5.2 Operation pipeline

1. Request.Validate
2. Context / Request deadline validation
3. Adapter identity revalidation
4. CapabilityVerifierによる宣言とEvidenceの照合
5. 対象methodのCapability gate
6. CredentialBroker.Acquire
7. Adapter method call
8. secret leakage scan
9. result identity / status / digest / evidence validation
10. CredentialLease.Close
11. safe resultまたはsafe OperationErrorをControllerへ返す

Capability gateはcredential取得より先に実行する。Request不正・期限切れ・Capability不足の場合、BrokerとAdapterに副作用を発生させない。

### 5.3 Error mapping

```go
type FailureClass string
const (
    FailureDefinitive FailureClass = "definitive"
    FailureAmbiguous  FailureClass = "ambiguous"
)

type OperationError struct {
    Class FailureClass
    Code  string
}
```

OperationCodeは固定allowlistとする。Adapter raw error、URL、request body、response body、credential値をOperationErrorへ格納しない。

次の場合はAmbiguousとする。

- timeout / deadline / connection loss
- Adapter panic
- Provider受理後のreceipt欠落
- result identity / digest / evidence mismatch
- secret leakage検出
- Lease Close失敗
- operationが成立したか否かを確定できない応答

## 6. Domain facts

### 6.1 ProviderStartObservation（実装済み）

```go
type ProviderStartObservation struct {
    ID, CommandID, JobID, RoleExecutionID, OperationID string
    ProviderID, AccountID, ConfigurationSHA256         string
    Status, ProviderSessionID, ReceiptID, EvidenceSHA256 string
    ReconcilesUnknown bool
    CreatedAt time.Time
    CreatedRevision uint64
}
```

UNKNOWNは同一OperationIDの照合Observationで追記できる。terminal ACCEPTED / FAILEDは先行UNKNOWNだけを解消できる。ACCEPTED observationのSessionIDと一致しないTurn / Resume intentは新規作成できない。既存Jobの読み取り互換性を維持するため、Job.ProviderLifecycleEnforcedFromRevisionを使用する。新規StartIntent作成時はそのCreatedRevisionを記録する。旧Jobにこの値がない場合は、最初のProviderStartObservationのCreatedRevisionを記録する。そのrevisionより前の既存Turn / Resumeは読み取り可能とし、そのrevision以降の新しいTurn / Resumeにはaccepted Start observationとSessionIDの一致を要求する。

### 6.2 ProviderEventObservation（Domain / Store / Controller実装済み）

```go
type ProviderEventObservation struct {
    ID              string
    CommandID       string
    JobID           string
    RoleExecutionID string
    OperationID     string
    TurnID          string
    SessionID       string
    EventID         string
    Kind            ProviderEventKind
    PreviousCursor  string
    Cursor          string
    EvidenceSHA256  string
    ObservedAt      time.Time
    CreatedRevision uint64
}
```

### 6.3 ProviderResultObservation（Domain / Store実装済み）

```go
type ProviderResultObservation struct {
    ID              string
    CommandID       string
    JobID           string
    RoleExecutionID string
    OperationID     string
    TurnID          string
    SessionID       string
    Content         string
    ContentSHA256   string
    Final           bool
    EvidenceSHA256  string
    CreatedAt       time.Time
    CreatedRevision uint64
}
```

Result contentはJobAggregateへ保持する。contentは有効なUTF-8で1 MiB以下とし、上限を超える結果は受理しない。ContentSHA256はcontentのSHA-256と一致しなければならない。Final=falseはterminal resultとして保存しない。Runtimeによるcredential検査を通過していない結果は保存しない。

### 6.4 ProviderUsageObservation（Domain / Store実装済み）

```go
type ProviderUsageObservation struct {
    ID              string
    CommandID       string
    JobID           string
    RoleExecutionID string
    OperationID     string
    TurnID          string
    SessionID       string
    UsageID         string
    Tokens          uint64
    Complete        bool
    EvidenceSHA256  string
    CreatedAt       time.Time
    CreatedRevision uint64
}
```

Complete=falseは途中値として記録可能だが、budget finalizationには使用しない。同じUsageIDのComplete=true observationを後続に追加して確定する。Complete=trueはUsageIDごとに一度だけ許可し、その後の追加Observationを拒否する。EvidenceSHA256はCompleteの値にかかわらず必須とする。

### 6.5 ProviderStopIntent / ProviderStopObservation（Domain / Store実装済み）

```go
type ProviderStopIntent struct {
    ID, CommandID, JobID, RoleExecutionID string
    OperationID, SessionID                string
    ProviderID, AccountID                 string
    ConfigurationSHA256, Reason           string
    CreatedAt                             time.Time
    CreatedRevision                       uint64
}

type ProviderStopObservation struct {
    ID, CommandID, JobID, RoleExecutionID string
    OperationID, SessionID                string
    Stopped, Quiescent                    bool
    EvidenceSHA256                        string
    ReconcilesUnknown                     bool
    CreatedAt                             time.Time
    CreatedRevision                       uint64
}
```

Stop intentをRuntime.Stopより先にappend-onlyで保存する。同一Sessionに複数のStop intentを作成せず、Stop intent以降のTurn / Resume intentを拒否する。Stop observationはUNKNOWNを同一OperationIDの新しい証拠で解消する。Quiescent=trueはStopped=trueと有効なevidenceを必須とする。

### 6.6 Job invariant

Job.Validateは次を検証する。

- Fact / Command / Event identityの一意性。UsageIDはSession内で一意とし、同一UsageIDの途中値から確定値への履歴だけを許可する
- Job / RoleExecution / grant / Provider identityの一致
- Resultはaccepted Startまたは同一SessionのTurn intentへ束縛し、Final=true、content UTF-8 / 1 MiB上限 / digest一致を検証する
- Usageはaccepted Startまたは同一SessionのTurn intentへ束縛し、Complete=true後の追加を拒否する
- OperationID / TurnID / SessionIDの相関
- Intentより後のCreatedRevision
- append-only順序と時刻の単調性
- Event cursorの連続性、重複、逆行の不存在
- UNKNOWN解消Observationが同一OperationID / TurnIDを参照すること
- Complete=falseのUsageをfinal budgetへ計上しないこと
- Quiescent=falseのStopをrelease proofとして使用しないこと
- terminal result / usage / stop proofが揃う前に完了へ進まないこと

## 7. Store設計

既存SQLite JobAggregate JSONとCommand receiptを利用する。Provider専用の独立した可変状態テーブルを追加しない。

Provider Start / Event / Stop / Result / Usage intent・observationは既存のplanning lifecycle fact transactionへ統合済み。Result / Usage factも共通persist fact patternを使用し、JobAggregate更新とCommand receiptを同一transactionで確定する。

1. BeginTx
2. JobAggregateを読む
3. expected revisionとCommand IDを検証
4. 同一Command IDが存在する場合はpayload digestを照合
5. Domain mutationを適用
6. Job.Validate
7. aggregate revision / metadataを更新
8. Command receiptをINSERT
9. Commit
10. commit outcomeが不明な場合はCommand ID / Fact IDでread-back

aggregate updateとreceipt insertのいずれかが失敗した場合は両方rollbackする。EventIDの重複は同じpayloadなら同じfactへ収束し、異なるpayloadなら拒否する。Result / Usageは同じCommand ID・同じpayloadで同じfactへ収束し、異なるpayloadでの再利用を拒否する。UsageIDのComplete=true後に新しいfactを追加しない。

## 8. Controller設計

### 8.1 Dependencies

```go
type ProviderExecutionStore interface {
    PlanningLifecycleStore
    StartTasklessPlanner(...)
    AddProviderEventObservation(...)
    AddProviderResultObservation(...)
    AddProviderUsageObservation(...)
}

type PlanningProviderRuntime interface {
    Identity() provider.Identity
    Capabilities(context.Context) (provider.Capabilities, error)
    Start(context.Context, provider.StartRequest) (provider.StartResult, error)
    Send(context.Context, provider.TurnRequest) (provider.TurnResult, error)
    Lookup(context.Context, provider.LookupRequest) (provider.LookupResult, error)
    ReplayEvents(context.Context, provider.ReplayEventsRequest) (provider.EventPage, error)
    Result(context.Context, provider.ResultRequest) (provider.Result, error)
    Usage(context.Context, provider.UsageRequest) (provider.Usage, error)
}

type ProviderController struct {
    store    ProviderExecutionStore
    runtime  PlanningProviderRuntime
    planning *PlanningController
    now      func() time.Time
}
```

現時点のController interfaceはDRAFT Planning Start / Send / Resume / Stop、accepted Sessionに束縛したEvent replay、Result / Usage retrievalに対応する。Sendはintent-first、E4 Send idempotency / lookup gate、同一OperationID / TurnIDのLookup recovery、Usage Complete=true確認を実装済み。ResumeはE5 SessionReattach evidence、intent-first永続化、同一OperationID / SessionIDのUNKNOWN reconciliationを実装済み。Controller testsではfake implementationを注入する。

### 8.2 Start sequence（実装済み）

```text
Load Job
  -> Validate Principal / PlanningAuthorization / Provider identity
  -> Persist StartIntent + CommandReceipt
  -> Confirm commit by receipt read-back if outcome UNKNOWN
  -> Reload current Job / revalidate authority
  -> Runtime.Start
  -> Validate StartResult
  -> Persist ProviderOperationObservation
```

Start intentが保存された後にauthorityが失効した場合、新規Startを呼び出さない。既存OperationIDのLookupによる照合は許可し、NOT_FOUND後の新規Startは期限内の場合だけ許可する。

### 8.3 Send sequence（実装済み）

```text
Load Job
  -> Validate Principal / grant / RoleExecution / accepted Session / budget / deadline
  -> Verify E4 Send idempotency or Send lookup capability
  -> Persist TurnIntent + CommandReceipt
  -> Confirm commit / reload Job / revalidate authority
  -> Lookup same OperationID / TurnID when recovery is required
  -> Runtime.Send only for a new intent, verified NOT_FOUND, or verified Send idempotency
  -> Persist terminal or UNKNOWN PlanningTurnObservation
  -> Retrieve Usage and require Complete=true before reconciling a terminal Lookup result
  -> Retrieve Result / Usage as separate append-only facts
```

Sendの結果がAmbiguousの場合は同じTurnIDのUNKNOWNを追加し、新しいTurnIDを生成しない。既存UNKNOWNはLookupまたは実証済みSend idempotencyでのみ照合し、Lookupがterminal statusを返してもUsage Complete=trueを得るまでterminal TurnObservationへ遷移させない。

### 8.4 Recovery sequence（Start / Send / Resume / Event replay実装済み）

```text
Load JobAggregate
  -> Validate schema / revision / invariants
  -> Find unresolved OperationID / TurnID
  -> Verify capability for Lookup / Replay / Result / Usage
  -> Query same identity
  -> Validate Provider response
  -> Append new Observation
  -> Re-evaluate terminality / usage / quiescence
```

Start / Sendの照合能力がない場合はUNKNOWNを保持する。Sendは同じOperationID / TurnIDのLookupまたは実証済みidempotencyでのみ再実行する。ResumeはE5 SessionReattach capabilityを確認し、intent-firstで同じOperationID / SessionIDを再利用する。曖昧なResume結果はUNKNOWNを保持し、同じOperationIDのATTACHED / FAILED evidenceだけで解消する。Event replayはaccepted Start Sessionと保存済みcursor prefixへ束縛し、eventをappend-onlyで保存する。Result / UsageのControllerはaccepted Startとoptional Turnのidentityを検証し、E4 Capability確認後に取得結果を既存OperationID / TurnIDへappend-onlyで保存する。Complete=falseのUsageは途中値として残し、Complete=trueでのみ確定する。Resume ControllerはE5 capabilityと同一OperationID / SessionIDで照合し、新identityを作って回避しない。

### 8.5 Result / Usage sequence（Domain / Store / Controller実装済み）

### 8.6 Resume sequence（Domain / Store / Controller実装済み）

```text
Load Job
  -> Validate Principal / grant / RoleExecution / accepted Start Session
  -> Verify E5 SessionReattach capability evidence
  -> Persist PlanningResumeIntent + CommandReceipt
  -> Confirm commit / reload Job / revalidate authority
  -> Runtime.Resume with the same OperationID / SessionID
  -> Persist ATTACHED / FAILED / UNKNOWN observation
```

UNKNOWN解消は同じResume OperationIDのterminal evidenceだけを許可する。ATTACHED observationが保存されるまで次Turnを作成しない。ResumeはSessionを置き換えず、別Sessionへの付け替えを禁止する。

```text
Load JobAggregate
  -> Validate Principal / DRAFT Job / PlanningAuthorization / Provider identity
  -> Find accepted Start Session and optional Turn intent
  -> Verify Result E4 or Usage identity + completeness E4 capabilities
  -> Runtime.Result / Runtime.Usage
  -> Reload JobAggregate / revalidate target binding
  -> Append Result / Usage observation + CommandReceipt atomically
```

ResultはFinal=trueとcontent digestを検証して保存する。Usage Complete=falseは途中Observationとして保存し、同じUsageIDのComplete=trueを後続追加する。取得失敗・identity mismatch・保存失敗を成功として扱わず、既存OperationID / TurnIDで再照合する。

## 9. Transaction / external side effect boundary

DB transactionをProvider API呼出しと同一transactionに見せかけない。

- intent commit前にProviderを呼び出さない。
- intent commitのUNKNOWN時はread-backを行う。
- Provider呼出し後のObservation保存失敗はUNKNOWNとして復旧対象にする。
- Providerの成功応答を受けても、Observation保存が成功するまでJob内の成功事実にしない。
- 外部操作の結果とDB commitの結果を別々に記録する。

## 10. Logging / telemetry

許可する情報:
- JobID / RoleExecutionID / OperationID / TurnID
- ProviderID / SDKVersion / configuration digest
- Capability名 / Evidence level / Evidence digest
- safe OperationCode / FailureClass
- elapsed time / retry count / cursor identity

禁止する情報:
- raw credential / access token / refresh token / cookie
- Authorization header
- raw Provider request / response body
- prompt / context document本文の無制限出力
- raw Provider error
- credentialを含むURLや環境変数

## 11. 実装上の注意

- CredentialLease.Closeは必ずdeferで保証する。
- panic valueをログ・errorへ出力しない。operation成立可能性が残る場合はUNKNOWNへ変換する。
- capabilityはoperationごとにgateする。
- UsageにCompleteを持たせ、UsageCompleteness capabilityだけで個別resultの完全性を推測しない。
- StopにQuiescentを持たせ、Stoppedだけでreleaseしない。
- fake AdapterにE4 / E5を自己申告させない。
- Adapter identity driftを検出したら操作を開始しない。
- Storeがcommit outcomeを確定できない場合はProviderを呼び出さない。
- Provider選定・Live credentialが未確定のため、E4 / E5 capabilityを有効化しない。

## 12. 設計確定待ち

- Capability Evidence artifactの署名形式とtrusted catalogの保管先
- Live CredentialBrokerのsecret storage backend
- 初期Provider / SDK version
- Provider Result contentの最大サイズと将来のblob storage移行
- ProviderごとのStop Quiescent proofとUsage Completeの実証方法
