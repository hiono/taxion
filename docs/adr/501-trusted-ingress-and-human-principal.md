# ADR-501: Trusted Ingressで認証済みHUMAN Principalを確定する

Date: 2026-11-20

## Status

Accepted

## Context

Job intakeとJobApprovalには認証済みhuman actorが必要です。自然言語Body、任意header、client-supplied user ID、email、外部Agentの自己申告は本人性を証明しません。

## Decision

**人間の認証には管理者が設定したOpenID Connect（OIDC）issuerを使用し、Trusted Ingressだけが検証済みHUMAN Principalを生成します。**

### Authentication

- 標準フローはOIDC Authorization Code Flow with PKCEとします。
- 特定の商用Identity Providerには固定せず、管理者がissuer、client ID、audience等を設定します。
- 信頼するissuerは事前設定されたものに限定し、未検証token claimや利用者入力から信頼先を選択しません。
- 設定済みissuerのmetadataとJWKSをTLSで取得し、ID Tokenの署名、issuer、audience、expiration、nonceを検証します。認証開始時のPKCE verifierとstateも照合します。
- emailやdisplay name等の可変属性は表示用とし、Principal identityには使用しません。

### HUMAN Principal

外部identityは検証済みOIDC issuerとsubject（sub）の組で識別し、内部PrincipalIDへ対応付けます。Trusted Ingressは認証成功後にAuthenticatedPrincipalを生成し、Controllerへ不変値として渡します。

生のtoken、cookie、Authorization header、秘密鍵はJobやログへ保存しません。A2A Agentとservice identityはHUMAN Principalと区別します。

### Trusted Ingress and provenance

HTTP / CLI等のIngress adapterは認証結果を検証し、Principalとserver-derived ingress sourceを生成します。request body、query、任意headerからPrincipalを確定しません。

Job作成時に、原文依頼と同じJobAggregate transactionでIntakeProvenanceを保存します。provenanceはPrincipal、issuer / subject、server-derived source、認証・受付時刻、request identity、原文依頼digest、schema versionを保持するimmutable factとします。

### Authorization boundary

認証成功はJobApprovalやExecution Permissionを付与しません。JobApprovalは認証済みHUMAN PrincipalによるJobContract revisionの明示承認として別途記録します。認証・provenanceが不明または不整合の場合はJob作成・承認・外部副作用を開始しません。

## Consequences

HUMAN identityの信頼源とPrincipal生成箇所を明確にします。OIDC準拠の複数環境に対応し、既存の未認証CLI経路はtrusted intakeとして扱いません。

## Relationship to other ADRs

ADR-201はPrincipalとIntakeProvenanceの保存、ADR-202は不変条件、ADR-504はJobApproval、ADR-507はExecution Permission、ADR-502はIngress ClientとExecution Agentの役割を定義します。
