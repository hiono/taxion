# 通常 Reviewer の引継ぎと未開始取消の確認記録

## 原因と再現範囲

比較元は公開済み `v0.7.22` の `915671ead330d790a17cdf0962875d6b6ca0f001`。
運用機で観測された旧版の失敗と、このソースに対する合成再現は区別する。
運用機の保存証拠原本は取得しておらず、製品コード・本番 store・実モデルは使用していない。
運用側が報告した CTest、hooks、MATLAB の成功をこちらで再検証済みとは扱わない。

`internal/runtime/controller/normal_review_handoff_test.go` は、一時 Git repository で
持越しの24パスを commit し、4パスを今回編集する。実 Controller の Coder 受理は
元 baseline A、異なる出力 commit B、完全 tree T、全28変更パス、申告4パスを保存する。
修正前は通常 Reviewer の準備が `Reviewer target paths differ from the approved Coder snapshot` で失敗した。
持込み target でしか元 baseline を解決しなかったため、B→T の4パスと A→T の28パスを比較していた。

## 合成回帰テスト

| 条件 | 確認する結果 |
| --- | --- |
| A と B が異なる dirty snapshot、今回の申告は4パス | 受理済み A/B/T/全28パスを保持して Reviewer を準備・受理 |
| commit 済み target | 同じ完全 target と全変更パスを独立レビュー |
| 未開始、準備なし／あり、旧期限が有効／期限切れ | 静止・不確実性・preview hash を承認して Reviewer だけを取消 |
| 同じ T の別 worktree と新期限 | 成功 Coder を継承し、Coder0・Reviewer1・repair0で新 Flow |
| 元の成功 Coder、旧 Flow、receipt、利用量と停止 | 取消後も保持。取消・後続開始は元の4検証を再実行しない |
| 新 Reviewer の結果受理 | 通常の4検証を実行し、品質ゲートを省略しない |
| ACK、実行開始、session、利用量、owner、runtime receipt | 未開始取消を拒否 |
| 不正 manifest、対象パス／契約の変更、source receipt の欠落 | 拒否。取消後の intake と後続 start における receipt 欠落も拒否 |
| 管理 worktree の改変、index、HEAD、余分／ignored ファイル、未記録のパス | 拒否し、保存状態とファイルを保全 |
| 期限切れ、承認した期限の変更、取消 audit の欠落 | 後続を拒否し、旧 Flow を復活させない |
| 通常 Flow に対する持込み成果物専用復旧 | 保存状態で確認した拒否条件と未評価のゲートを分ける |

既存の持込み成果物の準備済み復旧テストも、登録解除、別 source、欠落 worktree などの拒否を確認する。
不変な旧指示を編集する運用手順は提供しない。新指示で差分出典を保存し、旧指示は履歴として保持する。

### 決定表とテストの対応

許可・拒否の規範は [ADR-0071 の条件表](adr/0071-preserve-accepted-coder-review-lineage.md#差分対象パスの条件表)を正本とする。
上の結果表と以下の対応は確認範囲であり、条件の全組合せを試験済みという主張ではない。
テスト名は `internal/runtime/controller/normal_review_handoff_test.go` 内を指す。

| 決定表の対応 | 実際の確認範囲 | 回帰テスト |
| --- | --- | --- |
| P2〜P5、E1／E2／E6 | A≠B、dirty snapshot／commit target、全28パスと今回申告4パス、受理から独立レビューまで | `TestNormalReviewerUsesOriginalCoderDiffBaseline` |
| R1〜R4、R8／R10、E3〜E6 | 準備有無×旧期限の有効／期限切れ、取消後の Coder0・Reviewer1・repair0、固定期限と receipt 再照合 | `TestNormalReviewerReconciliationContinuesAcceptedOutputOnly` |
| P7、R5／R6／R8／R9 | ACK・開始・session・利用量・owner・receipt、不正 manifest・対象・契約、不確実性の承認不足で取消拒否 | `TestNormalReviewerReconciliationRejectsUnsafeState` |
| R2／R7、E3 | 準備有無、source・extra・ignored・index・HEAD の drift、未記録の実パスで拒否・保存不変 | `TestNormalReviewerReconciliationRejectsMutableGitDrift` |
| G2、E3 | 出典 metadata がない旧版形式の不変な指示を保持して取消 | `TestNormalReviewerReconcilesLegacyInstructionWithoutLineage` |
| G2／G3 | 通常 Flow を持込み成果物専用復旧へ渡した場合の確認済み／未評価条件 | `TestNormalReviewerCombinedRecoveryExplainsUnsupportedSource` |

A＝B など既存の基本経路は既存の reviewflow／snapshot テストの対象であり、
今回追加した A≠B の専用再現と同一のケースとして数えない。
P6 の「P を R に縮小しない」は全28パス保持の肯定検査と、不正 manifest／target の拒否で確認する。
表のすべての不正表現を一つずつ注入した試験という意味ではない。
E7 の品質・公開条件は既存の品質確認を参照し、この対応表で全完了経路を試験済みとはしない。

[RC 履歴監査](rc-decision-boundary-audit.md)の全16版の対応と、
本表の現行合成回帰を区別する。過去の全 RC を再実行してはいない。
運用機の原本照合・通常レビュー再開、全条件の直積、実サービスでの費用・見逃し率は未確認である。

## 検証と独立レビュー

2026-11-05、修正候補で `go vet ./...`、`go build ./...`、
`go test ./...` は成功した（全 Go 2,459件）。
独立レビューは「取消後の受理 receipt 再照合不足」と「期限切れ通常 Flow の説明が
持込み成果物専用復旧へ誘導する矛盾」を指摘した。両方を修正し、関連137件で成功を確認した。
公開手順の最終 CI と配布物検査の結果は、対応する PR と Release workflow を正本とする。
文書中の件数はその時点の合成検査であり、実サービス受入れや将来の CI 成功を表すものではない。

管理スキルは `1.3.23`。`terminate` の Coder 限定契約、
通常 Reviewer の取消・後続承認の完全 JSON、固定期限と期待状態を help と同じ定義から提供する。
操作手順・停止条件は [CLI reference](../userguide/cli-reference.md#受理済み-coder-出力からのレビュー継続)を参照する。

## 未確認事項と運用上の制限

運用機の原本照合、process 静止、実モデルの再開、本番の受入れは運用側の明示権限で確認する必要がある。
本記録は製品の成功証跡を変更せず、利用量不明をゼロへ変換しない。
旧 store を削除・リセットせず、配布物の hash、出典、公開処理全体の成功と管理スキルを確認してから更新する。
公開が未完了なら修正済み source を運用機へ適用済みとは扱わない。
