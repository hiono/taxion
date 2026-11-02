# イベント観測の検証

## 対象と再現条件

RC14 の観測経路を、モデルも現場の台帳も使わずに計測した。
Linux amd64、Go 1.27.2、Intel Core i5-1345U で、各履歴件数について
同じ SQLite 保存状態を完全取得する方式と、変更番号を照合する方式を 100 回ずつ比較した。
各履歴は Task、Dispatch、約 3.9 KB の出力 receipt を一件ずつ含む。
計測は初回の検証済み状態取得後から行う。OS と SQLite のキャッシュが温まった条件であり、
低速ストレージの試験ではない。

```sh
go test ./internal/store -run '^$' -bench '^BenchmarkWorkspaceObservation$' -benchtime=100x -count=1 -cpuprofile=observation.pprof
go tool pprof -top observation.pprof
go test ./internal/runtime/controller -run TestProgressMonitorPersistsOnlyWhenSelected -v -count=1
```

## 変更のない状態の取得コスト

| 履歴件数 | 全体取得 / 回 | 変更番号照合 / 回 | 全体取得の割当量 / 回 | 変更番号照合の割当量 / 回 |
|---|---:|---:|---:|---:|
| 0 | 0.454 ms | 0.0134 ms | 428 KB | 1.25 KB |
| 100 | 10.02 ms | 0.0114 ms | 20.99 MB | 1.34 KB |
| 1000 | 84.76 ms | 0.0138 ms | 210.89 MB | 1.19 KB |

`/proc/self/io` の `rchar` は両方式とも約 17〜19 bytes/回、
`syscr` は約 1.02〜1.25 回/回、`read_bytes` は 0 だった。
この条件では DB の物理読取りが増えず、全体展開・解析とメモリ割当の CPU 負荷が増える。
これらはプロセスの読取り syscall 計測であり、SQL 文の実行数ではない。
CPU profile は 14.19 秒の壁時計時間に対して 22.70 CPU 秒を採取し、
メモリ初期化・移動、JSON 文字列解析、GC の走査が上位を占めた。
profile は fixture 作成と全比較を含み、個別の receipt 確定時間を表さない。

## 通常受信と正式保存

`TestProgressMonitorPersistsOnlyWhenSelected` は過去の receipt 100 件を保持した状態で、
稼働中の確認 300 回、小さい回答断片 300 件、実行観測、正式取消を処理する。
進捗監視の有効・無効を比較し、旧 receipt と全断片を失わず、
確認処理だけでは保存状態が変わらないことを検証する。
300 回の確認と前後の状態照合は、それぞれ約 359 ms、362 ms だった。
取消を成功結果へ置き換える試験ではない。

通常の実装・レビュー・修正後レビューの既存回帰にも、各ターン 300 件の断片を追加した。
保存回答の確定、停止、重複、利用量、期限変更は既存の検証条件を保つ。
正式 receipt は従来どおり取引内で保存し、その処理量を定期観測の数値と混同しない。

## 未開始 Reviewer の期限停止と復旧

合成した持込み成果物 Flow を実 CLI で入力検証し、Reviewer を未開始のまま
8 秒の保存期限まで待った。`workflow advance` はモデルを起動せず、
`BUDGET_STOP` と合格済み入力 receipt を保存した。
修正前候補では、その後の期限再契約 preview が適格性の不一致で拒否された。
従来の期限再契約試験が扱っていたのは、先に `SAFETY_STOP` になってから期限が過ぎた場合であり、
期限そのものによる停止を検証していなかった。

未開始の検証済み成果物 Reviewer の停止適格性を共通判定へそろえ、
Controller の整合検査と保存監査も同じ判定を使う。
通常の初回 Coder の `SAFETY_STOP` 条件は変えない。
`TestReviewRecoveryRecontractsUnstartedReviewerAfterDeadlineStop` は、
正式な期限停止から一括 preview・適用・同じ候補の後続レビューまでを検証する。
旧停止・不明配送・候補・入力 receipt と先行消費を保持し、
入力検証コマンドを再実行せず、結果受理検証だけを実施することを確認する。
新期限、対象、予算、静止確認、承認 hash が変わった要求は引き続き拒否する。

## 適用範囲

受信ループの定期確認が全体更新を行い、複数の期限監視が同じ全体取得と mutex を
繰り返す増幅は、コードと合成計測で確認した。`output_delta` ごとの
全履歴保存を確認したわけではない。断片は従来から正式 receipt の保存対象ではない。

特定現場で約 90 分を要した実行について、関数別 profile は取得していない。
その時間全体をこの一因だけで説明したり、別マシンでの改善を実証済みとしたりはしない。
初回取得・正式な状態変更・提供元通信の時間保証も行わない。
正式配布と各環境への導入検証は公開 workflow、現場適用はその環境の観測を根拠に判定する。
