# ADR-300: Taxion内部の6 Actor通信をJobMessageへ統一する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の設計資料に記録されたJobMessage、6 Actor identity、Envelope / Body分離、Question / Answer / Handoff、配信観測の意味を確認し、新しい履歴へ再確立しました。

## Context

Jobは利用者の依頼、承認、協調、成果および証拠の唯一の継続単位であり、JobAggregateがその正本です。JobMessageはappend-onlyの協調事実であり、メッセージや回答は実行権限を付与しません。

通信主体をRole文字列だけで表現すると、利用者、Controller、Orchestrator、planning agent、implementation agent、review agentを同じActorモデルとして識別できず、実行主体のprovenanceと宛先の検証が曖昧になります。利用者にJSON、固定タグ、QuestionID、Source / Target等を本文へ記述させる方式も、自然言語による利用を妨げます。

## Decision

**Taxion内部のHUMAN / CONTROLLER / ORCHESTRATOR / PLANNER / CODER / REVIEWER間の通信は、JobAggregate内のappend-only JobMessageを共通契約として扱います。**

### 1. 共通Actor identity

通信主体は次の6種類とします。

- HUMAN
- CONTROLLER
- ORCHESTRATOR
- PLANNER
- CODER
- REVIEWER

Actor種別とActor実体識別子を分離します。

- HUMANは認証済み利用者principalに結び付けます。
- CONTROLLERとORCHESTRATORはTaxionが管理するcomponent identityに結び付けます。
- PLANNER、CODER、REVIEWERはRoleExecutionIDに結び付けます。Role種別だけで実行主体を識別しません。
- Sender Actorは認証済み接続、実行中RoleExecution、またはController内部のcall contextからシステムが確定します。本文中の自己申告をActor identityとして扱いません。

共通モデルは6種類の非自己宛て有向関係を表現できます。これは全経路を許可する意味ではありません。送信、受信、応答、転送の可否はActor種別、Actor実体、Job状態、RoleExecution状態、Task scopeおよび既存認可境界に基づきControllerが判定します。

### 2. Envelopeと自然言語Bodyを分離する

JobMessageは機械処理用Envelopeと自然言語Bodyを保持します。Envelopeは少なくとも次を含みます。

- 一意なMessageIDおよびJobID
- システムが確定したSender ActorRef
- Controllerが検証したRecipient ActorRef
- MessageKind
- ThreadIDおよび任意のReplyToMessageID
- 作成時刻、送信元provenance、必要なRoleExecution / Task scope
- 冪等性および配信・応答観測を相関する識別情報

Bodyは利用者 / Agent共通の自然言語テキストとします。本文に固定JSON、タグ、Actor名、ID、routing metadataを要求しません。Agentの出力も同じBodyとして保存します。

利用者の宛先指定はUI上の会話相手・操作contextで受け付けます。通常の入力欄に構造化Envelopeを入力させません。自由入力の既定MessageKindはMESSAGEとし、QUESTIONやHANDOFF等の意味が必要な場合はUI上の操作または明示的Controller commandで指定します。自然言語解釈やLLM分類は候補提示に利用できますが、それだけでActor identity、宛先の実体、認可、承認、Handoffを確定しません。曖昧な宛先や操作は必要な一点を確認します。

### 3. 入力受付とidentityの確定

- 認証済み利用者入力経路からの自然言語入力は、Envelope metadataが未指定または不完全でも受理し、Controllerが信頼できるcontextから正規化します。
- Job、Thread、Recipient、ReplyTo等は、現在のUI / conversation contextから一意に解決できる場合だけ補完します。
- 複数候補がある、または必要情報が不足する場合は推測せず利用者に確認します。確認前に曖昧な宛先への配信や状態変更を行いません。
- AgentまたはSystemの認証済み入力経路から到着したmetadata欠落・不完全・矛盾メッセージを利用者入力へ変換しません。送信元認証情報を保持したまま検証失敗として拒否または隔離します。
- metadata補完はidentity変更や認可迂回を許可しません。補完後もRecipient、Job scope、MessageKindおよび操作をController policyで検証します。

### 4. 宛先とfan-out

基本単位は1メッセージ・1宛先です。複数宛先への送信は、Controllerが共通BatchIDで相関した宛先別メッセージを同一Job commandで記録するfan-outとします。宛先ごとの配信、応答、拒否、期限切れを独立して追跡します。複数宛先を一つの共有応答状態へ押し込みません。

転送・エスカレーションは元メッセージを変更せず、新しいSender、Recipient、MessageIDを持つメッセージとして記録し、元メッセージとの相関を保持します。

### 5. MessageKind

MessageKindは少なくとも次を区別します。

- MESSAGE: 通常の情報共有
- QUESTION: 回答を求める情報照会
- ANSWER: 特定のQUESTIONへの回答
- REQUEST: 情報提供または作業を依頼するメッセージ。担当責任は移転しません。
- NOTIFICATION: 応答を要求しない通知
- HANDOFF: 作業または実行責任を移転する明示的な協調intent

QUESTION / ANSWERはJobMessageの相関関係として表現し、独立Clarification runtimeや固定質問schemaを新設しません。ANSWERは元QUESTIONをReplyToMessageIDで参照します。回答・返信は元メッセージを変更せずappend-onlyで追加します。

REQUESTは受信者に行動を求めますが、担当・実行責任を移転しません。責任移転が必要な場合はHANDOFFを明示します。自然言語本文の意味解釈だけでREQUESTをHANDOFFへ昇格させません。

### 6. Question / AnswerとHandoffを分離する

Question / Answerは情報交換であり、それだけではRoleExecution、Task ownership、Provider sessionまたはWorktree ownershipを移転しません。

HANDOFFはJobAggregate commandとして記録します。受信RoleとTask scopeを明示し、送信側Providerの独立した停止証明、terminal result / usageの照合、必要な成果物の固定、Worktree ownership releaseが永続化された後に限り、Controllerが受信側の実行を許可します。WAITING、SUSPENDEDまたはHandoff messageの存在を停止証明として扱いません。

PlanningAuthorization、JobQuestionAuthorization、JobApproval、Task scope、Effect approvalおよびProvider operation authorityは通信によって作成・変更・拡張されません。

承認済みJob内でRoleからTaskless PLANNERへ質問し、個別のbounded read-only executionが必要な場合は、ADR-201 / ADR-500のJobQuestionAuthorizationを別途検証します。これはJobMessageのEnvelopeやQUESTION種別とは独立したimmutable grantです。Question本文、QUESTIONの保存、ANSWERの受信だけではgrantを発行・拡張しません。

### 7. 配信・応答・再送・期限

JobMessageの保存と外部Actorへの配信を別の事実として扱います。JobMessageを先にJobAggregateへ確定し、その後の配信結果を宛先単位のappend-only observationとして記録します。

- REQUESTED、ACCEPTED、FAILED、UNKNOWNを区別します。
- 応答待ち、回答済み、取消し、期限切れは関連Messageとlifecycle observationから導出します。
- 同じMessageID / BatchIDの再送は冪等に処理し、同一メッセージを重複して新規作成しません。
- 配信結果UNKNOWNを未配信と推定しません。安全に照会・再送できない場合は不明状態を保持します。
- 期限と取消しは明示的なappend-only factsとし、過去の本文・Envelopeを削除または書き換えません。
- 同期UI応答は非同期の永続モデル上の待機表示として実現します。

Exactly-once deliveryを保証しません。配信基盤の再試行とMessageIDによる重複排除を組み合わせ、配信試行と結果の追跡可能性を提供します。

### 8. ControllerとOrchestrator

Controllerは認証済みActor endpointであると同時に、Envelope生成、宛先検証、Actor policy、Job scope、永続化、配信観測、冪等性および実行権限の境界を強制します。Controller endpointとしての会話と認可・操作command APIを混同しません。メッセージ本文をController commandの認証・承認入力として直接実行しません。

Orchestratorは独立したActor endpointとしてJob内の調整・ルーティング依頼・通知を行えます。ただしOrchestratorであること自体はJobApproval、Provider operation、Task作成またはWorktree ownershipを付与しません。外部副作用を伴う操作はControllerの既存commandと認可境界を通します。

### 9. Append-onlyとprovenance

JobMessage、宛先別配信観測、回答・転送相関およびHandoff intentはJobAggregateに属するappend-only factsとします。Sender、Recipient、Job、RoleExecution、Task scope、時刻、MessageIDおよび相関IDを監査可能にします。配信状態や未回答状態をprojectionとして構築してもよいですが、projectionを正本にしません。

独立Message DB、旧Task / Dispatch / ReviewFlow通信モデル、Clarification専用runtime、旧Memory storeを再導入しません。

### 10. Actor policyと曖昧性処理

送信・受信・回答・転送の可否は共通policyで判定します。全30の非自己宛て組み合わせをモデル化できることと、すべてを許可することを区別します。RoleExecutionが終了・停止・期限切れの場合の送信や回答は、そのprovenanceと許可された継続方式を検証します。

LLMは本文分類、要約、宛先候補、回答候補を提案できます。ただし曖昧な場合は確認し、LLM解釈だけで宛先、本人性、policy、承認、実行またはHandoffを確定しません。

## Security and operational impact

Sender / Recipient / Job / Task / RoleExecution identityは認証済みcontextとControllerが確定し、自然言語BodyやAgentの自己申告から推定しません。JobMessage、QUESTION、ANSWER、REQUEST、NOTIFICATION、HANDOFFはそれ自体でJobApproval、Execution Permission、Provider authorityを作成・拡張しません。外部入力はuntrusted inputとして検証し、配信結果UNKNOWNを未配信と推測してblind resendしません。Messageと配信観測のprovenanceをJobAggregateに保持します。

## Alternatives considered

- **利用者にJSONや固定タグを入力させる:** 自然言語による利用要件に反するため採用しません。
- **Actorごとに専用Message APIを作る:** 6 Actor間で共通の相関、監査、配信、policyを維持できないため採用しません。
- **Role文字列だけをActor identityとして使う:** Controller / OrchestratorとRoleExecution provenanceを正しく識別できないため採用しません。
- **Question / AnswerとHandoffを同一操作にする:** 情報交換が作業責任や実行可能性を暗黙に変更するため採用しません。
- **Message本文やQUESTION種別から認可を推定する:** JobQuestionAuthorization等の明示的な権限境界を迂回するため採用しません。
- **Exactly-once deliveryを保証するとする:** 配信基盤の実際の保証を超えるため採用しません。
## Consequences

- 利用者 / Agentの本文形式を統一し、EnvelopeはUI・Controller・Runtimeが生成・検証します。
- 質問・回答はJobMessageとしてJobAggregateに統合します。
- HandoffはProvider停止、結果照合、Worktree ownershipの確定ゲートを維持します。
- 配信状態はappend-only observationから導出し、JobAggregateを唯一の正本とします。
- 30の非自己宛てActor関係は表現可能にしつつ、Controller policyで許可を制御します。

## Acceptance criteria

- 6 Actor種別と実体identityを共通Envelopeで表現できます。
- 全30の非自己宛てActor関係をモデル化でき、共通policyで許可を制御します。
- 利用者とAgentが同じ自然言語Bodyを使用し、利用者に構造化metadata入力を要求しません。
- metadataは認証済みActorとController contextから生成し、曖昧な宛先は推測しません。
- MESSAGE / QUESTION / ANSWER / REQUEST / NOTIFICATION / HANDOFFの意味と相関を検証できます。
- Question / Answerは実行責任を移転せず、HandoffはProvider停止・結果照合・Worktree release gateを維持します。
- 配信、応答、取消し、期限切れ、再送および不明結果をappend-onlyで監査できます。
- 通信によってJobApproval、PlanningAuthorization、Task scope、Effect approvalまたはProvider authorityが変化しません。

## Relationship to other ADRs

- ADR-100は製品目的を定義します。
- ADR-101は旧設計の制約を温存しない再構築原則を定義します。
- ADR-201はJobAggregate、JobMessageの保存境界、Provider / Worktree factsおよびCommand境界を定義します。
- ADR-500は通信によって認可・承認・禁止副作用を迂回できないことを定義します。
- ADR-202はActor identity、Job / Task scope、correlation、append-only、idempotencyを全mutation / recovery pathで強制することを定義します。
- ADR-700は外部Agent interoperabilityのA2A境界を定義します。
