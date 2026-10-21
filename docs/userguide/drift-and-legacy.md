# drift と legacy Task

承認 bundle は、承認した時点の Task の basis を覆う。basis が変わった場合、以前の bundle は現在の契約を承認したものとして扱わない。この差分を drift と呼ぶ。

## drift の見方

`taxion gate contract --task <task-id>` は、最新 bundle と現在の basis を比較し、差分を `drifts` に出す。

```bash
taxion gate contract --task <task-id> --json
```

各 drift は次の値を持つ。

| field | 意味 |
| --- | --- |
| `dimension` | 変わった dimension |
| `previousDigest` | 承認時の digest |
| `currentDigest` | 現在の digest |
| `authorizationImpact` | 再承認への影響 |
| `redactedFieldPaths` | 変わった field の path。値は含まない |

値そのものは report に含めない。credential らしい値は伏せる。

## authorization impact

| 値 | 意味 | 次の操作 |
| --- | --- | --- |
| `none`（予約） | 現行実装は返さない。表示だけの差分に使う予定 | — |
| `reapproval-required` | 現在の bundle が覆わない差分 | 新しい manifest で再承認する |
| `invalid` | 保存記録の矛盾、未知の版、Task の不一致 | 実行しない。記録を調査して修復する |

`reapproval-required` は自動承認されない。gate は `blocked` を返し、`nextRequiredApproval` に次に必要な承認を示す。`invalid` は `fail` を返す。

## Task の分類

Task は保存済みの schema 情報だけで次の3つに分類される。

| 分類 | 条件 | 実行入口 |
| --- | --- | --- |
| `bundle-required` | approval schema version が現在値と一致する | 一致する bundle が必要 |
| `verified-legacy` | approval schema version がなく、bundle もない | 従来の契約検査で続行 |
| `invalid-or-ambiguous` | 未知の schema version、または schema version なしで bundle がある | 拒否 |

`verified-legacy` は v0.6.x の保存記録を読むための分類である。新しい Task の承認を省略する一般則ではない。

`invalid-or-ambiguous` は修復されるまで実行入口で拒否する。bundle を削除したり、schema version を手で書き換えたりしない。保存記録を調査し、必要なら新しい Task として承認する。

## legacy Task の再承認

編集可能な legacy Task を bundle-required の運用に移す場合、現在の契約で `task authorize` を実行する。

1. `taxion task validate --manifest task.json` で契約を確認する。
2. `taxion task authorize --manifest task.json` で `confirmationToken` を確認する。
3. 同じ token を `--confirm` に渡す。
4. `taxion gate contract --task <task-id>` で `pass` を確認する。

再承認は追記であり、以前の記録を書き換えない。

既存の READY Task で保存済み設定承認と effective configuration だけが異なる場合は、manifest tracking を使わず `taxion task reauthorize --task TASK_ID` を使う。Task 契約と state を保ったまま、設定承認 decision と bundle を追記する。対象は既存の設定承認があり、Task-bound configuration check が不一致になる Task に限る。手順は[Task の検証と承認](task-validate-and-authorize.md#ready-task-の設定を再承認する)を参照する。

## 変わると再承認が必要な項目

- baseline、scope、acceptance、requiredEvidence、validationCommands、exitCondition
- 予算、owner、reviewer、worktree、branch、outputMode、target tree
- risk、exception、specification、memoryRef
- 承認時の設定選択
- bundle または正規化の版

承認済み basis の内側で完結する Coder・Reviewer・Repair の進行、repair 反復、read-only の観測、handoff の hold と release は再承認を要求しない。

bundle の構造は[承認 bundle](approval-bundle.md)を参照する。
