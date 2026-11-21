# Development Quality Gates

Version: 1.1
Date: 2026-11-21

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
| Secret scan | Commit前に秘密情報検査を実施します。Push / Pull RequestのCIではGitleaksで取得可能な全branch・tag・Pull Request headのGit履歴を検査します。疑いが未解決の場合はCommitしません。 |
| Docs-only | 文書のみの変更ではbuildを必須としません。 |

macOS固有の問題は記録して保留し、まとまったタイミングで検証・修正します。macOS native検証自体は廃止しません。

### 2.1 Secret scan

ローカルではGitleaksを使用し、Commit前に次のコマンドで現在のGit refsが到達可能な履歴を検査します。

```sh
gitleaks git --redact --exit-code 1 --log-opts="--all" .
```

GitHub Actionsでも同じGit履歴スキャンを実行します。CIはshallow cloneを使用せず、remote branch・tag・Pull Request head refsを取得してからスキャンします。Gitleaksのバージョンを固定し、配布アーカイブのSHA-256を検証してから実行します。

検出結果はredactされた出力だけを扱います。秘密値をIssue、PRコメント、ログ、artifactへ転記しません。検出された値が実Credentialの場合は発行元で失効・ローテーションします。Git履歴の書き換えは明示的な承認なしに実施しません。

スキャン対象は実行時点でGitHubから取得可能なrefsと、それらから到達可能なcommitです。削除済みrefsのみから到達可能なcommit、外部fork側だけに存在するcommit、GitHubから取得できないオブジェクトは対象外です。GitHub Secret Scanningも併用し、Security alertsを確認します。

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
