# 33. 結果の ID 参照とレコード単位の SQLite 保存

Date: 2026-10-15

## Status

Accepted

## Context

Task・Dispatch・承認・利用量は既に SQLite に保存している。
しかし、担当間の decision に過去 instruction と結果を埋め込むため、
後続 instruction に再帰的な複製が生じていた。
さらに、event 一件の追記でも Ledger・Graph・records の集合全体を書き戻していた。
request の相談 receipt と実行ログも、DB の外に JSON として保存していた。
この構造はモデル入力、保存量、バックアップ対象をそれぞれ増やす。

SDK session の役割分離は維持する。ROLE を普通の Send ごとに再送していなくても、
保持した system context は推論入力になる。ID を渡してから同じ全文を
tool で読み戻させるだけでは、入力を減らしたことにならない。

## Decision

SQLite を継続し、Dolt・DuckDB や新しい DB server を追加しない。
新しい `result:` decision は、workspace 内の Dispatch ID、
不変 instruction hash、結果 hash を持つ `taxion.result-ref.v1` とする。
Controller が保存済みの出典と完全一致を照合し、担当に必要な結果を組み立てる。
旧 decision の読取りも残し、既存の承認原本・instruction hash を変更しない。

`--compact-context` は役割別の JSON projection を使う。
目的、許可・禁止範囲、受入れ、必要な証拠、検証、レビュー対象、
利用者の判断、Memory、仕様引用、予算方針を維持する。
設定・intake 承認は ID・出典・理由を渡し、設定全体の転記を省く。
通常の Task 関係情報の再掲も省き、依存と引継ぎ理由は保持する。
Planner・質問 context では、別に結果が含まれる箇所の decision を ID 参照にする。
モデルに任意 SQL や他 workspace を読む tool は提供しない。

同梱 prompt の実行は、session に束縛した結果 schema
`urn:taxion:dispatch-result:bound:v1` を使う。
Dispatch・Task・role・repository・観測済み開始 SHA と snapshot mode の
出力 commit SHA は host が補完する。モデルの値が矛盾すれば拒否する。
verdict、finding、実装内容、証拠を host が成功へ補正することはない。
独自 prompt・CLI/ACP の既存結果 schema は維持する。
表示と schema の変更は runtime config hash に反映し、
旧 session を新 hash で再接続したことにしない。停止専用操作は維持する。

SQLite の集合 JSON を、領域・field・配列位置で識別するレコードへ移行する。
変更していないレコードは書き戻さない。event の追記はそのレコードと
workspace revision を更新し、Graph の集合を書き戻さない。
長い JSON 文字列は内容 hash で一つにまとめ、レコードと文字列を zlib で圧縮する。
原文の key 順序、数値、escape を復元し、hash・長さ・圧縮 checksum を照合する。
短い文字列は参照のほうが大きくなるため、512 bytes から共有対象にする。
これは保存形式の選択であり、利用量の上限ではない。

移行と承認・結果・runner claim の更新には既存の SQLite transaction を使う。
集合 JSON の正本は残さず、旧 column は空 object の検査用 sentinel とする。
旧形式は read-only で観測でき、書込み接続では検証して移行する。
移行失敗時は元データを保持し、未知版・改変・欠落した文字列参照を拒否する。
移行後はこの保存形式に対応する binary を使う。
空き page の回収や既存ファイルの削除は自動で行わない。

request の計画 receipt と実行先・失敗の記録は、request ID・種類・連番・hash とともに
DB に保存する。`records show` を明示 export として使い、
新しい `requests/*.json`・`execution.log` は通常操作で作らない。
実行イベントを二重保存せず、Flow ID から正式な Dispatch・event receipt を参照する。
相談保存前に workspace DB を所有者だけが読み書きする mode にする。
過去に保存した外部ファイルは削除・自動取込みしない。

## Consequences

モデル入力、DB への実書込み、物理ファイルサイズは別々に測る。
入力 bytes の減少を token 数・cache 課金・料金の削減率とは扱わない。
更新 callback は引き続き全状態を読み、比較用に符号化するため、
部分読取りや CPU・memory 使用量まで定数時間になったとは主張しない。

この判断の第1段階だけでは、別マシンへの DB 単体復元は成立しない。
Memory、必要な成果・部分成果、Git object、配置対応の収集と復元は、
後続の [ADR-0034](0034-portable-sqlite-workspace-snapshots.md) で実装する。
DB 内の hash は外部の実体を代替しないため、この条件を完了扱いにしない。
認証情報や実行中 process の移送、同一 SDK session の再接続も保証しない。

## Alternatives

DB engine の変更だけでは、入れ子の instruction とモデルへの全文再展開は残る。
外部 JSON を別の directory に集める案も、正本の多重化を解消しない。
既存 instruction を参照形式へ書き換える案は、承認済み hash を変更するため採用しない。
