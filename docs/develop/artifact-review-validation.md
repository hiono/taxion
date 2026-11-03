# 持込み成果物の検証・独立レビューの確認記録

Issue: <https://github.com/hiono/taxion/issues/201>

対象は ADR-0056 の初期実装。配布済み RC4 の動作確認や製品 Task の再実行を表すものではない。
検証では一時 repository・store と runtime の test double を使い、実 provider のモデルは起動していない。

## 確認した動作

`internal/runtime/controller/artifact_test.go` で次を確認した。

| 条件 | 結果 |
| --- | --- |
| 未検証の29ファイルを持込み、追加編集なし | Controller が全変更パスを観測し、実コマンド検証後に同じ tree の Reviewer だけを起動 |
| baseline と入力 tree が同一 | 空差分でも独立レビューまで進む。入力プログラムのレビューは省略しない |
| `USER` owner の `VERIFICATION` Task | Coder Dispatch・session・許可上限はいずれもゼロ |
| 既存の Coder 契約から入力工程を選択 | 直接 Coder Dispatch を発行してゼロ上限を迂回できない |
| `workflow validate-input` 相当の API | 検証だけを実行し、モデル未起動の Reviewer 指示を残す。再実行を拒否 |
| 既存 ApprovalBundle | 元承認を保持し、同じ承認範囲の Reviewer 束を派生。入力をモデル成功へ変換しない |
| 検証失敗・検証による入力変更 | 実測結果を保存して終端停止し、Reviewer を起動しない |
| 検証中断後の runner recovery | `RUNNING` の結果不明記録を維持して停止。コマンドを再送しない |
| 承認後の入力変更・保存済み検証出力の書換え | 古い工程 hash、追記監査と一致しない変更を拒否 |
| Dispatch 作成前の停止と portable snapshot | 元の29ファイル・tree object・出典・未検証状態を保存／復元 |

preview と start が検証コマンドを実行せず、preview・検証・レビューが実 index と入力ファイルを
変更しないことも照合した。portable restore は保全の確認であり、復元先での直接実行を承認しない。

## 実行した検証

Go は repository の `mise` toolchain を使用した。

```sh
go generate ./cmd/taxion
go test ./cmd/taxion ./internal/core ./internal/gitproof ./internal/portable ./internal/store
go test ./internal/runtime/controller -run 'TestArtifact|TestPortable|Test.*Intake|Test.*Confirm' -count=1
go test -race ./internal/core ./internal/gitproof ./internal/store ./internal/runtime/controller \
  -run 'TestArtifact|Test.*ReviewFlow|Test.*Snapshot|Test.*StagePlan|Test.*BudgetAudit' -count=1
go test -race ./internal/portable ./internal/runtime/controller -run 'TestArtifact|TestPortable' -count=1
go vet ./...
go build ./...
git diff --check
mise run check
```

上記は成功した。初回の全体回帰も成功したが、CLI 追加後の全体回帰では completion の生成差分と
help 索引件数の未更新を検出した。生成物を更新し、既存の category 件数上限を緩めずに
証跡用コマンドへ分類した後、CLI 全体と変更 package・関連経路を再検証した。最後に live integration を無効にする
repository 標準の `mise run check` も成功し、Go 全体回帰・build・vet・整形確認と
Web の12テストを通過した。

## 独立実装レビュー

実装 commit `b8b9287`（比較元 `de10b0acf2d4356147eef0ff5e9614bf29901b9f`）を
別の code-review agent が読み取り専用で確認した。判定は
`APPROVE — No significant issues found in the reviewed branch diff.` だった。
これは今回の初期実装に対するコードレビューであり、製品の成果物レビューや実行許可ではない。

## 未対応範囲

初期実装では修正ループ、任意 packet lifecycle 委譲、restore 後の直接実行を拒否する。
通常 Coder Flow の変更一覧は既存の `git-diff-v1` で Controller が全差分を観測する。
持込み入力も同じ Git ツリー観測と検証コマンド実行を使い、出典と目的を別に保持する。
公開・リリース・製品 Task の再開は、このローカルな検証記録からは承認されない。

## 目的別の入力と結果受理

`review-input.v1` の必須コマンド合格を維持し、v2 は明示した検証方針を承認に結び付ける。
診断の完了には読める `workReport` を要求し、製品品質と公開 gate は通さない。
実装上の判断は [ADR-0065](adr/0065-separate-review-purpose-from-validation-success.md)を参照する。

| 合成ケース | 期待する結果 | 回帰試験 |
|---|---|---|
| 人の途中成果と既知の失敗を渡す | Coder 0、入力と実 Reviewer 指示に証拠を保持 | `TestSuppliedReviewIntakeCarriesHumanArtifactsAndFailureDiagnosisWithoutCoder` |
| 失敗コマンドと後続コマンドを診断する | exit 7 を `OBSERVED` に保存し、結果受理でも失敗を保持 | 同上の `failed-tests` |
| 編集を残せない失敗反復の記録 | 空のコマンド・空の差分で診断し、架空の編集を作らない | 同上の `no-edited-artifact` |
| コマンド不要と承認した文書レビュー | `identity-only` で対象・受入れ条件・結果を照合 | 同上の `document-review-only` |
| baseline と異なる人の commit | 両 SHA、全差分、対象 tree を固定して Reviewer 結果を受理 | `TestArtifactCommittedTargetUsesApprovedBaselineWithoutCoder` |
| 検証による対象変更・signal 終了 | 失敗を保存して停止し、Reviewer を発行しない | `TestDiagnosticObservationDoesNotWaiveIdentityOrUnknownExecution` |
| 形式の降格・目的矛盾・曖昧な重複 | 新しい実行を許可しない | `TestReviewInputRejectsPolicyDowngradeAndAmbiguousPurpose` |
| 診断 Reviewer の期限停止 | 正式再契約で元 receipt と消費を保持し、入力コマンドを再実行しない | `TestDiagnosticObservationRecoveryReusesFailedChecksWithoutReplay` |

上表は通常の Go 回帰試験で扱う合成ケースであり、実モデルや別マシンでの結果の代用ではない。

2026-11-02 に開発候補 `64b6302` の CLI と実モデルで、差分のない対象に対する診断を確認した。
合成した失敗反復の記録と exit 7 のコマンドを入力し、Coder の許可上限・実行数ともに 0 のまま、
独立 Reviewer の報告を正式受理した。入力と結果受理の双方で exit 7 を保持し、
入力 receipt は `OBSERVED`、診断は完了、品質評価は `NOT_ASSESSED_DIAGNOSTIC`、
公開判定は `diagnostic-not-product-approval` により拒否された。
この結果は実製品のテスト失敗や原因を再現したものではない。

最初の診断実行は、未開始 Reviewer に残る工程時間が不足して停止した。
元結果・対象・期限を保全し、既存の開発承認内で新しい工程期限を明示して正式復旧した。
保存した `OBSERVED` receipt を再利用し、入力コマンドと旧セッションは再実行していない。
結果受理時のコマンド実行は、元から承認した別の確認として保持した。

通常経路は `5cd5c30` の隔離した合成対象で、Coder 1 回、コントローラー検証、
独立 Reviewer 1 回、同一対象の `APPROVE` と Flow 完了まで確認した。
ただし、この試験契約は文書整合の必須条件を含めていなかったため、
`workflow evaluate --require-quality` は `UNMET` を返した。
完了した Flow を品質合格と読み替えず、元の試験契約・結果を保全した。

実装 `fd31366` の独立レビューでは、復旧説明が `OBSERVED` receipt も合格済みと表現する
不整合を 1 件検出した。`546c432` で保存済み receipt という中立な説明へ修正し、
関連する help・管理スキルも同期した。初回の `REQUEST_CHANGES` は変更していない。

検証は `fd31366` と同一 commit の通常 checkout で `go test ./...`、
説明修正後に復旧・ready・help・文書の対象試験、
`64b6302` で `go test -race -timeout 30m ./internal/runtime/controller -count=1`、
`5cd5c30` で `go build ./...` と `go vet ./...` を実施した。
Controller の race 試験は約 18 分 28 秒で終了した。
linked worktree の版表示試験は VCS revision が付かず失敗したため、同一 commit の
通常 checkout と未変更の main で成功することを分けて確認した。
最初の race 試験は既定の 10 分で timeout し、CI と同じ 30 分設定で完了した。

保存済み v1 の解釈は変更しない。v2 と `OBSERVED` を保存する前に workspace と Git を保全し、
旧版への rollback はその保全状態から行う。旧版で新しい store の更新を試みない。

## Dispatch のない入力検証失敗の後続契約（Issue #203）

実装 commit: `0a1558c`（比較元 `de2d6f5e203e19f642c7c11799fa7bee4ccebf02`）。
最終指摘修正後の `mise run check`、対象 race test、help 入力テスト、`git diff --check` は成功した。

`artifact_successor_test.go` では、持込み29ファイルを actual host command で不合格にし、
Dispatch なしの停止から別 worktree に同じ入力を保持して通常後続を提案・確認した。
失敗 artifact と receipt の digest は自動決定として提案 hash に含める。
新 Flow は `maxRepairs: 0`、Coder 1回・Reviewer 1回で、scripted runtime の Coder が修正し、
Controller の実コマンド検証と別 Reviewer を経て完了する。
実際の provider/model は使用せず、旧 Flow・receipt の完全一致を最後に確認する。

古い提案 hash、架空 target、`replacesFlow` 省略、両 worktree の drift を確認時に拒否する。
稼働中 runner／Flow、検証 `RUNNING`／`PENDING`／`PASSED` は後続根拠にせず、
receipt 変更で自動決定が変わることも確認した。
CLI help と ready／管理 skill 1.3.10 の回復案内は、workspace を作らないテストで照合する。
既存の artifact・再承認テスト、変更箇所の race test と `mise run check` は成功した。

独立実装レビューで、symlink 別名による同一 worktree の再利用を拒否できない点を指摘された。
文字列比較を directory identity の比較へ変更し、同一 directory と symlink 別名を拒否する回帰テストを追加した。
修正前の独立レビューはこの1点について `REQUEST_CHANGES` を返した。
指摘箇所に限定した独立追レビューは、directory identity の比較と回帰テストを確認し `APPROVE` を返した。
この追レビューを差分全体の新たな再承認とは扱わない。

この実装は未配布である。RC5 操作機の記録は利用者からの報告であり、この開発機で読み出したとは扱わない。
製品の修正・検証再実行・モデル実行・追加実行枠を許可せず、Issue #201 を完了扱いにしない。

## 現在の観測を新契約の入力にする（Issue #248）

実装候補は `908aa3366a19c95022c0765dbbd2537013890988`、
操作索引・補完と取得前フィルター検査の修正候補は
`1407fc758b9c39a4de121b990c40f52f11f541fc`。
設計判断は [ADR-0066](adr/0066-separate-observation-admission-and-execution.md)、
公開差分と継続的インテグレーションは
[PR #250](https://github.com/hiono/taxion/pull/250)で照合する。
以下は開発環境で確認した範囲の記録であり、配布や外部確認の完了を示さない。

実際の Git 作業ツリーと模擬実行基盤で、範囲外の入力によって部分成果の取得が失敗する
正式な取消処理を再現した。処理識別子と結果受理記録が残る状態から、
明示した新しい観測範囲で現在の入力を記録し、新契約の提案・承認、
別作業ツリーでの実装、実コマンド検証、独立した模擬レビューまで進む。
旧結果、未記録の利用量、停止・保留状態、元の実索引は保持する。
モデル提供元への実行は行っていない。

確認用の取得は保存状態と実索引を変更しない。変更なしの入力、
メタデータなしの通常入力、同じ承認済み観測の重複適用を扱う。
不正なメタデータ、元記録の省略、結果受理記録の欠落、所有権の残存、
禁止パス、承認後の入力変更、入れ子を含む観測記録の書換えを拒否する。
禁止した内容を Git オブジェクトへ格納する前に拒否するケース、
パス探索中に追跡済みファイルの外部フィルターを起動しないケース、
移送対象に観測の木と保持参照を含めるケースも追加した。

担当への実際の指示に、元の具体的な部分成果取得の阻害理由と
`unverified-current-input` の区別が含まれることを確認した。
参照ハッシュだけを診断として渡さず、作者・停止時点の同一性・
未記録の検証・利用量は未知として示す。

変更対象の全パッケージ、操作入口・文書・管理スキルの対象テスト、
`go vet ./...`、`go build ./...`、整形、公開文章検査は成功した。
最初の全体テストでは補完と操作数の不整合を検出し、生成コマンドと
索引の更新で修正した。修正後の通常複製で操作全体・Git 証拠・移送の
テストが成功した。リンクした作業ツリーではビルド版の識別が得られない
既存の試験条件があるため、通常複製で同じ候補を照合した。
最初の実装差分の独立レビューには重大な指摘がなかった。
この記録の時点では並行性検査と最終の継続的インテグレーションは未確定であり、
後からの結果を過去の成功へ読み替えない。

外部利用環境の保存状態は、この開発環境では読み出していない。
候補の配布・適用、正式な後続契約、必要な検証・独立レビュー、
元の依頼の受入れ条件の確認は未完了であり、Issue #248 の完了条件として残す。
この差分の統合は、外部実行、追加課金、通常環境のスキル更新、
タグ・リリース作成の承認にはならない。

### 統合時の確定結果

最終候補 `5411919148a934b447fde4d5f1349318748eb570` は通常複製で
`go test ./...` と操作入口全体の `go test -race ./cmd/taxion` に合格した。
補完が未対応の `assess-input --preview` を案内する独立レビュー指摘も修正し、
限定した再レビューで解消を確認した。同候補の継続的インテグレーションは、
全体の並行性検査と6環境の配布試験を含む14件すべてが成功した。
[実行記録](https://github.com/hiono/taxion/actions/runs/41450852169)と
[配布試験](https://github.com/hiono/taxion/actions/runs/41450852153)で照合する。
PR #250 は `bd8c676af77137b5ddd7ad181da20c8bd149b645` として統合した。

初期候補 `908aa33` の並行性検査は、操作入口の既知の補完・操作数の2件が失敗し、
それ以外の変更パッケージは合格した。この実行全体を成功とは記録しない。
試験版の公開はその後の明示承認による別工程であり、外部利用環境での
元の依頼の受入れは、引き続き Issue #248 の未達条件である。

### 役割間の共通受入れへの接続

通常のレビュー発行は、任意の packet lifecycle を使わない場合、
元の契約の bundle があっても派生した担当の bundle を生成していなかった。
持込み入力と packet lifecycle はそれぞれ別の生成処理を持っていた。
これは開発側の構造確認であり、外部側の拒否の完全な原因を再現したとの主張ではない。

派生認可の判定を Dispatch 発行直前の共通経路へ移し、持込み入力専用の
bundle 生成を削除した。通常の明示した設定対象で、初回レビュー、修正、
再レビュー、独立レビュー完了へ進む回帰試験を追加した。
元の提案確認を持つ bundle-required の契約を使い、親の認可が追記後も
変わらないこと、対象に含まれない担当には仕事や認可を発行しないことを確認する。
任意の packet lifecycle が修正を暗黙委譲しない既存試験と、
設定変更時の入口拒否、持込み入力の既存試験も維持する。

対象試験は成功した。全体試験、独立レビュー、CI、統合、配布と外部受入れは、
この追記の時点では未完了であり、先の RC15 の成功記録で代用しない。

初期候補 `f90940d` は通常複製で全体試験に合格し、対象の並行性検査、
ビルド、静的検査、独立レビューを実施した。その後、判定を副作用のない
`assessFlowRole` と認可の追記へ分離し、根拠ハッシュに Flow の上限を含めた。
対象・役割・範囲・元の認可が変わる場合の拒否と、同じ根拠の決定性・不変性を追加確認した。
実 CLI の `ready` レシピも bundle-required で実行し、任意の役割委譲なしで
契約確認、計画、指示発行、実コマンド検証、同一対象の独立レビュー完了へ接続する。

| 受入れ例 | 確認する既存・追加試験 |
| --- | --- |
| 人の文書を実装担当なしでレビュー | `TestSuppliedReviewIntakeCarriesHumanArtifactsAndFailureDiagnosisWithoutCoder` の `document-review-only` |
| 失敗・変更なしの診断を製品合格にしない | 同試験の `failed-tests` / `no-edited-artifact` と `TestDiagnosticObservationDoesNotWaiveIdentityOrUnknownExecution` |
| 計画・引用の再利用と失効 | `TestPlannerDraftRequiresExplicitApprovalAndPublishesWithoutRewritingResults`、`TestPlannerDraftRejectsStaleForgedAndNonExecutableAdvice`、`TestPlanObservationIdentifiesReturnedRecordsWithoutChangingWorkspace` |
| 助言の目的・メタデータと実行の分離 | `TestAdviceAdmissionSeparatesPurposeMetadataAndExecution`、既存の質問採用・非実行判断の試験 |
| 記録なし・不正・証拠不足の区別 | `TestInputObservationAcceptsUnchangedSuppliedInputButNotMalformedMetadata`、`TestInputObservationBlocksUnverifiedReceiptAndForbiddenCapture` |
| 通常の役割遷移と明示した設定対象 | `TestBundleRoleAdmissionUsesExplicitCoverageWithoutPacketLifecycle`、bundle-required の `TestReadyRecipeDrivesApprovedSameTreeReviewWithoutPlanner` |
| 停止・保存・移送と改変拒否 | 入力観測の後続契約試験、既存の receipt・packet・portable 試験 |

これらは実 Git と模擬実行基盤による開発側の証拠である。外部側では RC15 の
レビュー発行に失敗したとの報告があり、その受入れは不合格のまま保持する。
同じ環境へ再試行や資料の再提出を要求せず、今回の内部試験や統合で
外部側の成功へ読み替えない。外部製品の受入れと Issue #248 の設計・実装の
受入れは別であり、未承認の外部操作を本 Issue に新しい完了条件として追加しない。
本 Issue は記載した実 CLI の代表例、影響する経路、独立レビュー、
移行・復旧記録まで照合したうえで判定する。

### 共通判定を接続した候補の確定結果

実装候補: `c674a9fa565ad5a89b2702d4dd79ec3bba63a462`。
Go の確認には repository の `1.26.0` toolchain を使用した。

| 確認 | 結果 |
| --- | --- |
| 通常複製の `go test ./...` | 全体成功。操作入口 44.761 秒、Controller 290.725 秒 |
| 変更した役割・助言・実 CLI の対象試験 | 成功。引用、目的、設定、認可・対象の改変と副作用なしを含む |
| 同じ対象の `go test -race` | 成功。操作入口 9.085 秒、Controller 45.558 秒 |
| `gofmt`、`git diff --check`、`go vet ./...`、`go build ./...` | 成功 |
| 文書・リンク・公開方針 | 成功。保護識別子の照合を含む120ファイル |
| `npm --prefix web test` | 12件成功 |
| 独立実装レビュー | `c674a9f` までの差分に重大な不具合の指摘なし。公開用 PR 本文も正確な内容と方針に対して承認 |

全体試験は worktree 固有の版識別に影響されない通常複製で、上記の実装 commit を
照合して実行した。後続の文書記録だけの変更を、この実装試験へ混同しない。
統合の判定には [PR #252](https://github.com/hiono/taxion/pull/252) の
正確な最終候補の CI と独立レビューを使い、途中の成功や配布済み RC15 を使わない。

### RC16 の配布対象と外部実験

PR #252 は `646d68157697dcac6b59138a18f7bf5d36cdd7a9` として統合した。
最終候補 `047ea0f` の CI は14件すべて成功した。RC16 はこの役割・助言の
共通受入れを含む試験版であり、RC15 の導入や外部試験の成功を流用しない。
管理スキルの版は 1.3.21 を維持する。

外部実験では旧結果と未コミット成果を保全し、保存した検証成果を再利用して、
必要な独立レビューの発行と受入れへ進めるかを確認する。元 Task の
`already-authorized` と、派生担当の認可・明示した設定対象を区別する。
停止済み Flow の自動修復や、設定対象の黙示的な拡張を期待しない。
正式な代替経路が拒否された場合は照合と拒否理由を保存し、
同じ承認・観測・モデル実行を反復しない。

この時点では RC16 の公開処理と外部実験は未完了である。配布の照合結果は
タグに対応する公開処理と配布情報で確認し、外部製品の受入れとは区別する。

### 正式版への受入れ照合

2026-11-03 の追補。過去の未完了・失敗記録は、その時点の結果として保持する。
[PR #253](https://github.com/hiono/taxion/pull/253) を
`01ae242badd2a5a9b361c5ce74f971bcd335a73f` として統合し、
[RC16](https://github.com/hiono/taxion/releases/tag/v0.7.21-rc.16) を公開した。
[正式な配布処理](https://github.com/hiono/taxion/actions/runs/41562253425)は
15件すべて成功した。6環境の直接実行と公開 `mise` 導入を含む。
公開9ファイルのハッシュ、6配布物の実行ファイル・版・リビジョン・配布経路も照合した。

外部側の操作者から、RC16 で正式な認可照合、Reviewer の発行・実行・結果受理が
成功したとの報告を受けた。成功済みの Coder 成果を再利用し、Coder は再起動していない。
報告された結果は Flow 完了、独立レビューの承認・指摘なし、
当該レビューの受入れ5項目合格、作業ツリー変更なし、結果受理の6検証成功である。
その後、同じレビュー済み tree の統合・送信、統合前後の CI 成功、
元の作業の受入れ7項目確認と終了が報告された。外部製品のリリースは未実施である。
これは外部操作者の報告であり、開発側による外部台帳・非公開証拠の直接検査ではない。

一方、旧契約には現在必須の文書整合 criterion がなく、
`workflow evaluate --require-quality` と公開ゲートは未達と報告された。
個別の文書確認や上記の成功報告を、欠けた契約条件や品質ゲート合格へ読み替えない。
旧承認の遡及変更、旧結果の書換え、停止済み実行の再送は行っていない。
同じ公開版のヘルプと公式文書を照合し、完了済みレビューへ新条件を付け足す
正式操作は確認できなかった。品質ゲートの合格が必要な場合は、
[復旧ガイド](../userguide/failure-recovery.md#gate-が-blocked-を返す)の新契約・独立レビューを使う。
追加モデルや外部操作をこの記録のために開始していない。

| #248 の受入れ条件 | 根拠と確認範囲 |
| --- | --- |
| 原典、現状、候補比較、採否理由 | ADR-0066 の責務分離と一次資料・代替案。参照した Jev と性能未測定の範囲を区別 |
| 実際の入口・実行への接続 | PR #250 / #252。入力観測、提案・確認、Dispatch 直前の役割認可、計画・質問・非実行助言の採用 |
| 代表例と影響する経路 | 上記の試験対応表、実 CLI の bundle-required レシピ、全体・対象並行性検査、独立実装レビュー、最終候補 CI14件成功 |
| 不正・古い記録、証拠・権限不足への拒否 | 改変、引用、目的・設定の不一致、期限、所有権・実行不明を含む回帰。診断完了を製品合格・公開許可にしない |
| 前工程の重複回避と費用の境界 | 外部側で保存済み成功 Coder を再起動せず独立レビューへ到達。判定用の有料モデルは追加せず、改善率は未測定 |
| 互換性、移行、停止・復旧、制約 | ADR-0066、CLI・利用者・開発者・復旧ガイド。旧履歴を保持し、未対応形式や旧契約の品質ゲート未達を明記 |
| 外部側の利用確認と元の作業の受入れ | 上記の操作者報告。レビューと元の作業の完了を確認範囲とし、Taxion の品質ゲート合格や全役割の実モデル検証とは数えない |

正式版の範囲は #201 と #248 とする。#221 の全完了経路の品質証拠、
#90 の行動・費用改善、その他の継続改善は未達条件を保ったまま追跡する。
今回の共通受入れに必要な代表ケースと、任意形式・全役割を自動解釈する構想を区別する。
正式版の公開完了は、別途そのタグの全配布処理と公開物を照合して判断する。
