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
