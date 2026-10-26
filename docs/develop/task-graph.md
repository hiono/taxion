# Task Graph の登録・遷移・保持

Task Graph と Ledger は SQLite の `workspace.db` に同時に保存する。
設計の理由と移行の決定は
[ADR-0022](adr/0022-sqlite-atomic-workspace-state.md) を参照する。

現在の SQLite 保存は、集合 JSON からレコード単位へ移行している。
変更のない Task・Dispatch・履歴を保存し直さず、event の追記ではその event と
workspace revision を更新する。長い文字列は内容 hash で共有し、レコードとともに圧縮する。
展開後の原文を照合するため、既存 instruction・承認の hash は変えない。
書込み接続は旧 DB を transaction で移行し、read-only 接続は移行しない。
移行後は新形式に対応する binary を使う。DB の空き page を自動で切り詰める処理はない。
保存形式の詳細は
[ADR-0033](adr/0033-reference-results-and-persist-workspace-records-incrementally.md) を参照する。
停止済み workspace・Memory・必要な Git object の一ファイル移送と復元は
[ADR-0034](adr/0034-portable-sqlite-workspace-snapshots.md) を参照する。

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
選択済みの利用者の判断は判断記録とともに復元し、新たな選択が必要な質問を利用者に提示する。
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
自然言語の脇の質問・追加調査・本流への反映は #74、受け渡し制御は #73 として、2026-10-12 に完了した M12 の実 SDK 受入れで確認した。

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
`validationCommands` は利用者が許可した検証コマンドを実行順に保持する。
Dispatch の発行には一つ以上のコマンドを要求し、controller は worktree を作業ディレクトリとして
`sh -c` で実行する。コマンドの権限は controller の実行環境に従うため、
Task の承認時にコマンドの副作用も確認する。

`fileAccess` は SDK の file read/write permission を定め、Task と Dispatch の契約 hash に含める。
新しい Task の省略時は `{"level":"committed"}` となり、Dispatch 開始 SHA で追跡済みの
repository-relative path を許可する。`{"level":"exact","paths":["src/file.go"]}` は列挙した
normalized path だけを許可し、明示した新規 path も作成できる。
`{"level":"all"}` は割当 worktree 内の全 path を許可する。どの level でも worktree 外と
`.git` は拒否し、Reviewer は読み取り専用のままとする。
この設定は `scope` による変更範囲や SDK tool allowlist とは別の制御である。
SDK permission callback と Git の成果物検証は同じ Dispatch snapshot を使う。
fileAccess のない保存済み Task／Dispatch は移行せず、以前のアクセス挙動を維持する。
既存 Task の fileAccess を変えるには新しい authorization decision が必要で、更新時に field を
省略した場合は保存済みの policy を維持する。

`outputMode` は成果物の識別方式を選ぶ。省略時と `commit` は従来どおり commit SHA を使う。
`worktree-tree-snapshot` を指定すると、Coder は commit を作らずに実行し、
Controller が開始時の commit SHA を `outputSha`、Git tree SHA を `outputTreeSha` として保存する。
変更 path と tree SHA は Task・Dispatch・Reviewer handoff で照合する。
Controller は tree object を `refs/taxion/snapshots/` のローカル ref で保持する。
snapshot ref は branch や commit ではないが、Git repository 内に残る。
ignored file は Git tree に含まれず Reviewer に渡らないため、成果物や検証結果に使わない。
後続 Task で snapshot を引き継ぐときは、承認する `targetSha`（baseline commit）、
`targetTreeSha`、`targetChangedFiles` を一致させる。

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

`specifications` は、出典・該当箇所・版・本文・用途と、
取り込み・Copilot 送信・公開の個別承認を保持する。
Task と Dispatch の契約に含め、役割間で同じ引用スナップショットを使用する。
入力と利用許可は [仕様入力の参照文書](specification-input.md) に従う。

## 上限付きレビュー進行

以下はオーケストレータ・自動化の実装者向けの CLI 入力仕様である。
`REQUEST.json` は入力ファイルの仮名であり、利用者に手書きを求めるものではない。
利用者は `ready` を読んだ agent または `request` の Planner に自然言語で依頼し、
提案された作業条件と実行上限を承認する。JSON の生成とコマンド実行は
オーケストレータまたは `request` が担当する。

`workflow start --file REQUEST.json` は、READY の Coder Task に対する
独立レビューと修正の許可を保存し、最初の Dispatch を発行する。
Task の `owner` は `CODER`、`reviewer` は `REVIEWER` とする。
初回実行用の Task を使用し、以前の実行からの後続作業は出典を持つ新しい Task として登録する。
`maxRepairs` は追加の Coder 実行回数、`timeoutSeconds` は開始からの経過時間の上限を表す。
`timeoutSeconds` を省略するか0を指定すると、既定の3600秒（1時間）を適用する。負の値は拒否する。
新しい Flow は観測 token 数の閾値を停止条件にせず、期限・repair 回数・契約と権限の検査を行う。
利用者は Task の実行契約とともに、この進行と権限を承認する。

```json
{
  "id": "application-flow",
  "taskId": "TASK-ID",
  "repository": "owner/repository",
  "reviewRoot": "/absolute/path/reviews",
  "maxRepairs": 2,
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
修正回数、実行順の Dispatch ID、進行状態と理由を SQLite に保持する。
既存記録の `maxTokens` は履歴互換のため読み取るが、新しい Flow の入力・停止条件には使わない。
`workflow show --id application-flow` と後続計画からこの記録を復元できる。
進行中の Dispatch に対する advance の再実行は、保存済みの同じ Dispatch を返す。
停止状態は再起動後も保持し、追加の実行は新たな許可を持つ後続 Task で扱う。

期限到達、scope・acceptance・権限の変更、新たな判断、実行の失敗・cancel・停止要求、
Reviewer の `BLOCKED`、必須 Memory の失効では、理由を持つ `STOPPED` を保存する。
利用量が欠落・不完全な場合はその状態を明示し、token 数を推測しない。
利用量の不完全だけを理由に承認済み Flow を停止しない。
実行中 Dispatch の安全停止は `runtime stop` で確認し、保留した worktree と証拠を調査する。
`workflow advance` は Dispatch の発行境界で上限を照合する。
`workflow run --id application-flow --coder-prompt-file CODER.txt --reviewer-prompt-file REVIEWER.txt`
は役割別の SDK adapter を作成し、保存済み指示の実行、イベント購読、最終応答の照合、
次の Dispatch 発行を連続して行う。各実行の終了時に SDK 接続を閉じる。
途中の説明はイベントとして表示し、turn の最終応答を結果の採用に使う。

run は保存済み期限を context の deadline に設定し、各 usage event を保存・報告する。
実行中は1秒ごとにも進行状態・Task 契約・必須 Memory を確認し、
期限到達や契約変更・停止要求で `runtime stop` と同じ停止処理を行う。
観測した token 数は実行を止める上限ではない。
usage event を decode できない場合は event 処理を error として停止する。
数値の欠落・不完全は unavailable として報告し、それ自体では停止しない。
失敗した安全停止は `CANCEL_REQUESTED` とエラーを保持し、
停止を確認できた実行は `CANCELLED` と停止証拠を保存する。

runner は Graph の `runnerId` を取得して単独で進行する。
別 runner の run と手動 advance はこの所有者を照合する。
正常終了とエラー処理の完了後に所有者を解放し、
プロセスが強制終了した場合は保存済み所有者を残して二重実行を防ぐ。
再開時は以前の SDK 実行と worktree・停止証拠を調査し、
`workflow recover --file RECOVERY.json` で所有者を回復する。
利用者は元のプロセスの終了を確認し、active Dispatch を
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
保存済み期限・修正回数・flow 状態を維持するため、
停止済み flow の回復はその実行を再承認する操作と分けて扱う。
成功結果が保存済みなら通常の advance でその SHA の独立レビューへ進み、
取消・失敗の結果は通常の停止判定で利用者への確認に戻る。
role prompt と `--coder-tools`・`--reviewer-tools`、任意の `--cli`・`--base-directory` は
既存 SDK adapter の設定・権限検査を使用する。
#53 で Coder の Git 操作を controller 管理ツールへ接続し、#54 の実 SDK 受入れで
一連の動作を確認した。これらは 2026-10-12 に完了した M12 の実 SDK 受入れに含まれる。

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

保留中も期限、契約・Memory 変更、判断待ち、失敗・取消の停止判定を維持する。
保留時間も既存期限に含める。安全停止した flow の解除は保留だけを取り除き、
`STOPPED` を `RUNNING` に戻さず、新たな実行許可の代用にしない。
現在の実行を止める場合は別途 `runtime stop` を使い、停止証拠を確認する。

実行開始の transaction と保留の transaction は順序付けるが、
保存済み `RUNNING` と SDK `Send` は外部操作をまたぐため原子的ではない。
開始の保存が先なら、その後の保留は現在の実行を許容し、後続を止める。
SDK の実際の受付時刻や停止完了を保留の成功だけで保証しない。
「いま止める」と「次を待つ」を一つの操作として扱わない。
受け渡し制御・指示案の版管理と対象付き反映は #73、脇の質問と追加調査は #74、
実 SDK の介入受入れは #54 で扱い、2026-10-12 に完了した M12 の実 SDK 受入れでは
質問・追加調査で選択した事項の反映と SDK 中断を確認した。初回利用にわたる実モデルの end-to-end 受入れは
Release 0.1.0 の #78 で引き続き追跡する。

### repair を自動で続行できるかの確認

`workflow repair-status --id application-flow` は、workspace を読み取り専用で開き、保存済みの Flow を変更せずに読み、
次の repair を利用者に尋ねずに進めてよいかを JSON（`version: 1`）で返す。
`stateChanged` は常に `false` である。`status` は次のいずれかになる。

- `repair-authorized`: 承認済み basis と保存済み ceiling の内側にある。
  `workflow advance` または `run` で続行し、利用者には尋ねない。
  `advance` は Git の証拠、Memory、承認済み draft を改めて検証し、不一致なら停止する。
- `reapproval-required`: 契約が保存済み Flow と一致しない、bundle-required で一致する bundle がない、
  仕様ソースの利用許可が無効になった、または停止コードが `RECONTRACT` である。`task reauthorize` が必要である。
- `ceiling-exhausted`: repair 回数、期限、session・Dispatch・credit の予算のいずれかが上限に達した。
  または停止コードが `BUDGET_STOP`・`BUDGET_LIMIT` である。
- `human-decision-required`: `SAFETY_STOP` など、上記以外の理由で Flow が `STOPPED` になっている。
  Task が `CANCELLED`・`REJECTED`・`SUPERSEDED`・`BLOCKED`・`HUMAN_DECISION_REQUIRED` であるか、
  利用者の選択待ちである場合、handoff が hold されている場合、
  Task が `WAITING_FOR_REVIEW` でないか review 結果が現在の target SHA と一致しない場合も含む。
  `STOPPED` は終端状態であり、自動では再開しない。
- `invalid`: Task Graph の検証または承認の分類に失敗した。
- `not-applicable`: Flow が repair の境界にない。

`run`・`step`・`advance` が停止済み Flow に当たると、stdout の JSON は変えずに、
stderr へこの分類と `repair-status` の再実行コマンドを2行で出す。

### 保存済みの出力を指定した利用者の補足

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
記録しただけで内容の正しさや利用者の認証が検証されたことにはしない。
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
自然言語の相談、指示案の改訂、変更の再承認は #73・#74 として、2026-10-12 に完了した M12 の実 SDK 受入れで確認した。
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
契約を維持する補足の反映は後述の `workflow draft-approve` で行う。
scope・acceptance・実行主体・上限を変える場合の再承認は、この `KEEP_CONTRACT` 操作では行わない。
変更契約の再承認は #73 の対象として実装され、2026-10-12 に完了した M12 の実 SDK 受入れで確認した。

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

### 未発行の指示案を版付きで保存・改訂する

`workflow draft --file REQUEST.json` は、保留中の RUNNING flow に
次の担当・操作・本文を提案として保存する。指示案の保存は実行指示の発行ではない。
各版に新しい ID を付け、改訂前の ID を `expectedDraftId` へ渡す。
controller は版番号と UTC 時刻を付け、本文、出典、理由、証拠、
未確認事項、参照した補足 ID、根拠とした観測対象を保持する。

`workflow feedback-target` は `expectedDraftId` と `pendingDraftId` も返す。
最初の案では `expectedDraftId` は空文字列となる。
次の例は観測対象と保留 ID を転記し、未確認事項を含む最初の案を保存する。

```bash
taxion workflow feedback-target --id application-flow |
  jq '{
    flowId, expectedDraftId, expectedChangeId, target,
    id: "draft-1",
    nextRole: "REVIEWER",
    operation: "互換性の証拠をレビューする",
    text: "既存の acceptance に対する互換性の証拠を確認する。",
    actor: "human",
    source: "human:compatibility-proposal",
    reason: "次の受け渡し前に確認内容を相談するため",
    evidence: [],
    unconfirmed: ["互換性を検証した記録は未確認"],
    feedbackIds: []
  }' > draft.json
taxion workflow draft --file draft.json
```

`evidence`、`unconfirmed`、`feedbackIds` は空の場合も配列を明示する。
`feedbackIds` に既存の補足 ID を指定すると、案が参照した補足を記録できる。
参照だけで補足を処理済みにはしない。`nextRole` は CODER または REVIEWER を提案する値で、
担当変更の承認や、その担当に実行権限があることの証明ではない。
`target` は保存済み指示・結果の識別であり、次の Dispatch の発行契約はまだ構成しない。

改訂は再観測して `id` を新しい値へ変更し、同じコマンドで保存する。
transaction 内で最新の案 ID、保留変更 ID、保存済み対象を照合する。
古い版、観測後の結果変更、不明な補足 ID、同じ ID で異なる本文は拒否する。
同じ入力の再送は現在の flow を返すだけで、過去の案を最新に戻さない。
現在の ROLE が実行中でも提案を保存できるが、後から成果が保存された場合は
新しい対象を観測して改訂する。元の版の観測対象は変更しない。

未承認・未破棄の最新案がある RUNNING flow の `RELEASE` は拒否する。
案を採用しない場合は、現在の対象を再観測して明示的に破棄する。

```bash
taxion workflow feedback-target --id application-flow |
  jq '{
    flowId, expectedChangeId, target,
    id: "discard-draft-1",
    draftId: .expectedDraftId,
    actor: "human",
    source: "human:proposal-decision",
    reason: "提案は採用せず、承認済み契約を維持する"
  }' > discard.json
taxion workflow draft-discard --file discard.json
```

破棄は最新の案だけに出典付きで記録し、過去の版も保持する。
破棄・再送は補足を処理済みにせず、保留も解除しない。
未処理の補足について契約維持を判断した場合は `workflow feedback-resolve` が別途必要となる。
STOPPED flow は保留解除後も実行できず、未破棄の案を記録に残す。

案を採用しない場合は、明示的に破棄し、必要な補足の処理判断と解除を行って
元の承認済み契約を使う。案の保存や破棄で発行済みの instruction、
Reviewer の verdict、Task の承認、ROLE session へ送る本文は変更しない。
`workflow show` と共通の計画出力で、SQLite 再起動後も全版を確認できる。
actor と source は入力された出典情報であり、認証済み利用者の本人証明ではない。

### 契約を維持する補足を承認し、次の指示へ反映する

`workflow draft-approve --file REQUEST.json` は最新の案を
`APPROVE_SUPPLEMENT` として承認する。変更できるのは次の指示に加える説明であり、
scope、acceptance、禁止操作、実行主体・設定、費用・上限を変更する承認ではない。
レビューへの異議を承認して元の verdict を覆す操作にも使わない。

承認できるのは、現在の ROLE の成功結果と Git 証拠が保存された受け渡し境界に限る。
Coder の後は REVIEWER、REQUEST_CHANGES を返した Reviewer の後は
既存の修正回数内で CODER を指定する。APPROVE・BLOCKED のレビュー結果を
この操作で修正実行へ変更する要求は拒否する。
実行中に作った案は、結果の保存後に再観測して新しい版へ改訂する必要がある。

```bash
taxion workflow feedback-target --id application-flow |
  jq '{
    flowId, expectedChangeId, target,
    id: "approve-draft-1",
    draftId: .expectedDraftId,
    contractHash: .draftContractHash,
    action: "APPROVE_SUPPLEMENT",
    actor: "human",
    source: "human:supplement-approval",
    reason: "承認済み契約を維持したまま、この版の補足を次の指示へ加える"
  }' > approval.json
taxion workflow draft-approve --file approval.json
```

承認 transaction は案 ID、案の観測対象、現在の対象、保留 ID、
現在の Task と保持した契約を照合する。
`draftContractHash` は保持した Task 契約、flow の承認、review root、
修正回数上限、期限の JSON 内容 hash である。
認証やライブの状態確認を代替する値ではない。
承認が付いた未発行案を改訂する場合は、保留中に先にその承認付き案を破棄する。

承認だけでは保留を解除せず、Dispatch も発行しない。
案の `feedbackIds` に指定した補足は承認待ちの対象から外れるが、
発行前は `feedbackDispositions` に `APPROVED_FOR_HANDOFF` と表示する。
案で参照していない未処理の補足は引き続き解除を止める。
承認済みの案を破棄すれば、その案に結び付く補足は未処理に戻る。
契約維持の処理済み補足を再適用したり、複数の承認済み案へ同じ補足を割り当てたりする操作は拒否する。

明示的に保留を解除して run/advance を呼ぶと、既存の安全条件と
承認した案の対象を再照合する。次の Task の `DECISIONS` に
案の本文・版・証拠・未確認事項・承認を含む出典付き補足を加え、
その Task から既存の生成処理で Dispatch を発行する。
案の `publication` に Dispatch ID、instruction hash、時刻を
同じ workspace transaction で保存する。
指示へ反映された補足は `INSTRUCTION_PUBLISHED` と表示する。
worker の開始や本文に書かれた検証の完了を、この表示だけで証明しない。

新しい指示にも元の scope・acceptance・禁止操作・証拠条件を保持する。
既存の結果・verdict・instruction と過去の版は書き換えず、
修正は既存の AMENDS、独立レビューは INDEPENDENT の関係を使う。
設定・認証の開始検査も維持する。Task・Memory の変更、期限・token・修正回数の
上限違反があれば承認後も停止する。発行失敗時は新しい Task・Dispatch・反映記録を
部分的に保存せず、安全停止を明示する。
発行後の再送で二重発行せず、発行済みの案を破棄して履歴を取り消す操作は拒否する。

M12 の #73・#74 の実装と、変更契約の再承認を含む実 SDK 受入れは
2026-10-12 に完了した。受入れでは質問・追加調査で選択した事項の反映と SDK 中断も確認した。
初回利用にわたる実モデルの end-to-end 受入れは
Release 0.1.0 の #78 で引き続き追跡する。

### 停止した作業の継続

旧 workspace に保存された `BUDGET_LIMIT` の停止と結果は履歴互換のため保持する。新しい Flow は token 閾値で停止しない。どの停止記録でも `workflow show --id FLOW-ID` で stop code、usage、最後の Dispatch result と Git evidence を確認する。停止済み Flow 自体は書き換えず、続行には新しい Task の承認が必要となる。

`workflow recover` は、プロセス停止後に残った runner の所有権を解放する操作である。停止したプロセス、runner ID、全 Dispatch の終端状態を確認してから、次の内容を `taxion workflow recover --file REQUEST.json` に渡す。

```json
{
  "flowId": "application-flow",
  "expectedRunnerId": "runner-id-from-workflow-show",
  "previousProcessStopped": true,
  "actor": "human",
  "reason": "停止済みプロセスと全 Dispatch の結果を確認した",
  "evidence": ["operator-log:recovery-check"]
}
```

この操作は停止理由や handoff hold を解除せず、Task や Dispatch を作らない。旧 `BUDGET_LIMIT` で停止した Flow は通常 runner の所有権も解放済みであり、`workflow recover` は続行に不要である。

最後の Coder が `CANCELLED` になった場合、result の `partialGitEvidence` に停止時点の baseline、HEAD、Git tree、変更ファイルが記録される。作業を続けるには、停止した worktree で `git add -A -- .` と `git write-tree` を実行し、得られた tree SHA が保存済み `treeSha` と一致することを確認してから commit する。一致しなければ commit や後続 Task の承認に進まず、保存された変更を復元する。次に、その commit を target とする別の clean worktree を作る。

この手順は commit mode の場合に使う。snapshot mode では commit を作らず、別 worktree を元の baseline から作り、保存 tree の差分を適用する。
後続 Task は `outputMode: "worktree-tree-snapshot"`、`targetSha` に baseline、`targetTreeSha` に保存済み tree、`targetChangedFiles` に保存済み path を指定する。
`conversation confirm` は、後続 worktree の tree と path が停止時の証拠に一致する場合だけ承認する。次の Flow は Coder から始まり、開始前にも保存 ref と tree SHA を照合する。

```sh
git -C /path/to/repository worktree add -b work/successor /absolute/path/to/successor BASELINE-SHA
git -C /path/to/repository diff --binary BASELINE-SHA TREE-SHA |
  git -C /absolute/path/to/successor apply --binary
```

`git apply` は作業ファイルを更新するが、実 index や commit は変更しない。ignored file は Git tree に含まれないため、この経路の成果物として扱わない。

```sh
git -C /path/to/stopped-worktree add -A -- .
git -C /path/to/stopped-worktree write-tree
# write-tree の SHA が partialGitEvidence.treeSha と一致することを確認する
git -C /path/to/stopped-worktree commit -m "feat: preserve stopped work"
git -C /path/to/repository worktree add -b work/successor /absolute/path/to/successor TARGET-SHA
git -C /absolute/path/to/successor status --porcelain
```

後続 Task の baseline は元の承認済み baseline のままにし、target SHA はその commit にする。最後のコマンドが空の出力を返すことも確認する。controller は承認時に tree、差分、scope と repository を照合する。部分証拠を確認できない場合や tree が一致しない場合は続行を拒否する。

最後の Coder が `SUCCEEDED` になった後、旧 `BUDGET_LIMIT` または期限切れなどの `SAFETY_STOP` で Flow が停止した場合は、保存済み output SHA と同じ commit を target とする別の clean worktree を用意する。
この手順は commit mode の場合に使う。snapshot mode では baseline commit から別 worktree を作り、保存済み tree の差分を適用して、後続 Task の `targetTreeSha` と `targetChangedFiles` を一致させる。

```sh
git -C /path/to/repository worktree add -b work/review-successor /absolute/path/to/successor OUTPUT-SHA
git -C /absolute/path/to/successor status --porcelain
```

後続 Task を人が承認したうえで、`workflow start` の request に元の Coder Dispatch ID を `continueFromDispatchId` として指定すると、新しい flow は Coder を再実行せず Reviewer から始まる。snapshot mode でもこの指定方法は同じで、Reviewer は同じ baseline commit と tree SHA を使う。

packet の検証証跡を再利用する場合、後続契約は元 Flow の `objective`、scope、acceptance、出力方式を維持する。`objective` は成果物の全体目的であり、後続で実行する役割を表す field ではない。レビューだけの実行は `continueFromDispatchId` で選ぶ。目的の文字列をレビュー専用の説明へ変更しても同じ意味かどうかを controller は推測しないため、元の検証結果を流用しない。security と実行設定の一致、元の承認による検証証跡の照合も必要になる。

v0.7.16 は `objective` と出力方式の不一致も `packet continuation changed the verified scope or acceptance` と表示する。v0.7.17 では目的と出力方式を分けて診断する。承認済み後続 Task の目的を変えてしまった場合は、その承認を上書きせず、別IDの後続 Task に元の目的を保持して新たに承認する。旧 Task、成果、検証、取消履歴は維持し、Coder や検証を繰り返すために通常 Flow へ戻さない。

旧 `BUDGET_LIMIT` または `SAFETY_STOP` で Reviewer 実行が停止した場合も、直前の成功 Coder 結果を引き継げる。元の flow は `STOPPED`、上記いずれかの stop code、runner 所有権なし、handoff 保留を要求する。最後の Dispatch は、その Coder に独立レビューとして結び付いた `CANCELLED` の Reviewer であり、保存 session に対応する `runtime-stop:` 証拠が必要になる。実行中・停止未確認・別の Coder へのレビューは継続元にできない。停止理由の分類だけで続行を許可せず、成功結果、同一成果物、新しい Task と設定・期限の承認も照合する。旧 Flow の stop code や結果は変更しない。

この場合、後続承認の `target` は最後の取消済み Reviewer を指し、`continueFromDispatchId` はその直前の成功 Coder を指す。後続 worktree と保存済み成果物を照合し、新しい Reviewer には後続 Task の設定承認を引き継ぐ。前の Reviewer ID だけを許可した設定承認を、新しい Reviewer の許可として流用しない。

どちらの経路でも、後続 Task の承認では置き換える停止済み flow とその保存済み結果を指定する。新しい Flow には別の `id`、`taskId`、`timeoutSeconds` と必要な session ceiling の承認を渡す。部分成果から続ける場合は通常の初回開始と同じく Coder から開始し、完了済み Coder 結果を引き継ぐ場合だけ `continueFromDispatchId` を指定する。

```json
{
  "id": "application-review-successor",
  "taskId": "approved-successor-task",
  "continueFromDispatchId": "saved-coder-dispatch",
  "repository": "owner/repository",
  "reviewRoot": "/absolute/path/to/reviews",
  "maxRepairs": 0,
  "timeoutSeconds": 1800,
  "authorization": {
    "id": "new-budget-approval",
    "source": "human:budget-approval",
    "text": "保存済み Coder 結果の独立レビューを上記の期限内で許可する"
  }
}
```

部分成果を引き継ぐ場合、この例から `continueFromDispatchId` を除く。commit mode では承認済み Task の target SHA と clean worktree が部分 tree を再現した commit を指す。snapshot mode では baseline SHA を維持し、target tree SHA と変更 path を明示する。いずれの場合も、新しい Task の承認や予算承認は `workflow recover`、旧 flow の handoff 解除、自然言語の依頼だけでは代替できない。

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
Reviewer に worktree 内の file read を認める。Coder の shell request は、allowlist に shell tool があり、
worktree 内に閉じ、ネットワーク・sandbox bypass・Git の更新を含まない場合だけ一回限り認める（ADR-0047）。
Reviewer の shell request と worktree 外の file access は reject decision を返す。
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
HTTP API は `internal/runtime/local` を利用する。ACP は CLI が発行した新規 Dispatch ID を受け取り、CLI と同じ controller 経由で一度だけ Copilot session を実行する。ACP から Task・Dispatch は発行しない。

## 不要な停止済み試行を一覧と計画から外す

v0.5.0 の `task archive` は、停止済みの試行を通常の Task／Dispatch 一覧と
`task plan` から外す。保存記録の削除や Git cleanup は行わない。
元の Task 状態・承認・実行結果・履歴は保持し、Graph の `archives` に
`taskIds`、`actor`、`source`、`reason`、`expectedHash`、`at` を追加する。
入力、確認、参照方法は[利用者ガイド](../userguide/user-guide.md#不要になった試行を一覧と計画から外す)に記載する。

選択した Task と、その Flow の発行済み role Task は同じ対象となる。
稼働中・suspended Dispatch、稼働中 Flow、未解放 runner と未解決の外部公開を拒否する。
別の非終了 Task が依存・親子・継続元・Dispatch 関係で参照する場合も拒否し、
その作業を継続するか、一緒に通常の作業対象から外すかを明示的に決める。
適用は出典・理由と保存 workspace の確認 hash を transaction 内で照合する。

archive 済みの契約・実行履歴は改変できず、元 Task の再開も拒否する。
別途承認した後続 Task から保存成果を参照することはできる。
明示参照の `task graph`、`workflow show`、`records show` は維持し、
`task plan --include-archived` では action を `ARCHIVED` として返す。
すべての Task が archive 済みの repository の通常計画は空となり、追加の実行を提案しない。
これは未登録 repository を正常な空計画として扱う変更ではない。

## 保存・互換性と制約

保存済み独立レビューの受理専用操作は [ADR-0052](adr/0052-accept-saved-review-without-restarting-execution.md) と [CLI リファレンス](../userguide/cli-reference.md#停止後の保存済みレビュー結果の受理)に従う。`Records.ReviewAcceptances` は追記専用で、元の停止 Flow 全体と exact preview hash、actor・source・reason、期限後受理の明示判断を保持する。読取り時も受理記録と現在の完了 Flow・結果を照合する。通常の停止解除や実行再開ではなく、既知の理由消失に限定した保存結果の受理である。Reviewer Task の `BLOCKED` はそのままで、元の HOLD も残す。検証では `gitproof.VerifySavedSnapshotAcceptance` が実行用ブランチ検査と保存 snapshot の照合を区別し、Git index・object・ref の更新や検証 command の実行を行わない。

Graph を持たない workspace は Ledger の旧方式で動作する。
Graph を導入した workspace は dispatch 対象をすべて登録し、CLI・HTTP・ACP が
登録状態、READY、repository の一致を検査する。
移行前に既存の active Dispatch を解決し、実行中の Task 状態を保って Graph に登録する。
Web は HTTP の検査結果を表示する。Graph の read/edit API は今後の HTTP・ACP 拡張に含める。
既存 Ledger の wire format は維持する。

CLI・HTTP・ACP の更新は SQLite transaction で Ledger と Graph を一緒に変更する。
v0.7.18 では、workspace の各接続に `busy_timeout(5000)` を設定し、更新 transaction は
`BEGIN IMMEDIATE` で読取り前に writer を予約する。短時間の別接続の読取りや更新競合は、
SQLite 内で最大5秒待つ。workspace 全体の読取りは一つの read transaction で行い、
Ledger・Graph・保存記録を異なる commit から組み合わせない。
5秒を超えるロックや他の保存エラーは成功として扱わない。runner で回復できない保存エラーは、
従来どおり runtime の停止・照合と `SAFETY_STOP`・`HOLD` の対象になる。
モデル送信、tool 実行、transaction の callback は自動再実行しない。

`OpenReadOnly` は `mode=ro` で開き、schema を初期化・移行しない。一方、
`task graph` を含む一部の既存 CLI は通常の workspace 初期化経路を使うため、
表示目的でも無書込みとは限らない。長時間の外部 DB transaction や同時 schema 移行との
並行動作、無制限の待機は保証しない。journal mode は変更せず、WAL への自動移行は行わない。
v0.7.17 以前は通常経路にこの待機と writer 予約がなく、実行中の CLI 照会でも
`SQLITE_BUSY` による停止が起こり得る。旧版では実行中の DB・CLI 照会を避ける。
開発途中の JSON 保存形式を持つ workspace の読取り互換を実装に残している。
対象は `.taxion/ledger.json` と `.graph.json` で、SQLite への初回取込みに限り読み、
以後は更新しない。これは旧開発環境の記録を保持するための処理であり、
新規利用者がこれらの JSON を作成・管理する必要はない。
移行後の正本とバックアップ元は SQLite であり、残った JSON を現状態として扱わない。
Graph 読み込み時は version、キー、履歴、現状態の整合性を検査する。
archive を記録した DB は `archives` を理解しない v0.4.1 以前へ戻して使わない。
履歴検査は transition と proof を照合し、不整合があればエラーを返す。

approval schema version を持つ graph は、Task ごとに対応する ApprovalBundle を要求する。
version がない graph は bundle を含まない場合だけ `verified-legacy` として従来の検査を使い、
未知 version や schema version なしの bundle は `invalid-or-ambiguous` として拒否する。
bundle-required の graph は、review flow 開始と各 role の Dispatch 発行時に、現在の Task と設定に
一致する bundle を確認する。一致しない場合は入口を拒否し、承認履歴や Task Graph を変更しない。
各 gate report は保存済み状態を読むだけで、承認、状態遷移、Dispatch 発行を行わない。

Memory の参照先の検証と指示生成、役割別 Session の実行・再開は保存済み契約に従う。
instruction v1〜v3 の履歴は読み込み、現在の実行には v4 の新しい Dispatch を発行する。
利用者の実際の認証・承認確認は後続 Issue で実装する。
actor・decision・proof は caller が供給する作業記録である。
実行環境の権限確認と repository・test record の証拠は、それぞれの管理元で確認する。

## Task budget accounting

Review Flow は承認時に Dispatch 数、Coder/Reviewer session 数、Task-wide nano-AIU、elapsed time、repair 数の ceiling を保存する。`workflow evaluate --id FLOW_ID` は各上限の approved・consumed・remaining と使用量の完全性を報告する。re-review 数も修正上限から導出する。

Controller は新しい Dispatch/session の開始前と usage event の記録後に ceiling を検査する。正の利用額上限を持つ契約では、usage が不明または不完全なら nano-AIU remaining を0として fail-closed にし、`BUDGET_STOP`、安全停止、held Flow、checkpoint を保存する。利用額上限を省略できる課金条件・方式は[課金ガイド](../userguide/billing.md)で定義する。課金条件の解決・検証と予算方式の整合性は `internal/core/billing.go` を使い、外部予算方式でも期限・回数の上限を維持する。SDK の `--max-ai-credits` は session 別 soft ceiling であり Task-wide nano-AIU ceiling の代わりにはならない。

Budget audit は Dispatch 発行、session 開始、usage 観測、stop、recontract を workspace record に追記する。既存 record の変更・削除は拒否する。`workflow terminate` が扱えるのは最新の未確認・未開始 Coder Dispatch だけである。既に実行された work を続ける場合は、保存された evidence を確認し、新しい Task 契約と予算を承認する。
