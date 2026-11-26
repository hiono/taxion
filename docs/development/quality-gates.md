# Development Quality Gates

Version: 1.1
Date: 2026-11-26

本書はTaxionの開発作業に適用する品質ゲートを定義します。これらは製品機能の規範やProduct Acceptance Test Specificationの受入れシナリオではなく、開発・変更を安全に進めるための運用条件です。

## 1. 開発方式

- リモート完結型の開発を許容します。ローカルcheckoutの存在を作業開始条件にしません。
- 作業開始時は現在のリポジトリHEAD、関連ADR、Issue、実装、テスト、PR、CIを確認します。
- 古い計画や以前の作業記録をそのまま実行せず、現在確認できる情報と現HEADを基準に作業を選択します。
- 実施していないテスト、Provider検証、CIを成功扱いしません。

## 2. 必須品質ゲート

| Gate | 要件 |
|---|---|
| Linux | format / vet / test / race / buildを実施し、失敗を修正します。 |
| Windows | リリース準備段階まで通常開発の必須ゲートから除外します。 |
| GoReleaser cross-build | macOS targetを含め、リリース準備段階まで通常開発の必須ゲートから除外します。 |
| macOS native | 任意です。未実施・実行中・失敗を理由に通常開発を停止しません。 |
| Secret scan | すべてのCommit前に秘密情報検査を実施します。疑いが未解決の場合はCommitしません。 |
| Docs-only | 文書のみの変更ではbuildを必須としません。 |

macOS固有の問題は記録して保留し、まとまったタイミングで検証・修正します。macOS native検証自体は廃止しません。

## 3. CI証拠

- GitHub ActionsのLinux CIはgo.modに従うGo toolchain（現行はGo 1.26）でformat / vet / test / race / buildを実行します。
- ローカルのformat検査はPATH上のgofmtを使用します。Goのバージョンだけを理由にローカル検査やPushを拒否しません。
- Go 1.26専用の構文・API・動作が必要な変更は、Go 1.26を使用するCIで検証します。
- CI Runはworkflow、Run ID、検証対象の完全なHEAD SHAと対応付けます。
- CI結果はそのRunが実際に検証したSHAにのみ有効です。HEADが更新された場合、以前のRunの成功を新しいHEADの検証結果として扱いません。
- 未実施の必須ゲートは未確認として扱い、成功と報告しません。

## 4. Commitと修正

- 新しい独立した意味的変更は通常Commitとします。
- 既存の意味的変更に対する修正・リカバリは、原則として対象Commitを指定したfixup! Commitとします。
- フォーマットのみの変更は意味的変更と分離します。
- Commit前に差分レビューとSecret scanを完了します。

## 5. Push前のローカルゲート

- Push前に .githooks/pre-push が scripts/check-gofmt.sh を実行し、cmd / internal 配下のGoファイルを検査します。
- scripts/check-gofmt.shはPATH上で利用可能なgofmtを直接実行します。go.modのgo directiveに合わせたtoolchain取得、GOTOOLCHAINによるバージョン固定、Workspaceのtoolchain切替は行いません。
- ローカルのGoが1.23等であっても、gofmtが利用可能で書式検査が成功すればPushを許可します。gofmtが見つからない場合、または書式不備がある場合だけPushを拒否し、原因を表示します。
- CIではactions/setup-goがgo.modに従ってGo 1.26を設定した後、同じscripts/check-gofmt.shを実行します。ローカルは利用可能なgofmtで迅速に検査し、Go 1.26での最終的な書式・コンパイル・テスト検証はCIで行います。
- Git hookとcore.hooksPathの設定はcloneごとのローカル設定であり、Gitのclone / fetchでは自動的に有効になりません。各cloneで初回に次を実行します。

    ./scripts/install-git-hooks.sh

- InstallerはRepositoryローカルのcore.hooksPathを.githooksに設定します。別のローカルhook pathが既に設定されている場合は上書きせず、エラーで停止します。
- このゲートは書式検査専用です。vet / test / race / build / Secret scanは既存のCommit前運用・CIゲートで検証します。
