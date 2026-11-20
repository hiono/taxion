# ADR-502: 人間起点の依頼とAgent実行先を分離する

Date: 2026-11-20

## Status

Accepted

## Context

Taxionへの依頼は、人間が自然言語で意図を示し、Copilot、OpenCode、Claude Code、Antigravity等のCoding AgentやCLIが、その意図を構造化された指示へ変換して送信する形を取り得ます。この場合、payloadを直接作成した主体はIngress Clientですが、Jobの依頼者はTaxionへの送信を認証済みの形で許可した人間です。

TaxionはJobの内容・制約・受入条件に基づき、外部Agentへ実行を委譲します。依頼をTaxionへ届けるCoding Agentと、Taxionが実行先として選択するAgentは、同じ製品である場合があっても異なる役割です。

Ingress Clientの認証情報を人間の本人性とみなすと、Agentやservice credentialが人間の依頼・承認を偽装できます。Ingress ClientとExecution Agentを同一視すると、TaxionがJobに適した実行先を独立して選択・管理する責務が曖昧になります。

## Decision

**TaxionのJobは認証済みHUMAN Principalを依頼主体として受け付け、Ingress Clientとは独立して、Jobの実行先となるAgentをTaxionが選択・ルーティングします。**

### 1. Human-originated request

- Jobの依頼主体および人間承認の主体はHUMAN Principalとします。認証とPrincipal生成はADR-501に従います。
- 人間はTaxionへ直接依頼を送信することも、Coding Agent、CLI、Web UI、その他のIngress Clientを介して依頼を送信することもできます。
- Coding Agentが自然言語の依頼を整理・要約・分解し、構造化された指示へ変換しても、その内容が人間の認証済み依頼として受け付けられた場合、Jobの依頼主体は当該HUMAN Principalとします。
- 依頼本文、prompt、metadata、任意header、client-supplied user ID、Agentによる「人間から依頼された」という自己申告は、HUMAN Principalの本人性を証明しません。
- Ingress Clientを介した依頼は、ADR-501に従う認証済みHUMAN session、または認証済みHUMAN Principalが発行しTrusted Ingressが検証できる委譲assertionに束縛します。Agent自身のservice credentialだけでHUMAN Principalを確定しません。
- 委譲assertionを使用する場合、対象HUMAN Principal、Taxion audience、intake用途、期限および一意性を検証します。委譲はJobApproval、PlanningAuthorization、Execution Permissionを付与しません。
- 認証済みHUMAN Principalへ束縛できない依頼は、human-originated trusted intakeとしてJobを作成しません。

### 2. Ingress Client identity

- Ingress Clientは、人間の依頼をTaxionへ伝達する接続元・入力仲介者として扱います。例としてCoding Agent、CLI、Web UI、API clientを含みます。
- Ingress Clientのidentity・client credential・adapter種別は、HUMAN Principalとは別の属性として扱います。
- Ingress Clientが認証される場合、そのclient identityを独立して検証・記録できます。ただしclient identityをHUMAN Principalへ昇格・変換しません。
- Ingress Clientは依頼内容の構造化や伝達を行えますが、それ自体をJobの依頼者、承認者、Taxion内部Actor、Execution Agentとはみなしません。
- IntakeProvenanceは、HUMAN Principalとserver-derived ingress sourceに加え、取得可能な場合はIngress Client identityおよびclient種別を別フィールドとして記録します。raw token、cookie、secret等のcredentialは保存しません。

### 3. Taxion-controlled Agent routing

- Taxionは受け付けたJobの目的、制約、必要能力、承認境界、資源条件および実行状態に基づき、外部Execution Agentを選択・ルーティングします。
- Ingress Clientは、同じAgent製品であるという理由だけで、そのJobのExecution Agentに自動選択されません。依頼者が候補を指定した場合も、TaxionはJobの制約・能力・信頼条件を検証します。
- Taxionが選択した外部Execution Agentとの相互運用はADR-700のA2A境界に従います。内部6 Actor間通信はADR-300のJobMessageを維持します。
- Execution Agentのidentityは外部Agent identityとして扱い、HUMAN PrincipalやIngress Client identityと混同しません。
- Execution Agentへのルーティング、Agentからの応答、成果物の受領は、HUMAN Principalの認証、JobApproval、PlanningAuthorizationまたはJob完了を代替しません。結果はControllerによる検証とJobAggregateへの記録を経て反映します。
- 同一の製品・プロセスがIngress ClientとExecution Agentの両方を担うことは可能です。ただし、それぞれの接続、identity、認証、権限、provenanceおよびライフサイクルは独立して扱います。

### 4. Authority and provenance

- Jobの依頼主体はHUMAN Principalです。Ingress ClientとExecution Agentは依頼の伝達・実行を補助する別個の主体です。
- JobApprovalはHUMAN Principalによる明示的な承認事実とし、Ingress ClientまたはExecution Agentの操作・応答から生成しません。
- IntakeProvenanceには、認証済みHUMAN Principal、server-derived ingress source、受信時刻、request identity、payload digestを必須記録とし、利用可能な場合はIngress Client identityを独立して記録します。
- Agentが変換した構造化payloadのdigestは、Taxionが実際に受け付けたcanonical payloadに対して計算します。変換前の人間の原文が送信されていない場合、その原文を受領・保存したと推定しません。
- 認証・委譲・provenance・Agent identityのいずれかに不整合がある場合はfail-closedとし、Job作成、承認または外部実行を開始しません。

## Consequences

- Coding Agent経由の入力であっても、認証済み人間の依頼としてJobを開始できます。
- Coding Agentのclient credentialや自己申告を人間の本人性・承認権限として扱いません。
- Taxionは入力仲介者から独立してExecution Agentを選択・ルーティングできます。
- Ingress ClientとExecution Agentが同じ製品であっても、役割とidentityの混同を防止できます。
- Trusted Ingressは人間認証に加えて、Ingress Clientの識別および人間による委譲の検証を実装する必要があります。

## Security and operational impact

人間の本人性はADR-501のOIDC issuer / subjectに基づいて確定します。Agent credential、自然言語本文、任意header、client metadataを人間認証の代替にしません。委譲assertionはTaxionへのintake用途・audience・期限・一意性へ束縛し、承認権限へ拡張しません。Ingress Client、HUMAN Principal、Execution Agentのidentityとprovenanceを分離し、認証不能・委譲不正・主体の不一致ではfail-closedとします。外部Agentへの実行要求はJob authorityとControllerの検証を通し、A2Aの応答だけでJob状態や権限を変更しません。

## Alternatives considered

- **Ingress ClientをJobの依頼主体とする:** Coding Agent等の接続元が人間の意思や承認を証明しないため採用しません。
- **依頼本文の「人間からの依頼」という記載を信頼する:** 本文は送信者が自由に作成でき、本人性を証明しないため採用しません。
- **Ingress Clientを常にExecution Agentとして扱う:** 入力仲介と実行先選択を結合し、TaxionによるJob単位の独立したルーティングを妨げるため採用しません。
- **Ingress ClientとExecution Agentを常に別製品に限定する:** 同一製品が異なる接続・認証・権限境界で両方の役割を担う可能性を不必要に排除するため採用しません。
- **Agentのservice credentialだけで人間の委譲を推定する:** Agentの接続権限は人間の依頼・承認を証明しないため採用しません。

## Acceptance criteria

- 認証済みHUMAN Principalを伴わないCoding Agentの依頼をtrusted human intakeとして受け付けません。
- request body、query、任意header、client-supplied user IDによるPrincipal偽装を拒否します。
- Ingress Client identityとHUMAN Principal identityを別々に検証・記録します。
- 有効な人間の委譲を伴うIngress Clientの依頼は、元のHUMAN Principalに束縛されたJobとして記録します。
- 委譲assertionだけでJobApproval、PlanningAuthorization、Execution Permissionを生成しません。
- TaxionはIngress Clientとは独立してExecution Agentを選択できます。
- Execution Agent identityをHUMAN PrincipalまたはIngress Client identityへ変換しません。
- Ingress ClientとExecution Agentが同じ製品である場合も、別々のidentity・権限・provenance境界を維持します。
- A2A応答やAgentの自己申告だけでJobの承認・完了・権限を確定しません。

## Relationship to other ADRs

- ADR-100は人間の曖昧な意図を継続可能な協調作業へ昇華する製品目的を定義します。
- ADR-201はJobAggregateとIntakeProvenanceの保存・Command境界を定義します。
- ADR-202はidentity、scope、provenance等の不変条件を全状態経路で強制します。
- ADR-300はTaxion内部6 ActorのJobMessage通信を定義します。
- ADR-500はJobApproval、PlanningAuthorization、禁止副作用およびExecution Permissionを定義します。
- ADR-501はHUMAN PrincipalのOIDC認証とTrusted Ingress境界を定義します。
- ADR-700はTaxionが選択する外部Execution AgentとのA2A相互運用境界を定義します。
