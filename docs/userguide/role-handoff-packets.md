# ロール間の handoff packet

handoff packet は、Coder・Validator・Reviewer に渡す参照情報を固定した JSON です。`role-packet create` は標準出力へ canonical JSON を出し、`role-packet verify` は受信側が別途記録した hash と照合してから同じ JSON を返します。両コマンドとも workspace、承認記録、実行状態を変更しません。

## packetVersion 1 の field

packet は次の12 field だけを持ちます。読取り時には、未知・欠落・重複 field、大文字小文字だけ異なる field、`null`、後続の JSON を拒否します。

| field | 内容 |
| --- | --- |
| `packetVersion` | `1`。serialization と検査規則の版 |
| `taskId` | Task ID。文字・数字・`.`・`_`・`-` のみ |
| `taskHash` | 保存 Task に既存の `core.CanonicalSHA256` を適用した SHA-256。GateReport の `hashes.task` と同じ意味 |
| `baselineSha` | baseline commit の完全な Git object hash |
| `targetTreeHash` | 対象 tree の完全な Git object hash |
| `scopeDigest` | ApprovalBundle の `dimensions.scopeDigest` |
| `acceptanceDigest` | ApprovalBundle の `dimensions.acceptanceDigest` |
| `prohibitedActions` | 禁止操作の文字列配列。指定なしは `[]` |
| `role` | `CODER`・`VALIDATOR`・`REVIEWER` のいずれか |
| `previousEvidenceHash` | 直前ロールの既存 evidence hash。最初の Coder だけ空文字を許容 |
| `requiredOutputSchema` | Coder・Reviewer は `taxion.dispatch-result/v1`（既存の `core.DispatchResult`）、Validator は `taxion.gate-report/v1`（既存の `controller.GateReport` version 1） |
| `packetHash` | `packetHash` を空文字にした packet 全体の canonical SHA-256 |

SHA-256 は小文字64桁の16進数、Git object hash は小文字40桁または64桁の16進数を使います。schema 名は既存 record 型への参照であり、schema 本文や新しい evidence 形式ではありません。`prohibitedActions` の順序も hash に含まれます。

Go API は `core.NewRoleHandoffPacket(fields)` で入力 slice を複製し、非公開 field に保存します。`Readback()` が返す slice も複製です。`MarshalJSON()` は既存の ApprovalBundle と同じ `core.CanonicalJSON` を使い、`ReadRoleHandoffPacket(data, expectedHash)` は検査と hash 照合を行います。受信 JSON の空白や object key の順序は hash を変えません。

## 生成と照合

生成側は、同じ保存 Task と ApprovalBundle から task hash・scope digest・acceptance digest を取得します。Go の呼出し側が dimension を計算する場合は、既存の `core.BundleDimensions(task, configuration)` の `Scope` と `Acceptance` を使います。

```bash
taxion role-packet create \
  --task "$TASK_ID" --task-hash "$TASK_HASH" \
  --baseline-sha "$BASELINE_SHA" --target-tree-hash "$TREE_HASH" \
  --scope-digest "$SCOPE_DIGEST" --acceptance-digest "$ACCEPTANCE_DIGEST" \
  --role CODER --prohibit 'Do not merge' --prohibit 'Do not publish' \
  > coder-packet.json

taxion role-packet verify \
  --file coder-packet.json --expected-hash "$RECORDED_PACKET_HASH"
```

生成側は出力された `packetHash` を packet 本文とは別に記録し、受信側へ伝えます。受信側が受信本文から読み取った hash をそのまま `--expected-hash` に使っても、本文と hash を一緒に置換する改ざんは検出できません。CLI は署名や送信者認証を提供しません。

Validator には `--role VALIDATOR --previous-evidence-hash "$CODER_EVIDENCE_HASH"`、Reviewer には `--role REVIEWER --previous-evidence-hash "$VALIDATION_EVIDENCE_HASH"` を指定します。既存の evidence hash をそのまま使い、packet hash で置き換えません。GateReport が既存の検証 evidence を指す場合は `hashes.evidence` を引き継ぎます。

ロールごとの確認事項は、Coder が対象 Task・baseline・scope・禁止操作、Validator が対象 tree・acceptance・Coder evidence、Reviewer が対象 tree・別途取得した diff・validation evidence です。role と出力 schema は固定の組合せで検査します。契約全文、diff 本文、会話履歴、private reasoning を packet に含めません。

## 承認・credential・実行上の制限

packet は実行許可ではありません。CLI は明示された参照値の形式と整合性だけを検査し、保存 Task・Git object・evidence の存在や内容までは照合しません。呼出し側は既存の ApprovalBundle 検査と `role-packet` stage の承認条件を維持し、同一 bundle の内部遷移のためだけに再承認を要求しません。packet の生成で既存の fail-closed gate を迂回できるわけではありません。

生成時には既存の `core.Redact` で `prohibitedActions` 中の既知の credential パターンを置換してから hash を計算します。Task ID 内の既知の credential は拒否します。読取り時に credential を検出した場合は、本文を黙って書き換えず拒否します。packet の検査エラーには受信 field 名・値を含めません。既知のパターンに一致しない秘密情報まで検出する仕組みではないため、禁止操作の記述にも秘密情報や会話を入力しないでください。

この MVP は参照 packet の生成・読取り・照合のみです。既存 runtime への自動注入、Validator の自動実行、packet 専用 DB・監査台帳、transport、message bus、dashboard、replay は追加しません。生成物と受信側 hash の保存先・引渡し方法は呼出し側が指定します。
