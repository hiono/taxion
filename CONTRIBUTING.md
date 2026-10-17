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

CLI のコマンド・flag を変えた場合は `mise exec -- go generate ./cmd/taxion` で
bash 補完定義を更新します。生成処理は隔離した CLI の help を使い、
通常テストの `TestCompletionMatchesActualCLIHelp` が生成済み定義との一致を確認します。

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

文書名は内容を表す小文字の英語名をハイフンで区切ります。一般のガイド・仕様書に通読順ではない連番を付けず、[利用者向け索引](docs/userguide/README.md)と[開発者向け索引](docs/develop/README.md)で読者の作業順を示します。ADR の識別番号は判断の参照として保持します。リリースノートは GitHub Release の本文として管理し、repository には保存しません。移動時は相互リンクだけでなく、文書を読むテストとコード内参照も更新します。[旧パスの対応表](docs/develop/README.md#文書名と旧パス)は過去の記録から現在の文書を探すために使います。

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

help を分類し直す場合はコマンド行の脱落・重複と分類内の件数を確認します。見出しはインデントせず、コマンド行と区別してください。`go generate ./cmd/taxion` の再生成前後で、表示だけの変更なら `completion.bash` に差分がないことも確認します。`ready` の原稿を変える場合は配置済み skill の互換性・明示更新と JSON 例の通し回帰を検証します。見出し追加だけで実 LLM の誤操作率や token 消費が改善したとは報告しません。

初回利用のゴールは、今の coding agent が `taxion ready` を読み、依頼の改善から承認済みの Coder・検証・独立 Reviewer・復旧までを操作することです。利用者による JSON や担当ごとの指示の転記は前提にしません。`ready` は静的な手順出力だけを行い、モデルや workspace を開きません。[ADR-0031](docs/develop/adr/0031-bootstrap-the-current-coding-agent-with-ready.md)を参照してください。

`go test ./cmd/taxion -run TestReady` は、実出力の JSON、未承認・異なる hash の拒否、設定承認、Flow、host 検証、同一 tree の独立レビューを合成 Runtime と実 Git で確認します。操作は決定済みであり、実 agent が指示を理解し判断する品質の受入れとは区別します。既存の停止・successor 回帰も維持します。

外側の agent を使わず、内部 Planner と計画を相談する入口も残します。`request` は intake、出典付き設定承認の照合、既存の Flow 実行処理を使います。別の実行処理は設けません。同梱指示は snapshot mode の明示選択であり、旧方式の空の tool 既定値は維持します。[ADR-0030](docs/develop/adr/0030-refine-natural-language-requests-with-planner.md)を参照してください。

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

#78 では、別途承認した実文書の作業で、選択した3 skill の Coder による実 invocation、読み込み結果、点検の証拠、同じ tree の成果物に対する独立レビューの `SUCCEEDED / APPROVE` 受入れを確認しました。#78・#79・#80 の受入れは、停止後の部分成果復旧と Copilot 取消後の OpenCode 継続を含めて完了しています。外部 skill は操作者が試用時に用意する入力であり、Taxion の依存ではありません。その本文を fixture にコピーしません。

実文書の前に、操作者が用意した3つの執筆 skill と `natural-japanese` の付随 lint を無課金で確認できます。次の絶対パスには `natural-japanese/`、`japanese-tech-writing/`、`cognitive-rhythm-writing/` が必要です。実行には既存の `uv` と、その script の依存・Python がローカルに揃っている必要があり、テストは `uv run --offline` でダウンロードを禁止します。

```bash
TAXION_DOCUMENT_SKILL_ROOT=/absolute/path/to/skills \
  go test ./internal/gitproof -run '^TestDocumentSkillValidationPreflightWithoutModel$' -count=1 -v
```

この明示 opt-in は各 skill の内容 hash と原本不変を照合し、一時 Git repository の小さな文書へ、承認した host validation として実 lint を実行します。結果の JSON、検証時刻、対象 SHA を確認します。指定後に skill・実行ファイル・依存が不足していれば失敗し、成功に見せる skip はしません。未指定の通常テストでは実行せず、`mise run check` はこの入力を空にして外部 skill を使いません。

この検査は SDK session を作らず、skill の実ロード、モデルによる規範の適用、実文書の品質、独立レビューは確認しません。SDK 内の任意 shell や worktree 外 read も許可しません。実作業では Task の `validationCommands` に入力と出力先を固定した検査を明示承認し、role が使える tool と file access は別に照合してください。lint は指摘があっても exit 0 を返すため、成功終了だけで合格とせず、指摘への対応または残す理由を記録します。

保存済み障害の復旧前には、一貫した SQLite backup と元の worktree を保持してください。SDK の取消と、その後の workspace transaction は別々に確認します。取消が成功しても結果の保存に失敗する場合があります。suspension・cancellation の receipt、成果物 hash、観測済み利用量を残し、成功扱いにするために旧契約や予算を書き換えません。

private database、生ログ、credential 値は commit に含めません。一般化した知見と採択した制約は ADR、現在の動作は担当する仕様文書、操作者の手順は利用者ガイドに記載します。

### OpenCode ACP の回帰と実受入れ

`go test ./internal/runtime/openacp` は独自の fake ACP peer で接続・権限拒否・切断・期限・
取消・usage 欠落を確認します。外部 OpenCode、login、モデル、network は不要です。
Linux の namespace 境界は `TAXION_NAMESPACE_TEST=1 go test -race ./internal/runtime/openacp`
で opt-in にできます。この検査も外部モデルは使いません。隔離に必要な Linux command と
PATH 上の native `rg` が必要であり、隔離内の検索成功と実行ファイルの read-only も照合します。

実 OpenCode 1.19.0 の確認は、操作者が用意した executable と `model` だけの JSON 設定を
絶対パスで指定し、両方の生 SHA-256 をそれぞれ `TAXION_OPENCODE_HASH` と
`TAXION_OPENCODE_CONFIG_HASH` に `sha256:` 付きで設定します。
`TAXION_OPENCODE_PATH` と `TAXION_OPENCODE_CONFIG` は各ファイルのパスです。
`TAXION_OPENCODE_ACP_HANDSHAKE=1 go test ./internal/runtime/openacp -run '^TestLiveOpenCodeHandshake$'`
は接続だけを確認し、prompt を送りません。`TAXION_OPENCODE_ACP_PROMPT=free-model-diagnostic`
を明示した `TestLiveOpenCodePrompt` は最小 prompt を送る別検査です。stderr や model 応答を
含む出力は非公開で扱い、接続成功を無料・認証・quota の保証にはしません。

`TestOpenCodeLiveDocumentFlow` は実 Planner、限定した公開文書の Coder、host lint、
独立 Reviewer を接続する別の受入れです。モデル送信と scope の承認後にだけ実行します。
設定の model は `opencode/big-pickle` に固定し、他モデルへ切り替えません。
公開 HEAD から独立 worktree を作り、選択した公開 ADR を検索・用途別承認付きで添付します。
このテストは絶対 `TAXION_DOCUMENT_SKILL_ROOT` と、未作成の絶対保存先
`TAXION_OPENCODE_LIVE_OUTPUT`、`TAXION_OPENCODE_LIVE_APPROVAL=free-big-pickle-document` を要求します。
実行は `go test ./cmd/taxion -run '^TestOpenCodeLiveDocumentFlow$' -count=1 -v -timeout 17m` で、
15 分の共通期限・修正 0 回です。private な DB、応答、event、worktree を保存し、
失敗時も証拠を消しません。通常の `mise run check` はこれらの opt-in を無効にします。
成功した Coder を検査名目で繰り返さず、失敗原文と正式 receipt から次の修正を判断します。
追加質問に答えて計画を修正する場合は、同じ公開 baseline の有効な保存済み Planner 応答を
`TAXION_OPENCODE_LIVE_PLANNER_PREVIOUS` に絶対パスで指定できます。保存ファイルの hash と
引継ぎ元を別に記録し、以前の質問を再実行せず、新しい返答を実 Planner に渡します。
質問が解消した有効な提案を指定した場合は、公開 ancestor baseline から worktree を作り、
範囲・検証・skill 条件を再照合して、その計画を新しい Task の承認に使います。
過去の計画を自動承認する機能や、成功済み Coder の再実行機能ではありません。
成功判定では native event にある3 skill 各々の完了と、対象節末尾への1段落の挿入を確認します。
元の本文や他の節を書き換えた場合、モデルの作業報告だけで成功とは扱いません。
結果の解析に失敗した実 Coder の追記を引き継ぐ場合は、その保存済み `workspace.json` を
`TAXION_OPENCODE_LIVE_CANDIDATE_PREVIOUS` に絶対パスで指定できます。
旧 Flow の停止、Coder の失敗状態、同じ公開 baseline、対象1件・1段落を照合し、
観測した tree と原本 hash を保存して新 Task の承認へ含めます。旧結果は変更しません。
今回の skill 呼出し・本文確認と必要な誤りの修正を実施し、書き直しは避けます。
各試行の Flow と候補の操作者観測には固有 ID を使います。同じ repository の
保存参照を共有するためであり、以前の参照が別 tree を指す場合に上書きしません。
操作者の新しい観測を、旧 Dispatch の正式な取消・成功の receipt と混同しないでください。

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

通常は M15 #79 と Release #78 の受入れ条件を満たすまで公開しません。Release owner が明示的に例外を承認する場合は、未達条件と判断根拠を対応する Issue に記録し、checkbox は完了扱いにせず残します。GitHub Release の本文には公開する version の機能と利用上の制約だけを記載します。未確認を成功として報告してはいけません。

tag 作成前に開発環境の全確認コマンドを実行し、`git diff` と `git diff --cached` に非公開情報がないか確認します。GitHub Release に載せる本文を確認済み動作と利用上の制約に合わせ、候補 commit の SHA を固定します。通常は受入れ条件を満たしてから公開します。例外を承認した場合は Issue に未達条件と理由を記録します。tag と同名の Release が既にある場合は、対象 SHA と状態を確認し、自動で削除・移動しません。

```bash
git tag -a v0.3.0 -m "Taxion 0.3.0"
git push origin v0.3.0
git rev-parse 'v0.3.0^{commit}'
git ls-remote origin 'refs/tags/v0.3.0*'
```

公開した tag の対象 commit が固定した候補と一致することを確認します。その後、既存の設定・インストールに影響しない隔離環境で、[利用者ガイドの導入・更新・削除手順](docs/userguide/user-guide.md#現在の導入方法)を実行します。pseudo-version の過去成功を公開 tag の導入成功として扱いません。失敗した場合は Release object を公開せず、tag を保持して失敗箇所を記録します。

導入確認の成功後に、GitHub Release 用の Markdown 本文を repository 外で確定し、確認結果を Issue に記録します。例外承認がある場合は、Issue に受入れ未達と判断を記録してから公開します。次の操作は tag が存在することを要求し、GitHub CLI による暗黙の tag 作成を避けます。

```bash
gh release create v0.3.0 --verify-tag --title "Taxion 0.3.0" \
  --notes-file /tmp/taxion-v0.3.0-release-notes.md
gh release view v0.3.0 --json tagName,targetCommitish,isDraft,isPrerelease,url,body
```

Release の tag・本文・公開状態と URL を再取得し、送信したノートと対象 tag を照合します。Release 作成後の記録だけで、未実施の導入確認や実 SDK 受入れを補ったことにはしません。
