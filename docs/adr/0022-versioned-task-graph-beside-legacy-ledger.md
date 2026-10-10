# 22. 既存 Ledger と別の版付き Task Graph

Date: 2026-10-10

## Status

Accepted

## Context

ADR-0021 の Task Graph は既存 Task の四つのキーでは表せない。
既存 Ledger は CLI・HTTP・ACP・Web・SQLite の共有 wire であり、
一度に置き換えると実行契約と各 client の移行が混在する。
一方、自由文の title に関係や状態を埋め込むと、検査と復元ができない。

## Decision

既存 Ledger のキーは保持し、選択した Ledger のパスに `.graph.json` を付けた
ファイルへ `version: 1` の Task Graph を保存する。
Task ID、title、repository は既存 Ledger と一致することを要求する。
現在の実行契約、concern と Task の対応、判断、変更前後の履歴を保存する。
children・siblings・blocks は parentId と dependsOn から導出し、別の正本にしない。

CLI の `task track` と `task transition` で登録と更新を行い、
`task graph` で保存した状態と履歴を表示する。
登録には既存 Ledger の Task が必要である。
既存 Task を自動で承認・完了へ移行しない。
新しい記録は DRAFT または BLOCKED から始め、
状態変更は遷移・必要な情報・証拠を検査した後に保存する。
判断の書き換えや削除、Task の識別と起点の変更を拒否する。

Graph が存在しない workspace は既存の挙動を保持する。
導入した workspace の dispatch は CLI・HTTP・ACP で同じ検査を行い、
未登録 Task、repository の不一致、READY 以外、必須情報不足、未充足の依存を拒否する。
既存 Graph の破損、非対応の版、未知のキー、読み取り失敗はエラーとする。
この互換性方式は旧 workspace に新しい制約を自動適用するものではない。

保存は同じ directory の一時ファイルへ書き込み・同期した後、rename する。
CLI の Graph 更新は排他的な lock file で直列化する。
異常終了で残った lock は自動削除せず、実行状況を確認した後で復旧する。
Graph と Ledger は別の保存であり、複数ファイルにまたがる transaction ではない。
dispatch の終了・差し替えと保存の整合性は #48、実行の復旧は #55 で扱う。

## Consequences

旧 client の JSON shape と wire tests を変更せずに、Task Graph を利用できる。
HTTP・ACP の既存 read API は旧 Ledger を返し、Graph の編集は現時点では CLI に限定する。
外部 client の会話接続は後続段階で実装する。
Graph と Ledger を一緒に保持しないと契約を復元できないため、
移行と backup では両方を対象にする。
管理者が Graph を削除すると旧方式へ戻るため、このファイルは制約と状態の保存先として扱う。

approval・completion の proof は必須の証拠参照、acceptance、
reviewer と対象 SHA の対応を検査する。
明示的な例外は記録済み decision ID と免除する条件・子 Task を組にする。
必要なレビューそのものを例外で省略しない。
現時点では全 Task の承認・完了にレビュー情報を要求する保守的な方式とする。
証拠の実在、Git の事実、Reviewer の実行と権限は #52 で別途検証する。
自己申告の proof を実際の独立レビューの実行証拠とは扱わない。

Memory は参照 ID を保持する段階であり、保存先との照合は #66、
指示への反映は #67 で実装する。
現在の操作と制約は [Task Graph reference](../10-task-graph.md) に記載する。
