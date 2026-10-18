# 41. 不要になった試行を履歴を消さずに退役させる

Date: 2026-10-18

## Status

Accepted

## Context

停止・破棄した試行の Task、Flow、Dispatch が通常の一覧と次の作業計画に残り、
利用者が DB を直接編集せずに整理する操作がなかった。停止は実行を止める操作であり、
今後の作業対象から外す意思の記録ではない。監査記録を保持することと、
通常の作業候補として提示し続けることを分ける必要がある。

元の事例には、受理済みの成果を確認せず古い実装から作業を始めた問題も含まれる。
これは入力源の選定・照合の問題であり、履歴整理だけで解決したとは扱わない。

## Decision

Issue #103 で Task 単位の archive を追加する。元の状態・契約・承認・結果・履歴を
書き換えず、対象 ID、actor、source、reason、確認 hash、時刻を Graph の別記録に残す。
Task または発行済み role Task を選ぶと、同じ Flow の所有 Task と発行済み role Task を
対象に含め、dry-run で Task／Flow／Dispatch の全 ID を表示する。

適用には表示済み hash を要求し、出典・理由と workspace の保存状態を同じ SQLite
transaction で再照合する。稼働中・suspended Dispatch、稼働中 Flow、未解放 runner、
未解決の外部公開がある場合は拒否する。別の非終了 Task の依存・親子・継続元・
Dispatch 参照は勝手に巻き込まず、明示的に対象へ含める前の確認を求める。
この確認は保存状態に対するものであり、未記録の外部 process の停止を証明しない。

CLI の通常 Task／Dispatch 一覧と CLI／HTTP の後続計画は archive を除外する。
HTTP の ledger／Task／Dispatch 一覧は保存記録の参照として従来の出力を維持する。
`--include-archived`、`task graph`、ID 指定の履歴参照では記録を保持し、
archive 済みの計画上の action は `ARCHIVED` とする。
元 Task の編集・状態遷移・実行再開を拒否し、新しい作業は別途承認した後続 Task とする。
archive 済みの契約・実行履歴と退役記録は、通常の workspace 更新でも改変を拒否する。

Git の変更、branch、worktree、runtime の session ファイル、Memory、外部 Issue は
削除しない。物理削除、保持期限、unarchive は今回の実装範囲に含めない。
機密情報の消去や容量削減を求める要件は archive では満たせず、別に評価する。

## Consequences

不要な試行を日常の一覧・計画から外しながら、同じ ID で判断と実行の証拠を参照できる。
Task の終了状態と archive は別の情報なので、停止した Flow の Task を完了扱いにする
必要はない。空の通常計画は「この repository に未退役の Task がない」ことを表し、
未登録 repository のエラーとは区別する。

Graph の JSON に `archives` を追加する。archive のない既存 DB はそのまま読み込める。
archive を記録した DB は、この field を理解しない旧 CLI へ戻して使わない。
物理ファイルの掃除や DB の縮小を実施したとは報告しない。
