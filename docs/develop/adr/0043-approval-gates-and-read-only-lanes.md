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

## Decision

### 3つの gate

承認を次の3つの gate に集約する。

- Contract gate: Task が実行に入れるかを、保存済み Task と承認 bundle から判定する。
- Execution gate: 保存済み Flow の証拠が次の role または公開へ進めるかを判定する。
- Publication gate: 外部への公開要求を作れるかを、実行 gate と Task の状態から判定する。

各 gate の verdict は `pass`・`fail`・`blocked` の3値とする。`blocked` は入力または承認が不足していて、人間の判断または追加証拠を待つ状態を表す。`fail` は保存記録が矛盾しているか、前提を満たさない状態を表す。gate は判定だけを行い、承認・Dispatch・状態遷移を代行しない。gate の失敗は保存状態を変更しない。

### proposalHash と recordHash

Task の契約提案を正規化 JSON にし、SHA-256 を取った値を `proposalHash` とする。保存後の Task record から同じ手順で取った値を `recordHash` とする。保存時に既定値が補われると両者は異なりうる。bundle は両方を記録し、提案から保存 record への差分を後から確認できるようにする。差分の存在だけでは承認を無効にしない。

`proposalHash` は人間が確認する契約の同一性を表し、`recordHash` は保存記録の同一性を表す。bundle は両方を残す。

既存 READY Task の再承認では、`intake-confirmation:TASK_ID` decision の `expectedHash` を immutable な `proposalHash` とする。現在の Task record の hash や過去の bundle から提案の同一性を推定しない。preview、confirmation basis、保存 bundle、Verify はこの参照との一致を確認する。参照の欠落・曖昧さ・競合、既存 bundle との不一致は書込み前に拒否する。正規化の版は digest の計算規則であり、proposal identity を置換する根拠にはしない。旧 hash の暗黙変換や既存承認の修正も行わない。

### ApprovalBundle の7次元

承認 bundle は次の7つの dimension の digest を持つ。

| dimension | 対象 |
| --- | --- |
| baselineDigest | baseline SHA |
| scopeDigest | scope、outOfScope、fileAccess |
| acceptanceDigest | acceptance、requiredEvidence、validationCommands、exitCondition |
| budgetDigest | budgetGuidance |
| permissionsDigest | owner、reviewer、authorizationId、通常の decisions、worktree、branch、outputMode、target tree |
| securityDigest | risk、exceptionIds、specifications、memoryRefs |
| configurationDigest | 承認時に選択した設定 tuple の digest |

`configurationDigest` は選択の digest である。bundle は選択した設定 tuple と出典を `configuration.selection` に保存するが、解決済みの credential 値は保存しない。

### 版と正規化

bundle は `bundleVersion` と `canonicalizationVersion` を持つ。v0.7.0 の bundle はどちらも1である。v0.7.1 の READY Task 再承認 bundle は canonicalization version 2 を使う。version 2 では設定承認 decision を permissions digest から除く。設定選択そのものは configuration digest で検証するため、追記した設定承認が Task の permission contract を変えない。その他の decision は引き続き permissions digest に含める。hash は key 順を固定した正規化 JSON に対して取り、同じ値からは常に同じ hash を得る。未知の版を持つ bundle は `invalid` として扱い、互換扱いにしない。

### confirmation token

confirmation token は `proposalHash`、`bundleVersion`、`canonicalizationVersion` と7次元の digest から導出する確認用の digest である。人間が表示された契約と承認 basis を照合するために使う。認証 token ではなく、秘密でもない。token を知っていることは承認権限を意味しない。`taxion task authorize` と `taxion task reauthorize` は token を表示し、`--confirm` に同じ値を要求する。

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

drift の authorization impact は `reapproval-required` と `invalid` の2値を使う。`none` は予約値で、現行実装は返さない。`reapproval-required` は新しい bundle が必要な差分、`invalid` は保存記録の矛盾または未知の版を表す。`reapproval-required` を自動承認しない。

### Task の分類

Task は保存済み schema 情報だけで次の3つに分類する。

| 分類 | 条件 | 実行入口 |
| --- | --- | --- |
| `bundle-required` | approval schema version が現在値と一致する | 一致する bundle がなければ拒否 |
| `verified-legacy` | approval schema version がなく、bundle もない | 従来の契約検査で続行 |
| `invalid-or-ambiguous` | 未知の schema version、または schema version なしで bundle がある | 拒否 |

`verified-legacy` は v0.6.x の保存記録を読むための分類であり、新しい承認を省略する一般則ではない。分類は workspace の graph 全体に対して行い、bundle-required と verified-legacy を同じ graph に混在させない。

### 実行入口での fail-closed

bundle-required の graph では、各 Task は実行入口の各 stage（start-review-flow、coder-dispatch、reviewer-dispatch、repair-dispatch、re-review-dispatch）で、現在の設定選択に対する一致 bundle を要求する。bundle がない、または一致しない場合は Dispatch を発行しない。入口の拒否 code は `unknown-stage`・`unknown-task`・`task-not-ready`・`no-matching-bundle`・`invalid-bundle` である。

`verified-legacy` の Task は従来の検査を通る。`invalid-or-ambiguous` の Task は、修復されるまで実行入口で拒否する。

### 追記のみの承認履歴

fresh な READY Task に matching configuration approval があり、Flow・Dispatch と ApprovalBundle がまだない場合、`task authorize --manifest` は保存 Task と完全一致する manifest から初期 bundle だけを追記する。既存の immutable proposal reference と設定承認を再検証し、canonicalization version 2 を使う。Task state や decisions を変更して tracking をやり直したり、設定を変えて reauthorization を発生させたりしない。確認 token は workspace snapshot、manifest、configuration と出典に束縛し、transaction 内で再検証する。既存 bundle の内容が異なる場合は拒否し、同一要求は再利用する。Flow・Dispatch・deadline・budget と resume 契約はこの処理では変更しない。

承認は append-only で保存する。以前の bundle を書き換えたり削除したりしない。同じ basis に対する複数の承認がある場合、最新の一致 bundle を現在の根拠として扱う。同じ内容の再承認は既存 bundle を再利用し、監査記録を増やさない。

v0.7.1 の `taxion task reauthorize --task TASK_ID` は既存 READY Task 専用である。preview は書込みを行わず、Task state、proposalHash、現在の recordHash、7次元、redacted drift path、確認 token を示す。確認時に Task と effective configuration を再読込し、preview 後の drift を拒否する。確認が成立した場合だけ、一意な設定承認 decision と canonicalization version 2 の bundle を追記する。Task 契約、proposalHash、state、既存 decision は維持し、保存後に readback、bundle Verify、Task-bound configuration check を行う。同一 bundle の再送は既存 record を再利用する。

### credential の伏せ字

gate report、lane report、入口拒否の診断、drift の field path は、credential らしい値を伏せる。bundle ID も診断では伏せる。解決済みの credential 値を report に含めない。

### controller の read-only lane

controller は次の4つの read-only lane を並行実行する API を提供する。v0.7.0 では `taxion workflow lanes` から明示的に呼ぶ実行前確認であり、gate と実行入口は lane report を自動では消費しない。

| lane | 確認内容 |
| --- | --- |
| repository-identity | Task、Dispatch、instruction の repository が一致し、埋め込み credential がない |
| baseline-worktree | baseline SHA、branch、worktree が完全で、worktree が repository root の内側に実在する |
| contract-consistency | Task と最新 Dispatch instruction の objective、scope、acceptance、requiredEvidence が一致する |
| handoff-preparation | 有効な Dispatch があり、return format と instruction hash が揃い、running flow が handoff を保持していない |

lane は最大4本を並行実行し、固定順で集約する。各 lane には個別の timeout を設ける。一つでも失敗、取消、timeout になれば report の `executionGateOpen` を `false` にする。v0.7.0 ではこの値は運用者向けの advisory で、実行 gate の自動条件ではない。lane は Task Graph、Dispatch、Flow、worktree、runtime を変更せず、モデル session も開始しない。結果は version 付きの機械可読 report とし、credential を伏せる。

### immutable な role handoff packet

role handoff packet v1 は CODER・VALIDATOR・REVIEWER 向けに、packet version、Task ID/hash、baseline SHA、target tree hash、scope/acceptance digest、prohibited actions、前段 evidence hash、required output schema を含む。canonical serialization から packet hash を算出し、readback で hash と参照値を検証する。変更・欠落・余分な field は tamper として拒否する。credential を伏せ、会話全文と private reasoning は含めない。packet は参照情報であり実行許可ではない。role instruction は発行後に黙って書き換えず、契約外の変更は再承認を要求する。handoff の hold は次の role を止めるが、実行中の role を取り消さない。

保存 packet が欠落した READY Task には、既存の matching ApprovalBundle を変えずに `task authorize-packets` で lifecycle 承認を追記する。Task 本文や decisions には書き込まない。確認 token は Task record、immutable proposal reference、bundle、baseline / tree、scope / acceptance、欠落参照の申告、同一契約 Reviewer の承認方針に束縛する。既存 Dispatch の採用や、hash からの本文復元は認めない。

packet の canonical bytes と hash、lifecycle 承認、audit は既存 workspace の Records として append-only 保存する。初回 Coder は空の predecessor を許す。Coder の保存済み実 evidence から Validator packet を先に保存し、非モデル Controller が既存の結果 receipt と Git 再検証を行う。実検証に合格した GateReport の hash から Reviewer packet を作る。未来の evidence や packet hash を先取りしない。

明示確認した `same-contract-reviewer/v1` 方針に限り、Controller は同じ objective・scope・acceptance と完全一致の configuration を持つ派生 Reviewer Task の専用 bundle を追加する。`sha256:` 形式の元参照では、派生 Task の confirmation をその Task ID と元 proposal reference に束縛する。intake receipt を持たない manifest 承認 v1 では receipt を創作せず、元の prefix なし proposal reference を bundle に保持する。元 Task の confirmation・decisions・ApprovalBundle は変更しない。既存の execution-entry checker を緩和せず、追加した専用 bundle も通常どおり検査する。repair の契約変更や configuration / tool の許可拡張は委譲しない。

起動前に packet、Dispatch instruction、predecessor evidence を照合する。未知版、欠落、改変、不一致は拒否し、未承認の legacy 参照は検証済み packet と扱わない。非対応 binary への rollback のためにこれらの記録を消すことも認めない。詳細な利用条件は [role handoff packet](../../userguide/role-handoff-packets.md) を参照する。

### Task 単位の集計と再契約

`workflow evaluate` は Flow の Dispatch 数、Coder/Reviewer session 数、nano-AIU usage、elapsed time、repair/re-review 数について、approved limit・consumed・remaining を報告する。未実行の session/Dispatch を始める前と、usage event 後に ceiling を検査する。Task-wide usage が不明・不完全なら remaining を0として `BUDGET_STOP` にし、新しい session/Dispatch を許可しない。観測した usage が ceiling に達した実行中 session は安全に停止し、Flow を hold して checkpoint と report を保存する。

budget audit は Dispatch 発行、session 開始、usage 観測、budget stop、recontract を追記のみで保存する。既存 record の削除・変更を workspace transaction が拒否する。`workflow terminate` は最新の未確認・未開始 Coder Dispatch だけを取り消し、Flow を hold する。新しい Task と契約の再承認で続行し、過去の authorization/evidence history は保持する。Task-wide nano-AIU ceiling は provider の請求残高を予測せず、session ごとの SDK credit ceiling とは別の単位である。

### v0.6.x との互換性

- v0.6.x の ledger はそのまま読み込める。必須の移行 command はない。
- approval schema version を持たない既存 Task は `verified-legacy` として扱う。
- approval schema version を持たない graph に bundle が1つでもある場合は `invalid-or-ambiguous` になる。
- 新しい bundle は追記であり、既存の Task、Dispatch、Flow の意味を変えない。
- 旧 binary は未知の approval field を無視して読めるが、新しい gate の判定は行わない。

### 移行と rollback

移行は additive である。`task authorize` と `task reauthorize` が bundle を追記し、それ以外の command は保存形式を変換しない。rollback は binary の差し替えで行い、ledger を書き換えない。v0.6.x は従来の検査だけを行い、v0.7.0 は canonicalization version 2 の reauthorization bundle を拒否する。したがって、READY Task を再承認した workspace では v0.7.1 以降を使う。bundle を削除・置換して rollback する経路はない。

### v0.7.1 に送る項目

- 一般的な DAG executor
- 動的な lane plugin、retry scheduler、高度な cache、UI
- lane 結果の gate report への埋め込み

最後の項目は v0.7.0 では未接続のまま、それぞれの report を別々に読む。

## Consequences

- 人間は一つの confirmation token と7次元の表示で承認 basis を確認できる。
- 実行入口の検査が role と stage によらず同じ bundle を参照する。
- 保存記録の変更は drift として現れ、自動承認されない。
- read-only lane はモデルを呼ばずに実行前の不整合を見つける。v0.7.0 では運用者が明示的に実行し、結果は gate の自動条件ではない。
- v0.6.x の Task は移行なしで読める。workspace は graph 単位で bundle-required か verified-legacy のどちらかに分類されるため、運用者は workspace の分類を確認する。
- gate report は判定の観測であり、承認・公開・完了の証拠にはならない。


## Alternatives

- 単一の承認 hash だけを保存する: どの dimension が変わったかを説明できず、再承認の範囲を決められない。
- 承認時に解決済み credential 値も保存する: secret を ledger に残すため採用しない。
- legacy Task を一括で bundle-required に変換する: 未承認の変更を承認済みと誤認する危険がある。
- lane をモデル session 内で実行する: 実行前の確認に課金と非決定性を持ち込むため採用しない。
- gate report を既定の人間出力に混ぜる: 既存 CLI の出力契約を変えるため、機械可読 mode を明示 opt-in とする。
