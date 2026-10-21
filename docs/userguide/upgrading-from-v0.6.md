# v0.6.x からの更新

v0.7.0 は v0.6.x の ledger をそのまま読む。必須の移行 command はない。更新後に必要な操作は、workspace の承認分類の確認だけである。

## 変わること

- Task の承認が bundle として追記される。
- 実行入口が stage ごとに bundle を検査する。
- contract・execution・publication の gate report を CLI から読める。
- 実行前の read-only lane をモデル session なしで実行できる。
- gate と lane の report は credential を伏せる。
- Task 単位で Dispatch、session、usage、経過時間、repair/re-review を集計し、承認済み上限を表示する。
- usage が不明・不完全な場合や上限到達時は `BUDGET_STOP` とし、新しい session・Dispatch を開始しない。
- 未確認・未開始の Coder Dispatch に限り、Flow を hold して新しい Task 契約で再承認する最低限の再契約を行える。
- approval、usage、budget stop、再契約の監査記録は追記のみで保存される。

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

## 分類の確認

更新直後の workspace は、保存済み schema 情報から `bundle-required` か `verified-legacy` のどちらかに分類される。分類は graph 全体で決まり、Task ごとに混在させない。実行予定の Task について、gate で分類と verdict を確認する。

## rollback

binary を v0.6.x に戻す。ledger は書き換えない。v0.7.0 が追記した bundle は残るが、旧 binary は従来の検査だけを行う。再度 v0.7.0 に戻しても bundle はそのまま読める。

rollback 後に v0.7.0 の gate report を再実行した場合、report の `observedAt` は新しくなるが、保存状態は変わらない。

## 既知の制限

v0.7.0 の範囲と制限は[v0.7.0 の既知の制限](v0.7.0-known-limitations.md)を参照する。Task 単位の集計、hard budget stop、最低限の再契約は v0.7.0 に含まれる。高度な集計分析、provider 横断の billing 集計、利用量予測、一般化した再契約 policy、dashboard と履歴分析は対象外である。
