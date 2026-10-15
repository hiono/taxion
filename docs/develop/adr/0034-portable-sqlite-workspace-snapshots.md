# 34. SQLite 一ファイルによる停止済み workspace の移送

Date: 2026-10-15

## Status

Accepted

## Context

利用者は、別マシンへ移るために多数の JSON、Memory、Git worktree を個別に
バックアップする負担を減らしたい。ADR-0033 のメタデータ保存だけでは、
DB に記録された commit・tree の実体を移送できない。
承認済みの絶対パスを書き換えて同じ実行を再開すると、
旧マシンの認可を異なる環境へ転用してしまう。

## Decision

`workspace snapshot` は、停止・照合済みの正式状態、Memory DB、
必要な commit・tree・blob を一つの SQLite ファイルへ収める。
通常の workspace に Git 全体を常時複製せず、明示した移送時に収集する。
同じ内容の payload は SHA-256 を主キーとして共有する。
Git は必要な object の到達閉包を pack にし、commit の親履歴も含める。

取得中は workspace と存在する Memory に `BEGIN IMMEDIATE` の書込み予約を保持する。
別接続の `VACUUM INTO` で整合コピーを取得し、元 DB の revision は変更しない。
Flow の実行中状態・runner claim、未終了 Dispatch、未照合の外部送信は拒否する。
保存済み Git object の不足、観測した worktree の未記録変更、
submodule、禁止されたローカル状態の path は親履歴内も含めて拒否する。
Memory の参照先は履歴分も含めて存在を照合し、失効済みの記録も元の状態で保持する。
SDK home、環境変数、Git config、hook、認証情報は収集元にしない。
既知の token・秘密鍵パターンを正式状態・Memory・Git object から検出した場合も拒否する。
パターン検査は未知の秘密まで検出する保証ではなく、移送ファイルは非公開として扱う。

`workspace inspect-snapshot` は隔離した一時配置で DB と Git を復元し、
checksum、形式版、保存契約が要求する repository・object の対応を照合する。
ネットワーク、provider、モデル、validation command は実行しない。
`workspace restore` は、ファイル全体の SHA-256 と actor・source の明示承認を要求する。
既存 destination は上書きせず、新規 directory に限定する。
準備失敗・取消時は、その操作が新規作成した directory を除去する。
復元中の destination は利用せず、コマンドの成功後に使う。

Task・Dispatch・承認・結果・usage・元の hash は保持する。
旧絶対パスと復元先の対応、移送元 hash と承認を別 receipt として記録する。
再移送では過去の配置 receipt も保持する。
Git の再検証と successor の検査だけが、この対応で読み取り先を解決する。
元 Task への新規 Dispatch と旧 SDK session の再開は拒否する。
継続には、新しい配置・設定・予算の承認を持つ successor Task を使う。
完了済み Coder 結果は再作成せず、既存の独立 Reviewer への継続経路を使う。
取消済み Coder の部分成果も、保存 tree を検証して後続 Coder に渡す。

## Consequences

移送に必要な実体は一つの SQLite に含まれ、元 DB・外部 JSON・元 Git の場所が
利用できなくても復元できる。展開した Git と作業ファイルは再生成可能な配置である。
解放済み Reviewer の path は復元先でも存在しない状態に保つ。
retention ref を復元し、Git GC 後も保存 tree を保持する。

一方、履歴を含む Git object は大きくなる場合がある。
現在の実装は payload と検査対象をメモリへ読み、容量・処理時間は入力全体に依存する。
Memory には既存の256 MiB snapshot 上限を適用する。
新たな自動削除、容量上限、依存パッケージは追加しない。
ignored file、SDK の内部状態、未保存の成果、外部 skill・MCP の実体、
Git LFS の外部実体は移送せず、LFS pointer のある移送を拒否する。別マシンでの認証と設定は別途必要になる。
既存の Windows 実行制約も、この判断で解消したとは扱わない。

## Alternatives

稼働中の DB ファイルを単純コピーする方法は、WAL と別 DB の不整合を見落とす。
外部ファイルを集めた archive だけでは、一ファイルの SQL DB で完結する要望を満たさない。
Dolt・DuckDB の導入は必要な Git 実体や認可の移送問題を解決しないため採用しない。
