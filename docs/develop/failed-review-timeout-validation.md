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
cleanupの追加失敗で元工程を失う問題は、CLIの独立した回帰試験で確認する。
結果schemaの状態値と、不一致フィールド名だけを示す検証も対象に含める。

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
