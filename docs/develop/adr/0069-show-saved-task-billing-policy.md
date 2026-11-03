# 69. 保存した課金方式を新規提案の既定値と区別して表示する

Date: 2026-11-03

## Status

Accepted

## Context

ADR-0063 は利用枠と Task ごとの判断を定め、既存の Copilot quota 操作も実装済みである。
しかし `config check --billing` は新規提案だけを表示し、保存契約の対象指定を拒否していた。
Task が選んだ `account` と現在の新規提案の `provider` を別の表示から照合する必要があり、
OpenCode Go の数値必須・提供元停止の判断を混同しやすかった。

## Decision

`config check --billing --task ID` と `--dispatch ID` を追加する。
既存の Controller の設定承認検査を使い、現在の完全な設定候補が許可されていることを確認する。
方式は保存済み Task または不変の Dispatch 指示から読み、新規提案用の既定値で置換しない。
旧契約に方式がなければ欠落として拒否する。通常の承認照合が拒否する終端・停止状態にも
この操作を実行許可として使えず、履歴の参照は既存の保存表示に分ける。

`contractBasis` と対象 ID を表示し、全表示に `executionAuthorized:false` を付ける。
新規提案の `sources` は従来どおりだが、保存契約では現在の設定元を保存方式の由来にしない。
`policy.source` は保存された値であり、提供元の契約資格は従来どおり未検証である。
既存の workspace snapshot を再利用し、DB・WAL・SHM の元ファイルは変更しない。
新しい課金エンジン、設定キー、提供元の自動選択、残量照会、モデル実行は追加しない。

## Compatibility and recovery

対象を付けない新規提案表示と通常の `config check` は維持する。
保存承認と現在の設定が異なれば非ゼロ終了し、新しい条件の承認を要求する既存動作を維持する。
既存結果、Flow、予算、停止理由を変更しない。管理スキルは説明更新に伴い `1.3.22` とし、
必要な更新では既存の `ready --update-skill` を使う。利用者の編集済みスキルを上書きしない。

## Validation and limitations

CLI の合成確認では Copilot の数値必須、OpenCode Free の提供元停止、
OpenCode Go の Task ごとの数値必須・提供元停止を保存グラフで区別する。
未知の有料 fallback・保存承認との不一致・方式のない旧契約は成功表示せず、
DB・WAL・SHM を含む workspace の bytes 不変を確認する。
既存の数値取得成功・取得不能・鮮度切れ・枯渇の試験と、提供元停止で照会しない試験を再利用する。

これはローカルの契約表示・合成確認であり、OpenCode Free／Go の実契約で
モデル応答・提供元上限停止を新たに実証したものではない。実契約での必要な未達確認は
Issue #234 と [バックエンド別の確認記録](../backend-acceptance.md)で保持し、
追加実行は構成・費用・期限の別承認を必要とする。接続成功だけで完了にしない。
