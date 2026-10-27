# 56. 持込み成果物を実装成功と分離して検証する

Date: 2026-10-27

## Status

Accepted

v0.7.21-rc.5 の初期実装。v0.7.21-rc.4 の仕様ではない。
Issue: <https://github.com/hiono/taxion/issues/201>

## Context

従来の Flow は、成功した Coder Dispatch を検証・独立レビューへの入口として要求していた。
持込み成果物に追加修正がない場合でも Coder の起動と全変更パスの申告が必要になり、
入力の存在、今回の編集、品質判断を区別できなかった。

## Decision

入力の同一性と実行・品質の証跡を別の型にする。`ArtifactTarget` は repository、
baseline commit、入力 commit、tree、全変更パス、利用者が示した出典と内容 hash を保持する。
出典の文字列は由来の申告であり、Controller が作者や旧モデルの成功を証明したものではない。
`ArtifactValidation` は承認済みコマンドの実行時刻・終了 code・出力・対象を保持する。
独立 Reviewer はその入力と実測検証記録を受け取る。

初期実装は snapshot・修正なしに限定する。`workflow plan` は入力を観測して
正確な工程 hash に含め、`start` はその同じ入力を再照合・保持する。
実際の Coder Dispatch/session 数と許可上限はいずれもゼロ。
既存契約のほか、`VERIFICATION` 型・`USER` owner の非モデル Task も扱う。
その Task からモデル Dispatch を直接作ることはできない。

入力受理時点では Dispatch が存在しない。`workflow validate-input` は runner claim と
実行開始記録を先に保存し、承認済みコマンドを実行する。開始前後の tree が一致し、
全コマンドが成功した場合だけ、別 Task の Reviewer Dispatch を発行する。
`workflow run` も同じ入力工程を利用し、別の実行エンジンは追加しない。
Reviewer の結果提出時に行う既存の host 検証は維持し、入力工程の記録を別 worktree の実績へ付け替えない。

失敗・timeout・対象変更は証跡を保存して停止する。開始記録だけが残る中断は実行結果不明として扱い、
process-stop を確認した正式 recovery でもコマンドの再実行を許可しない。
入力・検証・工程の対応は append-only budget audit とも照合する。
空差分でも、承認対象のプログラムと受入れ条件のレビューは省略しない。

既存の契約・設定承認は正式に対象を含む範囲で再利用する。新しい対象・工程の承認は必要だが、
派生 Reviewer の ApprovalBundle という記録形式だけを理由に再承認を要求しない。
publication は従来どおり別承認。モデルの file tools は検証コマンドの実行権限の代わりにならない。

## Consequences

旧結果・旧 session・終端 Flow は変更せず、持込み入力を Coder 成果や品質合格へ変換しない。
入力の観測は private index を使うため、実 index・branch・作業ファイルは変えない。
Git object の生成と、開始時の保持 ref の追加は行う。

初期実装では修正ループ、任意 packet lifecycle 委譲、restore 後の直接実行を拒否する。
後続対応は同じ Issue の段階計画で管理し、通常 Coder Flow の既存検証条件は弱めない。
新しい入力記録を使った workspace は、古い binary へ戻して実行しない。
更新前の保全済み workspace を別環境で扱い、新記録を旧形式へ書き換えない。

## C4 と実装境界

| 層 | 今回の対応 |
| --- | --- |
| C1 | 利用者が作成済みのプログラムを、Taxion へ未検証入力として渡す |
| C2 | 既存 CLI／Controller、検証 subprocess、Reviewer runtime、SQLite／Git を維持する。新サービスは作らない |
| C3 | 操作入口、対象観測、承認済み検証、独立レビュー、証跡保存を分ける |
| C4 | core の入力・証跡型、gitproof の観測とコマンド実行、Controller の入力工程、store の追記監査を対応させる |

これらは責務・モジュールの境界であり、新しい独立 process の追加を意味しない。
