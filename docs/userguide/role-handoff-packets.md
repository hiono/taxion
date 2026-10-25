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

packet 自体は実行許可ではありません。オフラインの `create` / `verify` は参照値の形式と自己整合性を検査し、保存 Task・Git object・evidence の存在までは照合しません。後述の Controller lifecycle は保存済みの Task、ApprovalBundle、Dispatch、実 evidence と packet を照合します。どちらも既存の実行入口の検査を迂回しません。

生成時には既存の `core.Redact` で `prohibitedActions` 中の既知の credential パターンを置換してから hash を計算します。Task ID 内の既知の credential は拒否します。読取り時に credential を検出した場合は、本文を黙って書き換えず拒否します。packet の検査エラーには受信 field 名・値を含めません。既知のパターンに一致しない秘密情報まで検出する仕組みではないため、禁止操作の記述にも秘密情報や会話を入力しないでください。

オフライン生成物の保存先と引渡し方法は呼出し側が指定します。Controller lifecycle を使う場合は、canonical bytes と hash を workspace の既存 SQLite document store に保存します。外部 transport、message bus、会話の保存、packet analytics はありません。

## READY Task の packet lifecycle 承認

`task authorize-packets` は、既存の matching ApprovalBundle を持つ READY の CODER Task（REVIEWER 指定あり）に対し、逐次 packet の使用を明示的に承認します。Task に Dispatch が発行済みの場合は拒否します。configuration が既存 bundle と一致しない場合は、先に [READY Task の設定再承認](approval-bundle.md)を行ってください。

request は次の形式です。`unavailable` は、外部 manifest に参照だけが残り、artifact を保存できていなかったことを人間が申告する記録です。role ごとに1件まで指定できます。hash は prefix なしの小文字64桁、`evidence` は欠落を確認した根拠や参照です。秘密情報や会話全文を入力しないでください。

```json
{
  "version": 1,
  "taskId": "approved-task",
  "actor": "human",
  "source": "human:packet-recovery",
  "reason": "Recover unavailable packet artifacts",
  "unavailable": []
}
```

欠落参照がある場合は、`unavailable` の各要素に `role`（`CODER` / `VALIDATOR` / `REVIEWER`）、`hash`、`evidence` を指定します。Taxion の store に現物がある hash を unavailable として記録することはできません。外部 manifest 自体を書き換えたり、その hash から packet 本文を復元したりはしません。

```sh
taxion task authorize-packets --request packet-lifecycle.json
taxion task authorize-packets --request packet-lifecycle.json --confirm '<confirmationToken>'
```

preview は DB・WAL・SHM に書き込みません。token は Task の現在の record hash、immutable proposal reference、bundle hash、baseline / initial tree、scope / acceptance digest、欠落参照、申告者・出典・理由、`reviewerApprovalPolicy` の全体に束縛されます。`sha256:` に続く digest は認証用 bearer token ではありません。確認時に basis を再計算し、異なれば保存しません。同じ承認の再送は重複追加しません。

確認後に追記する lifecycle 承認は Task とは別の記録です。元 Task の objective、scope、acceptance、baseline、state READY、proposal hash、decisions、既存 ApprovalBundle、Project config は変更しません。

### 派生 Reviewer の承認範囲

preview の `reviewerApprovalPolicy: same-contract-reviewer/v1` は、実 Coder evidence の検査後に作る派生 Reviewer Task の bundle 追加も承認します。Controller は同じ objective・scope・acceptance と、完全に一致する configuration を要求し、元 proposal reference と lifecycle 承認 hash を出典にして派生 Task 専用 bundle を追記します。元 Task の intake confirmation は変更せず、`sha256:` 形式の元参照がある場合は派生 Task ID に束縛した confirmation を作ります。intake receipt を持たない manifest 承認の v1 Task では receipt を創作せず、prefix なしの元 proposal reference をそのまま bundle に保持します。既存の bundle 検査はそのまま適用します。

この承認は repair の契約変更や configuration の権限拡張を認めません。repair / re-review、tool、configuration の追加 Task ID などに既存の承認が必要なら、その条件を満たすまで停止します。

## Controller による逐次保存と検証

承認済み Task から既存の bounded review Flow を開始すると、次の順序で処理します。

1. Coder packet を保存し、Dispatch 発行と同じ transaction で instruction hash に束縛する。初回の `previousEvidenceHash` は空文字列とする。
2. Coder の成功結果と Controller の実行済み validation / Git evidence が保存された後、その結果の canonical hash を predecessor とする Validator packet を別 transaction で保存する。
3. Controller が非モデルの Validator として、保存結果の receipt、Task の検証契約、現在の Git evidence を検査する。既存の `Finish` が実行した validation command の結果を使い、モデルの自己申告を成功 evidence として代用しない。
4. 合格した検査結果を versioned GateReport として追記し、その canonical hash を predecessor とする Reviewer packet と派生 Reviewer bundle を保存してから Reviewer Dispatch を発行する。
5. Reviewer の worktree 準備前、runner の runtime 生成前、runtime start / resume / send 時に保存 packet と Dispatch / predecessor を照合する。

Validator は新しいモデル session や Dispatch ではありません。検査失敗時に成功 report や Reviewer packet を作りません。packet の欠落、改変、不一致、未知の version、実 evidence の欠落は fail-closed です。packet、lifecycle 承認、audit の削除・置換・上書きも transaction で拒否します。

Reviewer configuration preflight は、従来の明示的な追加 Task 承認に加え、matching な lifecycle 承認による同一契約・完全一致設定の Reviewer 委譲も照合します。preflight は派生 decision や未来の packet を保存しません。実際の Reviewer を発行するときだけ、lifecycle hash に束縛した Reviewer 専用の configuration decision を追加し、通常の設定照合と実行入口の検査を通します。元 Task の契約、decision、ApprovalBundle、Project 設定は変更しません。この委譲は別 Flow の Reviewer、設定や tools の拡張、未承認の repair 契約を許可しません。

```sh
taxion role-packet list --task approved-task
taxion role-packet read --expected-hash '<packetHash>'
```

`list` は承認履歴、欠落申告、Dispatch / instruction / packet の hash と検査記録を返します。未承認の既存 Task は `legacy-unverified`、lifecycle 承認済みは `lifecycle-authorized` と表示します。後者は「未来の packet まで検証済み」を意味しません。`read` は保存された canonical bytes を hash と照合して返します。

## 互換性と rollback

既存の packet create / verify と、lifecycle を明示承認していない Flow の動作は変えません。旧参照を保存 packet とみなす自動移行もありません。検証済み packet のない発行済み Dispatch の採用や、未保存 evidence からの再構成は対象外です。

開発版のレビュー専用継続では、停止を確認した元 Flow の成功済み Coder と、
その元承認に結び付いた Validator の保存証跡を再利用できます。後続 Task の契約・予算、
ApprovalBundle と packet lifecycle は別途承認してください。新しい Reviewer packet は
新承認に結び付け、前段の evidence hash には元の検証報告を参照します。
元の報告や承認を書き換えず、Coder と成功済みの検証コマンドを再実行しません。

対象 Git 成果物に加え、目的、scope、acceptance、検証コマンド、security 条件、
設定選択と出力方式の一致を要求します。これらの変更、取消未確認、元 packet・検証報告の
欠落や改変では拒否します。新しい予算の承認を旧検証の成功証明として使うことはありません。
保存 schema と packet の版は変更しません。旧版でこの継続を再試行しても、
`Controller validation evidence mismatch` になる場合があり、旧証跡の破損を意味するとは限りません。

lifecycle 記録のある workspace は新しい Records field を含みます。非対応版へ同じ DB を戻さず、必要なら承認前に取得した workspace snapshot を別配置で復元してください。現在の承認・artifact・audit を削除して rollback する操作は認めません。
