# ADR-503: ACTIVE Jobの契約変更は独立したHUMAN authorityで行う

- Status: Accepted
- Date: 2026-11-22
- Supersedes: None

## Context

JobContractRevisionは、Jobの承認済みscope・resource boundary・budget・deadline・禁止副作用を固定する。DRAFT Jobに対するPlanner proposal authorityをACTIVE Jobへ再利用すると、既存Approvalを根拠に承認済みscopeを変更できるため、権限境界が曖昧になる。

## Decision

- ACTIVE Jobの契約変更には、DRAFT Planner proposalとは独立した認証済みHUMAN commandを使用する。
- Commandは最新の承認済みRevisionとCanonicalHashを明示し、変更理由を記録する。HUMAN Principalは、対象RevisionのJobApproval後に認証されていなければならない。
- 変更は新しいimmutable JobContractRevisionとして記録し、originを`HUMAN_AMENDMENT`とする。Principal ID・OIDC issuer / subject・認証時刻・reason・superseded Revision / CanonicalHashを保持する。
- ACTIVEからDRAFTへの遷移とRevision追加は、同一JobAggregate transactionで行う。既存JobApprovalは履歴として保持するが、新Revisionの承認には使用しない。新Revisionには新しいJobApprovalを要求する。
- HUMAN amendmentはExecutionPermissionを作成せず、Provider operationを開始しない。以前のRevisionに結び付いた実行権限が存在する場合も、新Revisionのscopeを認可してはならない。
- HUMAN amendmentはPlanner proposalではないため、Provider Result provenanceを偽装・流用しない。

## Rationale

ACTIVE Jobの変更は、Plannerに与えたDRAFT専用権限の延長ではなく、人間による新しい明示的なauthorityとして扱う。Revisionのoriginとsuperseded hashを固定し、旧Approvalの再利用と承認済みscopeの暗黙変更を防止する。

## Consequences

- Planner proposalとHUMAN amendmentは、異なるRevision originとprovenanceを持つ。
- Amendment後のJobはDRAFTとなり、新Revisionの承認が完了するまでACTIVEへ戻らない。
- 実行権限の生成・検証はJobApprovalとは別の認可境界で行う。
