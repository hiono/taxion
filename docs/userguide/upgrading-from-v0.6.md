# v0.6.x の保存状態からの更新

このページは古い workspace の承認分類を確認するための補足である。
現在の配布物の導入・policy 更新は[正式バイナリと運用モード](binary-provenance.md)、
一般の操作は[利用者ガイド](user-guide.md)を参照する。
旧版同士の差分を、現在の唯一の更新手順として使わない。

## 更新前に状態を保全する

実行を正式に停止し、session・runner の状態を確認して、整合した DB と設定・Git 成果物を保全する。
同じ保存先を維持し、新配布物の固定値を承認して provenance policy と管理 skill を更新する。
新 binary が旧 policy を自動で上書きすることはない。

## 保存済み承認を分類する

承認束の導入前の Task は、保存形式と根拠から `verified-legacy` または
`invalid-or-ambiguous` に分類する。承認束を要求する graph は `bundle-required` として扱う。
分類は graph 全体で決まり、Task ごとに混在させない。

`taxion task graph` で保存状態を確認し、実行予定の Task の contract gate を読む。
`pass` は既存契約での判定、`blocked` は必要な承認・証拠の不足、
`fail` は矛盾や前提の不成立を示す。gate の成功だけでモデルや公開を自動開始しない。

`verified-legacy` を bundle 必須の運用へ移す場合は現在の契約を正式に承認する。
既存 READY Task の設定変更は `taxion task reauthorize --task TASK_ID` で追記し、
元の契約・決定・proposal hash を上書きしない。詳しくは
[drift と legacy](drift-and-legacy.md)と[承認操作](task-validate-and-authorize.md)を参照する。

## rollback

新しい承認・指示・検証・復旧・延長監査を保存した DB を、それを理解しない旧 binary で操作しない。
戻す場合は現在の成果を保全し、適用前の整合バックアップを別配置で使う。
承認束や監査を削除して旧版に合わせる操作は行わない。
利用可能な戻し先は導入するリリースの manifest、現在の適用範囲は[既知の制限](v0.7.0-known-limitations.md)で確認する。
