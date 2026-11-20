# CLI usage

## Authenticate a HUMAN Principal

Trusted CLI authentication uses an administrator-configured OIDC issuer and Authorization Code Flow with PKCE. Register the loopback redirect URI with the identity provider.

```sh
export TAXION_OIDC_ISSUER="https://identity.example"
export TAXION_OIDC_CLIENT_ID="taxion-cli"
export TAXION_OIDC_AUDIENCE="taxion-cli"
export TAXION_OIDC_REDIRECT_URI="http://127.0.0.1:8765/callback"

taxion auth login
```

The command displays an authorization URL. Open it in a browser and complete authentication. Taxion receives the authorization code through the loopback callback, validates state, PKCE, ID Token signature, issuer, audience, expiration, and nonce, then displays the internal HUMAN Principal ID.

The command does not save ID Tokens, access tokens, refresh tokens, cookies, or a login session. Authentication verification does not create a Job or grant JobApproval / Execution Permission.

## Create a local DRAFT Job

```sh
taxion dev job create --request "I have several ideas for a small product. Help me clarify the goal and build it."
```

This explicit `dev` command creates an untrusted local DRAFT Job for development and persistence testing only. It does not represent Trusted Ingress, authenticated Job intake, IntakeProvenance, or JobApproval. The production `taxion job create` command remains unavailable until trusted Job intake and provenance persistence are implemented.

## Show a Job

```sh
taxion job show job_<id>
```

Jobの保存内容をJSONで表示します。payload hashとdomain validationに失敗した場合は読み取りを拒否します。

## List Jobs

```sh
taxion job list
taxion job list --limit 100
```

更新日時の降順でJobを一覧表示します。

## Store location

既定のSQLite databaseはOSのユーザー設定ディレクトリ配下の taxion/jobs.sqlite に作成します。保存先を変更する場合は各コマンドに --store を指定します。

```sh
taxion dev job create --store ./taxion-local.sqlite --request "..."
taxion job list --store ./taxion-local.sqlite
```

SQLite databaseとWALファイルは秘密情報を含む可能性があるため、Gitへ追加しないでください。

## Current limitation

本段階ではOIDC認証とHUMAN Principal生成、および未認証のローカルDRAFT作成経路の分離までを提供します。AuthenticatedPrincipalとIntakeProvenanceのJobAggregateへの永続化、OIDC認証済みJob作成、HTTP ingress、Planner / Role / Provider実行は後続作業です。OIDC認証成功をJob作成や実行許可の証拠として扱いません。
