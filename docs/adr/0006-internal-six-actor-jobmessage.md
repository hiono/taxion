# ADR-0006: 内部6 Actor通信をJobMessageとして定義する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` のAccepted ADR-0087を根拠とし、情報不足だけを理由にRole協調を拒否しないAccepted ADR-0067の原則を適用して、新TaxionのJobAggregate・認可・A2A境界と整合するように再構成した。

## Context

Taxion内部には人間、Controller、Orchestrator、Planner、Coder、Reviewerの協調責務がある。内部通信を外部Agent向けprotocolへ直接写像すると、Job identity、承認、RoleExecution、Task scope、永続化、provenanceの正本が外部protocolへ流出する。また、質問・回答と責任移転を同じ意味として扱うと、会話だけで実行権限が移る危険がある。

## Decision

Taxion内部の6 Actor間の通信は、Taxion固有のJobMessage契約で表現する。A2Aを内部6 Actor通信の代替として使用しない。

### 1. Actor identity

- HUMANは認証済み利用者principalに結び付ける。
- CONTROLLERとORCHESTRATORはTaxionが管理するcomponent identityに結び付ける。
- PLANNER、CODER、REVIEWERはRoleExecutionIDに結び付ける。Role種別だけで実行主体を識別しない。
- Sender identityは認証済み接続、実行中RoleExecution、またはController内部呼出コンテキストから確定する。本文中の自己申告をidentityとして扱わない。
- 全30の非自己宛て有向Actor関係を表現可能とする。これは全経路の許可を意味しない。許可はActor実体、Job状態、RoleExecution状態、Task scope、認可境界に基づいてControllerが判定する。

### 2. Envelopeと自然言語Body

JobMessageはJobに属し、MessageID、JobID、trusted Sender ActorRef、検証済みRecipient ActorRef、MessageKind、ThreadID、任意のReplyTo、時刻、provenance、必要なRoleExecution / Task scope、相関identityを保持する。

Bodyは利用者とAgentに共通の自然言語とし、固定JSON、タグ、Actor名、ID、routing metadataの入力を要求しない。EnvelopeはUI、Controller、Runtimeが生成・検証する。自然言語の解釈は候補提示に使えるが、identity、宛先の実体、認可、承認、Handoffを確定しない。曖昧な宛先や操作は確認する。

### 3. MessageKind

- MESSAGE: 通常の情報共有
- QUESTION: 回答を求める情報照会
- ANSWER: 特定のQUESTIONへの相関付き回答
- REQUEST: 情報提供または作業依頼。これ自体は責任移転しない
- NOTIFICATION: 応答を要求しない通知
- HANDOFF: 作業または実行責任を移転する明示的intent

Question / Answerは情報交換であり、実行責任、Task ownership、Provider session、Worktree ownershipを移転しない。HANDOFFはADR-0003のJobAggregate commandとして記録し、受信RoleとTask scopeを明示する。送信側Providerの停止証明、terminal result / usage照合、Worktree releaseが永続化されるまで受信側をrunnableにしない。

### 4. Recipient、fan-out、Delivery

基本単位は1 Message・1宛先とする。fan-outは不変BatchIDで相関した宛先別Messageを一つのJob commandで記録する。宛先別Delivery状態はMessage本文と分離し、append-only observationから導出する。配信要求、受理、失敗、UNKNOWNを区別する。UNKNOWNを未配信と推定せず、blind resendしない。Exactly-once deliveryを保証せず、MessageIDによる重複排除と配信結果の追跡可能性を提供する。

### 5. 入力受付の寛容性とidentityの厳格性

認証済み利用者入力経路からの自然言語入力は、routing metadataが欠落・不完全でも受け付け、Controllerが信頼できるUI / conversation contextから補完する。一意に解決できない宛先・Job・Threadは推測せず確認する。Agent / Systemの認証済み入力経路でmetadataが欠落・矛盾する場合は、利用者入力へ変換せず拒否または隔離する。補完後も通常の認可・scope・MessageKind policyを適用する。

情報不足、任意metadataの欠落、自然言語の表現差、未確定の実装詳細だけを理由に、受け渡しやJobを拒否しない。受信Roleは自然言語、既存Context、成果物、証拠から先に判断し、判断可能な範囲を進める。可逆で影響の小さい不足は、仮定と不確実性を明示して進めてよい。判断に不可欠な情報だけを適切な相手へ質問し、既存Contextから分かることを聞き直さない。回答と部分成果は元のJobに結び付けて継続する。Controllerは意味情報の完全性を一律審査してRoleの判断を代行しない。

情報不足を理由に拒否しない原則は、認証・認可・秘密情報・禁止操作・人間承認・承認範囲変更・Job / Task / Artifact identity・provenance・データ整合性・二重実行・不可逆副作用・実行状態不明の安全境界を緩和しない。これらが不明または不正な場合はfail-closedとする。

### 6. ControllerとOrchestrator

Controllerはtrusted ingress、Envelope生成、宛先検証、Actor policy、Job scope、永続化、冪等性、実行権限の境界を強制する。Orchestratorは独立ActorとしてJob内の調整・routing依頼・通知を行えるが、そのidentity自体はJobApproval、Provider operation、Task作成、Worktree ownershipを付与しない。

## Consequences

内部通信はJobの同一性・権限・履歴へ直接結び付く。Message、Delivery、Provider operation、RoleExecutionの事実を混同しない。6 Actorの表現可能性と個別通信の認可は別の検証対象とする。

## Security and operational impact

trusted ingress、Actor identity、Job scope、ReplyTo、Task scope、provenanceを検証する。自然言語本文だけで認証・認可・承認を成立させない。拒否された操作はJob状態を変更しない。

## Alternatives considered

- 内部通信をA2A Taskへ置き換える方式は、Taxion内部のJob正本と権限モデルを外部protocolへ委譲するため採用しない。
- QuestionとHandoffを同一視する方式は、情報交換だけで責任が移るため採用しない。
- MessageとDeliveryを同じレコードにする方式は、宛先別観測を正確に表せないため採用しない。

## Relationship to other ADRs

ADR-0003のJobAggregate、ADR-0004の認可、ADR-0005の全経路不変条件に従う。外部Agent相互運用はADR-0007で定義する。
