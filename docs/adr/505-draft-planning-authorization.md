# ADR-505: DRAFT planningを限定的なread-only authorityとして分離する

Date: 2026-11-23

## Status

Accepted

## Context

JobApproval前の計画段階では、依頼内容を整理するための情報取得が必要です。一方、計画権限が実装・変更権限へ拡張されると、利用者が承認していない副作用が発生します。

## Decision

**DRAFT JobのPlanner実行には、明示的な利用者起動に基づく限定的なread-only PlanningAuthorizationを使用します。**

この判断はTrusted Jobに適用します。ローカルの `taxion run` では、Plannerを文章専用で実行し、PlanningAuthorizationを発行しません。ProviderプロセスはOS隔離されません。

PlanningAuthorizationは対象Jobと依頼、認証済みPrincipal、Provider構成、参照範囲および期限に束縛します。利用量の制限はADR-400に従いProviderに委ねます。この権限はJobApproval、実装Task、repository write、commit、publicationまたは外部mutationを許可しません。

## Consequences

Jobの計画と承認済み実行を分離し、Plannerの会話や提案による暗黙の権限拡張を防止します。grantの保存形式、検証順序、Provider capabilityの扱いは製品仕様と詳細設計で定義します。

## Relationship to other ADRs

ADR-500はAuthorization & Securityの基本方針、ADR-501はHUMAN Principal、ADR-504はJobApproval、ADR-409はProvider operationの管理を定義します。
