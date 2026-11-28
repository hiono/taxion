# CLI使用方法

## OIDC認証

Trusted Job作成では、管理者が設定したOIDC issuer / client ID / audienceを使用します。Identity Providerにはloopback redirect URIを登録してください。

```sh
export TAXION_OIDC_ISSUER="https://identity.example"
export TAXION_OIDC_CLIENT_ID="taxion-cli"
export TAXION_OIDC_AUDIENCE="taxion-cli"
export TAXION_OIDC_REDIRECT_URI="http://127.0.0.1:8765/callback"

taxion auth login
```

`taxion auth login` はAuthorization Code Flow with PKCEを実行し、state、PKCE、ID Token署名、issuer、audience、有効期限、nonceを検証します。認証URLをブラウザーで開いて認証を完了してください。検証成功後にHUMAN Principal IDを表示します。ID Token、access token、refresh token、cookie、ログインSessionは保存しません。このコマンド単独ではJobを作成しません。

## Trusted Jobの作成

```sh
taxion job create --request "I have several ideas for a small product. Help me clarify the goal and build it."
```

このコマンドはOIDC認証成功後にDRAFT Jobを作成します。原文依頼、HUMAN Principal、issuer / subject、CLI ingress source、認証時刻、受付時刻、request identity、原文依頼SHA-256、provenance schema versionをIntakeProvenanceとして保持し、JobAggregateとCommand receiptを同一SQLite transactionで保存します。token、cookie、Authorization headerはJobやprovenanceへ保存しません。

認証失敗、Principal検証失敗、provenance検証失敗、transaction失敗ではJobを作成しません。Job作成はDRAFT状態までであり、JobApproval、PlanningAuthorization、Planner実行、Agent実行を付与しません。

## PlanningAuthorizationの発行

Trusted DRAFT Jobに対して、明示的にbounded planning grantを発行します。

```sh
taxion job authorize-planning \
  --provider openai \
  --account primary \
  --configuration-sha256 <64-hex> \
  --repository https://github.com/example/project \
  --commit <40-hex> \
  --read-only-mode snapshot \
  --read-only-reference snapshot_<id> \
  --read-only-sha256 <64-hex> \
  --deadline <RFC3339> \
  job_<id>
```

OIDCで再認証し、認証済みHUMAN PrincipalがJobのIntakeProvenanceと一致することを検証します。grantはJob ID、原文依頼digest、Provider / account / configuration、repository URLとimmutable commit、read-only snapshotまたは明示文書のidentityとdigest、deadlineへ束縛します。Provider configurationの秘密情報は入力・保存しません。

許可effectはrepository readに限定します。JobはDRAFTのままであり、JobApproval、実装Task、write、validation、commit、publish、任意network access、external mutation、Planner実行を許可しません。このコマンドはgrantを永続化するだけで、Providerを起動しません。

成功時に表示されるCommand IDは、同じ要求を再送するときに`--command-id`へ指定します。同一Command ID・同一要求は同じreceiptへ収束し、異なる要求での再利用は拒否します。

read-only boundaryには、内容digestを持つimmutable snapshotまたは明示文書セットを指定します。参照先の実体検証とProvider側のread-only強制は、後続のProvider実行機能で行います。

## Taskless PLANNERの開始要求

有効なPlanningAuthorizationを指定し、Taskless PLANNER RoleExecutionとProvider Start intentをJobAggregateへ記録します。

```sh
taxion job start-planning \
  --authorization-id plan_auth_<id> \
  job_<id>
```

OIDCで再認証し、認証済みHUMAN Principal、DRAFT Job、grantの有効期間、Job / request digest、Provider / account / configuration、read-only context、permitted effects、deadlineを再検証します。RoleExecutionはPLANNERとして作成し、TaskIDは持ちません。RoleExecution、Provider Start intent、Command receiptは同一SQLite transactionで保存します。

このコマンドはProviderを起動しません。Provider呼出しはStart intentのcommit確認後にProvider Adapterが行う後続機能です。JobはDRAFTのままであり、Task、JobApproval、write、commit、publish、external mutationを許可しません。

成功時に表示されるCommand IDは、同じ要求を再送するときに`--command-id`へ指定します。同一Command ID・同一要求は同じreceiptへ収束し、異なる要求での再利用は拒否します。

## ローカルDRAFT Jobの作成

```sh
taxion dev job create --request "Local untrusted DRAFT for development."
```

この明示的な`dev`コマンドは、開発および永続化テスト専用の未認証DRAFT Jobを作成します。IntakeProvenanceを持たず、Trusted Ingress、認証済みJob intake、JobApprovalを表すものではありません。

作成したJobの依頼をProviderへ渡す場合、`taxion dev provider run --job <job-id>`を使います。別の指示を渡す場合は`--prompt`を指定します。これは認可済みJobの実行ではありません。

`--review --allow-coder-tools`を指定すると、Coderのツール権限要求を許可し、作業ディレクトリで直接編集させます。OS隔離はなく、ディレクトリ外やネットワークにもアクセス可能です。失敗時も変更は残り、終了後の子プロセス停止は保証されません。PlannerとReviewerのツール権限要求は拒否しますが、Provider自体もOSで隔離されていません。ReviewerはCoderの報告テキストを確認し、実際の変更を独立に検証しません。

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
taxion job create --store ./taxion.sqlite --request "Trusted request."
taxion dev job create --store ./taxion-local.sqlite --request "Local request."
taxion job list --store ./taxion.sqlite
```

SQLite databaseとWALファイルは秘密情報を含む可能性があるため、Gitへ追加しないでください。

保存形式はschema version 3です。Taxionのversion 1・2のデータベースは変更せず拒否し、移行は行いません。以前のデータを残す場合は別の`--store`を指定してください。

## 現在の制限

本段階ではOIDC認証済みCLIからのDRAFT Job作成、PlanningAuthorizationの発行・永続化、Taskless PLANNER RoleExecutionとProvider Start intentの永続化までを提供します。HTTP ingress、Provider Adapterによる実起動、turn / effect / resume、JobApproval、Job完了は未実装です。grant発行の成功をplanning実行やJob完遂の証拠として扱いません。
