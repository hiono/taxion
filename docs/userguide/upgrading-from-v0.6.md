# v0.6.x からの更新

v0.7.0 は v0.6.x の ledger をそのまま読む。必須の移行 command はない。更新後に必要な操作は、Task ごとの承認状態の確認だけである。

## 変わること

- Task の承認が bundle として追記される。
- 実行入口が stage ごとに bundle を検査する。
- contract・execution・publication の gate report を CLI から読める。
- 実行前の read-only lane をモデル session なしで実行できる。
- gate と lane の report は credential を伏せる。

## 変えないこと

- 既存 Task、Dispatch、Flow の保存形式と意味。
- 既存 CLI の人間向け出力。
- 既存の validation、review、publication の手順。
- v0.6.x の Task が `verified-legacy` として実行入口を通る挙動。

## 更新後の確認

1. binary を更新する。
2. `taxion doctor` で設定と保存先を確認する。
3. `taxion task graph` で Task と承認の保存状態を確認する。
4. 実行予定の Task で `taxion gate contract --task <task-id>` を実行する。

| verdict | 対応 |
| --- | --- |
| `pass` | そのまま実行できる |
| `blocked` | `nextRequiredApproval` に従って承認または証拠を追加する |
| `fail` | 保存記録の矛盾を調査する。自動承認しない |

`verified-legacy` の Task を bundle-required の運用に移す場合は、現在の契約で再承認する。手順は[drift と legacy Task](drift-and-legacy.md)を参照する。

## 混在する分類

更新直後は、`bundle-required` の Task と `verified-legacy` の Task が混在する。分類は Task ごとに保存済み schema 情報から決まり、一括変換されない。実行予定の Task だけ、gate で分類と verdict を確認する。

## rollback

binary を v0.6.x に戻す。ledger は書き換えない。v0.7.0 が追記した bundle は残るが、旧 binary は従来の検査だけを行う。再度 v0.7.0 に戻しても bundle はそのまま読める。

rollback 後に v0.7.0 の gate report を再実行した場合、report の `observedAt` は新しくなるが、保存状態は変わらない。

## 既知の制限

v0.7.0 の範囲と制限は[v0.7.0 の既知の制限](v0.7.0-known-limitations.md)を参照する。Task 単位の集計と再契約は v0.7.1 で追加する。
