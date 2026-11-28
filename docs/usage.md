# CLI使用方法

## ローカル協調実行

ローカル協調実行はLinuxで利用できます。Copilot CLI、Git、`diff`が必要です。最初にTaxion専用プロファイルへログインします。

```sh
taxion provider login
taxion run --request "依頼内容" --working-directory /path/to/work --allow-coder-tools
```

Plannerの計画、Coderの作業、Reviewerの指摘、Coderの修正、再レビューを順に実行し、会話をローカルJobに保存します。表示されたJob IDは `taxion job show <job-id>` で参照できます。`--store` で保存先、`--timeout` で実行期限を変更できます。

`--consult` を指定すると、Coderに作業前の質問を一つ求め、同じPlanner sessionの回答をCoderへ返します。

PlannerとReviewerは文章専用の設定で起動し、Coderの作業報告とGit差分をReviewerに渡します。ProviderプロセスはOS隔離されず、Providerのツール制御を信頼する必要があります。Coderはホストで実行され、指定ディレクトリ外やネットワークにもアクセスできます。`--allow-coder-tools` はこの実行を明示するために必須です。

差分はCoder開始前の作業ファイルとindexを基準に取得し、既存の未追跡ファイルへの変更も含みます。無視された未追跡ファイル、作業ディレクトリ外、バイナリとsubmoduleの内容は含みません。変更・削除された行や別名にコピーされた内容は送信されるため、秘密情報を含む作業領域には注意してください。差分は固定された成果物ではなく、他プロセスによって変更される可能性があります。Gitリポジトリでない場合など、Gitの確認ができない場合は警告してテキストだけをレビューします。未解決の競合、8 MiBを超える変更ファイルや差分などの取得エラーでは終了します。比較用の一時ファイルは所有者のみが読めるディレクトリに置いて削除しますが、強制終了時には残ることがあります。失敗時も保存済みの会話とファイル変更は残り、自動再開や終了後の子プロセス停止は保証されません。

この経路はOIDC認証、JobApproval、操作単位の許可やJob完了を記録しません。会話の一巡はレビュー合格や作業完了の保証ではありません。

### 外部エージェントによる操作

v0.1.0には次の3コマンドがありません。これらを含む版を入手するか、次のようにソースからビルドします。

```sh
git clone https://github.com/hiono/taxion.git
cd taxion
go build -o ./taxion ./cmd/taxion
./taxion init --dir /path/to/work
./taxion ready
./taxion doctor
```

`init` は `.agents/skills/taxion-local/SKILL.md` に実行したバイナリのパス、バージョン、SHA-256を記録し、異なる既存ファイルを上書きしません。対象プロジェクト内で新しいエージェントセッションを開始し、このSkillを読み込んでください。互換性のない別のTaxion管理Skillが有効なセッションでは、そのSkillを使わずに操作できるとは限りません。`doctor` はLinuxとCopilot CLI、Git、`diff`の有無を確認します。認証やProviderへの接続は確認しません。

外部エージェントは、依頼内容と作業ディレクトリを利用者と共有し、Coderのホスト実行が許可されていることを確認します。ProviderとCoderはOS隔離されないため、信頼できる作業ディレクトリを使います。依頼と差分はProviderへ送られるため、秘密情報を含めないでください。

1. Skillに記録されたバイナリの `version` とCopilot CLIの有無を確認します。ログインが必要な場合は、同じバイナリの `provider login` を使用します。
2. 利用者の依頼を `--request`、対象を `--working-directory` に指定し、同じバイナリの `run` を実行します。依頼文はシェルでコマンドとして解釈せず、一つの引数として渡します。`--consult` は必要な場合だけ指定します。
3. 表示されたJob IDと、指定した場合は `--store` のパスを控えます。保存内容は同じバイナリの `job show --store <path> <job-id>` で確認できます。既定の保存先を使った場合は `--store` を省略します。
4. 実際のファイル変更、確認できたテスト結果、Reviewerの指摘を確認して報告します。会話の終了だけで成功やレビュー合格と判断せず、Gitへのマージが必要な場合は最新の対象を独立してレビューします。

実行が失敗・中断した場合は、保存された会話と作業ディレクトリを確認します。v0.1.0には同じJobの自動再開やProvider操作の結果照会がないため、結果が不明な実行を自動で再送しません。保存済みの変更と会話を残し、未確認の状態を報告します。

v0.1.0には `init`、`ready`、`doctor` はありません。すでに有効な別バージョンの運用Skillとの互換性は保証しません。

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

`--review --allow-coder-tools`を指定すると、Coderのツール権限要求を許可し、作業ディレクトリで直接編集させます。OS隔離はなく、ディレクトリ外やネットワークにもアクセス可能です。失敗時も変更は残り、終了後の子プロセス停止は保証されません。PlannerとReviewerのツール権限要求は拒否しますが、Provider自体もOSで隔離されていません。

Gitリポジトリでは、Coder開始時のcommitとの差分と新しい未追跡ファイルをReviewerのProviderに送ります。作業前からの管理済み変更も含みます。作業前からの未追跡パスと無視された未追跡ファイル、作業ディレクトリ外、バイナリとsubmoduleの内容は含みません。既存の未追跡ファイルを別名にコピーした場合は区別できません。作業領域は取得中・取得後にも変更される可能性があるため、完全な独立検証は保証されません。Gitまたは開始時のcommitが利用できない場合は警告してテキストのみをレビューします。そのほかの取得エラーやGit出力が8 MiBを超えた場合は終了し、変更は残ります。

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

Trusted JobではOIDC認証済みDRAFT Jobの作成、PlanningAuthorizationの発行、Taskless PLANNERのStart intentの保存までを提供します。Start intentはProviderを起動しません。ローカル協調実行はTrusted Jobの実行・完了や中断後の自動再開ではありません。
