# 開発への参加方法

## 開発環境

必要な Go バージョンは `go.mod`、対応するツールチェーンは `mise.toml` に記載しています。ビルド前にリポジトリのツールを準備してください。

```bash
mise install
mise exec -- go version
mise exec -- go test ./...
```

Go 全体のテストは `mise exec -- go test ./...` で実行します。
PR を作成する前は `mise run check` で format・vet・Go test/build・Web test をまとめて確認します。
動作の変更には、対応するガードレールと回帰テストが必要です。

1. [ロードマップ](docs/develop/roadmap.md)から小さな作業を選びます。
2. `internal/core` の依存は標準ライブラリだけにします（SDK-001）。外部との接続は `internal/runtime/<name>/` に置きます。
3. 動作を変更するときは、ガードレール ID とテストを対応付けます。
4. 個人情報・内部情報・秘密情報を commit に含めません（PRIV-001）。push 前に `git diff` を確認します。
5. ローカルで次の確認を実行します。

```bash
mise run check
```

Web の確認には Node 22.18 以上を使います。mise と CI は最低対応版の 22.18.0 に揃えています。
依存 package のインストールは不要です。
CI でも Go と Web の両方を検査します。これらの通常検査ではモデルを呼びません。
README・利用者/開発者文書・ADR のローカルリンクと見出し参照も Go テストで検査します。
`mise run check` は live integration の環境変数を無効にするため、
shell に以前の opt-in が残っていても有料モデルや実サービスへの受入れを開始しません。
実サービスの受入れは、対象テストと範囲・予算を別途指定して実行します。

## 説明文の書き方

Issue 本文、Milestone の説明、README、利用者・開発者向け文書は日本語で記載します。Memory、sanitizer、rate limiting、lockout、audit hash-chain などの定着した技術用語や機能名、識別子は元の表記を保ちます。コード、コマンド、API 名、引用原文も変更しません。操作は実際のコマンド名と flag を使い、その操作で何をするかを説明します。比喩や意味の近い別の語で置き換えません。

提案、記録した判断、現在の実装を区別してください。過去の要件や結果は残し、現在の動作と誤解される場合は補足します。文章の整理を完了とする前に、関連する Issue、文書、コードコメント、表示文も確認します。

## 文書の配置と責務

文書名は内容を表す小文字の英語名をハイフンで区切ります。一般のガイド・仕様書に通読順ではない連番を付けず、[利用者向け索引](docs/userguide/README.md)と[開発者向け索引](docs/develop/README.md)で読者の作業順を示します。ADR の識別番号とリリースノートの版は保持します。移動時は相互リンクだけでなく、文書を読むテストとコード内参照も更新します。[旧パスの対応表](docs/develop/README.md#文書名と旧パス)は過去の記録から現在の文書を探すために使います。

| 配置 | 記載する内容 | 記載しない内容 |
| --- | --- | --- |
| `README.md` | プロジェクトの概要、導入への案内、参照文書 | 詳細仕様、作業報告 |
| `docs/userguide/user-guide.md` | 導入と最初の承認済み Coder・Reviewer 実行 | option の完全な一覧 |
| `docs/userguide/configuration.md` | 設定場所、優先順位、環境変数、実行前確認の範囲 | credential の保管、契約上の利用資格の証明 |
| `docs/userguide/cli-reference.md` | CLI 操作と許可・制約 | 製品要件、設計の理由 |
| `docs/userguide/remote-access.md` | 公開接続の設定と手順 | 認証設計の判断 |
| `docs/userguide/audit-log.md` | 監査項目、照合の限界、保存手順 | 監査機能の実装履歴 |
| `docs/userguide/memory-model.md` | 現在の Memory の保存、scope、昇格、検索、export | 最初の提案モデル |
| `docs/userguide/execution-records.md` | 実行の復旧、receipt、承認済み外部記録の公開 | 作業別の経過報告 |
| `docs/userguide/release-notes-0.1.0.md` | 公開対象、確認済み機能、既知の制約、導入方法 | 未確認の機能を公開済みとする説明 |
| `docs/develop/charter.md` | 設立時の対象範囲と制約 | 現在の機能一覧 |
| `docs/develop/guardrails.md` | 必要な不変条件と実装での確認箇所 | 未実装の要件が強制されるという主張 |
| `docs/develop/privacy-provenance.md` | 公開情報とレビューの規則 | 個別の情報削除報告 |
| `docs/develop/runtime-contract.md` | Runtime 契約と現在の adapter の制約 | Runtime の評価履歴 |
| `docs/develop/roadmap.md` | 計画と完了済み作業への参照 | 詳細な実装結果 |
| `docs/develop/ledger-schema.md` | データ形式、API の入出力、検証条件 | API 設計の理由 |
| `docs/develop/orchestration-requirements.md` | 採択済み要件、段階別受入れ、実装との差分 | 現在のコマンド一覧、完了の証拠 |
| `docs/develop/task-graph.md` | 現在の Task Graph 操作、保存、状態遷移の検査と制約 | Runtime 実行の主張、Memory 設計の理由 |
| `docs/develop/forge-capabilities.md` | 開発基盤ごとの仕様・実装・受入れと後続作業 | 未確認の基盤操作を対応済みとする説明 |
| `docs/develop/specification-input.md` | 仕様の取得・用途別承認・照合 | 出典不明の資料、個別の調査ログ |
| `docs/develop/decision-traceability.md` | ADR・Milestone と実装 Issue・現在の仕様の対応 | Issue 別の証拠、現在の仕様本文の代替 |
| `docs/develop/adr/` | 判断、選択肢、理由、後続の補足 | 現在のコマンド説明の複製 |
| `web/README.md` | Web console の準備と proxy の動作 | API 仕様の複製 |
| `SECURITY.md` | 非公開の報告方法、保護対策、既知の限界 | 完全な保護の保証 |
| Issues・PRs・Milestones | 範囲、進捗、実装結果、完了の証拠 | 現在の使い方を確認する唯一の資料 |
| コードコメント | API 契約、分かりにくい局所的判断の理由 | 作業履歴、操作ガイド、コードの逐語説明 |

説明を複製せず、担当する文書へリンクします。過去の ADR の前提に誤りがあった場合は、その ADR に実装時の補足を加え、現在の動作は担当する仕様文書に記載してください。元の判断を説明なしに書き換えたり、どちらが現在の仕様か分からない矛盾を残したりしません。

## モデルを呼ばないテストと復旧の証拠

初回利用のゴールは、人のことばによる依頼を Planner と改善し、JSON や担当ごとの指示をコピーせずに Coder・検証・独立 Reviewer へ進めることです。`request` は intake、出典付き設定承認の照合、既存の Flow 実行処理を使います。別の実行処理は設けません。同梱指示は snapshot mode の明示選択であり、旧方式の空の tool 既定値は維持します。[ADR-0030](docs/develop/adr/0030-refine-natural-language-requests-with-planner.md)を参照してください。

`go test ./cmd/taxion -run TestRequest` で、確認質問、計画改善、承認、同一 tree のレビュー、拒否と receipt を確認します。独自に作成した応答と実 Git を使い、モデル・login・ネットワークは不要です。実モデルが適切な計画を作ることや、skill を読み込むことの証明にはなりません。

file access の承認、保持した tree のレビュー、停止専用接続は ADR-0026〜0028 に分けて記録しています。変更時は[ガードレール](docs/develop/guardrails.md)の FILE-001、SHA-001、STOP-001、LED-002、BUD-001 を維持します。login 済みアカウントによるコード作成・レビューの受入れ前に、次のテストを実行してください。

```bash
go test ./cmd/taxion ./internal/runtime/copilot ./internal/runtime/controller \
  -run 'TestUserGuideColdStartExamplesWithoutSDK|TestStopOnly|TestSuspendedResultCanBeCancelled|TestReviewFlowUsesUnchangedExplicitMultiTaskConfigurationApproval'
go test ./internal/gitproof ./internal/core
```

一時 workspace、実 Git、合成 SDK protocol peer を使うため、モデル指示は送信しません。login 済み SDK の結合テストは明示選択時だけ実行します。ローカルテストの成功で、アカウントの利用資格、provider の課金上限、コード作成・レビューの通し受入れを確認したことにはなりません。

skill のテストでは、一時 directory に独自の合成指示を置き、リポジトリ所有の skill 専用 plugin fixture を使います。通常のテストに、特定の外部執筆 skill、個人の home path、global install、login、ネットワーク、有料モデルを要求しません。内容固定方式では担当別の承認、複数 asset、create・resume 時の照合、`skill` tool、成功・失敗の証拠、欠落・変更・未承認 asset の拒否を確認します。

```bash
go test ./internal/tools ./internal/runtime/copilot ./internal/runtime/controller \
  -run 'TestAssetsApprovalsAndPluginSurface|TestApprovedSkillsWithoutModelUse|TestSkillToolRequiresApprovedAssets|TestSDKToolsCreateResumeRoleAndImmutableHash|TestReviewFlowPreflightsFutureSkillAssetsWithoutModelUse'
```

ローカルの native plugin discovery だけを確認する場合は、`TAXION_TEST_LOCAL_PLUGIN_DISCOVERY=1 go test ./internal/runtime/copilot -run TestLocalPluginDiscoveryWithoutModelUse` を明示して実行します。モデル指示は送信しません。要求した確認に必要な CLI がなければ失敗します。合成 hook の結果や native discovery は、実際の skill 読み込みや文書品質の証拠にはなりません。

#78 では、別途承認した実文書の作業で、選択した skill、読み込み結果、点検の証拠、同じ成果物の独立レビューを確認します。外部 skill は操作者が試用時に用意する入力であり、Taxion の依存ではありません。その本文を fixture にコピーしません。

保存済み障害の復旧前には、一貫した SQLite backup と元の worktree を保持してください。SDK の取消と、その後の workspace transaction は別々に確認します。取消が成功しても結果の保存に失敗する場合があります。suspension・cancellation の receipt、成果物 hash、観測済み利用量を残し、成功扱いにするために旧契約や予算を書き換えません。

private database、生ログ、credential 値は commit に含めません。一般化した知見と採択した制約は ADR、現在の動作は担当する仕様文書、操作者の手順は利用者ガイドに記載します。

### バックエンドと同じ探索を検証する

通常のカスタマイズ探索は設定中のバックエンドに任せ、未選択バックエンドの配置を
追加検索しません。`taxion doctor` は同じ adapter の worktree・CLI・環境・home を使い、
native inventory と補足のファイル観測を表示します。回帰では一時 HOME と合成 CLI から
渡された条件を確認し、無効化・不正形式・取得不能と、file hooks・MCP・モデルを
起動しないことを検査します。doctor の成功を認証・実ロード・品質確認の代替にはしません。

## 残予算の案内

[ADR-0029](docs/develop/adr/0029-separate-budget-guidance-from-provider-quota.md)の残予算指示を controller と Copilot adapter に実装しています。Flow の token 計算、session の credit 上限（soft ceiling）、context 容量、アカウントの quota は区別します。provider 固有の quota API と課金型は adapter に置き、不明・期限切れの観測はその状態を明示します。

login 済みアカウントの受入れを追加する前に、合成 fixture で、既定値と依頼別の優先順位、単位、利用量の欠落、quota 失敗、account の照合、時間制限付き読取り、停止条件が変わらないことを確認します。変動する観測は送信 receipt に記録し、承認済み system prompt を変更しません。OpenCode は adapter 契約の実装前に対応済み設定へ追加しません。

新規 CLI proposal の `budgetGuidance` は Task・Dispatch の承認済み内容に含めます。旧 policy の省略時は、従来の指示形式を保ちます。account 読取りは任意の adapter 機能であり、選択済み account と policy の承認が必要です。`account.getQuota` は最大5秒の timeout で一回だけ呼びます。モデル turn、retry、内部 endpoint、Billing への代替照会は追加しません。

`budget-guidance` receipt は別の stream ID を持ち、送信した補足全文と prepared・returned・denied の段階を保持します。隔離した Planner、質問、SDK 検索の結果にも、それぞれの観測と補足を保持します。

モデルを呼ばないテストでは、設定層の既定値・override、期限切れ・取得不能、account 不一致、停止・利用量の確認、protocol frame を確認します。login 済み account の読取り専用受入れは、`TAXION_COPILOT_QUOTA_ACCEPTANCE=1` と `TAXION_QUOTA_ACCOUNT`、`TAXION_QUOTA_CLI`、`TAXION_QUOTA_BASE_DIRECTORY` を明示した場合だけ実行します。session を作成せず、モデル指示も送信しません。この成功はモデル品質やコード作成・レビューの Release 条件を満たした証拠にはなりません。

固定 Copilot SDK の `Account.GetQuota` と session の `Usage.GetMetrics` は実験的 API です。quota の requests・割合、session 累積 cost、期間別 Billing REST report は別々の観測です。累積値を call 別 receipt に合算したり、対応する単位の定義なしに requests を credits へ換算したり、課金情報の取得不能をゼロと扱ったりしません。組織負担の利用量は個人 Billing report に含まれません。quota error 時に内部 endpoint や管理者 credential へ切り替えません。

## コードコメント

コメントには、制約、順序、許可・拒否の条件、選択理由を記載します。理由を書く前に周辺コードと呼出し元を読み、実装だけから意図を創作しません。名前、代入、分岐、テストの assertion、呼出し順序を繰り返すだけのコメントは削除します。英語コメントも日本語本文と同じ基準で確認します。

公開 API と package の Go documentation は保ちます。戻り値、既定値、所有権、signature から分からない制約は、逐語説明ではなく API の契約です。protocol 上必要な動作と分かりにくい fixture の選択理由は関連コードの近くに置きます。長い設計理由は ADR に記録し、必要に応じてコードから参照します。

## リリース

M15 #79 と Release #78 の受入れ条件を満たすまで `v0.1.0` を公開しません。[リリースノート](docs/userguide/release-notes-0.1.0.md)は、clean workspace での試用と有料 runtime の確認が承認され、完了するまで草案のままです。

tag 作成前に開発環境の全確認コマンドを実行し、`git diff` と `git diff --cached` に非公開情報がないか確認します。リリースノートを確認済み動作と既知の制約に合わせた後、tag を作成して push し、その tag を GitHub Release として公開します。

```bash
git tag -a v0.1.0 -m "Taxion 0.1.0"
git push origin v0.1.0
gh release create v0.1.0 --title "Taxion 0.1.0" \
  --notes-file docs/userguide/release-notes-0.1.0.md
gh release view v0.1.0
```

公開後の導入は、リリースノートに記載した Go バージョンと `go:` backend コマンドで別途確認します。公開 tag に対する導入コマンドが成功するまで、導入確認済みとは報告しません。
