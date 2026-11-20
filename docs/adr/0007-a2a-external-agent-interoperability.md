# ADR-0007: 外部Agent相互運用の必須protocolをA2Aに一本化する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の redesign/job-centered-role-collaboration branch にある旧ADR-0108を原典とする（blob b0e5e3f070b55d73943a8386a8297dc83a51435a）。A2Aの必須外部protocol方針と、十分性が未実証の設計仮説であることを確認し、新しい履歴へ再確立した。

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

- Taxion内部のHUMAN / CONTROLLER / ORCHESTRATOR / PLANNER / CODER / REVIEWER間通信はADR-0006のJobMessage契約を維持します。
- A2AはTaxionの外部に存在するAgentとの相互運用境界として使用します。内部JobMessageやJobAggregateをA2AのTask・Messageへ置き換えません。
- A2AのTask state、Message、Artifact、Agent Card等をTaxionのJob、RoleExecution、認証済みActor identity、認可、承認またはJob完了状態と同一視しません。
- 外部Agentとの通信結果をTaxionのJobへ反映する場合は、既存ControllerおよびJobAggregateの認証・認可・provenance・永続化・状態遷移を通します。
- 外部Agent連携の失敗・切断・応答不明が、Taxion内部のJob状態や実行権限を暗黙に変更しません。

### 4. A2Aだけでは不足する場合

A2Aだけでは満たせない具体的な製品要件が確認された場合は、不足する責務と発生条件を明示します。そのうえで、Taxion側の責務として実装するか、必要最小限の任意Adapterを追加するかを判断します。

不足の可能性だけを理由に複数protocolを先行導入しません。追加方式はA2A標準経路を補完する任意機能とし、必須構成への昇格には別途明示的な設計判断を必要とします。

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

## Relationship to other ADRs

- ADR-0001は製品目的を定義します。
- ADR-0002は旧設計の制約を温存しない再構築原則を定義します。
- ADR-0003はJobAggregateとProvider / Worktree lifecycleの不変条件を定義します。
- ADR-0004はJob authority、禁止副作用および外部入力の検証を定義します。
- ADR-0005は全状態変更経路でのidentity、scope、provenance、idempotencyの強制を定義します。
- ADR-0006はTaxion内部の6 Actor通信をJobMessageで統一します。本ADRは外部Agentとの相互運用境界だけを定義します。
