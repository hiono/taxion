# 停止前の追加承認と同じ実行の継続の検証

対象は `0.7.21-rc.13` の [ADR-0058](adr/0058-recover-unstarted-artifact-review.md)。
元期限を変更せず、追加時間の承認だけを記録した実行は、延長成功とみなさない。
この記録はローカル回帰の結果であり、提供元の実モデルや利用者の製品環境での成功とは分ける。

## 旧期限を越えた実行を確認した

`TestLiveReviewerExtensionKeepsSessionReceiptAndUsageAcrossOriginalDeadline` は実際の
Controller runner、SQLite、Git と検証コマンドを使い、runtime だけを test double に置き換える。
旧期限を10秒に短縮し、時間だけの3時間延長と、時間・費用を増やす延長を別々に実行した。
3時間待った試験ではなく、承認した新期限の保存と旧期限後の同じ実行を確認する試験である。

| 確認 | 結果 |
|---|---|
| 時間だけの追加承認 | native 上限を据え置き、新しい有効期限を適用 |
| 時間・費用の一括承認 | preview と一致する両方の新上限を適用 |
| 承認待ちの間の usage 更新 | 累計を保持し、承認を失効させない |
| 旧期限を越えた Reviewer | 同じ session と一つの runtime factory で `APPROVE`、Flow は `COMPLETED`、停止呼出しなし |
| 検証 | 入力検証は一度だけ、結果受理検証は実施 |
| 元期限と追記履歴 | 元期限は不変。削除・書換え・二重適用を拒否 |

次の race 検証は成功した。

```sh
go test -race ./internal/runtime/controller \
  -run TestLiveReviewerExtensionKeepsSessionReceiptAndUsageAcrossOriginalDeadline -count=1
```

`TestLiveExtensionStillExpiresAtNewHardDeadline` は新しい絶対期限の発火、
`TestLiveExtensionRejectsDriftExpiredStageAndKeepsCallerDeadline` は契約差分と期限後の拒否、
独立した呼出し元の期限を確認する。CLI の preview・適用と無変更の拒否、
分割された判断要求の出力、費用と時間の見積りは対応する CLI・Controller テストで扱う。

## 実 CLI と保存履歴の照合

実 Copilot CLI `1.0.105` の Auto Reviewer を、隔離した保存先と作業場所で実行した。
入力検証の成功後にレビューを行い、SDK 履歴で完成回答と対応する turn 終了を確認してから
Controller の process を停止した。同じ回答を `workflow recover` で採用し、
host 検証、二段階の確定 receipt、管理 worktree 解放、正式結果保存、runner claim 回収を完了した。
モデルへの再送、入力検証や完了済みの結果検証の繰返し、修正回数の追加は行っていない。

保存した回答の `REQUEST_CHANGES` も正式結果として保持できることを確認した。
これは担当レビューの結果受理であり、製品品質の承認とは区別する。
以下の実観測と回帰試験の詳細な実行識別情報は、非公開の監査記録に保持する。

終了済み session の観測には、正式 SDK の `GetEvents` を使う。
内容が空の assistant message と tool 呼出しも保存され、per-call usage は保存されないことを確認した。
そのため、回答の message・turn・session・作業場所を結合し、空 message も照合対象として保持する。
usage は既存 receipt と正常 shutdown のモデル別集計を使い、同単位の不足分だけを補う。
履歴の照合はローカル記録の対応確認であり、提供元の署名とは扱わない。

この観測は、保存した単一 Reviewer の graph export を指定して再現できる。
新しい session・モデル指示・abort は発行しない。

```sh
TAXION_TEST_SAVED_REVIEW_GRAPH=/absolute/path/to/graph.json \
  go test ./internal/runtime/copilot -run '^TestSavedTurnNativeObservation$' -count=1 -v
```

`TestSavedReviewFinalizationRejoinsNormalValidationAfterRunnerLoss` は実 SQLite・Git と
host コマンドを使い、native 回答のみ、検証済み、runtime 終了済み、worktree 解放済みの
各中断点から正式結果と claim 回収までを検査する。元期限後の観測も含み、
入力検証や完了済みの host 検証を繰り返さず、元期限・修正回数・消費を保持する。
runtime は test double なので、この試験だけを実 CLI の受入れ証拠にはしない。

保存検証後の利用量増加は、検証済み段階の変更不能な prefix と
終了済み段階の最終利用量を分けて検査する。回帰試験では、利用量増加と再中断を組み合わせ、
コマンドを繰り返さず確定できることと、消費の減少・書換え・終了後の追記拒否を確認する。
結果不明の host コマンドを再実行しないことと、実 SDK の観測で完成回答と中断状態を
区別できることも検査する。

## 配布と現場の受入れを分ける

正式公開の判定は同じ対象の CI と[配布手順](binary-release.md)に従う。
公開 asset の照合、固定版の導入、管理スキルの更新を確認しても、
稼働している旧バイナリの deadline context が新機能へ置き換わるわけではない。
旧実行は停止証拠と成果を保全して正式復旧し、新機能の適用確認を省略しない。
