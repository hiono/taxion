# ADR-700: 外部Agent相互運用の必須protocolをA2Aに一本化する

Date: 2026-11-20

## Status

Accepted

## Context

Taxion内部の6 Actor通信はJobMessageで定義します。外部Agentとの相互運用には標準経路が必要であり、複数protocolを同時に必須化すると接続・検証・運用の負担が増えます。

## Decision

**外部Agentとの相互運用に使用する必須protocolをA2Aに一本化します。**

- 外部Agentの発見、能力確認、タスク依頼、進捗・結果の受領にA2Aを使用します。
- Taxion内部6 Actor間の通信にはADR-300のJobMessageを使用します。
- A2AのTask、Message、Artifact、Agent CardはTaxionのJob identity、認証済みActor、承認、認可、完了状態とは別の情報として扱います。
- 外部Agentからの入力・結果はControllerで認証、scope、provenance、不変条件を検証してからJobへ反映します。
- A2Aの十分性は設計仮説として扱い、実装・実験による証拠で評価します。
- 他protocolやSDKは、A2Aで満たせない具体的な製品要件が確認された場合に任意Adapterとして検討します。

## Consequences

外部Agent連携の設計・実装・受入れ対象をA2Aへ集約します。本ADRは実装完了や製品受入れを宣言しません。

## Relationship to other ADRs

ADR-300は内部JobMessage、ADR-201はJobAggregate、ADR-500はJob authority、ADR-501はHUMAN Principalの信頼源、ADR-502は人間起点の依頼とExecution Agentの選択を定義します。
