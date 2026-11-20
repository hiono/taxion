# CLI usage

## Create a Job

原文の依頼をそのまま保存してDRAFT Jobを作成します。Planner呼出しより前にJob identityと依頼を永続化するための最初の入口です。

```sh
taxion job create --request "I have several ideas for a small product. Help me clarify the goal and build it."
```

成功時はJob ID、DRAFT state、aggregate revisionを表示します。

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
taxion job create --store ./taxion-local.sqlite --request "..."
taxion job list --store ./taxion-local.sqlite
```

SQLite databaseとWALファイルは秘密情報を含む可能性があるため、Gitへ追加しないでください。

## Current limitation

このCLI sliceはローカル環境でのDRAFT Job作成・読取りと、プロセス再起動後の永続データ読戻しを提供します。これは永続化の確認であり、RoleExecutionやProvider operationを含むJob実行の再開を意味しません。Trusted Ingress、認証済み利用者principal、intake provenance、Planner実行、PlanningAuthorization、JobContract提案・承認、Coder / Reviewer実行、Provider lifecycle、Handoff、成果・証拠の受入れ、Job完了はまだ実装していません。このCLI経路をProduct Contract全体のintake適合とみなしません。
