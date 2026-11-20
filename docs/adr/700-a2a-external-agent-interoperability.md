# ADR-700: 外部Agent相互運用の必須protocolをA2Aに一本化する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の設計資料に記録されたA2Aの必須外部protocol方針と、十分性が未実証の設計仮説であることを確認し、新しい履歴へ再確立しました。

## Context

Taxion内部の6 Actor通信契約はJobMessageが定めます。一方、Taxionの外部に存在するAgentと連携する場合には、外部Agentとの相互運用境界が必要です。

外部Agent連携のために複数protocolやSDKを同時に必須化すると、それぞれの実行環境・接続方法・検証条件を個別に扱う必要があります。実際に維持・検証できる連携方式の数には運用上の制約があるため、複数方式の理論的な利点より、ひとつの標準経路に集中してTaxionの製品目的を達成する可能性を優先します。

A2Aを外部Agent間の相互運用protocolとして採用することで、Taxionの外部Agent連携に必要な目的をA2Aだけで満たせる可能性があります。ただし、この十分性は実験・実装によって実証された事実ではなく、本ADRが採用する設計仮説です。

## Decision

**Taxionが外部Agentと連携するための必須protocolをA2Aに一本化します。外部Agent連携の基本設計・実装・受入れは、まずA2AだけでTaxionの製品目的を満たせることを目指します。**

### 1. A2Aを唯一の必須外部Agent protocolとする

- 外部Agentとの発見、能力確認、タスク依頼、進捗・結果の受領など、Agent間相互運用の標準経路にはA2Aを使用します。
- 外部Agent連携の成立条件として、複数protocolやSDKの同時実装・同時検証を要求しません。
- A2Aだけで製品シナリオを成立させることを最初の設計目標とします。
- A2Aの十分性は仮説として扱い、未検証の機能を実証済みと表現しません。

### 2. その他の方式はオプションとする

MCP、ACP系の接続方式、Copilot SDKその他のprotocol・SDK・Provider固有連携は必須構成に含めません。必要性が具体的な製品要件から確認された場合に限り、任意Adapterまたは追加統合として検討できます。

これらの採用・検証・導入を、A2Aによる外部Agent連携の設計・実装・受入れの前提条件にしません。オプション方式を追加しても、A2Aを標準経路から外したり、複数方式の同時運用を既定にしたりしません。

### 3. Taxion内部通信と外部A2A境界を分離する

- Taxion内部のHUMAN / CONTROLLER / ORCHESTRATOR / PLANNER / CODER / REVIEWER間通信はADR-300のJobMessage契約を維持します。
- A2AはTaxionの外部に存在するAgentとの相互運用境界として使用します。内部JobMessageやJobAggregateをA2AのTask・Messageへ置き換えません。
- A2AのTask state、Message、Artifact、Agent Card等をTaxionのJob、RoleExecution、認証済みActor identity、認可、承認またはJob完了状態と同一視しません。
- 外部Agentとの通信結果をTaxionのJobへ反映する場合は、既存ControllerおよびJobAggregateの認証・認可・provenance・永続化・状態遷移を通します。
- 外部Agent連携の失敗・切断・応答不明が、Taxion内部のJob状態や実行権限を暗黙に変更しません。

### 4. 開発・運用上の集中方針

外部Agent連携の調査・設計・実装・受入れはA2Aを標準経路として集約します。複数の外部環境やprotocol間の手動コピー＆ペーストを常態化させず、任意方式の比較検証を並行作業の前提にしません。実験・実装へ進む場合は、A2Aで満たす製品シナリオと完了条件を先に定義します。本節は開発の順序・集中方針を定めるものであり、A2Aの十分性を実証済みとは扱いません。

### 5. A2Aだけでは不足する場合

A2Aだけでは満たせない具体的な製品要件が確認された場合は、不足する責務と発生条件を明示します。そのうえで、Taxion側の責務として実装するか、必要最小限の任意Adapterを追加するかを判断します。

不足の可能性だけを理由に複数protocolを先行導入しません。追加方式はA2A標準経路を補完する任意機能とし、必須構成への昇格には別途明示的な設計判断を必要とします。

## Alternatives considered

- **複数protocolを同時に必須化する:** 外部環境・接続・検証・状態同期の負担を増やすため採用しません。
- **A2AとMCPを最初から対等な必須構成にする:** 現時点の製品目的から同時必須化の必要性が確定していないため採用しません。
- **Provider / SDKごとに個別方式を標準化する:** 外部Agent連携の共通経路が分散するため採用しません。
- **A2Aの十分性を実証済みとして扱う:** 実験・実装による証拠がないため採用しません。
- **外部Agent連携を行わない:** 外部Agentとの協調が必要な製品利用を満たせないため採用しません。
## Consequences

- 外部Agentとの相互運用に関する必須protocolの数をA2A一つに限定します。
- 調査・設計・実装・運用の対象を集約します。
- A2AだけでTaxionの目的を達成できるかという設計仮説を、他方式の先行導入に分散せず評価します。
- Taxion内部のJobMessage、JobAggregate、認証・認可、Provider lifecycleおよびWorktree ownershipは、A2Aとは独立したTaxionの責務として維持します。
- 本ADRはA2A連携の実装完了や製品受入れを宣言しません。

## Security and operational impact

- 外部Agentのidentityと能力情報はA2A接続の認証・信頼境界で検証します。Agent Cardや通信上の自己申告だけでTaxion内部のActor identityや権限を付与しません。
- 外部Agentからの要求・結果は信頼境界を越える入力として扱い、Job scope、承認、禁止副作用、provenanceをControllerで検証します。
- A2Aの応答・event・ArtifactをJobの確定事実として扱う前に、Taxion側の永続化と相関を完了させます。
- 通信切断、重複、遅延、応答不明、再接続を考慮し、外部通信の観測とTaxion内部のJob状態を分離します。

## Acceptance criteria

- 外部Agentの標準相互運用経路はA2Aです。
- 内部6 Actor通信はJobMessageのまま維持します。
- A2AのTask / Message / Artifact / Agent CardをJob identity、authority、completionへ直接変換しません。
- 外部入力はControllerの認証・scope・provenance・Admission・invariant検証を通します。
- A2Aの十分性は未検証の設計仮説として扱い、実証結果と分離して記録します。
- 任意protocolは具体的な不足要件と明示的判断なしに必須化しません。
## Relationship to other ADRs

- ADR-100は製品目的を定義します。
- ADR-101は旧設計の制約を温存しない再構築原則を定義します。
- ADR-201はJobAggregateとProvider / Worktree lifecycleの不変条件を定義します。
- ADR-500はJob authority、禁止副作用および外部入力の検証を定義します。
- ADR-202は全状態変更経路でのidentity、scope、provenance、idempotencyの強制を定義します。
- ADR-300はTaxion内部の6 Actor通信をJobMessageで統一します。本ADRは外部Agentとの相互運用境界だけを定義します。
- ADR-502は、人間起点の依頼を受け付けるIngress Clientと、Taxionが選択する外部Execution Agentの役割・identity分離を定義します。
