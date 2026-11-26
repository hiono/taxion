# Development Quality Gates

Version: 1.0
Date: 2026-11-20

本書はTaxionの開発作業に適用する品質ゲートを定義します。これらは製品機能の規範やProduct Acceptance Test Specificationの受入れシナリオではなく、開発・変更を安全に進めるための運用条件です。

## 1. 開発方式

- リモート完結型の開発を許容します。ローカルcheckoutの存在を作業開始条件にしません。
- 作業開始時は現在のリポジトリHEAD、関連ADR、Issue、実装、テスト、PR、CIを確認します。
- 古い計画や以前の作業記録をそのまま実行せず、現在確認できる情報と現HEADを基準に作業を選択します。
- 実施していないテスト、Provider検証、CIを成功扱いしません。

## 2. 必須品質ゲート

| Gate | 要件 |
|---|---|
| Linux | format / vet / test / raceを実施し、失敗を修正します。 |
| Windows | vet / testを実施し、失敗を修正します。 |
| GoReleaser cross-build | macOS targetを含む必須targetを検証します。 |
| macOS native | 任意です。未実施・実行中・失敗を理由に通常開発を停止しません。 |
| Secret scan | すべてのCommit前に秘密情報検査を実施します。疑いが未解決の場合はCommitしません。 |
| Docs-only | 文書のみの変更ではbuildを必須としません。 |

macOS固有の問題は記録して保留し、まとまったタイミングで検証・修正します。macOS native検証自体は廃止しません。

## 3. CI証拠

- CI Runはworkflow、Run ID、検証対象の完全なHEAD SHAと対応付けます。
- CI結果はそのRunが実際に検証したSHAにのみ有効です。
- HEADが更新された場合、以前のRunの成功を新しいHEADの検証結果として扱いません。
- 未実施の必須ゲートは未確認として扱い、成功と報告しません。

## 4. Commitと修正

- 新しい独立した意味的変更は通常Commitとします。
- 既存の意味的変更に対する修正・リカバリは、原則として対象Commitを指定したfixup! Commitとします。
- フォーマットのみの変更は意味的変更と分離します。
- Commit前に差分レビューとSecret scanを完了します。

## 5. Push前のローカルゲート

- Push前に .githooks/pre-push が scripts/check-gofmt.sh を実行し、cmd / internal 配下のGoファイルを検査します。
- 検査にはgo.modに指定されたバージョンのGo toolchainに含まれるgofmtを使用します。ローカルのGo本体が古い場合はGoのtoolchain自動選択機能で必要なtoolchainを取得するため、Go 1.23環境でもGo 1.26のgofmtで検査できます。
- 必要なtoolchainを取得できない場合、または対応するgofmtが見つからない場合はPushを拒否し、原因を表示します。ネットワーク接続またはtoolchainのインストールを解決して再実行します。
- 書式不備がある場合は対象ファイルと差分を表示してPushを拒否します。修正後にPushを再実行します。
- CIも同じ scripts/check-gofmt.sh を実行します。ローカルとCIで書式判定の実装を分岐させません。
- Git hookと core.hooksPath の設定はcloneごとのローカル設定であり、Gitのclone / fetchでは自動的に有効になりません。各cloneで初回に次を実行します。

    ./scripts/install-git-hooks.sh

- InstallerはRepositoryローカルの core.hooksPath を .githooks に設定します。別のローカルhook pathが既に設定されている場合は上書きせず、エラーで停止します。
- このゲートは書式検査専用です。vet / test / race / build / Secret scanは既存のCommit前運用・CIゲートで検証します。
