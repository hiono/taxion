# 10 — Task Graph の登録・遷移・保持

Task Graph と Ledger は SQLite の `workspace.db` に同時に保存する。
既存の `.taxion/ledger.json` と `.graph.json` は初回移行に限り読み込み、
移行確認のため一時的に残す。これらの JSON は移行後に更新せず、正本やバックアップとして
利用しない。取り込みを確認した後に削除する。以後の更新元は SQLite workspace となる。
設計の理由と移行の決定は
[ADR-0022](adr/0022-sqlite-atomic-workspace-state.md) を参照する。

## 登録と読み出し

既存の `task create` で Task を作成し、その ID・title・repo と一致する
Graph の Task を `task track --file REQUEST.json` で登録する。
request は `task`、`actor`、`reason`、任意の `concern` を持つ。
未知のキーと複数の JSON 値は拒否する。
新規登録は DRAFT または BLOCKED に限る。

```json
{
  "actor": "human",
  "reason": "保守性の懸念を保留して追跡する",
  "task": {
    "id": "TASK-ID-FROM-LEDGER",
    "title": "名称の責任分担を調査する",
    "repo": "owner/repository",
    "type": "INVESTIGATION",
    "issue": "Issue #47",
    "status": "BLOCKED",
    "createdFrom": "request:1",
    "deferredReason": "現在の実装 scope 外",
    "resumeCondition": "調査範囲の承認",
    "nextAction": "調査範囲を確認する"
  },
  "concern": {
    "id": "concern:1",
    "source": "request:1",
    "text": "後続の保守で責任を識別しにくい",
    "taskId": "TASK-ID-FROM-LEDGER"
  }
}
```

`task graph` は現在の Tasks、concerns、履歴を JSON で表示する。
scope out した Task も消さずに返す。concern の ID、起点、本文、
登録済み Task ID が不足する場合は Task lost として登録全体を拒否する。
登録入力に含まれる懸念と Task の対応を検査する。

`task plan --repo owner/repository` は、SQLite の Task Graph と参照先の Memory から
後続計画を再構成する。各 Task に保存済み契約、判断、懸念と出典、状態変更の履歴、
Memory の本文・版・出典、発行済み Dispatch の契約と結果を添える。
保留 Task は元の ID・保留理由・nextAction・resumeCondition を使う再評価手順に含め、
別作業の結果保存や Session の再作成後も同じ記録を参照する。
依存待ちには必要な状態と観測した現在の状態を示す。

計画の `action` は次に行う確認を表す。`REASSESS_DEFERRED` は保存済み再開条件の評価、
`ASK_HUMAN` は未選択の質問への回答確認、`RESTORE_CONTRACT` は既存判断を使った契約の準備を示す。
選択済みの Human decision は判断記録とともに復元し、新たな選択が必要な質問を Human に提示する。
`DISPATCH` は READY の実行候補を示し、実行時には通常の発行検査と Git 開始照合を行う。
`gaps` は補完が必要な scope・acceptance・証拠・SHA・権限・検証コマンドなどの項目を示す。
計画生成は Task の状態と権限を維持し、保留解除や承認を通常の記録済み遷移で行う。

過去のレビューは、その Dispatch ID、instruction hash、対象 SHA、scope、acceptance、
findings と証拠を含む発行済み契約・結果の組として返す。
後続計画ではこの出典を使って修正内容を確認し、現在の成果物 SHA に対応するレビューを要求する。
必須 Memory の欠落・失効・記録済み矛盾は Task ID を含むエラーとして返す。
HTTP の `GET /api/tasks/plan?repo=owner/repository` と ACP の
`taxion/plan`（`{"repository":"owner/repository"}`）も同じ計画生成を使用する。
これらの読み出しは SQLite workspace と登録済み Task Graph を使用し、
役割 Session の履歴から独立して動作する。

共通の計画出力には `observation.startedAt`・`completedAt`（UTC）と
`contentHash` を付ける。時刻は workspace と Memory の読み出し・計画構成の区間を示す。
`contentHash` は `observation` を省いた計画 JSON の SHA-256 を
`sha256:` に続けた小文字の hex とする。同じ返却内容なら時刻や再起動に依存せず同じ hash になり、
含まれる契約・判断・懸念・依存状態・Dispatch 結果・Memory が変われば hash も変わる。
フィールドと配列の順序も返却内容の一部であり、意味的に同じ並べ替えを同一視しない。

これは [ADR-0024](adr/0024-human-handoff-control-and-side-questions.md) の質問用観測の基礎である。
Task・Dispatch の状態、履歴、承認は変更せず、SDK や Git のライブ確認を行わない。
workspace と Memory は別に読み出すため、両者の原子的な snapshot を保証しない。
既存の Memory 読み出しは schema の初期化・移行を伴う場合があり、
ファイルシステム全体の無書込みを保証する操作ではない。
hash は返却内容の識別子であり、未参照の policy、実効認証、実行中の出力、
次の指示案の版、実行許可や現在性の証明ではない。保留・反映・発行時の再照合は別途必要である。
自然言語の脇の質問・追加調査・本流への反映は #74、受け渡し制御は #73 で実装する。

## 関係と実行契約

Task は ADR-0021 の識別、担当、scope、acceptance、証拠、対象 SHA、
判断、外部記録を保持する。
`parentId` から children と siblings、
`dependsOn` の `taskId`・`state` から blocks を導出する。
必要な依存状態は APPROVED または COMPLETED とし、
APPROVED の依存は COMPLETED に進んだ後も満たされる。
参照不存在、自己参照、循環、同一依存の重複を拒否する。

READY には `owner`（CODER または REVIEWER）、objective、scope、
acceptance、requiredEvidence、exitCondition、
`authorizationId` と同じ ID の decision を要求する。
dispatch 前には branch、worktree、baselineSha も要求する。これらは Task の契約から
Dispatch 指示へ複写される。targetSha は dispatch 時点で未確定でもよく、
指示にはその時点の値を保存する。
`validationCommands` は Human が許可した検証コマンドを実行順に保持する。
Dispatch の発行には一つ以上のコマンドを要求し、controller は worktree を作業ディレクトリとして
`sh -c` で実行する。コマンドの権限は controller の実行環境に従うため、
Task の承認時にコマンドの副作用も確認する。

新しい Coder Dispatch は `commitMessagePolicy: "conventional-contextual-v1"` を
不変の instruction snapshot に保存する。Taxion が作成・受理するコミットは
Conventional Commits の subject と Contextual Commits の body を使用する。
subject は `type(scope): description`（scope は任意、破壊的変更は `!`）とし、
空行の後に実際の意図・判断・制約を表す action line を一つ以上記録する。
action line は `intent(scope):`、`decision(scope):`、`rejected(scope):`、
`constraint(scope):`、`learned(scope):` のいずれかを使用し、
任意の Git trailer は action line の後に置く。

```text
feat(parser): preserve source references

intent(parser): restore earlier decisions after session recreation
decision(parser): retain the issued source version in SQLite

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>
```

controller は実行開始 SHA から成果物 SHA までの全コミットを Git から読み、
subject・body・action line の形式を検査する。最新コミットだけでなく、
途中のコミットも結果の受理条件に含める。
形式違反は commit SHA と理由を持つエラーとして返し、成功結果の保存を停止する。
内容の正確さと判断の根拠は独立 Reviewer が確認する。
既存 repository の開始 SHA 以前のコミットと、
この policy を持たない保存済み Dispatch は発行時の契約を保持する。

decision は一意の ID、source、text を持ち、修正した判断は新しい ID で追記する。
人間の選択が必要な場合は `humanDecision` に質問、選択肢、推奨選択肢と理由を記録し、
Task を `HUMAN_DECISION_REQUIRED` にする。選択後は option ID と対応する decision record を
追記してから READY へ戻す。
`memoryRefs` は `lesson:ID`、`decision:ID`、`guardrail:ID`、`concern:ID` の形式で
Memory 記録を参照する。Task Graph は ID を保持し、Dispatch 発行時に参照先の存在、
状態、version、source refs を照合する。source refs が空の記録は発行を停止する。
発行済み Dispatch の instruction snapshot は
選択した Memory の title・body・scope・status・version・source refs を保持し、
承認済み guardrail には構造化 predicate を含める。
guardrail の参照先は承認済みとし、Memory reference を使う workspace では Memory DB を読み込む。
`memory conflict` に登録した矛盾は人が解決してから、その Memory reference を dispatch に使う。
issue は URL、`Issue #番号`、`INHERITED: Issue #番号`、
`PENDING: 登録TaskID`、または理由付きの `NOT_REQUIRED:` を使う。
PENDING は登録 Task の COMPLETED を依存に持ち、実行前に実際の参照へ更新する。
局所検査は参照形式と NOT_REQUIRED の理由記載を確認する。外部 Issue の状態は GitHub 上で確認する。

`deferredReason` を持つ Task は BLOCKED と nextAction・resumeCondition を要求する。
再開時は BLOCKED のまま `task track` で保留を解除し、
判断と理由を残してから READY へ遷移する。
却下・cancel・置換へ進めても以前の保留理由と履歴を保持する。
契約は DRAFT・BLOCKED・HUMAN_DECISION_REQUIRED の状態で編集する。
active と terminal の Task は履歴を維持し、変更は後続 Task として登録する。

## 上限付きレビュー進行

`workflow start --file REQUEST.json` は、READY の Coder Task に対する
独立レビューと修正の許可を保存し、最初の Dispatch を発行する。
Task の `owner` は `CODER`、`reviewer` は `REVIEWER` とする。
初回実行用の Task を使用し、以前の実行からの後続作業は出典を持つ新しい Task として登録する。
`maxRepairs` は追加の Coder 実行回数、`maxTokens` は観測した input/output token の合計、
`timeoutSeconds` は開始からの経過時間の上限を表す。
Human は Task の実行契約とともに、この進行の上限と権限を承認する。

```json
{
  "id": "application-flow",
  "taskId": "TASK-ID",
  "repository": "owner/repository",
  "reviewRoot": "/absolute/path/reviews",
  "maxRepairs": 2,
  "maxTokens": 50000,
  "timeoutSeconds": 1800,
  "authorization": {
    "id": "human-flow-authorization",
    "source": "human:request",
    "text": "保存済み scope と acceptance を保持し、独立レビューと最大2回の修正を許可する"
  }
}
```

`workflow advance --id application-flow` は最新の保存済み結果を照合し、
次の Dispatch を発行する。Coder の結果から、確定した SHA の独立 Reviewer Task を登録する。
Reviewer の `REQUEST_CHANGES` は、元の acceptance・scope・検証コマンド・Memory 参照を維持して
Coder の修正 Dispatch を発行する。指摘は元の Dispatch ID・instruction hash・対象 SHA・
結果とともに判断記録へ保存し、修正と再レビューの指示に含める。
修正の開始 SHA は前回成果物 SHA とし、修正後の SHA ごとに別の Reviewer worktree を使用する。
`APPROVE` は worktree の解放確認、現在の対象 SHA、acceptance、必須証拠、子 Task の完了を
照合して、実装 Task を `APPROVED`、`COMPLETED` へ進める。
Reviewer Task と Dispatch はレビューの実行契約・証拠として別に保持する。

Graph の `reviewFlows` は開始時の契約、出典付き許可、開始時刻と期限、
修正・token の上限、修正回数、実行順の Dispatch ID、進行状態と理由を SQLite に保持する。
`workflow show --id application-flow` と後続計画からこの記録を復元できる。
進行中の Dispatch に対する advance の再実行は、保存済みの同じ Dispatch を返す。
停止状態は再起動後も保持し、追加の実行は新たな許可を持つ後続 Task で扱う。

上限到達、scope・acceptance・権限の変更、新たな判断、実行の失敗・cancel・停止要求、
Reviewer の `BLOCKED`、必須 Memory の失効では、理由を持つ `STOPPED` を保存する。
input/output token を取得できない実行は利用量不明として停止し、必要な証拠の確認を要求する。
実行中 Dispatch の安全停止は `runtime stop` で確認し、保留した worktree と証拠を調査する。
`workflow advance` は Dispatch の発行境界で上限を照合する。
`workflow run --id application-flow --coder-prompt-file CODER.txt --reviewer-prompt-file REVIEWER.txt`
は役割別の SDK adapter を作成し、保存済み指示の実行、イベント購読、最終応答の照合、
次の Dispatch 発行を連続して行う。各実行の終了時に SDK 接続を閉じる。
途中の説明はイベントとして表示し、turn の最終応答を結果の採用に使う。

run は保存済み期限を context の deadline に設定し、usage イベントの保存後に token 上限を照合する。
実行中は1秒ごとにも進行状態・Task 契約・必須 Memory を確認し、
上限到達や契約変更・停止要求で `runtime stop` と同じ停止処理を行う。
token 上限は SDK の観測済み input/output token に基づくため、
提供側の利用量イベントが届くまでの消費量は未測定である。
利用量の欠落を含むイベントは明示的な停止として扱う。
失敗した安全停止は `CANCEL_REQUESTED` とエラーを保持し、
停止を確認できた実行は `CANCELLED` と停止証拠を保存する。

runner は Graph の `runnerId` を取得して単独で進行する。
別 runner の run と手動 advance はこの所有者を照合する。
正常終了とエラー処理の完了後に所有者を解放し、
プロセスが強制終了した場合は保存済み所有者を残して二重実行を防ぐ。
再開時は以前の SDK 実行と worktree・停止証拠を調査し、
`workflow recover --file RECOVERY.json` で所有者を回復する。
Human は元のプロセスの終了を確認し、active Dispatch を
`runtime stop` または実行結果の保存で終了状態にしてから回復を実行する。
実行前の ISSUED Dispatch は通常の `dispatch transition` で取消を記録できる。
SUSPENDED や CANCEL_REQUESTED は active な実行として照合し、停止確認を要求する。

```json
{
  "flowId": "application-flow",
  "expectedRunnerId": "workflow show で確認した runnerId",
  "previousProcessStopped": true,
  "actor": "human",
  "reason": "元プロセスの終了と Dispatch の停止を確認した",
  "evidence": ["process-stop:確認記録", "runtime-stop:SDK-SESSION-ID"]
}
```

回復は保存済み runnerId との一致と、flow に属するすべての Dispatch の終了を検査する。
確認済み所有者、actor、理由、証拠、時刻を `recoveries` に追記し、
runnerId を解放する。古い確認入力の再送は新しい所有者を解放できない。
保存済み期限・token 上限・修正回数・flow 状態を維持するため、
停止済み flow の回復はその実行を再承認する操作と分けて扱う。
成功結果が保存済みなら通常の advance でその SHA の独立レビューへ進み、
取消・失敗の結果は通常の停止判定で Human への確認に戻る。
role prompt と `--coder-tools`・`--reviewer-tools`、任意の `--cli`・`--base-directory` は
既存 SDK adapter の設定・権限検査を使用する。
Coder の Git 操作を controller 管理ツールへ接続する作業は #53、
実 SDK で一連の動作を確認する作業は #54 で扱う。

### 次の受け渡しの保留と解除

`workflow handoff --file REQUEST.json` は、既存 flow の受け渡しを
`HOLD` または `RELEASE` として保存する。最初の保留は次の形式とする。

```json
{
  "flowId": "application-flow",
  "id": "human-hold-1",
  "expectedChangeId": "",
  "action": "HOLD",
  "actor": "human",
  "source": "human:intervention-1",
  "reason": "互換性を確認してから次へ渡す"
}
```

解除は別の入力 ID を使い、`expectedChangeId` に最後の変更 ID を指定する。

```json
{
  "flowId": "application-flow",
  "id": "human-release-1",
  "expectedChangeId": "human-hold-1",
  "action": "RELEASE",
  "actor": "human",
  "source": "human:continue-1",
  "reason": "保存済みの契約と上限で続ける"
}
```

`workflow show` と `task plan` の `reviewFlows[].handoffChanges` は
変更 ID、直前の ID、操作、actor、出典、理由、controller が付けた UTC 時刻を返す。
最後の操作が `HOLD` なら保留中であり、flow 自体の `RUNNING` と区別する。
計画の `HOLD_HANDOFF` は、現在の実行を観測して後続を待つ手順を示す。
terminal Task の結果は従来どおり `RETAIN` として残す。

変更は SQLite workspace transaction で比較・保存する。
古い `expectedChangeId`、入力 ID の内容違い、状態が変わらない操作、
不正な操作・空の出典・時刻の指定を拒否する。
同じ内容の再送は最新の flow を返すだけで、以前の操作を再適用しない。
解除後に別の保留を記録した場合も、古い解除の再送でその保留を解除しない。
複数接続の競合では SQLite の busy error を返す場合がある。
失敗は保留受付ではなく、最新状態を確認して再送する必要がある。
actor と source は既存の構造化記録方式であり、承認者の認証を証明しない。

保留は flow の実装 Task と、その flow に属する Reviewer Task に適用する。
別の Task の独立作業まで保留しない。通常の Dispatch 発行、SDK session binding、
未開始 session の attach、Reviewer worktree の作成、実行開始の遷移でも検査する。
発行済みでも未開始の指示は保留中に開始せず、
現在実行中の ROLE の観測・結果保存・安全停止は維持する。
保留中の同じ session への追加 turn も拒否する。

runner は現在の許可済み作業を継続し、usage と最終結果を通常どおり保存する。
後続へ進む境界では handoff-held error と flow を返し、runner claim を解放する。
保留だけでは `STOPPED` や Dispatch の `CANCELLED` に変更しない。
未開始 session が既に binding 済みなら、解除後の run は同じ role・worktree・
configuration hash を検査して再接続し、勝手に新しい session へ置き換えない。
解除は続行を可能にする記録であり、実行そのものは別の run または advance で行う。

保留中も期限、token 上限、契約・Memory 変更、判断待ち、失敗・取消の停止判定を維持する。
保留時間も既存期限に含める。安全停止した flow の解除は保留だけを取り除き、
`STOPPED` を `RUNNING` に戻さず、新たな実行許可の代用にしない。
現在の実行を止める場合は別途 `runtime stop` を使い、停止証拠を確認する。

実行開始の transaction と保留の transaction は順序付けるが、
保存済み `RUNNING` と SDK `Send` は外部操作をまたぐため原子的ではない。
開始の保存が先なら、その後の保留は現在の実行を許容し、後続を止める。
SDK の実際の受付時刻や停止完了を保留の成功だけで保証しない。
「いま止める」と「次を待つ」を一つの操作として扱わない。
自然言語の入力受付、指示案の版管理・対象付き反映、会話 UI は #73、
脇の質問と追加調査は #74、実 SDK の介入受入れは #54 に残る。

### 保存済みの出力を指定した Human の補足

`workflow feedback-target --id application-flow` は、最新の保存済み Dispatch と
その結果を変更せずに読み出す。`flowId`、最新の `expectedChangeId` と
`target` を返す。`target` は Dispatch・Task・repository・recipient、
instruction hash、状態・更新時刻、指示の対象 SHA、結果の出力 SHA と result hash を含む。
result hash は保存済み `DispatchResult` の JSON に対する SHA-256 で、
`sha256:` に続く小文字の hex とする。結果が未保存なら出力 SHA と result hash は空である。
同じ SHA でも verdict・findings・証拠が変われば result hash は異なる。
runtime の途中出力、未保存の Git 変更、脇の質疑を観測したことにはしない。

`workflow feedback --file REQUEST.json` は、この観測から `flowId`・
`expectedChangeId`・`target` をそのまま使い、次の情報を追加する。

```json
{
  "flowId": "application-flow",
  "id": "human-feedback-1",
  "expectedChangeId": "",
  "target": {
    "dispatchId": "application-flow-dispatch-0",
    "taskId": "TASK-ID",
    "repository": "owner/repository",
    "recipient": "CODER",
    "instructionHash": "feedback-target が返した値",
    "status": "ISSUED",
    "updatedAt": "feedback-target が返した時刻",
    "targetSha": "feedback-target が返した値",
    "outputSha": "",
    "resultHash": ""
  },
  "actor": "human",
  "source": "human:feedback-1",
  "reason": "互換性の確認内容を相談してから次へ渡す",
  "text": "旧設定ファイルとの互換性を確認した根拠を説明してほしい。",
  "evidence": ["human:compatibility-question"]
}
```

この例の `target` は説明用であり、実際には観測で取得した値を使う。
更新時刻や hash を推測して入力しない。`evidence` は出典の参照であり、
記録しただけで内容の正しさや Human の認証が検証されたことにはしない。
`text` を実行指示、scope・acceptance の変更、承認として自動解釈しない。

共通 controller は transaction 内で最新 Dispatch とその結果、保留変更 ID を照合し、
補足と保留を同時に保存する。初回保留には `feedback:入力ID` を使う。
すでに保留中ならその変更 ID を再利用し、二重の保留イベントを作らない。
SQLite の再読込後も `reviewFlows[].feedback` から対象・本文・出典・証拠・
受付時刻と対応する `holdChangeId` を復元できる。
`task plan`、HTTP の計画、ACP の計画もこの flow 記録を返す。

観測後に次の ROLE が発行された場合、同じ Dispatch の状態・結果が変わった場合、
保留変更 ID が古い場合は明示的に拒否し、補足も保留も部分保存しない。
古い対象を最新 ROLE へ付け替えない。入力を考える間も確実に次を待たせたいときは、
先に `workflow handoff` で保留し、その状態を観測して補足する。
同じ入力の再送は保存済み対象を維持して現在の flow を返し、
解除後や ROLE 切替後に古い補足を再送しても新たな保留を作らない。
同じ ID の本文・出典・対象の変更は拒否する。

現在の ROLE の結果保存は保留中も継続し、記録した補足の観測時点を後から書き換えない。
Reviewer への異議も元の verdict、指摘、SHA を保持する。
補足の記録は worker への送信や指示案への適用ではない。
解除して続けても本文は自動適用されず、元の承認済み契約を使う。
自然言語の相談、指示案の改訂、変更の再承認は #73・#74 に残る。
この操作は実行停止を保証せず、開始の保存が先なら現在の実行を維持する。

### 補足を検討して元の契約を維持する判断

RUNNING flow に未処理の補足がある場合、通常の `RELEASE` は拒否する。
`workflow feedback-target` の `pendingFeedbackIds` で対象を確認し、
`workflow feedback-resolve --file REQUEST.json` で補足ごとの判断を記録する。
現在の対象と保留変更 ID は改めて観測し、受付当時の対象と混同しない。

```json
{
  "flowId": "application-flow",
  "feedbackId": "human-feedback-1",
  "id": "human-resolution-1",
  "expectedChangeId": "feedback:human-feedback-1",
  "target": {
    "dispatchId": "feedback-target が返した値",
    "taskId": "feedback-target が返した値",
    "repository": "feedback-target が返した値",
    "recipient": "feedback-target が返した値",
    "instructionHash": "feedback-target が返した値",
    "status": "feedback-target が返した値",
    "updatedAt": "feedback-target が返した時刻",
    "targetSha": "feedback-target が返した値",
    "outputSha": "feedback-target が返した値",
    "resultHash": "feedback-target が返した値"
  },
  "action": "KEEP_CONTRACT",
  "actor": "human",
  "source": "human:clarification-decision",
  "reason": "補足を検討した結果、既存の確認項目で対応できるため承認済み契約を維持する"
}
```

初期実装が受け付ける処理は `KEEP_CONTRACT` のみとする。
補足を検討したが指示契約を変更しないという明示的な判断であり、
単なる既読、修正指示の適用、レビューの承認、例外の承認ではない。
補足の本文を実行したことや調査が完了したことも、この操作だけでは証明しない。
必要な変更がある場合はこの処理で済ませず、保留を維持して承認と指示の変更へ進む。
指示案への適用経路は #73・#74 の未実装項目に残る。

判断は補足の `resolution` に ID、現在の観測対象、保留 ID、操作、actor、
出典、理由、controller が付けた UTC 時刻を保存する。
同じ入力の再送は記録を維持して現在の flow を返す。
判断を書き換えたり、同じ判断 ID を別の補足へ使ったりする操作は拒否する。
観測後の状態・結果の変化や古い保留 ID も transaction 内で拒否する。
補足を受けた後に現在の ROLE の結果が保存された場合は、
新しい結果を観測して判断できるが、元の補足の対象は変更しない。

すべての補足を処理した後も保留は維持する。明示的な解除と run/advance が別途必要で、
新たな補足が届けばその補足の処理まで解除を止める。
STOPPED flow の解除は従来どおり保留だけを取り除き、未処理の補足も記録に残す。
これは安全停止した実行の再承認ではなく、STOPPED のまま追加実行できない。
旧レコードの読み取りは維持し、既存の補足を自動で処理済みに移行しない。

## 状態遷移と完了

`task transition --file REQUEST.json` は
`taskId`、`status`、`actor`、`reason`、任意の `proof` を受け取る。
通常の進行は次の順序となる。

```text
DRAFT → READY → ACTIVE → WAITING_FOR_EVIDENCE → WAITING_FOR_REVIEW
WAITING_FOR_REVIEW → APPROVED → COMPLETED
WAITING_FOR_REVIEW → REQUEST_CHANGES → READY
```

ACTIVE から WAITING_FOR_REVIEW への直接の証拠受け渡しも許可する。
途中では BLOCKED・HUMAN_DECISION_REQUIRED・REJECTED・CANCELLED・SUPERSEDED に進める。
SUPERSEDED は置換する Task の登録を要求する。terminal 状態から戻す遷移は拒否する。
BLOCKED・HUMAN_DECISION_REQUIRED からは DRAFT または READY へ進む。
READY から契約を見直す場合は DRAFT に戻す。
依存の状態変更で後続の実行条件が失われた場合は BLOCKED に変更し、
理由を履歴に保存する。worker の停止は別の停止操作とその結果で記録する。

proof の `outputSha` は証拠受け渡し時に対象 SHA を更新する。
APPROVED・COMPLETED は次の proof を要求する。

```json
{
  "taskId": "TASK-ID-FROM-LEDGER",
  "status": "APPROVED",
  "actor": "controller",
  "reason": "対象 SHA の検証結果を照合した",
  "proof": {
    "actionPerformed": true,
    "evidence": ["test-report:1"],
    "acceptance": ["必要な回帰テストが通る"],
    "reviewedSha": "TARGET-SHA",
    "reviewer": "independent-reviewer"
  }
}
```

Task の requiredEvidence と acceptance、owner と異なる reviewer、targetSha との一致を検査する。
親の承認・完了には子の完了を要求し、
未完了の子を暗黙に達成扱いにしない。
例外は `exceptionIds` と decision に登録した ID を使い、
proof の `waivers` に decisionId と免除する acceptance・evidence・children を明示する。
waiver の適用後も reviewer と reviewedSha の proof を要求する。

completion proof は報告された action、evidence、acceptance、reviewer、reviewedSha を照合する。
Git object と検証レポートの実物は repository と test record で確認する。
Graph は reviewer ID が Task owner と異なることと、reviewedSha が targetSha に一致することを検査する。
既存の `review` command は SHA evidence を Ledger に記録し、Graph の承認・完了は proof を使う Task transition で行う。
dispatch は Graph の READY、依存、承認済み scope、worktree、baseline SHA を検査する。
発行時に Task を ACTIVE にし、Task の関係、実行範囲、必要証拠、返却形式を含む
Dispatch 指示を不変の snapshot として保存する。指示 hash は保存内容から計算し、
CLI と ACP は同じ snapshot を人が読める形で表示する。
指示には `DISPATCH TO` を先頭に、Dispatch の関係、Task の親子・依存関係、
目的、scope、証拠と終了条件、worktree・branch・SHA、返却形式を含める。
`AUTHORIZED ACTIONS` は Task の `scope`、`FORBIDDEN ACTIONS` は `outOfScope` から作る。
これらは role に渡す実行指示である。Copilot adapter は SDK permission request に対し、
Coder の worktree 内 file write と allowlist 済み SDK tool、Reviewer の worktree 内 read を制御する。
OS と Git、外部 tool の操作は、それぞれの実行環境と tool の権限で管理する。
後続 Dispatch は変更・維持した instruction field を保存する。`CONTINUES` はすべての
実行契約 field が前回と一致するときに限り、`AMENDS` は少なくとも一つを変更するときに使う。
`INDEPENDENT` は別 Task の指示を示し、その Task の契約を個別に検査する。

```sh
taxion dispatch --task TASK-ID --repo owner/repository
taxion dispatch transition --id DISPATCH-ID --status ACKNOWLEDGED --actor worker --reason accepted
taxion dispatch transition --id DISPATCH-ID --status RUNNING --actor worker --reason started
taxion dispatch transition --id DISPATCH-ID --status SUCCEEDED --actor worker --reason finished --result-file RESULT.json
```

終端状態への遷移には `DispatchResult` JSON を必要とする。result の Dispatch ID、
Task ID、recipient、repository、status が対象 Dispatch と一致し、startedSha が
指示の開始 SHA と一致することを検査する。最初の Coder 実行は baselineSha を開始 SHA とし、
修正実行は前回の成果物を示す targetSha を開始 SHA とする。
実行開始時に controller が Git HEAD を照合し、`executionStartSha` として Dispatch に保存する。
SUCCEEDED には outputSha、実行済み validation、必須 evidence を要求する。
Coder の成功と Reviewer の APPROVE には検証の exit code 0 を要求する。
終端状態は Dispatch ID ごとに一度確定する。
Dispatch ID で結果と状態遷移の対象を特定する。
Reviewer の SUCCEEDED result には verdict、全 acceptance の判断と evidence、
findings、worktree の変更有無を追加し、APPROVE は全件 PASS・finding なし・変更なしの場合に限る。
runtime はこの result schema を SDK に渡し、最終応答を Task・Dispatch の保存済み条件と照合する。
最終応答は controller が受理し、結果ファイルは `dispatch transition --result-file` から受理する。
SUCCEEDED の受理時には CLI が Git worktree から origin、branch、baseline、HEAD、commit parent、
差分、worktree の状態を読み直し、Git evidence として result に保存する。
HEAD は outputSha と一致し、baseline は HEAD の祖先であり、差分は result の changedFiles と
Task の scope に一致する必要がある。rename は移動元の削除と移動先の追加として両方の scope を確認する。
controller は保存済み `validationCommands` を実行し、command・exitCode・出力・開始終了時刻・
実行 worktree・SHA を result に保存する。実行の前後で Git root、HEAD、clean 状態を照合する。
agent が報告したコマンドは実行許可の根拠として使用せず、実行対象は Dispatch の保存済みコマンドから取得する。
Reviewer の result は reviewed SHA と HEAD の一致、および変更のない clean worktree を証拠に含める。
Reviewer の開始時は `runtime start --review-source-worktree SOURCE` を指定する。
controller は SOURCE と同じ Git repository に、Task の `worktree` を detached worktree として作成する。
Task の worktree は新規作成用の絶対パスとし、対象 commit を `baselineSha` と `targetSha` に指定する。
元 repository の場所は Dispatch の `reviewWorktreeSource` に保存する。
結果の受理時は SDK 接続を閉じ、clean 状態と対象 SHA を再確認してから
`git worktree remove` で解放する。パスと Git 登録の解放を確認し、`worktreeReleased` を Git evidence に保存する。
dirty、worktree lock、SDK 切断失敗では成功結果の保存を停止し、worktree を調査用に保持する。
解放操作・解放確認の失敗もエラーとして返し、成功結果の採用を停止する。
Reviewer の完了は runtime の最終化を通し、結果ファイルからの完了登録は Coder に使用する。
修正時は承認済み baseline と直前の成果物 SHA を区別し、Coder の新しい HEAD と実際の parent を記録する。
Reviewer の検証コマンドが失敗した場合は、REQUEST_CHANGES または BLOCKED の根拠として保存する。

result の形は次のとおりとする。

```json
{
  "dispatchId": "DISPATCH-ID",
  "taskId": "TASK-ID",
  "recipient": "CODER",
  "repository": "owner/repository",
  "status": "SUCCEEDED",
  "startedSha": "BASELINE-SHA",
  "outputSha": "OUTPUT-SHA",
  "changedFiles": ["src/example.go"],
  "validations": [{
    "command": "go test ./...",
    "exitCode": 0,
    "worktree": "/path/to/worktree",
    "sha": "OUTPUT-SHA"
  }],
  "evidence": ["test-report:1"],
  "blockers": [],
  "nextTransition": "WAITING_FOR_EVIDENCE",
  "resumeCondition": ""
}
```

以前の Dispatch がある Task の再発行では、relationship、related Dispatch ID、
relationship reason を明示する。最初の Dispatch だけが `NONE` を使える。
関連 Dispatch は同じ Task の直近の inactive Dispatch（`INDEPENDENT` は別 Task）に限る。
指示には relationship reason と比較対象 field の一覧を含める。
`SUSPENDED` の result は停止 evidence と `resumeCondition` を必須とし、履歴にも保存する。
停止中の Dispatch は active として扱い、同じ Task の競合 Dispatch を防ぐ。
`runtimeSessionId` と `runtimeConfigHash` は Dispatch に結び、SQLite の workspace state に保存する。
同じ SDK session は同一 Task・recipient・設定 hash の範囲だけで再利用できる。
`runtimeUsage` は SDK が返す model、Auto 選択、input/output token、cache read/write token を保持する。
取得できた値を usage event に記録し、未取得値は未知として保持する。各 event は計測時刻とともに Dispatch に保存する。
resume 時は role prompt、Dispatch snapshot、worktree、tool allowlist、permission policy を再指定し、設定 hash の一致を照合する。
Copilot adapter の permission handler は、Coder に worktree 内の file write と allowlist 済み SDK tool の一回限りの利用を認め、
Reviewer に worktree 内の file read を認める。shell request と worktree 外の file access は reject decision を返す。
この handler は SDK permission request を処理し、OS・Git・外部 tool は各実行環境の権限で動作する。

CLI `runtime start` は保存済み Dispatch 指示を role 固有 system prompt に加えて session を作り、
短い実行要求を送る。`send` は追加指示を渡す。event cursor で新しい turn の出力だけを読み、
SDK compaction 後も role と Task Graph の実行契約が system context に残る。
context 使用量と compaction の結果を event stream で確認できる。
CLI は turn の最終応答を共有 controller に渡す。controller は result の識別と契約、
Git の事実、承認済みコマンドの実行結果を照合し、Dispatch 結果と Ledger を SQLite に同時保存する。
途中の出力は event として表示し、状態の確定には turn の終了を必要とする。
operator が結果ファイルを投入する経路も同じ Git 検証とコマンド実行を通す。
`dispatch transition --validation-timeout` は検証全体の期限を指定し、既定値は 10 分である。
ACP と HTTP API は `internal/runtime/local` を利用し、Copilot session は明示的な runtime command から起動する。

## 保存・互換性と制約

Graph を持たない workspace は Ledger の旧方式で動作する。
Graph を導入した workspace は dispatch 対象をすべて登録し、CLI・HTTP・ACP が
登録状態、READY、repository の一致を検査する。
移行前に既存の active Dispatch を解決し、実行中の Task 状態を保って Graph に登録する。
Web は HTTP の検査結果を表示する。Graph の read/edit API は今後の HTTP・ACP 拡張に含める。
既存 Ledger の wire format は維持する。

Graph の登録・遷移は検証後に rename で保存し、検証が完了するまで現在のファイルを維持する。
CLI・HTTP・ACP の更新は SQLite transaction で Ledger と Graph を一緒に変更する。
SQLite が同時 writer の更新を受け入れられない場合は明示的なエラーとなり、
呼び出し元は成功として扱わず再試行できる。
移行元 JSON は初期データとして読み込み、以後の workspace 更新を SQLite に保存する。
移行後の正本は SQLite workspace state であり、移行元 JSON は初期 snapshot として保持する。
Graph 読み込み時は version、キー、履歴、現状態の整合性を検査する。
履歴検査は transition と proof を照合し、不整合があればエラーを返す。

Memory の参照先の検証と指示生成、役割別 Session の実行・再開は保存済み契約に従う。
instruction v1〜v3 の履歴は読み込み、現在の実行には v4 の新しい Dispatch を発行する。
Human の実際の認証・承認確認は後続 Issue で実装する。
actor・decision・proof は caller が供給する作業記録である。
実行環境の権限確認と repository・test record の証拠は、それぞれの管理元で確認する。
