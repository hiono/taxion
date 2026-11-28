# ADR-500: Jobの承認範囲と禁止副作用を中心に権限を強制する

Date: 2026-11-20

## Status

Accepted

## Context

Agentへ静的な操作許可一覧だけを与えると、承認済みJob内の通常作業を不必要に制限します。一方、Agentの自己判断だけに権限を委ねると、利用者の承認範囲や安全境界を維持できません。

## Decision

**Taxionの権限は、認証済みPrincipalによるJobの明示的承認を基礎とし、Controllerと実行基盤が承認済みscopeおよび禁止副作用を強制します。**

認証と認可を分離し、計画・質問・実行に必要な権限をそれぞれ独立して扱います。Agentは承認済みJobの範囲内で作業手段を選択できますが、Job scopeの拡張、禁止副作用、未承認の不可逆操作を自己判断で許可できません。

## Consequences

Agentの作業上の自由と、人間が定める承認境界を両立します。個別のauthority契約はADR-501およびADR-503〜505 / ADR-507で定義し、実装・テストの詳細は製品仕様と詳細設計へ配置します。

## Relationship to other ADRs

ADR-400はExecution & Runtimeの基本方針、ADR-201はJob内のauthority facts、ADR-202はドメイン不変条件、ADR-408はWorktree境界、ADR-501はHUMAN Principal、ADR-503はACTIVE Jobの契約変更、ADR-504 / ADR-505 / ADR-507は個別の認可判断を定義します。
