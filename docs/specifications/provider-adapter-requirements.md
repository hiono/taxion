# Provider Adapter 実行境界 要求仕様書

Date: 2026-11-21
Status: Draft
対象Issue: #29
基準ADR: ADR-201 / ADR-202 / ADR-400 / ADR-500 / ADR-800
上位仕様: Product Contract §4.2 / §4.7、Product Acceptance AT-002 / AT-008 / AT-012 / AT-019

## 1. 目的

本書は、Taxion Controllerと外部Providerの間に設けるProvider Adapterの要求仕様を定義する。Provider実行の意図、外部操作、観測結果、利用量、停止証拠をJobAggregateへ相関させ、認可境界とcredential isolationを維持する。

本書はProvider SDKの具体的な型や特定ベンダーの採用を決定しない。初期実装ではfake AdapterによるE2 synthetic integrationを成立させ、Live Provider E4はProvider選定・認証環境の準備後に別途検証する。

## 2. 適用範囲

### 2.1 対象

- Provider / account / SDK・protocol version / configuration digestに束縛したAdapter identity
- capability declarationと検証証拠
- Start / Send / Stop / Resume / Lookup / Result / Usage / event replay
- Controllerによるauthority再検証とintent先行永続化
- Provider receipt / event / terminal result / usage / stop evidenceのJobAggregate保存
- timeout、disconnect、receipt欠落、process restart時のUNKNOWN保持と照合
- CredentialBroker / CredentialLeaseによるcredential仲介とsecret redaction
- fake Adapter、Domain / Store / Controller / Adapterの自動テスト
- Task-bound Coder Startに対するJobApproval / Task / ExecutionPermission / RoleExecution / ProviderStartIntentの再検証と、Start observation / UNKNOWN reconciliation

### 2.2 対象外

- 特定Provider SDKを用いたLive Provider E4試験
- 実Providerの認証情報・アカウントの準備
- OS-level filesystem / process / network / host Sandboxの完全な強制
- Git Worktreeの作成、排他、Handoff
- Task-bound Coder Provider Start dispatchとStart observation / UNKNOWN reconciliation（CoderのTurn / Result / Worktree lifecycleは後続段階）
- ReviewerのProvider Startはimmutable Worktree snapshotとread-only boundaryの強制が成立するまで拒否する。Reviewerの実行・HandoffはPhase 7で扱う。

対象外の能力を本Issueの完了証拠として扱わない。

## 3. 用語

| 用語 | 定義 |
|---|---|
| Provider | Taxion外部でModel / Tool実行を提供するサービスまたは実行環境 |
| Adapter | Provider固有APIとTaxion共通実行契約を変換する信頼済みコンポーネント |
| Runtime | Adapter呼出し、capability gate、credential lease、応答検証を仲介する内部コンポーネント |
| OperationID | Taxionが生成するProvider操作の不変識別子 |
| TurnID | RoleExecution内のProvider Send単位の不変識別子 |
| Capability | Provider / SDK / configurationの組合せで検証された操作能力 |
| Evidence | CapabilityまたはProvider operationの検証結果を示す識別可能な証拠 |
| UNKNOWN | 外部操作の結果を確定できず、成功・失敗・未送信のいずれにも推定できない状態 |
| CredentialLease | Adapterの必要な処理中だけcredentialを参照できる短命のlease |

## 4. 上位設計要求

### 4.1 JobAggregateとCommand

1. JobAggregateをJob・RoleExecution・Provider operation・Observation・Usageの唯一のドメイン正本とする。
2. JobAggregateの変更はexpected revisionと安定したCommand IDを持つ明示的Commandで行う。
3. intent、関連facts、Command receiptは同一Job transactionで確定する。
4. 同一Command ID・同一要求の再送は同じreceiptへ収束させ、異なる要求での再利用を拒否する。
5. Provider呼出し前にintentのcommitを確認する。commit結果がUNKNOWNの場合はread-backで確定するまでProviderを呼び出さない。
6. DB transaction結果とProvider operation結果を別々の事実として保持する。

### 4.2 Authority

各Provider操作の直前にControllerは現在のJobAggregateを読み直し、次を再検証する。

- JobID、Job revision、RoleExecutionID
- 認証済みHUMAN PrincipalとIntakeProvenance
- DRAFT planningの場合はPlanningAuthorizationの有効性、Job / request digest、Provider / account / configuration、read-only context、budget、deadline
- 承認済み実行の場合はJobApprovalの正確なContract revision、Task scope、resource scope、budget、deadline。Task-bound StartではExecutionPermissionをdispatch直前に再検証し、Task・RoleExecution・ProviderStartIntent・Provider identityを完全一致させる
- 操作対象のProvider / account / SDK・protocol version / configuration identity
- Context documentはID、content digest、実contentの一致を検証する。Snapshot modeはReferenceと一致する単一documentを要求する。Documents modeはID昇順で各documentの「ID + LF + SHA256 + LF」を連結したUTF-8 bytesのSHA-256をReadOnlyBoundary.SHA256と照合する。
- 当該操作に必要なCapabilityとその証拠水準
- 前回operationのUNKNOWN、terminal result、usage、stop状態

認可に必要な事実が不明・不整合の場合はfail-closedとする。DRAFT PlanningAuthorizationをJobApprovalへ昇格させない。JobApprovalが未実装の段階では、承認済み実行を可能とする仮の権限経路を作らない。

### 4.3 Capability

CapabilityはProvider ID、account identity、SDK / protocol version、configuration digestの完全一致に束縛する。PlanningAuthorizationはProvider ID、account ID、configuration digestを固定する。configuration digestはSDK / protocol versionを含む正規化済みProvider configurationから生成し、trusted Capability Evidence catalogはSDK / protocol versionを含む完全なAdapter identityを照合する。次の能力を個別に宣言・検証する。

| Capability | 意味 |
|---|---|
| Start idempotency / lookup | Startの重複実行防止または同一OperationIDによる照合 |
| Send idempotency / lookup | Sendの重複実行防止または同一TurnIDによる照合 |
| Cross-process reattachment | 保存済みの同一Sessionへのprocess restart後の再接続 |
| Event replay | event cursorの連続性を保った再取得 |
| Stop proof | Providerが処理を停止し、後続書込みを継続しないことの証拠 |
| Final output retrieval | event欠落時を含む最終結果の取得 |
| Usage identity | 利用量をoperation / turnへ一意に束縛する能力 |
| Usage completeness | 利用量が確定値であり、欠落・重複・部分値でないことを判定する能力 |

Capabilityは個別に扱う。未検証のCapabilityを利用可能と推定しない。あるCapabilityの証拠不足によって、独立した別Capabilityの利用まで一律に無効化しない。

証拠水準はADR-800に従う。E2 synthetic、E3 SDK compatibility、E4 Live Provider、E5 Durable recoveryを混同しない。Provider自身の自己申告だけを検証済み証拠として扱わない。Runtimeは信頼されたCapability検証結果を受け取り、必要な最低水準を満たす操作だけを許可する。

最低水準:
- Start / Send / Stop / Lookup / Result / Usage: E4
- Cross-process reattachment / Event replay: E5
- fake Adapterによる契約・異常系試験: E2

### 4.4 外部操作の状態

1. Start / Send / Stop / Resumeは、呼出し前にTaxion生成OperationIDとimmutable intentを保存する。
2. Provider receiptの受理は、turn完了・停止・Usage確定を意味しない。
3. terminal result、usage、stop proofはそれぞれ別のObservationとして保存する。
   - ResultはFinal=trueのUTF-8 contentとし、contentは1 MiB以下、ContentSHA256とEvidenceSHA256を検証する。ResultをJobAggregateへ保存する前にcredential漏えいを検査する。
   - UsageはUsageID単位でappend-onlyに保存する。Complete=falseは途中Observationとして記録でき、同一UsageIDのComplete=trueを後続Observationとして追加する。Complete=trueの後に同じUsageIDのObservationを追加しない。
4. timeout、connection loss、process crash、receipt欠落、応答検証失敗はUNKNOWNとして扱う。
5. UNKNOWNは同じOperationID / TurnIDに対するlookup、event replay、final result retrieval等の証拠でのみ解消する。
6. 同じidentityに対するidempotencyが実証されていない場合はblind replayしない。
7. Start / SendのUNKNOWNを新しいOperationID / TurnIDで再送しない。
8. 未解決operationがある間は、次のturn、停止済み判定、Worktree release、Job完了へ進めない。

### 4.5 ResumeとStop

- Resumeは保存済みSessionID、Provider / account identity、SDK / protocol version、configuration digestの完全一致を要求する。
- E5で検証されたcross-process reattachment capabilityがない場合、同じRoleExecutionへ新しいSessionを割り当てない。
- ResumeのUNKNOWNは同じResume OperationIDで照合する。
- ResumeがATTACHEDと確定する前に新しいturnを開始しない。
- Stopの成功は単なるcancel通知では認定しない。Stop proof、terminal result、Usageを照合する。
- Stop状態またはresultが不明な資源は未解決として保持し、再利用・所有権解放を行わない。

### 4.6 Credential isolation

1. Provider credentialはCredentialBrokerが解決し、Adapterの必要な操作中だけCredentialLeaseとして渡す。
2. credentialをJobAggregate、Command receipt、Provider observation、CLI引数、ログ、エラー、Model / Tool / Sandboxへ保存・露出しない。
3. RuntimeはAdapterのraw errorを外部へ返さず、許可された固定error codeへ正規化する。
4. Provider response、event、result、usage、session identity等にcredentialが含まれる場合は結果を採用せず、operationをUNKNOWNとして扱う。
5. Leaseは処理終了、エラー、panic、context cancellationのすべての経路でCloseする。
6. LeaseのCloseは新規参照を拒否し、保持するsecret bufferをbest-effort zeroizeする。
7. CredentialLeaseの利用者はsecretを保持・複製・外部へ送信しない。

## 5. 機能要求

| ID | 要求 |
|---|---|
| PA-REQ-001 | AdapterはProvider / account / SDK・protocol version / configuration digestを返す。 |
| PA-REQ-002 | Capabilityはidentityと個別の検証証拠へ束縛される。 |
| PA-REQ-003 | Runtimeは要求・identity・digest・deadlineを検証してからcredentialを取得する。 |
| PA-REQ-004 | Controllerは現在のJob authorityを再検証し、intent commit後だけProviderを呼び出す。 |
| PA-REQ-005 | Start / Sendは同一OperationID / TurnIDの照合可能性を検証する。 |
| PA-REQ-006 | Stopは停止証拠とProvider quiescenceを確認する。 |
| PA-REQ-007 | Resumeは同一SessionのE5 reattachment capabilityを要求する。 |
| PA-REQ-008 | Lookupは要求したOperationID / SessionID / TurnIDとの一致を検証する。 |
| PA-REQ-009 | ResultはOperationID / SessionID / TurnID、Final=true、UTF-8 content（1 MiB以下）、content digest、evidence digestを検証する。 |
| PA-REQ-015 | Result factsはJobAggregateへappend-onlyで保存し、同じOperationID / TurnIDのterminal resultを重複確定しない。 |
| PA-REQ-010 | UsageはUsageID、operation / turn identity、token数、Complete、evidence digestを検証し、同一UsageIDの途中値から確定値への遷移をappend-onlyで保存する。 |
| PA-REQ-016 | Usage factsは同一UsageIDのComplete=true後に追加・変更せず、Complete=falseを確定利用量として扱わない。 |
| PA-REQ-011 | Event replayはsession identity、cursor continuity、event identity、重複を検証する。 |
| PA-REQ-012 | Providerのraw credentialとraw errorを永続化・ログ出力しない。 |
| PA-REQ-013 | UNKNOWNを推測変換せず、同じidentityで照合する。 |
| PA-REQ-014 | Domain / Store / Controller / Adapterの異常系をE2で再現可能にする。 |

## 6. 非機能要求

- **認証・認可**: Trusted Ingressで確定したPrincipalと現在のJob authorityを使用する。
- **秘密情報保護**: credentialは必要最小限の時間・範囲でのみ参照可能とする。
- **整合性**: Job facts、receipt、revision、idempotencyを同一transactionで維持する。
- **重複実行防止**: UNKNOWN時の新identity発行とblind replayを禁止する。
- **復旧性**: restart後は保存factsとProvider状態を照合し、推測で状態を確定しない。
- **監査性**: 操作intent、Observation、Usage、拒否理由をJobID / RoleExecutionID / OperationIDへ相関する。
- **最小権限**: AdapterはJob authorityを発行・拡張せず、許可されたProvider / account / configurationだけを使用する。
- **テスト可能性**: fake AdapterでE2の正常系・異常系を再現できる。E2をE4 / E5の証拠として扱わない。

## 7. 受入条件

1. Capabilityが別Provider / account / SDK version / configurationへ流用できない。
2. 未検証Capabilityを要求する操作はcredential取得・Provider呼出し前に拒否される。
3. intentのcommit失敗・UNKNOWN時にProvider呼出しが発生しない。
4. Provider受理応答だけでterminal result、stop、usageを確定しない。
5. timeout / disconnect / receipt欠落 / crashをUNKNOWNとして保持し、blind replayしない。
6. Stop proof、terminal result、Usageの未照合中は完了・解放を許可しない。
7. E5未検証のResumeで同一RoleExecutionへ新Sessionを付け替えない。
8. credentialがJob、receipt、Observation、ログ、error、Model / Tool / Sandboxへ漏えいしない。
9. credential leaseは正常・異常・panic・cancelの各経路でCloseされる。
10. E2 fake AdapterテストはE4 / E5の証拠を生成・偽装しない。
11. Linux format / vet / test / race / buildが対象HEADで成功する。
12. Secret scan、Category sync、Link/reference sync、独立レビューを完了する。

## 8. トレーサビリティ

| 要求領域 | 根拠 |
|---|---|
| JobAggregate / Command / Operation intent | ADR-201 |
| 全状態経路の不変条件 | ADR-202 |
| Controller / Runtime境界 | ADR-400、docs/design/runtime-boundaries.md |
| Job authority / prohibited effects | ADR-500 |
| Evidence level | ADR-800 |
| Bounded planning | Product Contract §4.2、AT-002 |
| Interruption / recovery | Product Contract §4.7、AT-008、AT-012 |
| Credential isolation | Product Acceptance AT-019 |
| 実Provider受入れ | ADR-800、AT-013 |
