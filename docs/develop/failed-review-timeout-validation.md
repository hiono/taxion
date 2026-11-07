# 保存レビュー追記の期限と診断の検証

Issue [#277](https://github.com/hiono/taxion/issues/277) と
[#278](https://github.com/hiono/taxion/issues/278) の修正を、
配布Issue [#279](https://github.com/hiono/taxion/issues/279)、
既存の [M40](https://github.com/hiono/taxion/milestone/39) に結び付ける。
元の運用store、native session、managed snapshotには接続しない。
正式公開の結果は配布IssueとRelease workflow全体で確認する。

## 回帰の確認

`TestFailedReviewSupplementPreservesFailureAndNeverReplaysValidation` は、
元結果・Flow・利用量・検証回数の不変、旧hashとの互換性、期限と既存追記の拒否を確認する。
native-read失敗は読取り段階に到達してから合成peer内で期限切れを起こす。
DB照合を含む全操作に20msを割り当てて特定工程を期待する、raceで不安定な試験は使わない。
実際のController期限切れは、独立したlock競合と公開CLI試験でも確認する。

Store試験は取消済みcontext、保存前競合、mutex待機中の取消とtransaction中の取消を確認する。
commit失敗後にrollbackを確認できない場合を成功や未保存へ置き換えない。
`TestFailedReviewTransactionFaultOutcomes`は合成driver経由で実SQLiteを使い、
実commit後のエラー、書込み後のrollbackエラー、成功commit直後の取消を注入する。
前二者は`UNKNOWN`を返し、実保存状態が異なっても未保存と推定しない。
成功commitはcontextが取消済みになっても`COMMITTED`を保持する。
この三例の通常・race試験とStoreの`go vet`はexit 0となった。
追加の`TestFailedReviewInFlightSQLiteCancellation`は実SQLiteの再帰文を使い、
SQL内の関数が呼ばれて処理開始を確認した後に取消を起こす。
初回読取り、文書内の文字列読取り、保存する文字列の書込みと、実行中の期限切れを対象にする。
3秒のfixture用安全期限による終了を、呼出し元の取消・期限切れの成功とは扱わない。
保存後のworkspace全体も比較し、取消だけで保存結果を推定しない。
cleanupの追加失敗で元工程を失う問題は、CLIの独立した回帰試験で確認する。
結果schemaの状態値と、不一致フィールド名だけを示す検証も対象に含める。

PR #280 の独立レビューでは、未ラップのsetupエラーと時刻拒否にcleanup失敗が
重なると、cleanupだけの診断になる問題が指摘された。
未ラップのエラーも元工程で安全な型へ変換し、deferと通常経路で同じ出力処理を使う。
時刻拒否では既存の期限・採用元・`failedChecks`を保ち、
追加失敗を`cleanupFailed: true`で示す。元エラーは変更せず、秘密値は出力しない。
時刻拒否も元のoperationの`phase`・`persistence`・観測期限・経過時間を保持し、
固定の`cause: timing-rejected`を返す。stderrにも工程・原因・保存結果を出すため、
stdout自体の出力失敗でも保存結果を取り違えない。

修正後に`go test ./cmd/taxion -run TestFailedReview -count=1`、
`go test -race ./cmd/taxion ./internal/runtime/controller -run TestFailedReview -count=1 -timeout 5m`、
変更packageの`go vet`、`git diff --check`がexit 0となった。
これは変更箇所の確認であり、新候補の独立品質合格や正式Releaseの完了ではない。

## 公開CLIの合成確認

候補binaryを明示して次を実行する。

```bash
TAXION_TEST_FAILED_REVIEW_CLI=build \
  go test ./internal/runtime/controller \
  -run '^TestFailedReviewPublicCLISynthetic$' -count=1 -v -timeout 2m
```

`build` は試験用の一時ディレクトリに候補CLIをビルドする。
試験は一時Git repository、合成store、明示したdevelopment policy、読取り専用の
ローカルSDK protocol peerを使い、実際のCLI processを起動する。
preview成功後に極小の観測期限でapplyを拒否し、store全体の不変を照合する。
同一入力・expectedHashで十分な期限のapplyが一度だけ追記すること、
元結果・Flow・利用量の不変と二重追記の拒否を確認する。
peerはsession作成・送信・実行再開のRPCを受け付けず、履歴読取りと終了だけを許す。
通常のGo試験ではbinary未指定時にこの実CLI試験をskipするため、
通常試験の成功だけを実CLIの確認済みとは扱わない。
PRの通常・race試験と正式Releaseの検証では、この変数を `build` に設定し、
実CLI試験も実行する。

これらは運用原本の適格性、実サービス受入れや製品品質の証明ではない。
旧Coderの取消結果と未受理Reviewer出力は保持した診断入力であり、合格へ書き換えない。
修正済み成果のController検証と独立Reviewer受理後に、正式配布の手順を進める。

## v0.8.1の公開拒否とv0.8.2の配布条件

PR #280の修正は`cac93438ffc25231cbaee221b42e111aa2da6ebd`へmergeした。
v0.8.1のRelease run `42233764036`は全検証と6環境のbuildを通過したが、
公開文の独立審査記録がv0.8.0のままだったため、draft作成前に拒否された。
v0.8.1のタグは保持し、移動・再利用しない。配布物は公開されていない。

v0.8.2では実装コードを変えず、版番号と公開文の版指定、独立審査記録、
本記録だけを更新する。公開するタイトル・本文・profile・参照先と判定時刻を
独立審査のhashへ結び付け、審査記録を含む最終treeの品質受理後にタグを作る。
タグ作成前に、公開文の厳密なhash照合と、利用可能な実policyによる検証を確認する。
実policyを使えない環境では検証済みと扱わず、未確認の段階を記録する。
公開前のUpgrade案内への修正要求は、正式公開と公開後検証の完了を条件に加えて対応した。
修正後の独立審査は`publication-082-corrected-text-review-flow-dispatch-0`で正式に
`APPROVE`となり、session `2ead3353-c5e8-4469-a992-5f137d0a91d3`が
本文hash `88014cfea3f6ec4b7aa30d0ce671a356467f2088db7242ec37035589d05f9dcc`を承認した。
この審査は公開文のみを対象とし、保護識別子policy、新しい最終treeの品質、
CIや公開処理の合格を代替しない。
この後の日本語表記の調整には、別の厳密な公開文審査を行い、上記承認を流用しない。
実profileによる決定的な表記検査は、保護識別子policyが必要な段階まで通過した。
手元にないpolicyを仮の値で置き換えず、実policyを使うCIと公開処理の検査を残す。
最終表記は`publication-082-presentation-review-flow-dispatch-0`で正式に`APPROVE`となった。
session `a88b8fee-650b-4c65-b1a9-968fda68ad42`が承認した本文hashは
`aa34e3c8d5666d73f52947a9b416b5f41e6cc5465f037a07bd70d414136667ac`である。
配布用の審査記録には、この本文と判定時刻に一致する値だけを保存する。
実Go実装の公開文検査も審査記録・表記の照合を通過し、
`publication requires TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256`で停止した。
これは手元の保護識別子検査と参照先API照合が未実施という記録であり、公開成功ではない。

### 最終成果物審査の未解決指摘

最終候補`0ab5b4bd2b081a91c16a72f748d9b5318df08665`、
tree `524dd5e0bf5b997f358ac6776aa2c30a0137d302`は、全14件のPR検査と
6コマンドのController検証を通過した。しかし、
`publication-082-final-quality-flow-dispatch-0`の独立審査は
`REQUEST_CHANGES`となった。sessionは`a565004c-242f-449f-a0ba-639d170335a6`である。
正式な結果保存の成功は、品質承認を意味しない。

未解決はMEDIUM 1件である。`internal/store/sqlite.go`では
`BeginTx`にcontextを渡しているが、読み込みの`QueryRow`と追記の`Exec`などには
渡していない。既に実行中のSQLite文に観測期限が伝わらず、その終了を待つ可能性がある。
現在の回帰検証は、処理前の取消、更新中の取消、確定・取り消し結果を検証するが、
実行中のSQLite文が取消に応答することまでは証明していない。
これは今回の審査で確認した不足であり、元の運用機のタイムアウト原因を確定するものではない。

文の実行までcontextを伝え、実行中の文の取消を検証する修正が必要である。
取消だけを根拠に`NOT_WRITTEN`と判定せず、確定・取り消しの証明に従う分類を保つ。
実装修正と対応する回帰検証、期限の適用範囲の文書をそろえて、改めて独立審査する。
この審査時点の追加承認は公開準備4ファイルに限定されており、実装修正を含まなかった。
その範囲を勝手に広げず、PR #281と正式公開を保留した。
旧審査結果と失敗したv0.8.1タグは変更せず、運用原本への接続・再適用・モデル再送も行わない。

### SQLite文へのcontext伝播の修正

2026-11-07の追加承認により、上の実装修正と対応する検証・文書化を再開した。
これは以前の`REQUEST_CHANGES`の変更ではなく、別の候補で不足に対応する作業である。
共通のtransaction用アダプターから、読取り・文書保存・文字列プール・更新文の
`QueryRowContext`・`QueryContext`・`ExecContext`へ同じcontextを渡す。
既存のcontextを指定しない入口と、保存結果の分類規則は保つ。
context引数のない`Commit`・`Rollback`やメモリ上の処理を強制中断する保証は追加しない。
このコード上の不備と合成再現は、元の運用障害の発生工程や原因を特定した証拠ではない。
同じ回帰試験だけを未修正の`f553fa95db6872baaeb3dfb0cd0f678d26e35f83`へ適用すると、
4ケースともfixture用安全期限まで終了できず失敗した。
修正後は全Store race試験とStoreの`go vet`、`git diff --check`がexit 0となった。
最終候補の独立審査・CI・正式公開検証は、改めて完了を確認する。
新しいCIと正式Release全体、公開前後それぞれ6環境のnative確認、
配布byte・checksum・provenanceの照合が完了するまでは、配布完了と扱わない。
