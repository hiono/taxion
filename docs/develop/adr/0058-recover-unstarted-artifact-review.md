# 58. 入力検証に合格した未開始レビューを後続契約へ引き継ぐ

Date: 2026-10-28

## Status

Accepted。Issue #213。

## Context

持込み成果物の入力検証に合格した後、レビュー用 worktree の準備に失敗すると、
Flow は終端停止し、モデル未起動の Reviewer 指示だけが残る。
従来の未開始指示の整合回復は Coder 専用であり、完了した検証を保持して
独立レビューへ進む正式操作がなかった。事前点検でレビュー用の場所を作った場合も該当する。

## Decision

`workflow reconcile-stopped` の対象に、最初の持込み成果物 Reviewer を追加する。
保存状態が `STOPPED / SAFETY_STOP`、引継ぎ保留、runner なし、
入力検証 `PASSED`、指示 `ISSUED` であることを要求する。
応答・開始・session・結果・利用量・runtime receipt・管理済みレビュー worktree
がある指示には適用しない。他の稼働実行も拒否する。
実行停止の確認証拠、配送不明の受容、現在の preview hash は従来どおり必要である。
Controller が承認済み判断と観測から入力を準備し、内部手続きだけを理由に同意を増やさない。

取消は旧指示と停止状態を監査へ保持し、Reviewer Task だけを停止する。
親 Task はレビュー待ちのまま、新しい正式な後続契約へ移す。
後続の intake と confirm に同じ `reconciliationHash`、confirm に
取消後の正確な feedback target と `replacesFlow` を渡す。
候補、元 worktree、branch、目的、範囲、権限、受入れ条件、検証コマンド、
必要証拠、仕様、利用量方針を変更しない。

後続の plan と start は元の `artifactSource` と
`reuseArtifactValidation:true` を明示する。
対象と実効設定を再照合し、既存の合格 receipt を内容・実施時刻・元の実施場所ごと継承する。
`validationSourceFlowId` と `INPUT_VALIDATION_REUSED` 監査が元の実施監査と
整合回復記録を結び付ける。架空の検証開始・完了や Coder 成功は生成しない。
旧 Flow は再開せず、旧停止理由・合格 receipt・結果 hash を変更しない。

新しい `reviewRoot` は Controller が管理する。利用者や事前点検は
実際のレビュー worktree を先に作らない。既存の場所を自動削除・流用しない。
合格した入力検証 stage は再実行しないが、Reviewer 結果受理時の既存の実検証は維持する。
新しい契約・工程計画の承認、実行前検査、独立判断、期限・課金上限は省略しない。

## Compatibility and rollback

新しい `validationSourceFlowId` と監査種別を持つ DB は旧版で操作しない。
適用前の整合した backup を別配置で使う。既存の Coder 整合回復と、
継承を指定しない入力検証の動作は維持する。汎用的な停止解除ではない。

## Validation

実際の場所衝突による停止を再現し、未起動指示の取消、正式な後続確認、
合格 receipt の継承、Coder なしの同一対象独立レビューと完了までを回帰試験で確認する。
検証 stage の実行回数、元の停止と場所の保存、対象 drift、設定・検証契約の変更、
開始・応答・session のある指示の拒否を確認する。

`mise run check` と、Controller・CLI・core・store の対象 race 試験に合格した。
独立した正当性レビューと、公開 Issue・PR・配布文の読者視点レビューも承認済みである。
実際の製品環境での適用は未実施であり、この記録を製品の品質合格とは扱わない。
