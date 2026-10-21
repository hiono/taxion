# Task の検証と承認

`task validate` と `task authorize` は、Task Graph に Task を登録する前の確認と、確認済み契約の承認を分けて行う。どちらも manifest の JSON を読む。

## 検証する

```bash
taxion task validate --manifest task.json
```

`task validate` は保存を伴わない。次の値を出力する。

| field | 意味 |
| --- | --- |
| `valid` | すべての validator を通過したか |
| `existingTask` | 同じ ID の Task が既に保存されているか |
| `stateChanges` | 保存した場合に起きる状態変化の説明 |
| `validatorErrors` | 違反の一覧。保存は行われない |
| `digestWithheld` | credential らしい値を含むため digest を出さないか |
| `canonicalJson` | 正規化した契約提案 |
| `canonicalSha256` | 契約提案の SHA-256。承認時の `proposalHash` になる |

`valid` が false の場合、終了 code は非zeroで、Task は保存されない。`digestWithheld` が true の場合も保存しない。credential を manifest から除いて再実行する。

## 承認する

`task authorize` は検証、保存、bundle の追記を一度に行う。ただし承認 basis の確認を二段階に分ける。

```bash
taxion task authorize --manifest task.json
```

`--confirm` がない場合、Task は保存されず、次の内容が表示される。

- `status`: `awaiting-approval`
- `proposalHash`: 承認対象の契約 hash
- `configurationDigest`: 選択した設定 tuple の digest
- `dimensions`: 7次元の digest
- `confirmationToken`: `proposalHash` と7次元から導出した確認用 digest
- `nextAction`: 次に実行する command

表示された契約と dimensions を確認し、同じ token を渡す。

```bash
taxion task authorize --manifest task.json --confirm <confirmationToken>
```

token が一致しない場合、Task は保存されない。`--confirm` は token の一致だけを確認する。人間の認証を行った証拠ではない。

## 成功時の出力

承認が成立すると、次の値が出力される。

| field | 意味 |
| --- | --- |
| `taskId`・`approvalId` | Task と bundle の識別子 |
| `bundleVersion`・`canonicalizationVersion` | bundle と正規化の版 |
| `proposalHash`・`recordHash` | 提案と保存 record の hash |
| `configurationDigest` | 選択した設定 tuple の digest |
| `bundleHash` | bundle 全体の hash |
| `priorBundleId`・`priorBundleHash` | 直前の bundle |
| `stages` | stage ごとの `approved`・`reused`・`reapproval-required` |
| `reapprovalRequired` | 再承認が必要な stage があるか |
| `drifts` | 直前の bundle と現在の basis の差分 |

同じ内容を再度承認した場合、既存 bundle を再利用し、`reused` を返す。監査記録は増えない。

## 保存の順序

1. manifest を検証する。
2. 提案の `proposalHash` を計算する。
3. Task を保存する。
4. 保存後の record から `recordHash` を計算し、提案と照合する。
5. bundle を組み立て、hash を検証する。
6. bundle を append する。

保存後の readback が提案と一致しない場合、bundle は追記されない。Task が保存済みでも bundle がない場合、その Task は実行入口で拒否される。契約を修正して再承認する。

## 関連する gate

承認後に Task が実行に入れるかは `taxion gate contract --task <task-id>` で確認できる。gate は承認を行わない。詳細は[契約・実行・公開の gate](gates.md)を参照する。
