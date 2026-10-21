# 43. 承認を3つの gate と read-only lane に集約する

Date: 2026-10-21

## Status

Accepted

## Context

v0.6.x は、Task の承認、実行開始、独立レビュー、公開をそれぞれ別の操作として扱ってきた。Task の契約は変更できるが、どの変更が以前の承認を無効にするかは、操作ごとの検査に散在していた。実行開始時には Task の現在値だけを読み、承認時点の baseline、scope、acceptance、予算、権限、security 条件、設定選択との差分を一つの記録として残していなかった。

この方式には三つの問題がある。

第一に、承認の根拠が一つの hash に集約されていない。Task の一部だけを変更した場合、どの項目が変わったかは ledger の履歴を人手で比べる必要がある。

第二に、実行直前の検査が role ごとに異なる。Coder の Dispatch は開始できても、Repair や再レビューの開始時に同じ検査が行われない経路があった。

第三に、実行を始める前に読むだけで済む確認と、モデル session を必要とする確認が分かれていない。repository identity、baseline、worktree、契約の整合、handoff の準備は、モデルを呼ばずに並行して確認できる。

Issue #109 で bundle と実行入口の検査を実装し、Issue #110 で read-only lane、Issue #112 で機械可読な gate report、Issue #114 でこの文書を追加する。v0.7.0 の範囲はここまでとし、Task 単位の集計と再契約は v0.7.1 に分ける。

## Decision

### 3つの gate

承認を次の3つの gate に集約する。

- Contract gate: Task が実行に入れるかを、保存済み Task と承認 bundle から判定する。
- Execution gate: 保存済み Flow の証拠が次の role または公開へ進めるかを判定する。
- Publication gate: 外部への公開要求を作れるかを、実行 gate と Task の状態から判定する。

各 gate の verdict は `pass`・`fail`・`blocked` の3値とする。`blocked` は入力または承認が不足していて、人間の判断または追加証拠を待つ状態を表す。`fail` は保存記録が矛盾しているか、前提を満たさない状態を表す。gate は判定だけを行い、承認・Dispatch・状態遷移を代行しない。gate の失敗は保存状態を変更しない。

### proposalHash と recordHash

Task の契約提案を正規化 JSON にし、SHA-256 を取った値を `proposalHash` とする。保存後の Task record から同じ手順で取った値を `recordHash` とする。両者が一致しない場合、保存の readback が提案と異なるため承認は成立しない。

`proposalHash` は人間が確認する契約の同一性を表し、`recordHash` は保存記録の同一性を表す。片方だけでは承認の根拠にならない。

### ApprovalBundle の7次元

承認 bundle は次の7つの dimension の digest を持つ。

| dimension | 対象 |
| --- | --- |
| baselineDigest | baseline SHA |
| scopeDigest | scope、outOfScope、fileAccess |
| acceptanceDigest | acceptance、requiredEvidence、validationCommands、exitCondition |
| budgetDigest | budgetGuidance |
| permissionsDigest | owner、reviewer、authorizationId、decisions、worktree、branch、outputMode、target tree |
| securityDigest | risk、exceptionIds、specifications、memoryRefs |
| configurationDigest | 承認時に選択した設定 tuple の digest |

`configurationDigest` は選択した設定値そのものではなく、選択の digest である。credential は保存しない。

### 版と正規化

bundle は `bundleVersion` と `canonicalizationVersion` を持つ。v0.7.0 ではどちらも1である。hash は key 順を固定した正規化 JSON に対して取り、同じ値からは常に同じ hash を得る。未知の版を持つ bundle は `invalid` として扱い、互換扱いにしない。

### confirmation token

confirmation token は `proposalHash` と7次元の digest から導出する確認用の digest である。人間が表示された契約と承認 basis を照合するために使う。認証 token ではなく、秘密でもない。token を知っていることは承認権限を意味しない。`taxion task authorize` は token を表示し、`--confirm` に同じ値を要求する。

### 再承認が不要な内部遷移

承認済みの basis の内側で完結する次の遷移は、再承認を要求しない。

- 承認済み scope 内の Coder・Reviewer・Repair Dispatch の進行
- 保存済み ceiling 内の repair 反復
- read-only の観測、評価、gate report、lane report
- handoff の hold と release
- 同一契約内の instruction draft の作成と破棄

これらは Task の契約、scope、acceptance、予算、権限、security 条件、設定選択を変更しない。Dispatch の発行自体は既存の実行入口検査を通る。

### 再承認が必要な条件

次のいずれかが変わると、以前の bundle は現在の basis を覆わない。

- baseline、scope、acceptance、requiredEvidence、validationCommands、exitCondition
- 予算、owner、reviewer、worktree、branch、outputMode、target tree
- risk、exception、specification、memoryRef
- 承認時の設定選択
- bundle または正規化の版

drift の authorization impact は `none`・`reapproval-required`・`invalid` の3値である。`none` は表示だけの差分、`reapproval-required` は新しい bundle が必要な差分、`invalid` は保存記録の矛盾または未知の版を表す。`reapproval-required` を自動承認しない。

### Task の分類

Task は保存済み schema 情報だけで次の3つに分類する。

| 分類 | 条件 | 実行入口 |
| --- | --- | --- |
| `bundle-required` | approval schema version が現在値と一致する | 一致する bundle がなければ拒否 |
| `verified-legacy` | approval schema version がなく、bundle もない | 従来の契約検査で続行 |
| `invalid-or-ambiguous` | 未知の schema version、または schema version なしで bundle がある | 拒否 |

`verified-legacy` は v0.6.x の保存記録を読むための分類であり、新しい承認を省略する一般則ではない。分類は Task ごとに行い、一括変換しない。

### 実行入口での fail-closed

bundle-required の Task は、実行入口の各 stage（start-review-flow、coder-dispatch、reviewer-dispatch、repair-dispatch、re-review-dispatch）で、現在の設定選択に対する一致 bundle を要求する。bundle がない、または一致しない場合は Dispatch を発行しない。入口の拒否 code は `unknown-stage`・`unknown-task`・`task-not-ready`・`no-matching-bundle`・`invalid-bundle` である。

`verified-legacy` の Task は従来の検査を通る。`invalid-or-ambiguous` の Task は、修復されるまで実行入口で拒否する。

### 追記のみの承認履歴

承認は append-only で保存する。以前の bundle を書き換えたり削除したりしない。同じ basis に対する複数の承認がある場合、最新の一致 bundle を現在の根拠として扱う。同じ内容の再承認は既存 bundle を再利用し、監査記録を増やさない。

### credential の伏せ字

gate report、lane report、入口拒否の診断、drift の field path は、credential らしい値を伏せる。bundle ID も診断では伏せる。設定値そのものを report に含めない。

### controller の read-only lane

実行入口の前に、controller は次の4つの lane を並行実行する。

| lane | 確認内容 |
| --- | --- |
| repository-identity | Task、Dispatch、instruction の repository が一致し、埋め込み credential がない |
| baseline-worktree | baseline SHA、branch、worktree が完全で、worktree が repository root の内側に実在する |
| contract-consistency | Task と最新 Dispatch instruction の objective、scope、acceptance、requiredEvidence が一致する |
| handoff-preparation | 有効な Dispatch があり、return format と instruction hash が揃い、running flow が handoff を保持していない |

lane は最大4本を並行実行し、固定順で集約する。各 lane には個別の timeout を設ける。一つでも失敗、取消、timeout になれば execution gate を閉じる。lane は Task Graph、Dispatch、Flow、worktree、runtime を変更せず、モデル session も開始しない。結果は version 付きの機械可読 report とし、credential を伏せる。

### immutable な role handoff packet

role への instruction は hash を持ち、発行後に黙って書き換えない。変更が必要な場合は新しい Dispatch または version 付き draft を作り、契約の外に出る変更は再承認を要求する。handoff の hold は次の role を止めるが、実行中の role を取り消さない。

### Task 単位の集計と再契約

Dispatch、credits、時間の Task 単位の集計と、未起動 Dispatch の再契約は v0.7.1 の範囲とする。v0.7.0 はこれらの記録形式を先取りしない。既存の Dispatch usage と review flow の評価は変更しない。

### v0.6.x との互換性

- v0.6.x の ledger はそのまま読み込める。必須の移行 command はない。
- approval schema version を持たない既存 Task は `verified-legacy` として扱う。
- 既存 Task に矛盾した bundle がある場合だけ `invalid-or-ambiguous` になる。
- 新しい bundle は追記であり、既存の Task、Dispatch、Flow の意味を変えない。
- 旧 binary は未知の approval field を無視して読めるが、新しい gate の判定は行わない。

### 移行と rollback

移行は additive である。`task authorize` が bundle を追記し、それ以外の command は保存形式を変換しない。rollback は binary を v0.6.x に戻すことで行い、ledger を書き換えない。bundle を残したまま旧 binary で実行すると、旧 binary は従来の検査だけを行う。rollback 後に再度 v0.7.0 を使う場合も、bundle はそのまま読める。

### v0.7.1 に送る項目

- Task 単位の Dispatch・credits・時間の集計
- 未起動 Dispatch の再契約
- 一般的な DAG executor
- 動的な lane plugin、retry scheduler、高度な cache、UI
- lane 結果の gate report への埋め込み

最後の項目は #110 と #112 の merge 後に接続する。それまでは両 report を別々に読む。

## Consequences

- 人間は一つの confirmation token と7次元の表示で承認 basis を確認できる。
- 実行入口の検査が role と stage によらず同じ bundle を参照する。
- 保存記録の変更は drift として現れ、自動承認されない。
- read-only lane はモデルを呼ばずに実行前の不整合を見つける。lane の追加は実行 gate の条件を増やすが、Task の状態を変えない。
- v0.6.x の Task は移行なしで読めるが、bundle-required の Task と verified-legacy の Task が混在する。運用者は Task ごとの分類を確認する必要がある。
- gate report は判定の観測であり、承認・公開・完了の証拠にはならない。

## Alternatives

- 単一の承認 hash だけを保存する: どの dimension が変わったかを説明できず、再承認の範囲を決められない。
- 承認時に全設定値を保存する: credential と環境依存値を ledger に残すため採用しない。
- legacy Task を一括で bundle-required に変換する: 未承認の変更を承認済みと誤認する危険がある。
- lane をモデル session 内で実行する: 実行前の確認に課金と非決定性を持ち込むため採用しない。
- gate report を既定の人間出力に混ぜる: 既存 CLI の出力契約を変えるため、機械可読 mode を明示 opt-in とする。
