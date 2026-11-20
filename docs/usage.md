# CLI使用方法

## HUMAN Principalの認証

Trusted CLI認証には、管理者が設定したOIDC issuerとAuthorization Code Flow with PKCEを使用します。Identity Providerにはloopback redirect URIを登録してください。

```sh
export TAXION_OIDC_ISSUER="https://identity.example"
export TAXION_OIDC_CLIENT_ID="taxion-cli"
export TAXION_OIDC_AUDIENCE="taxion-cli"
export TAXION_OIDC_REDIRECT_URI="http://127.0.0.1:8765/callback"

taxion auth login
```

コマンドが認証URLを表示します。URLをブラウザーで開いて認証を完了してください。Taxionはloopback callbackでauthorization codeを受信し、state、PKCE、ID Token署名、issuer、audience、有効期限、nonceを検証します。検証成功後に内部HUMAN Principal IDを表示します。

ID Token、access token、refresh token、cookie、ログインSessionは保存しません。このコマンドは認証結果の検証のみを行い、Job作成、JobApproval、Execution Permissionを付与しません。

## ローカルDRAFT Jobの作成

```sh
taxion dev job create --request "I have several ideas for a small product. Help me clarify the goal and build it."
```

この明示的な`dev`コマンドは、開発および永続化テスト専用の未認証DRAFT Jobを作成します。Trusted Ingress、認証済みJob intake、IntakeProvenance、JobApprovalを表すものではありません。Trusted Job intakeとprovenance永続化が実装されるまで、production用の`taxion job create`は使用できません。

## Jobの表示

```sh
taxion job show job_<id>
```

Jobの保存内容をJSONで表示します。payload hashとdomain validationに失敗した場合は読み取りを拒否します。

## Jobの一覧表示

```sh
taxion job list
taxion job list --limit 100
```

更新日時の降順でJobを一覧表示します。

## 保存先

既定のSQLite databaseはOSのユーザー設定ディレクトリ配下の`taxion/jobs.sqlite`に作成します。保存先を変更する場合は各コマンドに`--store`を指定します。

```sh
taxion dev job create --store ./taxion-local.sqlite --request "..."
taxion job list --store ./taxion-local.sqlite
```

SQLite databaseとWALファイルは秘密情報を含む可能性があるため、Gitへ追加しないでください。

## 現在の制限

本段階ではOIDC認証とHUMAN Principal生成、および未認証のローカルDRAFT作成経路の分離までを提供します。AuthenticatedPrincipalとIntakeProvenanceのJobAggregateへの永続化、OIDC認証済みJob作成、HTTP ingress、Planner / Role / Provider実行は後続作業です。OIDC認証成功をJob作成や実行許可の証拠として扱いません。
