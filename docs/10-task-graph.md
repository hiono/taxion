# 10 — Task Graph の登録・遷移・保持

Task Graph と Ledger は SQLite の `workspace.db` に同時に保存する。
既存の `.taxion/ledger.json` と `.graph.json` は初回移行に限り読み込み、
移行確認のため一時的に残す。これらの JSON は移行後に更新せず、正本やバックアップとして
利用しない。取り込みを確認した後に削除する。以後の更新元は SQLite workspace となる。
設計の理由と移行の決定は
[ADR-0022](adr/0022-versioned-task-graph-beside-legacy-ledger.md) を参照する。

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
decision は ID、source、text を持ち、以前の decision の削除・上書きはできない。
新しい判断は別 ID で追加する。
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
dispatch は Graph の READY を検査するが、Graph を自動で ACTIVE にする処理と
dispatch の終了・差し替えは #48 の対象として残る。

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
