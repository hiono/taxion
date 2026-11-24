# ADR-506: Job内の質問実行権限を独立したgrantとして扱う

Date: 2026-11-23

## Status

Accepted

## Context

承認済みJob内でRoleがPlannerへ質問し、追加のread-only context acquisitionを必要とする場合があります。質問やメッセージの存在だけでProvider実行権限を与えると、情報交換と外部実行の境界が失われます。

## Decision

**承認済みJob内のTaskless Plannerによる個別質問実行には、元のQuestionと実行範囲に束縛した独立のJobQuestionAuthorizationを使用します。**

JobMessage、QUESTIONというMessageKind、質問本文だけではgrantを発行しません。JobQuestionAuthorizationはJobApprovalやExecutionPermissionを拡張せず、質問に必要な限定的read-only実行だけを許可します。

## Consequences

情報交換とProvider実行を区別し、質問経由の暗黙の権限付与を防止します。質問の保存・相関はADR-301、Job内のauthority factはADR-201、実行時の認可原則はADR-507に従います。

## Relationship to other ADRs

ADR-300はCollaborationの基本方針、ADR-301は内部通信契約、ADR-500はAuthorization & Securityの基本方針、ADR-505はDRAFT planning、ADR-507はoperation単位の実行権限を定義します。
