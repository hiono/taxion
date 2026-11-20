# ADR-501: Trusted Ingressで認証済みHUMAN Principalを確定する

Date: 2026-11-20

## Status

Accepted

## Context

TaxionはJob intake時に認証済みhuman actor、intake source、時刻を記録し、JobApprovalを認証済みprincipalへ束縛する必要があります（ADR-500）。しかし、自然言語Body、任意のHTTP header、client-supplied user ID、email、Role名、外部Agentの自己申告は本人性の証明になりません。

認証方式を特定ベンダーのAPIへ固定すると、利用環境の選択を不必要に制約します。一方、認証済みprincipalを任意のIngress handlerが組み立てられる構造では、信頼境界を迂回できます。Taxionは標準化された人間認証と、検証済みidentityをJob commandへ渡す単一の内部境界を必要とします。

## Decision

**人間の認証には、管理者が明示設定したOpenID Connect（OIDC）issuerを信頼源として使用し、Trusted Ingressだけが検証済みHUMAN Principalを生成します。**

### 1. Human authentication authority

- 標準のinteractive authentication flowはOIDC Authorization Code Flow with PKCEとします。
- Web UIとnative CLIは外部system browserを使用し、CLIはloopback redirectとPKCEを使用します。client secretを配布物へ埋め込みません。
- 特定の商用Identity Providerには固定しません。管理者がissuer URL、client ID、Taxion向けaudience、許可する署名方式および必要なendpoint設定を明示的に登録します。
- 信頼するissuerは事前設定されたallowlisted issuerに限定します。tokenの未検証issuer claimや利用者入力からissuer metadata / JWKS URLを動的に選択しません。
- OIDC discovery metadataとJWKSはTLSで取得し、設定済みissuerとの一致を確認します。
- ID Tokenは署名、issuer、audience、必要に応じたauthorized party、expiration、issued-at、nonceを検証します。PKCE verifierとstateも認証開始時の値と照合します。検証不能・未知の署名方式・key取得不能・claim不一致は認証失敗とします。
- email、preferred_username、display name等の可変profile claimは表示・連絡用属性に限り、principal identityや承認者の同一性判定に使用しません。

### 2. Canonical HUMAN Principal

- 外部identityの一意な組は、検証済みOIDC `issuer` と不透明な `subject`（`sub`）とします。
- Taxionはこの組を一意制約付きで内部のopaque PrincipalIDへ対応付けます。同じissuer / subjectは同じPrincipalIDへ収束し、異なるissuerまたはsubjectをemail一致だけで自動統合しません。
- Principalは少なくとも `kind=HUMAN`、PrincipalID、issuer、subject、authentication method、authenticated-atを持ちます。必要な追加claimは明示的な目的がある場合だけ保存します。
- Principalは認証検証成功後にTrusted Ingressが生成し、内部Controllerへ不変値として渡します。Domain commandはclient-supplied PrincipalID、issuer、subjectを受け付けて本人性を確定しません。
- 生のID Token、Access Token、Refresh Token、Cookie、Authorization header、秘密鍵はJob、IntakeProvenance、Issue、ログへ保存しません。

### 3. Trusted Ingress boundary

- HTTP / CLI等のIngress adapterは認証結果を検証し、AuthenticatedPrincipalとserver-derived ingress sourceを生成します。Controllerはその型付きcontextを受け取り、Job commandへ束縛します。
- request body、query、任意header、自然言語Bodyからprincipalや認証状態を取得しません。利用者が送信した `X-User` 等のidentity headerは信頼しません。
- 初期実装では認証検証とControllerのPrincipal引渡しを同一の信頼されたprocess内に置きます。将来IngressとControllerを別processへ分離する場合は、mTLSとaudience・有効期限・一意nonceに束縛した署名済み内部assertionを別途設計し、未検証header転送を認証境界にしません。
- Ingress source（WEB、CLI、API等）は、実際に要求を受け付けたserver-side adapterが設定します。clientが任意に指定したsource値を正本にしません。
- A2A Gatewayで認証された外部AgentはHUMAN Principalではありません。外部Agent identity、Actor identity、service identityを人間の承認主体へ変換しません。
- Fake Principalや固定テストidentityはtest harness専用とし、production構成で有効化できないようにします。
- 既存の未認証ローカルCLI DRAFT作成機能は、Trusted Ingress適合経路として扱いません。認証済みprincipalを取得するまで製品のtrusted intake、PlanningAuthorization、JobApprovalへ接続しません。

### 4. Intake provenance

Job作成時に、原文依頼と同じJobAggregate transactionでimmutableなIntakeProvenanceを保存します。少なくとも次を含めます。

- PrincipalIDおよびprincipal kind
- 認証issuerとsubjectの識別情報
- server-derived ingress source
- 認証時刻とTaxionが受け付けた時刻
- Taxion生成のrequest identity
- 原文依頼のcanonical payload digest
- provenance schema version

IntakeProvenanceは原文依頼・Job identity・認証済みprincipalへ束縛し、更新で上書きしません。再送・重複排除はJobAggregate Commandのidempotency規則に従います。秘密情報、token、cookie、不要なprofile claimをprovenanceへ含めません。

### 5. Failure behavior and authorization separation

- 認証されていないrequest、issuer未登録、署名不正、audience不一致、期限切れ、nonce/state不一致、principal mappingの矛盾、provenance検証失敗ではJob作成・承認・外部副作用を開始しません。
- 認証成功はJobApprovalやExecution Permissionを付与しません。JobApprovalは別途、認証済みHUMAN Principalが正確なJobContract revisionを明示承認した事実として記録します。
- 認証済みprincipalと現在のJobApproval、scope、budget、deadlineは、承認またはeffect実行時にControllerが再検証します。
- 認証済み接続の存在やRole名だけで他Jobの参照・変更を許可しません。
- principalの失効・issuer設定変更・account無効化はIngress sessionと新しい操作の認証判定へ反映します。過去のappend-only provenanceとapproval factは履歴として保持し、過去の本人性を別principalへ書き換えません。

## Consequences

- TaxionのHUMAN identityは、設定済みOIDC issuerが認証したissuer / subjectの組に由来します。
- Identity Providerの商用ベンダーを固定せず、OIDC準拠の複数環境へ対応できます。
- Principal生成箇所をTrusted Ingressへ限定し、body/header spoofingやAgent自己申告による権限取得を防ぎます。
- 既存の未認証CLI経路は製品trusted intakeとして利用できず、認証adapterとの接続が必要です。
- OIDC issuer設定、session管理、principal mapping、IntakeProvenance保存に実装・運用コストが発生します。

## Security and operational impact

issuerは管理者が明示設定し、未検証tokenのclaimから信頼先を選択しません。OIDC署名・issuer・audience・期限・nonce・PKCEを検証し、token等のcredentialをJobやModel / Sandboxへ露出しません。Principalはissuer / subjectに束縛し、email等の可変属性による自動account linkingを禁止します。A2A Agentとservice identityを人間の承認主体から分離します。認証・provenanceが不明または不整合の場合はfail-closedとします。

## Alternatives considered

- **利用者指定のuser ID / email / headerをprincipalとする:** callerが任意に偽装でき、本人性を証明しないため採用しません。
- **特定商用Identity Providerへ固定する:** 利用環境を不必要に制限し、Taxionの認証境界を単一ベンダーのAPIへ結合するため採用しません。
- **独自のusername / password認証を実装する:** credential保管、password reset、MFA、攻撃対策をTaxionが独自に担うことになり、OIDC issuerを利用するより責務が増えるため採用しません。
- **reverse proxyのidentity headerを無条件に信頼する:** proxy設定ミスや直接到達でspoofingできるため採用しません。別process境界は認証済みassertionと相互認証を別途要求します。
- **OS process userを認証済みHUMANとみなす:** ローカルprocess identityは製品利用者の認証・明示承認を証明しないため採用しません。
- **外部A2A AgentをHUMAN Principalへ統合する:** Agentのidentityは人間の意思・承認を証明しないため採用しません。

## Acceptance criteria

- 設定済みOIDC issuer以外のtokenを受け付けません。
- signature、issuer、audience、expiration、nonce、PKCE / stateの不正を拒否します。
- issuer / subjectが同じ場合にPrincipalIDが安定し、email変更でPrincipalIDが変化・統合しません。
- request body、query、任意headerの偽装principalを拒否します。
- A2A Agent / service identityがJobApprovalのapproverになれません。
- Job作成時にPrincipalとIntakeProvenanceが原文依頼と同一Job transactionで保存され、改ざん・不一致を検出します。
- 認証失敗時にJob、Approval、Provider operation等の副作用がありません。
- token、cookie、secretがJob、provenance、ログへ保存されません。
- 未認証ローカルCLI経路はproduction trusted intakeとして有効になりません。

## Relationship to other ADRs

- ADR-000は設計判断と文書の規範性を定義します。
- ADR-100 / ADR-101は製品目的と再構築原則を定義します。
- ADR-201はPrincipalとIntakeProvenanceを含むJobAggregateの保存責務およびCommand境界を定義します。
- ADR-202は認証・provenanceの不変条件を全状態経路で強制します。
- ADR-500はJobApproval、PlanningAuthorization、禁止副作用および実行許可を定義し、本ADRはそれらが参照するHUMAN identityの信頼源を定義します。
- ADR-502はIngress ClientとHUMAN Principal、およびTaxionが選択するExecution Agentの役割・identity分離を具体化します。
- ADR-700は外部A2A Agentとの認証・相互運用境界を定義します。A2A Agent identityはHUMAN Principalではありません。
