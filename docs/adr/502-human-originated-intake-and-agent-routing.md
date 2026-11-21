# ADR-502: 人間起点の依頼とAgent実行先を分離する

Date: 2026-11-20

## Status

Accepted

## Context

人間はCopilot、OpenCode、Claude Code、Antigravity等のCoding Agentを使って依頼内容を整理し、Taxionへ渡せます。Taxionは受け付けたJobを外部Agentへルーティングします。

## Decision

**Jobの依頼主体は人間とし、実行AgentはTaxionが選択します。**

- 人間はTaxionへ直接、またはCoding Agent、CLI、Web UI等のIngress Clientを介して依頼します。
- Ingress Clientは依頼を構造化してTaxionへ伝達します。Jobの依頼主体はADR-501に従って認証されたHUMAN Principalです。
- TaxionはJobの目的と要求能力に基づいてExecution Agentを選択し、A2Aで実行を委譲します。
- Ingress ClientとExecution Agentは別の役割です。同じ製品が両方を担う場合も、TaxionはJobごとに実行先を選択します。

## Consequences

人間が依頼し、Taxionが適切なAgentへ実行を委譲する責務境界を確立します。依頼の構造化に使用したCoding Agentが、そのまま実行Agentになることを前提にしません。

## Relationship to other ADRs

- ADR-501: HUMAN Principalの認証とTrusted Ingress
- ADR-700: 外部Execution AgentとのA2A連携
- ADR-300: Taxion内部Actor間のJobMessage通信
