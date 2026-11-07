# 失敗からの復旧

実行入口と gate の拒否は、保存記録を変えずに理由を返す。復旧は、理由の分類を確認し、必要な承認または修正を行ってから再実行する。拒否を無視して Dispatch を発行し直さない。

## 実行入口の拒否

bundle-required の Task が実行入口で拒否された場合、code は次のいずれかである。

| code | 意味 | 次の操作 |
| --- | --- | --- |
| `unknown-stage` | 実行入口の stage が未知 | CLI と binary の版を確認する |
| `unknown-task` | Task が保存されていない | Task を承認する |
| `task-not-ready` | Task の状態が実行可能でない | 状態遷移を確認する |
| `no-matching-bundle` | 現在の basis を覆う bundle がない | 拒否された担当・設定と保存した承認の対象を照合し、未承認の差分だけを確認する |
| `invalid-bundle` | bundle の hash または版が一致しない | 保存記録を調査する |

拒否時は Dispatch を発行しない。Task、Flow、Dispatch の状態は変わらない。

通常の Flow 内のレビュー・修正・再レビューと持込み入力のレビューでは、
保存済みの契約と設定承認に含まれる内部遷移の認可を、発行直前の共通経路で追記する。
担当を設定承認の対象へ明示する必要があり、入力の受入れや実装成功だけで許可しない。
任意の packet lifecycle の限定委譲を、修正の許可に広げることもない。
一方、元の Flow 契約・設定・修正上限が既に許可する修正を、
任意 packet 機能を有効にしたという理由だけで拒否しない。

保存した RUNNING Flow の未開始 role は `gate resume --flow ID --json` で確認し、
`workflow resume --id ID` で同じ契約の続きへ進める。Coder 専用ではなく、
Reviewer、修正・再レビュー、持込み入力の検証後の Reviewer も対象になる。
packet lifecycle の追加承認は必要ない。明示的な hold は release し、
最初の send より前に作成済みの session は再作成せず同じ binding へ再接続する。
開始済みの実行、STOPPED Flow、FAILED の保存回答処理をこの操作で再送・上書きしない。

元の Task の再承認確認が `already-authorized` でも、別の担当の実行許可を示すとは限らない。
同じ確認を反復せず、拒否された Task ID、stage、設定、drift と保存済みの対象範囲を照合する。
停止済み Flow や発行済み指示をこの判定で書き換えたり、実行不明の処理を再送したりしない。

### 計画・調査の採用が拒否された

`advice-observation-missing` は出典となる観測の欠落、
`advice-answer-missing` は回答不足、`advice-answer-invalid` は不正な引用や形式、
`advice-observation-stale` は回答中の記録変更を表す。元の結果を保全し、
保存済み記録との比較や不足の補完を先に行う。同じ担当の再実行や同じ承認の
反復は必要条件ではない。正式な助言の記録がない資料は、新たな持込み入力として
扱えるが、元の実行・品質・権限の証明には使わない。

`advice-purpose-mismatch` の場合は、非実行の判断を修正指示へ転用していないか
確認する。`HOLD` や `REPORT` などは `workflow planner-action` で明示採用し、
担当を発行しない。対象や契約を変更する場合は、その差分に必要な承認を別に得る。

## gate が blocked を返す

`blocked` は入力または承認の不足を表す。`blockingConditions` と `nextRequiredApproval` を確認する。

- `missing-approval-bundle`: 元の正式な `conversation confirm --file confirmation.json` を先に照合・再実行する。未実行・未変更の READY Task に限り、確認履歴から不足 bundle を補完でき、同じ承認を取り直さない。元の要求や履歴がなければ推測で作らず、`taxion task authorize` で明示的に承認する。
- `reapproval-required`: drift を確認し、新しい token で再承認する。
- `quality-gate-unmet`: 同一 target の独立レビューと受入れ証拠を記録する。未検証のまま `pass` にしない。
- `handoff-held`: 次の role を一つだけ実行する場合は handoff を release する。release は実行中の role を取り消さない。
- `task-not-ready`・`task-not-approved`: Task の状態遷移または公開承認を確認する。

`quality-gate-unmet` では、対象の不一致だけでなく承認済み契約の受入れ条件も確認する。
旧契約に `ready` が示す必須の文書整合 criterion がなければ、
Flow が `COMPLETED`、Reviewer が `SUCCEEDED` / `APPROVE` でも品質ゲートは未達になる。
`workflow evaluate --id FLOW_ID --require-quality` の理由を先に読み、
成功した実装・レビューを診断のために再実行しない。
完了済み Flow の実行・公開ゲートにも評価の具体的な不足理由を表示する。
`targetSha` / `targetTreeSha` は保存済みの対象であり、現在の Git の再検証結果ではない。
命令形式4より前の成功結果も読めるが、Controller による検証・Git 証拠を
要求しない形式だったため、それだけでは品質合格にしない。

旧契約・結果・停止履歴を変更せず、品質ゲートの合格が必要な場合だけ、
不足する条件を含む新契約と同一対象の独立レビューを承認する。
持込み入力のレビューでは既存の成果と適用可能な検証証拠を再利用できるが、
旧レビューの承認を新条件へ付け替える正式操作はない。
コマンド不要と承認した `review-input.v2` の `identity-only` でも、
対象照合、受入れ条件、独立レビューの検査は省略しない。
追加の Reviewer・費用・期限が旧承認に含まれなければ差分承認が必要である。
`accept-saved-review` は限定した停止状態の結果受理であり、
完了済み Flow の条件追加や品質ゲート未達の迂回には使わない。

## gate が fail を返す

`fail` は保存記録の矛盾または前提の不成立を表す。自動で修復しない。

- `invalid-authorization-schema`: 未知の schema version か、矛盾した bundle がある。binary の版と保存記録を確認する。
- `approval-invalid`: bundle の hash または dimension が一致しない。再承認で上書きせず、まず記録を調査する。
- `flow-stopped`: 停止理由と保存済み findings を確認する。再開には新しい判断が必要である。
- `task-not-executable`: Task は既に完了、却下、取消のいずれかである。
- `evidence-unavailable`: 保存済み Flow の証拠を評価できない。Flow と Dispatch の結果を確認する。

## runner 終了後に回答だけが残っている

`conversation ask`・`conversation plan`・`conversation investigate` と request Planner は、回答の受理が不確実な場合、同じ workspace の実行を止める。cleanup の成功だけで回答を受理せず、失敗しても保存した回答や利用量を削除しない。`taxion request --status` で operation を確認し、元の送信を繰り返さない。

`taxion request recover ... --preview` は、保存した Copilot main turn と入力・Task・出典・元期限・利用量を照合する。新しい attempt receipt がない v0.8.2 の version-2 request には、元 session・native input の hash・元 process tree の停止証拠を指定する `--associate-legacy` がある。これは旧 receipt の捏造ではなく、新しい明示的な照合判断として保存する。binding のない旧直接質問や version-1 checkpoint は対象外となる。

同じ判断項目と preview hash の apply は回答・checkpoint・利用量・照合判断を一つの transaction で保存する。通常の継続は `--resolution continue`、その operation の終了は `--resolution cancel` で選ぶ。routine shutdown やnative 集計利用量が欠落・不完全な場合、不正な回答を未受理のまま残す場合は、停止証拠を示して `--confirm-stopped --accept-uncertainty --resolution cancel` を選ぶ。per-call usage から分かる利用量は下限として保持し、native 集計の不足や不確実性は unknown のまま記録する。既知の集計不一致は拒否する。未知の利用量をゼロ扱いしない。

モデルの送信・Abort、元期限・予算・承認の更新は行わない。期限超過後も期限前に完了した回答の照合と取消はできるが、継続実行はできない。実際の owner が lease を保持している場合、native 履歴が変化した場合、別の Flow が実行中の場合には apply を拒否する。解除後の作業は通常の gate を通す。旧版へ戻すために receipt を削除せず、更新前の整合した private backup を保持する。

保存した `RUNNING` は process が生きている証拠ではない。旧 runner の終了を確認し、
Flow・Dispatch・session・最後の受信記録を照合する。正式結果がない `RUNNING` Reviewer には
`workflow recover --file recovery.json` の `finalizeSavedResult:true` を使う。
入力形式は `workflow recover --help` を参照する。

Controller は元の Copilot 環境から同じ session の履歴を取得する。
pending work・tool・hook・transcript 修復を有効にせず、モデルへ再送しない。
保存済み実行観測との一致と、元の有効な提出期限内の最終回答・turn 終了、
正常 shutdown を要求する。回答ファイルや shutdown の申告だけでは受理しない。

`validationDeadline` は承認範囲内で設定した host 検証専用の期限であり、
モデル実行の期限延長ではない。通常と同じ結果検証、runtime 終了、管理 worktree 解放、
正式 receipt 保存を経て runner 所有権を回収する。入力検証や Coder は繰り返さない。
host 検証が開始済みで、検証済み結果の保存もない場合は、結果不明として再実行を拒否する。
保存結果を確認してから `workflow advance` へ進む。
`REQUEST_CHANGES` の受理は製品の承認ではなく、`maxRepairs:0` も変えない。
修正には元の指摘・候補を保持した、明示承認済みの範囲内の後続を使う。

既に正式 `FAILED` の Reviewer はこの確定操作の対象外である。
保存回答が `REQUEST_CHANGES` なら、
[`workflow record-failed-review`](cli-reference.md#failed-reviewer-の保存回答を追記する) の
preview と承認済み hash による追記を使う。元の失敗結果、利用量、Task、Flow は変更しない。
モデルへ再送せず、host 検証も繰り返さない。指摘は正式な追加記録になるが、
品質結果の受理・修正実行・Flow 完了にはならない。
元 native 履歴の完了、実行 anchor、期限、同一候補を照合できなければ拒否する。
回答・turn 終了が FAILED の `verified-result` 以前であれば、routine shutdown が
失敗後に記録されても、保存済み提出期限内・後続実行なしなどの条件を照合できる。
時刻拒否時は stdout の `failedChecks` と採用した `submissionDeadline` を確認する。
Flow の表示期限だけから提出期限を推測せず、`NOT_EVALUATED` の後続条件は未確認のままとする。
`--observation-timeout` は既定30秒、正の値で最大5分であり、呼出し元のより早い deadline/cancellation
を守る。CLI file/store read、Store open/migration、Copilot runtime setup、operation lease、
cleanup/output、Controller/Store mutex wait は上限外なので、
コマンド全体の厳密な wall-clock cap ではない。
Storeの読取りと追記は、文書・文字列プールを含むSQL文にも同じcontextを渡す。
context引数のない`Commit`・`Rollback`と、メモリ上の検証・JSON処理の強制中断は保証しない。
native/Git/SQLite の observation error には静的 phase と
persistence 診断が付く。`NOT_WRITTEN` は追記前または証明済み rollback、`COMMITTED` は成功 commit、
`UNKNOWN` は commit の成否不明である。`UNKNOWN` は保存記録を確認し、同じ hash だけで成功と推測しない。
運用機の原本を調べる権限や追記操作の承認は、開発側の合成検証とは別に確認する。

## Flow が停止している

runtime `Start` を試みる前の準備・出力先・runtime 構築の失敗だけなら、Flow は
`RUNNING`、同じ role の Dispatch は未開始のまま残る。`workflow budget` の
`preparation-blocked` 観測を確認し、承認済み範囲内の技術的問題を解消して同じ
`run` / `resume` を続ける。この場合に停止取消や後続 Task の再承認は要求しない。
実際の取消・期限・予算による停止と、runtime 開始を試みた後の実行不明は区別する。

通常 Flow で Coder の出力を正式受理した後、未開始 Reviewer の準備で停止した場合は、
持込み成果物用の `recover-review` ではなく、`reconcile-stopped` と既存の
`continueFromDispatchId` を使う。取消・後続作成の完全な入力例と停止条件は
[受理済み Coder 出力からのレビュー継続](cli-reference.md#受理済み-coder-出力からのレビュー継続)を参照する。
元 baseline と実行 commit が異なる snapshot では、元 baseline から完全 tree までの
全変更パスを保持する。Coder が今回申告したパスだけへ縮めない。

`review-input.v2` の診断用 `observe` では、コマンドの非ゼロ終了を
`OBSERVED` として保存できる。これは製品テストの合格ではない。
対象の改変、強制終了、期限超過、成否不明の実行は引き続き停止する。
`OBSERVED` で正式に発行した Reviewer の停止・期限再契約も、
下記と同じ `recover-review` で対象、目的、元のコマンド結果、累計消費を保持する。
入力コマンドを再実行して `PASSED` に置き換えない。

入力検証が `PASSED`、Reviewer が未開始の `ISSUED`、runner がない場合、
有効期限と既存承認を照合して、その同じ Flow の `workflow run` からレビューを開始する。
保存表示の `RUNNING` だけでは process の存在を判断しない。
導入更新だけを理由に新規 intake・plan、Coder、入力検証へ戻らない。
保存期限を過ぎていれば未開始でも無承認で実行せず、正式な停止後復旧と期限再契約を使う。
入力検証済みの持込み成果物 Flow で未開始 Reviewer に runner がなく、保存期限を過ぎた場合は、
`workflow advance --id FLOW_ID` がモデルを起動せず期限条件を評価し、`BUDGET_STOP` を保存する。
停止を示す非ゼロ終了と保存状態を確認し、`workflow recover-review --preview` へ進む。
検証済み成果物の未開始 Reviewer は予算停止からも復旧できるが、
新期限への承認、静止確認、同じ候補と合格 receipt の照合は省略しない。
通常 Coder Flow の Reviewer が既に `STOPPED/HOLD` なら `advance` で旧 Flow を復活させず、
上記の `reconcile-stopped`、後続の intake/confirm、`continueFromDispatchId` を使う。
新しい固定絶対期限を明示承認し、同じ Coder 成功結果と受理 receipt を照合する。
`CANCELLED` Coder の成果を持込み入力へ移した場合も、旧結果を成功へ書き換えない。

旧 runner の生存を確認でき、まだ `RUNNING` なら、`resource_approval_required` に応じて
時間と費用の不足を一度に確認します。`workflow budget --file` で明示した累計観測と
残工程・検証・レビュー・保存・回答待ちを見積もり、現在の工程期限前に一括承認を得ます。
`workflow extend` の preview hash を承認して適用すると、元記録を保持したまま
同じ Dispatch・session・runner の有効期限と native 上限を延長できます。
承認待ちの先回り取消や自動増額はしません。手順は
[実行を維持した期限・予算の延長](cli-reference.md#実行を維持した期限予算の延長)を参照してください。

正式停止した入力検証合格済みの成果物 Reviewer は `workflow recover-review` を使います。
未開始か実行済みかを正確に指定し、実行済みなら `continueStoppedExecution:true` を付けます。
期限切れ後は `deadlineRecontract` を同じ要求へ含め、必要な新期限と予算を一度に承認します。
候補・合格 receipt・保存解析・累計消費を保持し、Coder と入力検証は繰り返しません。
これは稼働 session の延長とは異なり、新しい Reviewer-only 後続を作る操作です。

`artifactSource` Flow は、持込み入力の実際の Controller 検証から開始できる。
`workflow show` の `artifact.validation` と `workflow budget` の `budget.artifact.validation` に、
`PENDING`／`RUNNING`／`PASSED`／`FAILED`、対象 hash、コマンド別の実測結果を保存する。
検証前には Dispatch がなく、持込み変更パスを Coder へ転記させる必要もない。
利用者作成コード・拒否済み結果の入力は、成功済みの Coder 結果とは扱わない。

`FAILED` または実行開始後の中断は、暗黙に再検証しない。中断後は旧 process の停止を確認して
`workflow recover` で保存済み runner を整合するが、結果不明の検証は終端停止する。
元入力・検証出力・旧履歴を保全し、新しい契約判断から通常後続を作る。
期限・予算・権限を無承認で変更せず、publication 承認も代用しない。

### Dispatch がない入力検証失敗からの修正用後続

対象は `artifactSource` Flow の `STOPPED / SAFETY_STOP`、`HOLD`、
`artifact.validation.status: FAILED`、`dispatchIds: []`、runner 解放済みの場合に限る。
`workflow show --id OLD_FLOW_ID` で保存状態を読み、検証コマンドの process も停止していることを確認する。
コマンドは外部 process の消滅を証明しない。`RUNNING` のまま中断した検証を失敗確定として流用しない。
既存の明示 `--store` を全操作で維持する。

別 worktree に未検証入力をそのまま保全し、新しい `DRAFT` 契約を用意する。
`supersedes` は失敗 Task、`createdFrom` は `flow:OLD_FLOW_ID` とする。
`baselineSha`、`targetSha`（元 baseline commit）、`targetTreeSha`、`targetChangedFiles` を維持する。
元の目的・受入れ条件・検証を引き継ぎ、通常の `CODER`／`REVIEWER` 担当にする。
入力を新しい Coder 成果・品質合格へ付け替えず、古い Coder Flow に置換元を戻さない。
元の承認や決定をコピーせず、正式な intake envelope を使う。

```sh
taxion conversation intake --help
taxion conversation confirm --help
taxion conversation intake --file intake.json
taxion conversation proposal --task NEW_TASK_ID
```

intake は `artifact-predecessor:NEW_TASK_ID` 決定を自動生成し、`flowId`、
`artifactHash`、`receiptHash` を提案へ固定する。`receiptHash` は対象・設定・検証 receipt を含む
旧 `artifact` 全体の canonical SHA-256 である。手入力・変更はしない。
完全な新提案 hash と実設定を提示し、差分承認を得てから確認する。

```json
{
  "taskId": "NEW_TASK_ID",
  "expectedHash": "sha256:ACTUAL_NEW_PROPOSAL_HASH",
  "actor": "human",
  "source": "human:ACTUAL_APPROVAL_REFERENCE",
  "reason": "Approve the presented bounded repair successor and configuration",
  "replacesFlow": "OLD_FLOW_ID",
  "additionalTaskIds": ["NEW_FLOW_ID-review-0"],
  "selection": {}
}
```

この雛形の `selection: {}` は実行可能な設定ではない。`conversation confirm --help` の完全形式に従い、
承認対象の実設定を入れる。必要ならその help にある Task-scoped `configurationDecisionId` を使う。
`target` はキーごと省略する。空 object、架空 Dispatch、`workflow feedback-target` は使わない。
confirm は提案固定 receipt、停止・所有権・保留、両 worktree の実入力同一性を再照合する。
旧 Flow・失敗 receipt は不変のまま、元 Task のみ `SUPERSEDED` にする。
保存提案にこの決定がない場合は未使用の新 Task ID で再提案し、新 hash を承認する。
古い DRAFT の決定を書き換えない。

```sh
taxion conversation confirm --file confirm.json
taxion workflow plan --file flow.json
# 実際に生成した工程 hash の承認後、その hash を stagePlanHash に設定する。
taxion workflow start --file flow.json
taxion workflow run --id NEW_FLOW_ID --builtin-prompts --controller-validation-submission
```

`flow.json` は通常 Flow とし、`artifactSource` と `continueFromDispatchId` を省略する。
`maxRepairs: 0`、`stageBudget.version: 1`、初回 Coder 1回・Reviewer 1回の承認範囲、
必要な工程見積り、新しい期限・実行枠を設定する。
`continuationChange` に今回完了可能な理由、`savedOutputDecision` に失敗入力を保持して修正する判断を記す。
scope 外ファイル・編集権限・正確な Reviewer 設定 ID など、変わる点だけを追加判断する。
未生成 hash や旧実行枠を新実行の許可に使わない。

Coder の `changedFiles` は baseline から最終 snapshot までの全差分であり、持込みと追加編集量を区別する。
未実施の Controller 検証・独立レビューを成功申告せず、それを待つことだけを理由に `SUSPENDED` ともしない。
製品編集は承認範囲内の Coder、実検証は Controller、独立判断は Reviewer の担当である。
外側 Controller の管理 shell 権限や無断編集を追加しない。
未知の実行状態、入力 drift、scope 不足、認証・上限・期限不足では停止し、旧 Flow は再送しない。

### その他の停止と継続

Coder が製品の実装をせず Taxion の管理 skill や管理 DB・session directory を調べて停止した場合は、役割と指示の混同を先に調べる。API 接続成功は、役割が正しく割り当てられた証拠ではない。管理用 shell や広いファイル権限の追加、同じ構成の再試行を第一の対処にしない。製品の目的・担当・必要なファイルへの既存の権限・controller による検証の分担を照合する。ログの `denied-interactively-by-user` だけで実際に利用者が拒否したと断定しない。

Copilot Coder／Reviewer には同名の `taxion` 管理 skill を除外し、Dispatch の共通指示にも役割の境界を明記する。他の製品 skill を一括無効化せず、権限も増やさない。設定や指示の修正だけで終端 Flow の再送や旧 session の書換えは許可しない。未実施工程を完了に変えず、停止・利用量・成果を保全して正式な後続を準備する。

停止した Flow は `workflow show --id <flow-id>` で状態、停止 code、理由、Dispatch を確認し、`workflow evaluate --id <flow-id>` で approved/consumed/remaining と usage の完全性を確認する。`BUDGET_STOP` は ceiling 到達または usage 不明を意味する。不明な nano-AIU remaining は0として扱い、既存 bundle で新しい session/Dispatch を再開しない。部分実行の checkpoint と audit は保持し、新しい Task 契約・予算が承認されてから続行する。`workflow terminate` は未確認・未開始の Coder Dispatch に限る。

`workflow recover` は記録済み終端結果から runner の所有権を回収する。
未確定の保存回答がある場合は前節の結果確定を先に行う。
停止条件の解除や同じ実行の再送は行わず、新たなモデル作業にはその範囲をカバーする承認が要る。

`workflow reconcile-stopped` は、`STOPPED / SAFETY_STOP` と `HOLD` が保存され、初回の snapshot 入力を持つ Coder Dispatch だけが `ISSUED` のまま残った場合を対象とする。ACK・実行開始・session・結果・runtime receipt がある場合や、他の実行が稼働中の場合には使えない。人間が継続中の実行がないことを確認し、その根拠と配送不確実性の受入れを明示してから、preview の完全ハッシュを承認する。コマンド自身が外部実行を停止・照会するわけではない。

適用すると Dispatch は `CANCELLED`、Task は `BLOCKED` となり、判断を追記保存する。元の停止理由・`HOLD`・承認・期限・予算は変更しない。未配信・未実行・無課金を証明せず、保持した tree はこの Dispatch の新成果ではなく、元の承認済み入力として扱う。後続 Task は別 worktree に同一入力を保全し、新提案と確認の両方に `reconciliationHash` を含める。新しい実行には別途承認が要る。通常取消だけではこの入力継承の根拠にならない。request と手順は [CLI リファレンス](cli-reference.md#停止済み初回指示の整合回復)を参照する。

`workflow repair-status --id <flow-id>` の
`reviewContinuationCandidate` に成功済み Coder の Dispatch ID が表示される場合がある。
保存結果の receipt と停止・所有権・引継ぎ保留の条件を確認した、レビュー専用継続の候補であり、
実行許可ではない。新しい Task・レビュー予算の承認と対象 Git 成果物・設定の再照合を経て、
`continueFromDispatchId` に候補を指定する。成功済み実装を最初からやり直す必要はない。
対象が変わった場合や停止を確認できない場合は、候補を無条件で流用しない。
停止した `workflow run`・`step`・`resume` は、期限エラーでも stderr に状態と次の操作を表示する。

過去の結果や検証証拠を保ったまま、指示の入れ子だけを省く既存の
`--compact-context` も利用できる。ただし既存 session の option と設定 hash を
通知なしに変えて再接続しない。モデル内部の思考状態や消費済みトークンは引き継がない。

Copilot の `workflow run|step|resume` と `runtime` は、明示的な `--review-progress` で
Reviewer の確認記録を保存できる。Reviewer は専用 tool で、確認済み範囲、未確認範囲、
指摘と根拠を累積した記録として送る。Controller はその session、指示、成功済み Coder の
commit・tree と実際の作業場所を照合し、資格情報を除去して追記保存する。
作業ファイルへの書込みやレビュー承認は許可しない。既存 session への再接続では同じ option を指定する。
OpenCode はこの専用 tool に対応しておらず、指定すると拒否される。

新しい承認を得たレビュー専用継続では、最後の有効な確認記録を後続 Reviewer の指示へ渡す。
記録は Reviewer の自己申告であり、確認済み範囲を独立検証済みとは扱わない。
必須の受入れ条件は改めて独立に検証する。記録のない旧実行は `missing` と明示し、
未確認事項を完了に置き換えず、成功済み Coder の結果と検証証拠だけを引き継ぐ。
記録の破損、対象の変更、取消失敗、所有権未解放では確認記録を流用しない。
tool が呼ばれる前の中断や保存失敗では、それ以前に受理した記録までしか残らない。

`workflow repair-status` の `stopCause` は `time-limit`、`usage-limit`、
`review-findings`、`other` を区別する。保存された停止理由を使うため、
観測時に期限を過ぎたことだけで過去の停止原因を時間切れへ変更しない。
これらの変更によるトークン消費や費用の削減率は未測定である。

handoff が hold されている場合、hold は次の role を止めるだけで、実行中の role を止めない。release する前に、表示された instruction、draft、結果の対象を確認する。

## 現在の入力は残るが部分成果の証拠がない

保存した停止結果と現在の入力は別の証拠として扱う。旧範囲への不適合で
部分成果の記録が失敗していても、結果・処理識別子・利用量は変更しない。
`workflow observe-input` を含む版では、新たな観測範囲と停止確認を示して
現在の入力を取得し、別契約の後続へ渡せる。
未開始専用の `reconcile-stopped` に、実行済みの指示を押し込む操作ではない。

最初に保存済みの作業・処理・結果受理記録を照合する。観測の確認結果には、
不足する項目、証拠の出所、必要な操作、新しい承認の要否が含まれる。
既存記録で解消できる項目を先に確認し、残る権限変更だけをまとめて確認する。
正式な結果受理記録の欠落、所有権の残存、禁止パス、対象変更は拒否の理由として残す。
証拠がないことを実行なし・利用量ゼロと読み替えない。

観測の承認・保存後は、後続の依頼と確定に同じ `observationHash` を指定する。
完全な対象、出典、目的に合う受入れ条件・検証方針を確認し、
新しい契約・設定・計画の承認を経て通常の実装または持込み検証へ進む。
記録だけでは元の依頼は完了しない。必要な検証、独立レビュー、
元の受入れ条件まで確認する。具体的な入力は
[観測と受入れのリファレンス](cli-reference.md#入力の観測と受入れ判定)に従う。

## OpenCode ACP が応答前に停止した

RPC の拒否では RPC code と `RPC provider signal`、応答のない切断では
`stderr provider signal` を確認する。`free-usage-limit-reported` は既知の制限文面を
受け取ったことを示し、`unknown` は取得できた範囲では分類できないことを示す。
認証・通信・枯渇を推測せず、別の直接実行のエラーを過去の ACP 停止原因へ転記しない。
標準エラーは末尾4096バイトだけをメモリで照合し、原文は表示・保存しない。

これらの理由は既存の実行出力・`failed` 記録で確認できる。利用量欠落は
`acp_usage_unavailable` であり、ゼロや無料とは扱わない。取消・期限切れはその理由を維持する。
無料制限の報告だけを根拠に有料経路・別モデルへ切り替えたり、同じ停止を自動再送したりしない。
部分成果と旧記録を保持し、必要な構成・費用・期限の差分を承認してから後続を用意する。
過去の実証と今回の未実施範囲は [バックエンド別の確認記録](../develop/backend-acceptance.md)を参照する。

## lane が失敗した

`taxion workflow lanes` は、いずれかの lane が失敗、取消、timeout になると execution gate を閉じる。診断には失敗した lane と redacted な理由が含まれる。

- `repository-identity`: repository の不一致、または埋め込み credential を除く。
- `baseline-worktree`: baseline SHA、branch、worktree の指定を直す。worktree は repository root の内側に置く。
- `contract-consistency`: Task と最新 instruction の契約を一致させる。Dispatch を黙って書き換えない。
- `handoff-preparation`: 有効な Dispatch、return format、instruction hash、handoff の状態を確認する。

lane は read-only であり、失敗しても保存状態は変わらない。

## rollback

入力観測記録を保存した作業領域は、その形式を理解する版で扱う。
旧版へ戻すために `inputObservations` や承認済みの参照を削除しない。
記録を含むデータベース、Git オブジェクト・保持参照、設定を保全し、
旧版を必要とする場合は更新前の整合した保全コピーを別配置で使う。

戻し先は導入する配布物の manifest で確認する。稼働実行を停止・保全し、新版の実行・承認・監査履歴を旧版で操作しない。旧版が新形式を理解できるとは限らないため、必要なら更新前の整合したバックアップを別配置で使う。履歴を削除して旧版に合わせない。具体的な導入・再登録は[配布物の rollback と制約](binary-provenance.md#rollback-と制約)に従う。

gate report と lane report は観測であり、失敗しても状態を変更しない。復旧の判断は人間が行う。
