# ADR-506: Job内の質問に伴う追加Provider実行を限定認可する

Date: 2026-11-23

## Status

Accepted

## Context

承認済みJob内でRoleがPlannerへ質問する場合があります。質問文を受け取り、既に利用可能なSession / Contextの範囲で自然言語回答を返す情報交換と、回答のためにProviderを新たに実行して追加のread-only context acquisitionを行うことは異なる操作です。両者を混同すると、通常の情報交換に不要な認可を要求するか、Provider実行権限を暗黙に付与することになります。

## Decision

**JobQuestionAuthorizationは、承認済みJob内の質問を契機にTaskless Plannerの追加bounded read-only Provider実行が必要な場合に限って使用します。**

- JobMessageの保存・参照・転送、自然言語の質問・回答、既存Contextを使った応答にはJobQuestionAuthorizationを要求しません。
- JobMessage、QUESTIONというMessageKind、質問本文だけではgrantを発行しません。
- 追加のProvider実行が必要な場合は、現在有効なJobApprovalと承認済みscopeを確認し、元Questionと実行対象へ束縛した独立grantを発行します。
- grantはJobApproval、元Question、Planner RoleExecution、Provider / account / configuration、必要なread-only context、budget、deadlineに限定して束縛します。他のQuestionやSessionへ再利用できません。
- grantはJobApproval、ExecutionPermission、Task assignment、実行責任を拡張しません。禁止副作用を許可しません。

## Consequences

通常のAgent間情報交換は通信専用認可なしで成立し、追加Provider実行だけが明示的な認可境界に従います。質問の保存・相関はADR-301、Job内のauthority factはADR-201、外部副作用の認可はADR-507に従います。

## Relationship to other ADRs

ADR-300はCollaborationの基本方針、ADR-301は内部メッセージ契約、ADR-500はAuthorization & Securityの基本方針、ADR-505はDRAFT planning、ADR-507はoperation単位の実行権限を定義します。
