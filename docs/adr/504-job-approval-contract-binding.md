# ADR-504: JobApprovalを承認済みJobContract revisionへ束縛する

Date: 2026-11-23

## Status

Accepted

## Context

Jobの承認をJobIDや可変状態だけに結び付けると、承認後の契約変更や古い承認の再利用によって、利用者が確認していないscopeで実行する危険があります。

## Decision

**JobApprovalは、認証済みHUMAN Principalが特定のimmutable JobContract revisionを明示的に承認した事実として記録します。**

承認はrevision identityとcanonical hashへ束縛し、承認後の契約変更には新しいrevisionと新しい承認を要求します。JobApprovalはJobを実行可能状態へ移す判断であり、個別operationの実行許可を代替しません。

## Consequences

承認対象と実行時に参照する契約を一意に照合できます。認証済みPrincipalの生成はADR-501、ACTIVE Jobの人間による契約変更はADR-503、operation単位の実行許可はADR-507で定義します。

## Relationship to other ADRs

ADR-500はAuthorization & Securityの基本方針、ADR-501はHUMAN Principal、ADR-503はACTIVE Jobの契約変更、ADR-507はExecution Permissionを定義します。
