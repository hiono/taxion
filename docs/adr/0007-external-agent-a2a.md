# ADR-0007: 外部Agent相互運用の必須プロトコルをA2Aに一本化する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` のAccepted ADR-0108を根拠とし、外部Agent連携をA2Aへ集中するという利用者の明示指示を反映して、新Taxion向けに再構成した。

## Context

Taxionは外部Agentとも協調する必要がある。一方、複数の外部protocolやSDKを同時に必須化すると、接続、認証、能力発見、状態同期、検証、運用の対象が分散する。Taxion内部の6 Actor通信は別のJob中心ドメイン契約を必要とする。

## Decision

Taxionが外部Agentと相互運用するための唯一の必須protocolをA2Aとする。外部Agent連携の設計・実装・受入れは、まずA2Aだけで製品シナリオを満たすことを目指す。

### 1. A2Aの責務

A2AはTaxionの外部に存在するAgentとの発見、能力確認、タスク依頼、進捗・結果の受領等の相互運用境界とする。A2AのTask、Message、Artifact、Agent Card、状態をTaxionのJob、RoleExecution、Actor identity、承認、認可、Job完了状態と同一視しない。

### 2. 任意方式

MCP、ACP系方式、Copilot SDK、Provider固有protocol等は初期必須構成に含めない。具体的な製品要件が確認された場合に限り、A2Aを補完する任意Adapterとして別途判断する。これらをA2A実装・受入れの前提条件にしない。

### 3. 信頼境界

外部Agentのidentityや能力はA2A接続時に検証する。Agent Cardや自己申告だけでTaxion内部のActor identityや権限を付与しない。外部からの要求、Message、Artifact、結果はuntrusted inputとして扱い、ControllerがJob scope、承認、禁止副作用、provenance、永続化を検証する。

### 4. 内部Jobとの接続

外部Agentの依頼・応答はTaxionのJobMessage / JobAggregateへ明示的に相関付ける。外部protocolの状態遷移が内部Jobの状態や実行権限を暗黙に変更しない。通信切断、重複、遅延、応答不明はTaxion内部のProvider operation observationとして照合する。

### 5. 十分性の検証

A2Aだけで全製品シナリオを満たせるかは、実証済みとしない。Agent discovery、authentication、task lifecycle、streaming、artifact、cancellation、retry、idempotency、provenance、failure recoveryを製品シナリオで検証する。不足が確認された場合は不足する責務を明示し、Taxion側で実装するか最小の任意Adapterを追加する。

## Consequences

外部Agent連携の標準経路をA2Aに集中できる。内部JobMessageと外部A2Aの責務が分離される。A2Aの十分性と実装完了は別々の証拠で判定する。

## Security and operational impact

外部Agentを信頼済みActorとして自動昇格しない。外部入力のschema、identity、authentication、authorization、resource scope、artifact provenanceを検証する。切断やUNKNOWNを成功扱いしない。

## Alternatives considered

- A2AとMCPを同時に必須化する方式は、外部Agent相互運用の必須範囲を不必要に広げるため採用しない。
- Providerごとに異なる必須protocolを設ける方式は、標準経路を分散させるため採用しない。
- 外部Agent連携を行わない方式は、外部Agentとの協調を必要とする製品シナリオを満たさないため採用しない。

## Relationship to other ADRs

ADR-0006はTaxion内部6 ActorのJobMessage通信を定義する。本ADRは外部Agent相互運用だけを定義し、ADR-0003のJob modelとADR-0004の認可境界を変更しない。
