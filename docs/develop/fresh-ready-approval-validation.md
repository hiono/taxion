# Fresh READY ApprovalBundle の検証記録

以前のローカル検証対象は `92989de9499a63c5bf794d373715ca00ad944c3a`。
PR #142 の exact-HEAD 検証対象は
`e31c0aeec3044ebeef75755a465c93f2807ecef1`。
[Issue #141](https://github.com/hiono/taxion/issues/141) と
[PR #142](https://github.com/hiono/taxion/pull/142) で管理する。
元候補の保存領域は利用できず、確定した再現条件から実装した。

## 検証結果

次の最初の表は旧 commit `92989de9499a63c5bf794d373715ca00ad944c3a` に対する
過去のローカル検証記録であり、PR #142 の exact-HEAD 検証結果ではない。

| 検証 | 結果 |
| --- | --- |
| `go test -count=1 -json ./...` | 18 packages、1,618 PASS、26 SKIP、FAIL なし |
| `go test -count=1 -race -timeout 30m -json ./...` | 18 packages、1,618 PASS、26 SKIP、FAIL なし |
| `go vet ./...` / `go build ./...` | PASS |
| `npm --prefix web test` | 12 tests PASS |
| fresh READY 正常系・拒否系・冪等性・拒否時不変性・再起動 | PASS |
| 別 process の同時承認を100回反復 | PASS |
| gofmt・diff check・docs・links・secret-pattern scan | PASS |
| [過去の CI run 39773736064](https://github.com/hiono/taxion/actions/runs/39773736064) | 旧 commit に対する build / core-purity / race / web / hygiene の全5 jobs PASS |

続く結果は exact-HEAD `e31c0aeec3044ebeef75755a465c93f2807ecef1` に対する検証。

| 検証 | 結果 |
| --- | --- |
| `go test -count=1 -json ./...` / `go test -count=1 -race -timeout 30m -json ./...` | PASS（`.git/fresh-ready-release/exact-head/tests.json`、`race.json`） |
| `go vet ./...` / `go build ./...` | PASS（`vet.log`、`build.log`） |
| web tests | PASS（`web.log`、12 tests） |
| 別 process の同時承認反復 | PASS（`concurrency.log`） |
| [CI run 39774787171](https://github.com/hiono/taxion/actions/runs/39774787171) | exact head `e31c0aeec3044ebeef75755a465c93f2807ecef1`、build / core-purity / race / web / hygiene の全5 jobs PASS |

exact-HEAD の記録は `exact-head/head` と CI の `headSha` が一致する commit に限る。
この文書を含む後続の evidence-only documentation 変更は上記 CI の対象ではないため、
変更後の commit に対する新しい CI と再レビューが必要。本記録は将来の commit の
検証結果、レビュー承認、merge または release を主張しない。

旧 commit のローカル JSON ログの SHA-256 は、full test が
`5104d0c4baa331ffdbcf2256d12ed8de63a08ee9f9466bec713525edf61db9a4`、
full race が
`7357e8872559d930e6a831ffbdf87f5154f4839532b406ed75f94ae6212fe71b`。

旧 CI の full race は4分30秒で完走した。過去の既定10分 timeout は PASS と扱わず、
回収できなかった元候補の証跡を旧 commit の実測値として転用していない。旧 CI では
全 package に race detector を適用し、package timeout を30分、job 上限を35分とした。
test、assertion、package の削除はない。SKIP は旧ローカル記録の既存 opt-in integration
tests で、full test と full race の対象 package を除外していない。新しいモデル runtime
や有料 endpoint は起動していない。会話自体の AI credits 残量を測定した記録ではない。

## 同時承認の不具合と修正

[旧 CI run 39772528796](https://github.com/hiono/taxion/actions/runs/39772528796) の build job では、同時承認の両 process が失敗した。子 process の診断を保持して反復した結果、両方の deferred transaction が `SQLITE_BUSY` になった。fixture、assertion、race detector を緩めず、確認済み bundle の生成時だけ immediate transaction と最大5秒の lock 待機を適用した。

待機後も transaction 内で workspace と effective configuration を確認する。先行 mutation があれば古い確認 token を拒否し、Task、configuration approval、Flow、Dispatch、deadline、budget は変更しない。lock 待機は承認の更新でも、自動 retry でもない。

## 互換性と公開条件

正式 v0.7.5 CLI で作成した合成 READY fixture でも bundle だけの追記、preview・再送時の DB bytes 不変、Contract Gate PASS、旧 CLI による同一 readback を確認した。設定が一致しない別 fixture は拒否した。合成 lifecycle 以外の operation や、既存 resume の許可条件は変更していない。

本記録は review、merge、release の完了を意味しない。正式 review は未完了で、利用可能な認証 identity は PR author のみだった。review を自己承認で代用せず、release gate を維持する。将来の一意な patch release では rollbackVersion を v0.7.7 とし、既存 tag と artifact を変更しない。
