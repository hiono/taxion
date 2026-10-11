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
decision は ID、source、text を持ち、以前の decision の削除・上書きはできない。
新しい判断は別 ID で追加する。
人間の選択が必要な場合は `humanDecision` に質問、選択肢、推奨選択肢と理由を記録し、
Task を `HUMAN_DECISION_REQUIRED` にする。選択した option ID と対応する decision record を
追記するまで READY に戻せず、実行もできない。
`memoryRefs` は `lesson:ID`、`decision:ID`、`guardrail:ID`、`concern:ID` の形式で
Memory 記録を参照する。Task Graph は参照 ID だけを保持し、Memory 本文を複製しない。
dispatch 時に参照先が存在し、失効・置換されていないことを確認する。
guardrail の参照先は承認済みでなければならない。Memory DB がないのに参照が設定されている場合も拒否する。
Memory 間の矛盾は `memory conflict` で人が明示し、未解決ならどちらの参照も dispatch できない。
issue は URL、`Issue #番号`、`INHERITED: Issue #番号`、
`PENDING: 登録TaskID`、または理由付きの `NOT_REQUIRED:` を使う。
PENDING は登録 Task の COMPLETED を依存に持ち、実行前に実際の参照へ更新する。
NOT_REQUIRED の理由の妥当性や外部 Issue の実在は、この局所検査だけでは保証しない。

`deferredReason` を持つ Task は BLOCKED と nextAction・resumeCondition を要求する。
再開時は BLOCKED のまま `task track` で保留を解除し、
判断と理由を残してから READY へ遷移する。
却下・cancel・置換へ進めても以前の保留理由と履歴を保持する。
契約の編集は DRAFT・BLOCKED・HUMAN_DECISION_REQUIRED に限定する。
active または terminal の契約は直接書き換えない。

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
理由を履歴に保存する。これは worker の停止が確認されたことを意味しない。

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

Task の requiredEvidence と acceptance を満たす参照、reviewer との一致、
owner とは異なる reviewer、targetSha との一致を検査する。
親の承認・完了には子の完了を要求し、
未完了の子を暗黙に達成扱いにしない。
例外は `exceptionIds` と decision に登録した ID を使い、
proof の `waivers` に decisionId と免除する acceptance・evidence・children を明示する。
レビュー情報の欠落は免除しない。

これは構造化した結果の照合であり、Git の object や検証レポートの実在、
Reviewer process の独立性を保証するものではない。
既存の `review` による SHA 記録だけで Graph は承認・完了へ進まない。
dispatch は Graph の READY、依存、承認済み scope、worktree、baseline SHA を検査する。
発行時に Task を ACTIVE にし、Task の関係、実行範囲、必要証拠、返却形式を含む
Dispatch 指示を不変の snapshot として保存する。指示 hash は保存内容から計算し、
CLI と ACP は同じ snapshot を人が読める形で表示する。
指示には `DISPATCH TO` を先頭に、Dispatch の関係、Task の親子・依存関係、
目的、scope、証拠と終了条件、worktree・branch・SHA、返却形式を含める。
`AUTHORIZED ACTIONS` は Task の `scope`、`FORBIDDEN ACTIONS` は `outOfScope` から作る。
これらは指示に保存する制約であり、OS や Git の操作を隔離する sandbox ではない。
後続 Dispatch は変更・維持した instruction field を保存する。`CONTINUES` はすべての
実行契約 field が前回と一致するときに限り、`AMENDS` は少なくとも一つを変更するときに使う。
`INDEPENDENT` は別 Task を参照するため、前回 instruction との比較は行わない。

```sh
taxion dispatch --task TASK-ID --repo owner/repository
taxion dispatch transition --id DISPATCH-ID --status ACKNOWLEDGED --actor worker --reason accepted
taxion dispatch transition --id DISPATCH-ID --status RUNNING --actor worker --reason started
taxion dispatch transition --id DISPATCH-ID --status SUCCEEDED --actor worker --reason finished --result-file RESULT.json
```

終端状態への遷移には `DispatchResult` JSON を必要とする。result の Dispatch ID、
Task ID、recipient、repository、status が対象 Dispatch と一致し、startedSha が
指示の baselineSha と一致することを検査する。SUCCEEDED には outputSha、成功した
validation、必須 evidence を要求する。終端 Dispatch の状態は再遷移できないため、
以前の Dispatch に属する結果で新しい Dispatch を完了させることはできない。
Reviewer の SUCCEEDED result には verdict、全 acceptance の判断と evidence、
findings、worktree の変更有無を追加し、APPROVE は全件 PASS・finding なし・変更なしの場合に限る。
runtime はこの result schema を SDK に渡し、最終応答を Task・Dispatch の保存済み条件と照合する。
照合済みの応答も Git object や検証コマンドの実行を証明しないため、状態を変えるには
別途 `dispatch transition --result-file` を実行する。
結果の SHA や検証内容は caller が申告した値であり、Git object・コマンド実行・証拠の
実在をこの検査だけで確認するわけではない。

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
取得できない値は省略し、ゼロとして扱わない。usage event は計測時刻とともに Dispatch に保存する。
resume 時は role prompt、Dispatch snapshot、worktree、tool allowlist、permission policy を再指定し、設定 hash の一致を照合する。
Coder は worktree 内の file write と allowlist 済み SDK tool の一回限りの利用を許可し、shell と worktree 外の操作を拒否する。
Reviewer は worktree 内の file read だけを許可し、write・shell 等を拒否する。
permission handler は OS sandbox ではなく、Git 操作やすべての外部 tool の隔離を証明しない。

CLI `runtime start` は保存済み Dispatch 指示を role 固有 system prompt に加えて session を作り、
短い実行要求を送る。`send` は追加指示を渡す。event cursor で新しい turn の出力だけを読み、
SDK compaction 後も role と Task Graph の実行契約が system context に残る。
context 使用量と compaction の結果を event stream で確認できる。
turn の完了や出力イベントは Dispatch 成功の証拠にならず、終了時は結果 schema に従った明示的な遷移が必要。
ACP と HTTP API は Copilot session を自動起動しない。

## 保存・互換性と制約

Graph がない workspace は旧方式を維持する。
Graph を導入した workspace は全 dispatch 対象の登録が必要で、
CLI・HTTP・ACP の共通検査が未登録、非 READY、repository 不一致を拒否する。
既存の active Dispatch がある場合は、新規 Graph の導入を拒否する。
実行状態を確認して既存の Dispatch を解決してから移行し、
active な作業を DRAFT として再登録しない。
Web は HTTP の拒否を返す。Graph の read/edit HTTP・ACP API はまだない。
既存 Ledger の wire は変更しない。

Graph の保存は検証後の rename とし、失敗した登録・遷移では保存内容を変更しない。
CLI・HTTP・ACP の更新は SQLite transaction で Ledger と Graph を一緒に変更する。
SQLite が同時 writer の更新を受け入れられない場合は明示的なエラーとなり、
呼び出し元は成功として扱わず再試行できる。
移行元 JSON を残している間も SQLite との同期は行わないため、SQLite の復元には使えない。
Graph の破損、非対応の version、未知のキー、履歴と現状態の不一致はエラーとする。
この履歴検査は改ざん防止や管理者による全体の書き換え検出を保証しない。

Memory の参照先の検証と指示生成、Session の実行・再開、
Human の実際の認証・承認確認は後続 Issue で実装する。
保存した actor・decision・proof は caller が供給する記録であり、
記録した文字列だけで実際の権限や実施を保証しない。
