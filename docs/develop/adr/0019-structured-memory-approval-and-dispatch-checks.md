# 19. Memory candidate の明示承認と構造化した dispatch 検査

Date: 2026-10-10

## Status

Accepted

## Context

M6-3 (#29) の当初要件は、繰り返しの指摘から candidate を作成し、承認後の guardrail を dispatch 時に検査することだった。初期実装は固定3回で candidate を作成する段階までだったため、しきい値の設定、承認、検査を追加する。

Memory の本文は自由文であり、そのままでは合否を判定できない。本文から条件を推測すると、記述した意図と実際の拒否条件が一致する保証がない。検査コマンドを登録する方式は任意コード実行の権限も追加する。

## Decision

candidate の承認時に条件の種類と値を明示する。初期対応は dispatch の repo 文字列の完全一致と、保存済み task title の Go 正規表現による照合に限定する。自由文の自動解釈、確認ボタンだけの遵守判定、shell の実行、ファイルの存在検査は採用しない。

警告のしきい値は workspace の Memory DB に保存し、既定値は3、設定可能な値は正の整数とする。警告回数と candidate の作成・関連づけは同一 transaction にまとめ、失敗時に未接続の candidate や進んだ counter が残らないようにする。

承認も guardrail、条件、lesson の status を同一 transaction で更新する。同一 candidate への同じ承認は同じ guardrail を返し、異なる条件での再承認は拒否する。直接保存した通常の lesson や未承認 guardrail は強制しない。

CLI・HTTP・ACP が同じ workspace の承認済み条件を検査する。Web は HTTP の結果を返し、条件を再実装しない。全条件の一致を必要とし、条件不一致と保存先の障害を区別する。既存 DB の読み込み失敗を「条件なし」とみなすと制約を迂回できるため、明示的に失敗させる。DB が存在しない既存 workspace は従来どおり使える。

## Consequences

core は標準ライブラリだけのまま維持する。Memory の条件と設定は SQLite に置き、凍結済み Ledger のキーや dispatch リクエストを変更しない。project/global は同じ DB 内の scope なので、承認済み条件はその DB を使う全 dispatch に適用する。

検査対象は repo と task title に限られる。自由文の guideline 全体への遵守、worker の動作、shell 操作を保証するものではない。条件の変更・失効 API は初期実装に含めない。Memory の検査と JSON Ledger の保存も単一 transaction ではなく、同時変更まで排他的に扱う仕組みではない。現在のコマンドと拒否条件は [Memory reference](../../userguide/memory-model.md) に記載する。
