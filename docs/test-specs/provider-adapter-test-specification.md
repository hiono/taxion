# Provider Adapter テスト仕様書

Date: 2026-11-21
Status: Draft
対象Issue: #29
要求仕様: [Provider Adapter 実行境界 要求仕様書](../specifications/provider-adapter-requirements.md)
詳細設計: [詳細設計書](../design/provider-adapter-detailed-design.md)
内部設計: [内部設計書](../design/provider-adapter-internal-design.md)
上位受入仕様: [Product Acceptance Test Specification](product-acceptance.md)

## 1. 目的

Provider Adapterの契約、Capability gate、Credential isolation、Controller / Storeの状態整合性を再現可能なテストとして定義する。

本書のfake AdapterによるテストはE2 synthetic integrationである。Live Provider E4、Durable Recovery E5、Product E2E E6の合格証拠として扱わない。

## 2. テストレベル

| Level | 対象 | 証明範囲 |
|---|---|---|
| Unit | Domain / Adapter / CredentialLease / Runtime | 局所validation、状態遷移、secret lifecycle |
| E2 | fake Adapter + Controller + Store + Runtime | request / response contract、transaction、異常系 |
| E3 | 実SDK / protocolとの互換性 | SDK型、API、event / receipt mapping |
| E4 | 実Provider | 実接続、turn、usage、stop semantics |
| E5 | process restart / crash recovery | durable stateとProvider状態の再照合 |
| E6 | 実Job完遂 | Planner / Coder / Reviewer / Humanを含むJob完遂 |

本Issueで必須とするのはUnitとE2である。E3〜E6は別の検証証拠として扱う。

## 3. 共通テストデータ

- Provider identity: ProviderID、AccountID、SDKVersion、ConfigurationSHA256をすべて固定する。
- Capability evidence: fake verifierがE2 evidenceだけを返す。
- Credential: テスト専用のランダムな固定byte列を使用する。実credentialを使用しない。
- Job: DRAFT Job、認証済みHUMAN Principal、IntakeProvenance、PlanningAuthorization、Taskless PLANNER RoleExecutionを用意する。
- Request: OperationID、TurnID、Prompt digest、Context digest、budget、deadlineを固定する。
- Store: 一時SQLite databaseを使用する。

テストfixtureにE4 / E5の自己申告値を設定しない。

Task-bound Coder Start fixtureはACTIVE Job、最新JobApproval、Task、JobApproval-bound ExecutionPermission、Task-bound Coder RoleExecution / ProviderStartIntentを使用する。ExecutionPermission assessmentはテスト用trusted assessorで発行し、Provider capabilityはE2 evidenceとして扱う。Reviewerはimmutable Worktree snapshotとread-only enforcement evidenceがない限りdispatch拒否を検証する。

## 4. Domain / invariant tests

| ID | 観点 | 入力・操作 | 期待結果 |
|---|---|---|---|
| DOM-001 | Provider identity | ProviderID欠落 | 拒否 |
| DOM-002 | Provider identity | Account / SDK version / config digest不一致 | 拒否 |
| DOM-003 | Fact identity | 重複Fact ID・Command ID | 同一payloadのみidempotent、異なるpayloadは拒否 |
| DOM-004 | Operation binding | 別Job / RoleExecution / OperationIDのObservation | 拒否 |
| DOM-005 | Turn binding | 別TurnIDのResult / Usage | 拒否 |
| DOM-006 | Session binding | 別SessionIDのResume / Event | 拒否 |
| DOM-007 | Append-only | 既存Observationの変更・削除 | 拒否 |
| DOM-008 | Revision | intentより前のObservation revision | 拒否 |
| DOM-009 | UNKNOWN | 別OperationIDでUNKNOWNを解消 | 拒否 |
| DOM-010 | Usage completeness | Complete=falseをfinal budgetへ計上 | 拒否 |
| DOM-011 | Stop quiescence | Stopped=true / Quiescent=false | release不可 |
| DOM-012 | Result finality | Final=falseをterminal resultとして採用 | 拒否 |
| DOM-013 | Event identity | 重複EventID / cursor | 同一内容のみ再送収束、異なる内容は拒否 |
| DOM-014 | Event cursor | cursor欠落・逆行・分岐 | 拒否 |
| DOM-015 | Start binding | accepted Start observationなしでTurn intent作成 | 拒否 |
| DOM-016 | Session binding | accepted Start observationと異なるSessionIDでTurn intent作成 | 拒否 |
| DOM-017 | Legacy aggregate | Start observation導入前のJobに既存Turn factsがある | 読み取り可能。新規Turn / Resumeはaccepted Start observationなしに作成不可 |
| DOM-018 | UNKNOWN history | 同一OperationIDでUNKNOWNを追記しterminal evidenceで解消 | 既存factを変更せずappend-onlyで解消 |
| DOM-019 | Stop intent | accepted Sessionなし、同一Sessionへの二重Stop、Stop後のTurn / Resume | 拒否 |
| DOM-020 | Stop observation | UNKNOWN解消のOperationID不一致、Quiescent=trueかつStopped=false、evidence欠落 | 拒否 |
| DOM-021 | Result finality | Final=false | terminal resultとして拒否 |
| DOM-022 | Result content | UTF-8不正、1 MiB超過、ContentSHA256不一致 | 拒否 |
| DOM-023 | Result binding | accepted Start / Turn intentとOperationID・TurnID・SessionID不一致 | 拒否 |
| DOM-024 | Result append-only | 同じOperationID / TurnIDにterminal resultを二重追加 | 拒否 |
| DOM-025 | Usage completeness | Complete=falseの後に同一UsageIDのComplete=trueを追加 | append-onlyで受理 |
| DOM-026 | Usage finality | Complete=true後の同一UsageID追加、別Operation / TurnへのUsageID再束縛 | 拒否 |
| DOM-027 | Result identity | Start operationとTurnIDの組合せ不一致、accepted Session以外 | 拒否 |
| DOM-028 | Usage identity | accepted Session以外、存在しないOperation / Turn | 拒否 |
| DOM-029 | Task Start observation | Task / ExecutionPermission / RoleExecution / ProviderStartIntentのidentity不一致、または異なるJobApproval | 拒否 |
| DOM-030 | Task Start observation authority | Task-bound observationへPlanner Principalを流用、またはPlanner observationにPrincipalなし | 拒否 |
| DOM-031 | Task Start observation history | Job amendment後の既存Task operation evidence復元 | historical permission / approval provenanceを維持して復元 |
| DOM-032 | JobOutput provenance | completed Coder Turn、final Result、complete Usage、Stopped / Quiescent、Worktree Assignmentとcommit / tree identity | append-only JobOutputへ束縛 |
| DOM-033 | JobOutput tampering | Stop evidence変更、invalid Git commit identity | 拒否 |

## 5. Adapter contract tests

| ID | 観点 | 入力・操作 | 期待結果 |
|---|---|---|---|
| ADP-001 | Start request | 有効なidentity / prompt / digest / budget | 受理 |
| ADP-002 | Start request | Prompt digest mismatch | 呼出し前に拒否 |
| ADP-003 | Start request | 重複Context ID | 拒否 |
| ADP-004 | Start request | deadline未設定・期限切れ | 拒否 |
| ADP-005 | Turn request | TurnID / SessionID mismatch | 拒否 |
| ADP-006 | Turn result | OperationID / TurnID / SessionID mismatch | UNKNOWNとして扱う |
| ADP-007 | Turn result | TokensUsedがMaxTokens超過 | UNKNOWNとして扱う |
| ADP-008 | Turn result | UNKNOWNにoutput / usageを含む | 不正応答として拒否 |
| ADP-009 | Lookup | NOT_FOUNDの証拠とidentity一致 | 不在Observationを返す |
| ADP-010 | Lookup | NOT_FOUNDなのにreceipt / result / usageがある | 拒否 |
| ADP-011 | Result | Content digest mismatch | UNKNOWNとして扱う |
| ADP-012 | Usage | UsageID欠落 | UNKNOWNとして扱う |
| ADP-013 | Usage | Complete=false | final usageにしない |
| ADP-014 | Stop | Stopped=true / Quiescent=false | Stop proof未成立 |
| ADP-015 | Resume | 別SessionID | 拒否 |
| ADP-016 | Event replay | page内cursor chain正常 | 受理 |
| ADP-017 | Event replay | duplicate EventID / cursor | 拒否 |
| ADP-018 | Event replay | HasMore=trueかつeventなし | 拒否 |
| ADP-019 | Event replay | FromCursorが要求cursorと不一致 | 拒否 |

## 6. Capability verification tests

| ID | 観点 | 入力・操作 | 期待結果 |
|---|---|---|---|
| CAP-001 | Identity binding | 別ProviderID | 全操作拒否 |
| CAP-002 | Identity binding | 別AccountID | 全操作拒否 |
| CAP-003 | Identity binding | 別SDKVersion | 全操作拒否 |
| CAP-004 | Identity binding | 別ConfigurationSHA256 | 全操作拒否 |
| CAP-005 | Evidence source | 未知TestID / artifact digest | 拒否 |
| CAP-006 | Evidence tampering | EvidenceSHA256変更 | 拒否 |
| CAP-007 | Evidence level | StartがE2 | Start拒否、credential未取得 |
| CAP-008 | Evidence level | ResumeがE4 | Resume拒否、credential未取得 |
| CAP-009 | Per-capability gate | Start E4、Event replay E2 | Startは許可、Replayは拒否 |
| CAP-010 | Missing capability | Start idempotency / lookupなし | Start拒否 |
| CAP-011 | Duplicate evidence | 同一Capabilityに複数Evidence | 拒否 |
| CAP-012 | Declaration mismatch | 未宣言CapabilityにEvidenceあり | 拒否 |
| CAP-013 | SDK drift | Runtime初期化後にAdapter identity変更 | 次操作前に拒否 |

CAP-007 / CAP-008の拒否ではCredentialBroker.AcquireとAdapter methodの呼出し回数が0であることを検証する。

## 7. Credential isolation tests

| ID | 観点 | 入力・操作 | 期待結果 |
|---|---|---|---|
| SEC-001 | Empty secret | 空secretでLease生成 | 拒否 |
| SEC-002 | Lease scope | WithSecret callback後 | callback copyがzeroizeされる |
| SEC-003 | Close | Close後にWithSecret | 拒否 |
| SEC-004 | Idempotent close | Closeを複数回実行 | 安全に完了 |
| SEC-005 | Concurrent close | callback実行中にClose | lease copyはcallback終了後zeroize |
| SEC-006 | Panic | Adapterがpanic | Lease.Close実行、secret copy zeroize、operation UNKNOWN |
| SEC-007 | Adapter error | raw errorにsecret / tokenを含む | 外部errorにraw文字列が出ない |
| SEC-008 | Start response | SessionID / receiptへsecretを混入 | result不採用、UNKNOWN |
| SEC-009 | Turn output | Outputへsecretを混入 | result不採用、UNKNOWN |
| SEC-010 | Result content | Contentへsecretを混入 | result不採用、UNKNOWN |
| SEC-011 | Lookup result | identity / statusへsecretを混入 | result不採用、UNKNOWN |
| SEC-012 | Usage / event | metadataへsecretを混入 | result不採用、UNKNOWN |
| SEC-013 | Broker error | Acquire errorにsecretを含む | 固定CredentialUnavailableのみ返す |
| SEC-014 | Lease close error | Close失敗 | 成功を返さずUNKNOWN |
| SEC-015 | Log inspection | 全操作のerror / telemetry | credential、token、raw bodyが存在しない |

## 8. Runtime tests

| ID | 観点 | 入力・操作 | 期待結果 |
|---|---|---|---|
| RUN-001 | Valid Start | E2 test verifier + fake Adapter | test-only policyで正常応答を検証 |
| RUN-002 | Unsupported Start | Start capability未検証 | Broker / Adapterを呼ばず拒否 |
| RUN-003 | Valid Send | 同一OperationID / TurnID / SessionID | 応答identityを検証 |
| RUN-004 | Ambiguous Send | timeout / connection loss | UNKNOWNを返す |
| RUN-005 | Stop proof | Quiescentなし | 停止確定を返さない |
| RUN-006 | Resume | E5 evidenceなし | Broker / Adapterを呼ばず拒否 |
| RUN-007 | Lookup | 同一OperationID / TurnID | 結果identityを検証 |
| RUN-008 | Result | content digest mismatch | UNKNOWN |
| RUN-009 | Usage | Complete=false | final Usageとして返さない |
| RUN-010 | Event replay | cursor chain discontinuity | UNKNOWN / invalid result |
| RUN-011 | Deadline | 期限切れrequest | Broker / Adapterを呼ばず拒否 |
| RUN-012 | Context cancellation | call前cancel | Broker / Adapterを呼ばず拒否 |
| RUN-013 | Identity drift | Adapter identity変更 | Broker / Adapterを呼ばず拒否 |
| RUN-014 | Error sanitization | 任意raw error | safe OperationErrorへ変換 |

E2 test-only policyはテストコードからのみ利用可能とし、本番constructorから選択できないことを確認する。

## 9. Store / transaction tests

| ID | 観点 | 入力・操作 | 期待結果 |
|---|---|---|---|
| STO-001 | Atomic append | aggregate更新とreceipt保存 | 同一transactionで確定 |
| STO-002 | Aggregate update failure | Job更新を失敗させる | receiptも残らない |
| STO-003 | Receipt failure | receipt INSERTを失敗させる | Job更新もrollback |
| STO-004 | Idempotency | 同じCommand ID / payload | 同じreceipt |
| STO-005 | Idempotency conflict | 同じCommand ID / 異なるpayload | 拒否、状態不変 |
| STO-006 | Expected revision | stale revision | 拒否、状態不変 |
| STO-007 | Duplicate event | 同じEventID / payload | 二重保存しない |
| STO-008 | Event conflict | 同じEventID / 異なるpayload | 拒否 |
| STO-009 | Read-back | commit outcome UNKNOWN | receipt / Fact IDで確定 |
| STO-010 | Restore | 再起動後のJob読み戻し | invariantを再検証 |
| STO-011 | Corruption | identity / revision / digest破損 | fail-closed |
| STO-012 | Secret scan | aggregate / receipt / serialized JSON | credential候補0件 |
| STO-013 | Stop facts | intent / observationのaggregate更新またはreceipt INSERT失敗 | 両方rollbackし、同一Command IDで再試行可能 |
| STO-014 | Result / Usage facts | aggregate更新またはreceipt INSERT失敗 | 両方rollbackし、同一Command IDで再試行可能 |
| STO-015 | Result / Usage idempotency | 同じCommand ID / payloadの再送、異なるpayloadでの再利用 | 同じfact / receiptへ収束、異なるpayloadは拒否 |
| STO-016 | Result / Usage restore | restart後のResult / Usage facts | identity、append順序、digest、Complete finalityを再検証 |
| STO-017 | JobOutput persistence | completed Coder Turnとfinal Result / Usage、Stop proofに束縛した出力 | exact replayとrestart後のrestore |

## 10. Controller E2 integration tests

| ID | シナリオ | 期待結果 |
|---|---|---|
| CTR-001 | 有効なPlanningAuthorizationでStart | intent commit後にのみAdapter呼出し |
| CTR-002 | grant期限切れ | Provider呼出し前に拒否 |
| CTR-003 | Provider identity mismatch | Broker / Adapter呼出し前に拒否 |
| CTR-004 | intent transaction rollback | Provider呼出し0回 |
| CTR-005 | intent commit UNKNOWN | read-back確定までProvider呼出し0回 |
| CTR-006 | commit確認後にJob revision競合 | authority再検証で拒否 |
| CTR-007 | Adapter Start timeout | 同一OperationIDのUNKNOWNを保存 |
| CTR-008 | Send timeout | 同一TurnIDのUNKNOWNを保存 |
| CTR-009 | UNKNOWN Send | 新しいTurnIDの発行を拒否 |
| CTR-010 | Lookupで同一identityのterminal result | 新Observationを追加して解消 |
| CTR-011 | Resume capability不足 | 新Sessionを作成せず拒否 |
| CTR-012 | Resume ATTACHED前 | 次Turnを拒否 |
| CTR-013 | Stop proof不足 | Worktree release / completionを拒否 |
| CTR-014 | Usage未確定 | budget finalization / completionを拒否 |
| CTR-015 | credential leak | resultを保存せずUNKNOWN |
| CTR-016 | Observation保存失敗 | Provider結果を成功扱いせずUNKNOWN / recovery対象 |
| CTR-017 | Start intent ordering | Start intent commit前にAdapterを呼び出す | テスト失敗 |
| CTR-018 | Ambiguous Start | UNKNOWN後に同じOperationIDでLookup | 新しいStartを発行せずObservationを追記 |
| CTR-019 | Expired grant recovery | 期限後に既存Startを照合 | Lookupは許可し、NOT_FOUND後の新規Startは拒否 |
| CTR-020 | Unsafe retry | UNKNOWN後にLookup / idempotency capabilityがない | blind replayを拒否しUNKNOWNを保持 |
| CTR-021 | Documents context | ID重複、content digest不一致、aggregate digest不一致 | Start intent作成前に拒否 |
| CTR-022 | Event replay | accepted Session、cursor prefix、event / turn identityが一致 | append-only Observationとして保存し、同一page再送は冪等 |
| CTR-023 | Result retrieval | accepted Start / Turn、E4 Final Output capability、identity一致 | Result observationをappend-only保存 |
| CTR-024 | Result validation | Final=false、identity mismatch、digest mismatch | 保存せず拒否 |
| CTR-025 | Usage retrieval | E4 Usage identity / completeness、Complete=false | 途中Observationとして保存し、final usageにしない |
| CTR-026 | Usage completion | 同一UsageIDのComplete=false後にComplete=true | 既存factを変更せず新Observationを追加 |
| CTR-027 | Capability gate | Result / Usage capability不足 | credential / Adapter呼出し前に拒否 |
| CTR-028 | Provider retrieval failure | Result / Usage取得時にRuntime error | 成功Observationを追加しない |
| CTR-029 | Send recovery | UNKNOWN Turnに対して同じOperationID / TurnIDでLookup | 新しいTurnIDを発行せず、verified NOT_FOUND後だけ再送 |
| CTR-030 | Send capability gate | Send idempotency / lookupのE4 evidence不足 | Turn intent作成・Runtime.Send前に拒否 |
| CTR-031 | Resume intent ordering | E5 SessionReattach evidence / accepted Session一致 | Runtime.Resume前にResume intentとreceiptを永続化 |
| CTR-032 | Resume ambiguity | Runtime.Resumeのambiguous failure | 同じOperationIDのUNKNOWN observationを保存 |
| CTR-033 | Resume reconciliation | UNKNOWN Resumeへの同じOperationID / SessionIDのATTACHED evidence | ReconcilesUnknown=trueでappend-only解消 |
| CTR-034 | Resume capability gate | E5 SessionReattach evidence不足 | Resume intent作成・Runtime.Resume前に拒否 |
| CTR-035 | Resume session binding | accepted Session不一致、別Sessionへの付け替え | 拒否し、既存Sessionを維持 |
| CTR-036 | Task Coder Start | exact current approval / Task / permission / RoleExecution / intent / Provider identity | intent永続化後、同一OperationIDでStartしACCEPTEDを保存 |
| CTR-037 | Task Start permission stale | approval / revision / scope / expiry / budgetがdispatch直前に不一致 | Provider呼出し0回、Job facts不変 |
| CTR-038 | Task Start Provider mismatch | Provider / account / configuration digest mismatch | Lookup / credential / Adapter呼出し前に拒否 |
| CTR-039 | Task Start retry | 既存intent、observationなし、Start LookupでNOT_FOUND | permission再検証後、同じOperationIDでのみStart |
| CTR-040 | Task Start ambiguous | StartまたはLookupがambiguous / unclassified error | UNKNOWNをappendし、新しいOperationIDで再送しない |
| CTR-041 | Task Start reconciliation | UNKNOWNに対する同一OperationIDのaccepted / failed Lookup evidence | ReconcilesUnknown=trueでappend-only解消 |
| CTR-042 | Task Start unsafe capability | Start Lookup / idempotency evidenceなし | Startを呼ばずUNKNOWN / unresolvedを保持 |
| CTR-043 | Reviewer Start boundary | immutable Worktree snapshot / read-only enforcement evidenceなし | Provider dispatch 0回 |
| CTR-044 | Task prompt integrity | caller-supplied prompt / context injection、Task immutable fieldsの改変 | 任意promptを受け付けず、deterministic prompt digestのみ使用 |

## 11. Regression tests

- PR #21: PlanningAuthorization発行、identity / scope / budget / deadline
- PR #25: Taskless PLANNER Start intent、DRAFT維持、idempotency
- PR #28: turn / effect / resume lifecycle、UNKNOWN、boundary denial、revision順序
- ADR-201 / ADR-202: JobAggregate唯一正本、append-only、transaction、recovery
- ADR-500: DRAFT read-only、禁止副作用、権限拡張禁止

既存テストを削除・弱体化して新テストを通さない。

## 12. CI / 完了判定

リリース準備段階に入るまではLinuxのみを自動実行対象とする。

必須:
- gofmt
- go vet
- go test ./...
- go test -race ./...
- go build ./...
- Linux CI
- Secret scan（差分とcredential関連実装を目視確認）
- Category sync
- Link/reference sync
- Independent review completion

Windows buildとGoReleaser cross-build（macOS targetを含む）は、リリース準備段階で検証を再開する。macOS native検証は任意とする。

CI成功はE2の証拠である。E4 / E5 / E6の完了を意味しない。
