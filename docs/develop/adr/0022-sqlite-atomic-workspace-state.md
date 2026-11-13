# 22. SQLite による Ledger・Task Graph・Dispatch の原子的保存

Date: 2026-10-10

## Status

Accepted

## Context

ADR-0021 の Task Graph は既存 Task の四つのキーでは表せない。
既存 Ledger は CLI・HTTP・ACP・Web・SQLite の共有 wire であり、
一度に置き換えると実行契約と各 client の移行が混在する。
一方、自由文の title に関係や状態を埋め込むと、検査と復元ができない。

## Decision

SQLite を workspace の正本とし、Ledger の既存 JSON 表現と版付き Task Graph を
同じ `workspace_state` row に保存する。revision、Ledger、Graph の更新は単一の
SQLite transaction でコミットし、読み込み・検証・変更も transaction 内で行う。
Task ID、title、repository は Ledger と一致させる。現在の実行契約、concern と
Task の対応、判断、変更前後の履歴を保持する。children・siblings・blocks は
parentId と dependsOn から導出し、別の正本にしない。

CLI の `task track` と `task transition` で登録と更新を行い、
`task graph` で保存した状態と履歴を表示する。CLI・HTTP・ACP の本番更新入口は
同じ SQLite workspace API を使う。
登録には既存 Ledger の Task が必要である。
既存 Task を自動で承認・完了へ移行しない。
新しい記録は DRAFT または BLOCKED から始め、
状態変更は遷移・必要な情報・証拠を検査した後に保存する。
判断の書き換えや削除、Task の識別と起点の変更を拒否する。

Graph を導入した workspace の dispatch は CLI・HTTP・ACP で同じ transaction 内に検査し、
未登録 Task、repository の不一致、READY 以外、必須情報不足、未充足の依存を拒否する。
dispatch 発行時は Graph の Task を ACTIVE にし、Dispatch の初期状態・履歴と Ledger の
Active projection を同時に保存する。終了遷移では三者を同じ transaction で更新する。
発行時には version 付き実行指示を Task の許可済み契約から生成し、依存・親子・sibling、
許可／禁止 scope、Memory と decision の参照、worktree、branch、SHA、返却形式を
immutable snapshot として保存する。保存した指示の hash を検査し、CLI・ACP は同じ
snapshot を表示する。HTTP は Dispatch ID から snapshot と hash を読み出せる。
2 回目以降の Dispatch は直近の inactive Dispatch と関係・理由を明示する。
終端結果は Dispatch ID・Task ID・role・repository・開始／出力 SHA・変更ファイル・
検証結果・証拠・blocker・要求する次の遷移を含み、対象の active Dispatch と照合する。
終端 Dispatch は再遷移できず、以前の結果で新しい Dispatch を完了させない。
これらの値は caller が申告した記録であり、Git・コマンド実行・証拠の実在を保証しない。
利用者の判断は選択肢・推奨と理由を記録し、選択を decision として残すまで READY にできない。
既存 Graph の破損、非対応の版、未知のキー、読み取り失敗はエラーとする。
初回アクセス時に旧 Ledger と任意の Graph を SQLite へ一度だけ取り込む。
JSON は旧 workspace からの一時的な移行入力に限り、移行後の正本・バックアップ・
互換出力として維持しない。現実装は移行元を自動削除しないため、SQLite の取り込みを
確認した後に管理者が削除する。JSON が残っていても SQLite と同期せず、SQLite を失った際の
復元元にはならない。破損や未解決の active Dispatch を inactive として再作成せず、
Graph 導入時に解決されていない active Dispatch があれば拒否する。
Graph のない workspace は旧 dispatch 検査の互換動作を維持する。

SQLite transaction が状態変更の原子性を担保する。SQLite が writer contention を
解決できない場合は更新エラーを返し、成功したように扱わない。既存 JSON Ledger と
Graph は移行後に更新しない。SQLite から別の永続化方式へ移る場合は別途判断し、
Dolt は将来の候補であって現時点の実装要件ではない。

## Consequences

旧 client の JSON shape と wire tests を変更せずに、SQLite 内で Task Graph を利用できる。
HTTP・ACP の既存 read API は従来どおり Ledger を返す。
外部 client の会話接続は後続段階で実装する。
移行元 JSON は検証中の一時ファイルであり、保持期間中も SQLite のバックアップとして扱わない。
SQLite 内の Graph と Ledger は整合性検証の対象であり、
active Dispatch の状態と履歴の不一致を読み込み・commit 時に拒否する。

approval・completion の proof は必須の証拠参照、acceptance、
reviewer と対象 SHA の対応を検査する。
明示的な例外は記録済み decision ID と免除する条件・子 Task を組にする。
必要なレビューそのものを例外で省略しない。
現時点では全 Task の承認・完了にレビュー情報を要求する保守的な方式とする。
証拠の実在、Git の事実、Reviewer の実行と権限は #52 で別途検証する。
自己申告の proof を実際の独立レビューの実行証拠とは扱わない。

Memory は参照 ID を保持する段階であり、保存先との照合は #66、
指示への反映は #67 で実装する。
この ADR は従来の「Ledger と別ファイルに Graph を保存する」方式を supersede する。
現在の操作と制約は [Task Graph reference](../task-graph.md) に記載する。
