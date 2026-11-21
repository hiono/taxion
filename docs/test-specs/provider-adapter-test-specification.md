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

## 11. Regression tests

- PR #21: PlanningAuthorization発行、identity / scope / budget / deadline
- PR #25: Taskless PLANNER Start intent、DRAFT維持、idempotency
- PR #28: turn / effect / resume lifecycle、UNKNOWN、boundary denial、revision順序
- ADR-201 / ADR-202: JobAggregate唯一正本、append-only、transaction、recovery
- ADR-500: DRAFT read-only、禁止副作用、権限拡張禁止

既存テストを削除・弱体化して新テストを通さない。

## 12. CI / 完了判定

必須:
- gofmt
- go vet
- go test ./...
- go test -race ./...
- go build ./...
- Secret scan（差分とcredential関連実装を目視確認）
- Category sync
- Link/reference sync
- Independent review completion

Windows native testおよびGoReleaser Windows / macOS targetは、Repositoryがpublicでtag releaseを行う段階で実行する。

CI成功はE2の証拠である。E4 / E5 / E6の完了を意味しない。
