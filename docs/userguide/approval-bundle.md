# 承認 bundle

承認 bundle は、人間が確認した Task の契約と、その契約を覆う範囲を一つの記録にまとめたものである。v0.7.0 以降、bundle を必要とする Task は、現在の契約に一致する bundle がなければ実行に入れない。

## bundle に含まれるもの

| field | 意味 |
| --- | --- |
| `bundleVersion` | bundle 形式の版。v0.7.0 は1 |
| `canonicalizationVersion` | hash の正規化方式の版。v0.7.0 は1 |
| `id` | 追記ごとの識別子 |
| `taskId` | 対象 Task |
| `proposalHash` | 承認時に提示した契約提案の SHA-256 |
| `recordHash` | 保存後の Task record の SHA-256 |
| `dimensions` | 7次元の digest |
| `configuration` | 承認時に選択した設定 tuple の digest と出典 |
| `bundleHash` | bundle 全体の SHA-256 |
| `actor`・`source`・`reason`・`at` | 承認者、出典、理由、時刻 |

`proposalHash` と `recordHash` が異なる場合は、保存時に既定値が補われたなどの差分がある。bundle は両方を記録し、差分を後から確認できるようにする。差分の存在だけでは承認を無効にしない。

## 7次元

bundle は次の7つの digest を持つ。値そのものではなく digest を保存する。

| dimension | 対象 |
| --- | --- |
| `baselineDigest` | baseline SHA |
| `scopeDigest` | scope、outOfScope、fileAccess |
| `acceptanceDigest` | acceptance、requiredEvidence、validationCommands、exitCondition |
| `budgetDigest` | budgetGuidance |
| `securityDigest` | risk、exceptionIds、specifications、memoryRefs |
| `permissionsDigest` | owner、reviewer、authorizationId、decisions、worktree、branch、outputMode、target tree |
| `configurationDigest` | 選択した設定 tuple の digest |

## confirmation token

`taxion task authorize` は、`--confirm` を付けずに実行すると `confirmationToken` を表示する。token は `proposalHash` と7次元から導出する確認用の digest であり、認証 token でも秘密でもない。表示された契約と dimensions を確認し、同じ token を `--confirm` に渡す。

```bash
taxion task authorize --manifest task.json
taxion task authorize --manifest task.json --confirm <confirmationToken>
```

token が現在の basis と一致しなければ承認は拒否される。token を知っているだけでは承認権限にならない。

## 承認の確認

`task authorize` の出力には、次の stage ごとの状態が含まれる。

- `readback`: 保存後の Task が提案と一致するか
- `eligibility`: Task の状態が実行可能か
- `preflight`: 実行前の前提が揃っているか
- `lane`: read-only lane の確認対象か
- `role-packet`: role への instruction が契約に一致するか

各 stage の値は `approved`・`reused`・`reapproval-required` のいずれかである。`reused` は同じ basis の既存 bundle を再利用したことを表し、監査記録を増やさない。

保存済みの bundle は `taxion task graph` の JSON で確認できる。`bundleHash` を再計算して照合する手段は CLI にはないため、表示された hash を記録として使う。

## 再承認

bundle は承認時点の basis を覆う。次のいずれかが変わると、以前の bundle は現在の basis を覆わない。

- baseline、scope、acceptance、requiredEvidence、validationCommands、exitCondition
- 予算、owner、reviewer、worktree、branch、outputMode、target tree
- risk、exception、specification、memoryRef
- 承認時の設定選択
- bundle または正規化の版

再承認が必要な場合は、新しい manifest で `task authorize` を実行し、新しい token を確認する。以前の bundle を書き換えない。drift の見方は[drift と legacy Task](drift-and-legacy.md)を参照する。

## 保存しないもの

bundle は解決済みの credential 値と環境変数の値を保存しない。選択した設定 tuple と field の出典、その digest を残す。gate report と診断は credential らしい値を伏せる。credential を Task や manifest に書かない。
