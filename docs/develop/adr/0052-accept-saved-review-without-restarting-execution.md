# ADR-0052: 停止後の保存済み独立レビューを実行再開なしで受理する

## 状態

採用。対象は #185 の停止理由消失で停止した、レビュー専用のツリースナップショット Flow に限定する。

## 背景

`v0.7.19` の停止処理では、Git エラー末尾の空白を引継ぎ保留の入力検証が拒否し、最初の停止理由を二次エラーに置き換える場合がある。独立レビューの成功結果が保存済みでも、通常の進行処理は停止済み Flow を再開しない。再レビューは同じ証拠を再取得する費用を生み、元 Coder のブランチ付け替えや DB の直接変更は監査記録との不一致を作る。

## 決定

同じ Flow に受理専用操作 `workflow accept-saved-review` を追加する。新しい Flow・Dispatch・モデル session を作らず、保存済み検証 command も再実行しない。現在の Task の契約、独立 Reviewer の成功結果・receipt、承認と設定、役割 packet、Memory、元 Coder と現在 Task のコミット・tree・snapshotRef・index・実ファイルを再検証する。元 Coder のブランチ名は過去の実行要件と区別し、detached HEAD を許すが、成果の不一致は許さない。現在 Task の承認済みブランチは維持する。

読取り専用 preview の hash は、保存状態全体、設定、承認の actor・source・reason、期限後受理の選択、表示対象を対応付ける。適用時も同じ検査を transaction 内で行い、hash 不一致や並行実行があれば状態を変えず拒否する。外部の Git ファイルを SQLite transaction と同時に固定できるわけではないため、利用者は受理中に対象 worktree を変更しない。

実行期限は延長しない。Controller の成功 receipt と Git 証拠が元の期限前に保存された場合だけ、`allowExpired:true` の明示承認により期限後の受理を許す。通常の advance・resume の期限検査や停止状態は変更しない。

成功時は実装 Task を `APPROVED` → `COMPLETED`、Flow を `COMPLETED` にする。Reviewer Task は `BLOCKED / Dispatch ended: SUCCEEDED` の履歴を維持する。その Task の再実行も、新たな成果の生成も行っていないためである。元 HOLD は解除しない。元の Flow 全体と actor・source・reason、受理対象・preview の hash、承認した期限例外を追記の `reviewAcceptances` に残す。元の理由は既に失われており、記録の `originalReasonUnavailable:true` によって、推定原因と保存事実を区別する。

## 制約と互換性

対象はレビュー専用 snapshot Flow の既知の二次エラーに限定する。別の停止原因、修正要求、未解決の介入、実行中の処理、欠落・不一致の証拠には適用しない。旧結果を成功とみなす補完、検証結果の捏造、予算・権限の追加、公開承認を行わない。actor・source は出典の記録であり、新しい認証方式ではない。

既存記録は変更せず、新しい受理時だけ監査記録を追加する。受理記録を作成した DB は旧版で更新しない。rollback は現 DB と成果を保存したうえで、受理前のバックアップを別配置へ復元する。詳細な操作は [CLI リファレンス](../../userguide/cli-reference.md#停止後の保存済みレビュー結果の受理)を参照する。

## 検証

`go test ./internal/runtime/controller -run TestAcceptSavedReview -count=1` で、実際の Controller が保存した成功結果から、元 Coder を detached HEAD にした停止状態を構成して検証した。preview 前後の workspace・Memory・Git object・ref・index・実ファイルのバイト列、受理後の同一 Flow・Task の遷移、Reviewer Task・HOLD・承認・receipt・予算の保持、追記記録の変更と削除の拒否を確認する。期限の直前・境界・超過、証拠や成果の不一致、複数接続からの同時受理も対象に含む。

`TestAcceptanceAuditCheckedAcrossStorageFormats` は、正しい保存ハッシュを持つが対応先のない受理記録を使い、正規化形式・旧 JSON 形式の再読取りと初期保存で拒否することを確認する。独立レビューで見つかった正規化形式の照合漏れを、この回帰検査で再現して修正した。実環境の非公開台帳や worktree に対する復旧は行っていない。
