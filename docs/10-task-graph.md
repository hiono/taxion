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
自然文の会話から懸念を自動抽出する機能ではなく、登録入力の整合性を検査する。

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
decision は一意の ID、source、text を持ち、修正した判断は新しい ID で追記する。
人間の選択が必要な場合は `humanDecision` に質問、選択肢、推奨選択肢と理由を記録し、
Task を `HUMAN_DECISION_REQUIRED` にする。選択後は option ID と対応する decision record を
追記してから READY へ戻す。
`memoryRefs` は `lesson:ID`、`decision:ID`、`guardrail:ID`、`concern:ID` の形式で
Memory 記録を参照する。Task Graph は参照 ID だけを保持し、Memory 本文を複製しない。
dispatch 時に参照先が存在し、失効・置換されていないことを確認する。
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
指示の baselineSha と一致することを検査する。SUCCEEDED には outputSha、成功した
validation、必須 evidence を要求する。終端状態は Dispatch ID ごとに一度確定する。
Dispatch ID で結果と状態遷移の対象を特定する。
Reviewer の SUCCEEDED result には verdict、全 acceptance の判断と evidence、
findings、worktree の変更有無を追加し、APPROVE は全件 PASS・finding なし・変更なしの場合に限る。
runtime はこの result schema を SDK に渡し、最終応答を Task・Dispatch の保存済み条件と照合する。
caller は照合済みの応答を `dispatch transition --result-file` に渡して状態を更新する。
transition は caller が報告した SHA、validation、evidence の必須項目と整合性を検査する。
Git object、検証コマンド、証拠の実物は、repository と test record を参照して別途確認する。

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
  "validations": [{"command": "go test ./...", "exitCode": 0}],
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
CLI は最終応答を Dispatch result schema と保存済み条件に照合する。
operator は照合結果を `dispatch transition --result-file` に渡して状態を更新する。
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

Memory の参照先の検証と指示生成、Session の実行・再開、
Human の実際の認証・承認確認は後続 Issue で実装する。
actor・decision・proof は caller が供給する作業記録である。
実行環境の権限確認と repository・test record の証拠は、それぞれの管理元で確認する。
