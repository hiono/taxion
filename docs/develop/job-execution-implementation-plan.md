# Job 中心の Role 実行モデル実装計画

Status: In progress — replacement, not compatibility migration  
Decision: [ADR-0077](adr/0077-job-centered-role-collaboration.md), [ADR-0079](adr/0079-enforce-domain-invariants-across-state-paths.md), [ADR-0080](adr/0080-job-boundary-and-prohibited-effects.md)  
Tracking: [Issue #283](https://github.com/hiono/taxion/issues/283)  
Implementation: [PR #292](https://github.com/hiono/taxion/pull/292)

## 規範方針\n\n本計画は旧 Task / Dispatch / ReviewFlow を温存する移行計画ではない。旧経路の責務を Job / Task / RoleExecution / Controller へ再配置し、Job-native経路が成立したら重複する旧実行・認可経路を削除する。旧経路のテストを通すために新モデルを曲げない。旧workspaceの履歴が必要な場合だけ、実行・認可を行わない明示的でloss-awareな移行境界を設ける。\n\n## 1. 完成条件

本計画の完了は、データ型や協調履歴 API の追加ではなく、次の一連の動作が Controller・Runtime adapter・永続化を通して成立した状態とする。

1. Job を開始し、担当 Role が Job Context を受け取って実行する。
2. Role は情報が不完全でも、判断可能な範囲を仮定と不確実性を伴って進められる。
3. Role は必要に応じて別 Role または人間へ自然言語で依頼できる。
4. 依頼元の実行は Job 上で待機し、受信側の実行は同じ Job に属する独立した実行として開始する。
5. 回答・判断・部分成果は元の Job に保存され、依頼元 Role がそれを受け取って続行する。
6. Role の回答や仮定では、承認・権限・対象・予算・期限・不可逆操作の許可範囲が拡張されない。
7. 再起動・重複要求・失敗・中断後も、実行状態と成果の出所を壊さず安全に復旧できる。
8. Reviewer は独立した実行環境と判断を維持し、成果物と検証証拠を基に受入れを判断する。

合成テストの成功、JobMessage の保存、Dispatch の発行だけでは完成と判定しない。実モデル E2E は別の受入れゲートとする。

## 2. 現状の制約と設計方針

現在の `TaskGraph` は Task、Dispatch、ReviewFlow、JobMessage を同じ保存領域に持つ。JobMessage の `TaskID` は Job の識別子として暫定利用されているが、Task と Job の責務・寿命は分離されていない。

現在の `GraphDispatch` は Task、Role、Runtime session、指示、承認根拠、結果を結合している。Controller は Dispatch を実行単位として Start / Send / Resume を行い、Task owner と Coder / Reviewer の固定条件が実行経路へ組み込まれている。ReviewFlow は Coder → Reviewer → Coder の工程を制御する。

このため、Planner を許可リストへ追加するだけの変更は採用しない。

設計上の責務を次のように分離する。

- **Job**: 利用者の依頼、目的、制約、承認 scope、成果、協調履歴を保持する継続単位。
- **Task**: Job 内で実行・検証する作業対象と範囲。Job と異なる識別子・寿命を持つ。
- **RoleExecution**: 一つの Role が一回の実行を行う単位。Job の寿命や他 Role の実行寿命から独立する。
- **Collaboration**: RoleExecution 間または Role と人間の依頼・回答・判断・部分成果を Job に記録する。
- **Controller**: Job / RoleExecution の状態遷移、実行権限、承認、予算、期限、排他、出所、復旧を管理する。
- **Runtime adapter**: 一つの RoleExecution に対する SDK 非依存の開始・送信・観測・停止を提供する。Job の工程順序は決めない。

### 正規の所有関係（実装着手前に固定する）

Job と Task の関連は **Task 側が `JobID` を保持する単方向参照**を正とする。Job 側に `TaskIDs` の逆引き配列を持たせてはならない。

- Job は独立した継続単位として ID、依頼、目的、制約、状態、成果、協調履歴を所有する。
- Task は独立した作業単位として ID と `JobID` を持つ。複数 Task が同じ JobID を参照できる。
- Task の所属検索は Task 側の JobID から解決する。Job.TaskIDs や RootTaskID による所有関係の二重管理を導入しない。
- RoleExecution は必須の JobID と、必要な場合だけ TaskID を持つ。TaskID がある場合は Task.JobID と一致しなければならない。
- Job の生成と Task の生成は同一操作である必要はない。Job は Task が存在しなくても成立でき、Task は必ず既存 Job に属する。
- 既存の Task approval hash、承認記録、履歴を変更しないことを理由に、この正規モデルを曲げてはならない。必要な場合は契約・hash・migration を新モデルに合わせて再設計する。
- 旧 workspace の移行はデータ保全のために行うが、旧 protocol の互換性維持を新モデルの実行条件にしない。

**移行境界:** 現行モデルは `Task.JobID` を正規参照とし、`Job.TaskIDs` / `RootTaskID` をドメイン構造として保持しない。旧 workspace の読込み時に限り `TaskGraph.UnmarshalJSON` が旧フィールドを入力形式として受け取り、Task 側の `JobID` へ移行する。移行後の保存形式・検索・認可・実行経路では旧フィールドを参照しない。旧 Task の複数 Job 所属や既存 JobID との衝突は暗黙に解決せず、移行エラーとして扱う。

既存 workspace の読み込みを壊さない移行を行う。旧データを暗黙に破棄したり、旧 protocol を新モデルの中心に残したりしない。

## 3. 実装フェーズ

### Phase 0 — 設計確定と実行経路の棚卸し

- [x] ADR-0077 と Issue #283 の要求を実装受入れ条件へ展開する。
- [x] 現行 Dispatch / Controller / Runtime の責務集中を確認する。
- [x] Job・Task・RoleExecution の正規の所有方向を確定する（Task.JobID を正とし、Job.TaskIDs / RootTaskID は採用しない）。
- [ ] Job・Task・RoleExecution の永続化境界、識別子、状態遷移をこの所有方向に基づいて確定する。
- [ ] Workspace の既存データ移行と読み込み互換方針を確定する。
- [ ] Runtime adapter の factory・設定・worktree・tool policy の結合点を特定する。
- [ ] 承認、認証、budget、deadline、repository access の再検証位置を明示する。

**Exit gate:** データモデル、状態遷移、実行開始から終了までの責務分担、移行規則がコード着手可能な粒度で定義されていること。

### Phase 1A — Job authority / output / evidence の設計ゲート

- [ ] Job の承認対象を immutable revision として定義する（request、goal、constraints、acceptance、repository/resource/worktree boundary、file-access ceiling、budget、deadline、explicit prohibitions、approval-required side effects、configuration references）。通常操作の allowlist は承認対象にしない（ADR-0080）。
- [ ] Job approval record の canonical proposal hash、record hash、version、approver/source/reason/time、append-only 更新規則を定義する。
- [x] Job resource boundary と Task resource boundary の包含関係、および RoleExecution ごとの境界・禁止副作用評価を定義する。通常操作の Role別 allowlist は使用しない（ADR-0080）。
- [ ] Job approval と既存 Task approval bundle の役割を分離する。旧 Task approval / packet / dispatch records を Job approval へ移行・継承しないことを migration test で固定する。
- [x] Job output / evidence reference の provenance、immutability、verification state、acceptance condition との対応を定義する。
- [x] Job completion の判定条件と unresolved execution / safety state による停止条件を定義する。論理条件・判定・遷移 gate・Graph validation・テストの対応は [domain-invariants.md](domain-invariants.md) に記録する。

**Exit gate:** Job approval が唯一の Job-level authority であり、Task/RoleExecution は承認済み Job scope を超えられないこと、旧 Task approval が新 Job の authority にならないこと、成果・証拠の provenance と受入れ責任が明確であることを ADR とテスト可能な規則で確定する。Exit gate 通過前に Runtime 実行経路へ Job approval を接続しない。

**2026-11-13 時点の是正事項:** Phase 1A が未完了であるにもかかわらず、現行コードには `JobRoleController.Start / Resume / Send` と操作認可経路が存在する。これらは Phase 1A の完了証拠とはみなさず、以降の Runtime 経路拡張・CLI 接続を停止する。先に Job approval の authority 契約、移行規則、Task approval 非継承テスト、Job completion 条件を確定する。

新しい Job intake は JobApproval のみを Job-level authority とする。旧 Task approval bundle や Task configuration decision を新規に authority として生成・継承・変換しない。旧 workspace に存在する Task approval 等は、明示的な migration/history として保全する場合に限り読み取る。新しい Job の認可判定は旧 Task approval の有無・内容に依存させない。

### Phase 1 — Job と RoleExecution の永続化モデル

- [x] Job を Task から独立した first-class entity として導入する。
- [x] Task に JobID を持たせ、Job.TaskIDs による逆方向の重複管理を撤廃する。
- [x] Job を Task の有無に依存せず生成・永続化できるようにする。
- [ ] intake を Job 生成と Task 生成の責務に分解し、Task は既存 Job に所属させる。
- [ ] RoleExecution に Job、担当 Role、対象 Task（必要な場合）、親実行、状態、Runtime binding、開始・終了・停止理由、結果参照を保持する。
- [ ] RoleExecution の状態遷移と重複実行防止を core で検証する。
- [ ] JobMessage を Job に直接結び付け、送信元・宛先・依頼元実行・応答対象実行を追跡できるようにする。
- [ ] 既存の JobMessage.TaskID 暫定モデルからの移行を行い、履歴を失わない。
- [ ] 旧 Task approval hash / approval record の互換維持を理由に Task.JobID モデルを妥協しない。必要な hash・承認契約の変更と移行を設計する。
- [ ] clone、validate、永続化、workspace backup/recovery を更新する。

**Exit gate:** Job と Task の ID が独立し、Task.JobID を正規の所有参照として複数 Task と複数 RoleExecution が一つの Job に属する状態を永続化・再読込できること。Job 側の TaskIDs / RootTaskID による二重管理がなく、異常な cross-Job 参照や不正遷移を拒否できること。

### Phase 2 — RoleExecution 中心の Controller / Runtime 経路

- [ ] Dispatch 固定の Start / Send / Resume 経路を RoleExecution 中心へ置き換える。
- [ ] RoleExecution ごとに Role、worktree、Runtime configuration、tool policy を固定して adapter を構築する。
- [ ] Job Context を目的、制約、関連 Task、成果、協調履歴から組み立てる。
- [ ] 実行開始・session binding・イベント観測・結果保存を永続状態遷移と整合させる。
- [ ] session の再利用・再接続は同一 RoleExecution に限定する。
- [ ] Runtime の開始前失敗と開始後の実行状態不明を区別し、安全な復旧を行う。

**Exit gate:** RoleExecution が Dispatch / ReviewFlow の固定順序に依存せず開始・観測・停止・再接続できること。安全性は旧Tool allowlistの温存ではなく、Job境界・禁止副作用・実行基盤の強制境界で成立すること。

### Phase 2A — Job-native authorization migration (ADR-0080)

**Implementation status (2026-11-14): migration is not complete.** The current
Job-native path still enforces `RoleToolPolicies`, `RoleOperations`, and
provider tool-policy equality in `internal/runtime/controller/job_execution.go`.
Copilot and OpenACP also retain provider-specific tool availability checks.
The comments in the controller now identify the Job ACL checks as transitional;
this is documentation of current debt, not a replacement for enforcement.

The first implementation unit must establish the domain-level boundary/effect
model and its canonical approval representation before removing the transitional
checks. Do not delete the checks independently: that would create an authorization
gap. Preserve immutable Job revision, execution/session identity, Task containment,
ValidationCommands exact binding, credential-specific controls, and legacy
Task/ReviewFlow protections throughout the migration.


- [ ] `JobContract.Operations` を通常操作の許可リストとして使用する検証・承認ハッシュ依存を置換する。
- [ ] `RoleOperations` による Role-to-operation ACL と、`RoleToolPolicies` の完全一致を Job authority の条件から分離する。
- [ ] Provider `AvailableTools` / `ToolPolicyIdentity` は Runtime が実際に提供する能力・構成の情報として扱い、Job approval の許可集合として扱わない。
- [ ] Job approval の repository/resource/worktree/file-access boundary と明示的禁止事項を、Start / Resume / Send / privileged tool preflight で一貫して検査する。
- [ ] ValidationCommands は承認済み command string との完全一致を維持し、generic な `process.exec` 能力だけで任意 command を承認しない。
- [ ] Copilot / OpenACP の未知 Tool は、名前が事前列挙されていないことだけを理由に拒否せず、Tool の実際の副作用・実行基盤の隔離・Job境界から判定する。副作用を判定・封じ込めできない場合は承認要求または拒否とする。
- [ ] 事前登録されていない通常の Job 内操作が通る正例と、Job境界外アクセス・秘密情報送信・承認外副作用・不明な危険副作用を拒否する負例を追加する。
- [ ] 旧 Task / ReviewFlow の実行・認可経路を並行runtimeとして維持せず、Job-nativeの同等以上の安全境界へ置換して削除する。

**Exit gate:** Role別操作許可リストを使わずに通常の Job 内操作が実行でき、対象境界・禁止副作用・人間承認・ValidationCommands完全一致・Job/Session/RoleExecution整合性が Copilot と OpenACP の両方で強制されること。ADR作成だけでは本Gateを通過したとみなさない。

### Phase 3 — Role 間依頼と元実行の再開

- [ ] RoleExecution から自然言語の依頼・質問・部分成果を Job に記録できるようにする。
- [ ] Controller が依頼を受け、宛先 Role の実行権限・承認 scope・予算・期限を独立に検査する。
- [ ] 元 RoleExecution を待機状態にし、宛先 RoleExecution を同じ Job 上で開始する。
- [ ] 受信 Role に必要な Job Context と依頼内容を渡す。固定質問 category や回答 schema は要求しない。
- [ ] 回答・部分成果を Job に追記し、元 RoleExecution の再開入力として渡す。
- [ ] 人間への依頼は Job の待機状態として保存し、回答後に元の RoleExecution を再開する。
- [ ] 再送・重複回答・宛先失敗・元実行消失・Job 終了済みのケースを安全に扱う。

**Exit gate:** Coder → Planner → Coder などの協調が、同一 Job の別 RoleExecution として実際に往復し、元の実行が回答を使って続行できること。固定された Role 順序を必要としないこと。

### Phase 4 — 安全境界・成果物・Reviewer の統合

- [ ] Role 間の受渡しで承認 scope を引き継ぐ場合も、宛先 Role・Task・操作を Controller が照合する。
- [ ] Role の発言・回答・仮定を承認や権限として扱わない。
- [ ] Job 全体と各 RoleExecution の予算・期限・同時実行上限を整合させる。
- [ ] repository、file access、tool policy、secret、不可逆操作の制限を Role ごとに維持する。
- [ ] Reviewer に独立 worktree / context / session を与え、Coder の自己申告を受入れ根拠にしない。
- [ ] 既存 ReviewFlow / Clarification / role packet のうち新モデルを阻害する経路を置換または削除する。
- [ ] 既存 workspace の移行、バックアップ、復旧、CLI 表示を更新する。

**Exit gate:** Role の受渡し・再開によって認可や実行 scope が拡張されず、Reviewer の独立性と成果物検証が保たれること。

### Phase 5 — 統合テストと実モデル E2E

- [ ] core の状態遷移・参照整合性・移行テストを追加する。
- [ ] Controller / Runtime adapter の開始、handoff、回答、再開、停止、復旧を統合テストする。
- [ ] 不完全な任意情報を仮定して進めるケースを実モデルで確認する。
- [ ] 判断に不可欠な情報を質問し、回答後に同じ Job の元 Role が続行するケースを実モデルで確認する。
- [ ] Role の回答が承認・権限・予算・期限を拡張しないことをテストする。
- [ ] Reviewer の独立実行と成果物に基づく受入れを確認する。
- [ ] `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` と CI を確認する。
- [ ] 合成テスト、ローカル adapter fixture、実モデル E2E の証拠を明確に分けて報告する。

**Exit gate:** Issue #283 の全受入れ条件を満たし、実モデル E2E の二つの協調シナリオが確認されていること。

## 4. 反復ループと変更管理

### 旧経路の識別と削除リファクタリング

旧設計に属すると確認できたコードは、削除までの間、コードコメントで旧経路であることを明示する。コメントには次を記載する。

- 旧経路であること、および現在の移行上の役割
- 現時点で残している具体的な依存先・理由
- Job-native 経路への移行先
- 削除可能になる条件
- 旧経路のデータ・判断・承認を新モデルの authority として扱ってはならない場合、その禁止事項

コメントは旧経路を恒久的に維持する理由にしてはならない。呼出し元から順に依存を解消し、削除条件を満たした時点で旧コードとコメントを同じ変更単位で削除する。単なる名称変更やコメント追加だけで移行完了とは判定しない。


各フェーズは、次のループで進める。

1. **計画:** 対象責務、変更ファイル、依存関係、受入れ条件、リスクを定義する。
2. **実装:** 受入れ条件を満たすまとまりで実装する。互換性だけを理由に旧設計へ分岐を追加しない。
3. **テスト:** 変更に直接対応するテストを追加し、関連 package / integration test を実行する。
4. **評価:** 失敗原因を分類し、設計不備なら実装を継ぎ足さず設計へ戻る。
5. **保存:** 公開対象ファイル全体を確認し、秘密情報・個人情報・内部運用情報・環境依存情報・削除済み機密資料の復活がないことを確認する。
6. **コミットとプッシュ:** コミット単位は変更行数ではなく、独立して説明・検証できる意味の単位とする。一つの論理変更を構成する複数ファイルは同一コミットにまとめ、独立した目的の変更は分離する。改行・空白・gofmt のみを目的とするコミットは原則作成せず、書式整形は関連する意味のある変更に含める。リポジトリ全体の書式統一など、独立した保守目的と受入れ条件がある場合のみ例外とする。コミット後は同じブランチへ push し、remote SHA と CI を確認する。
7. **計画更新:** 完了条件と残課題を本計画および Issue #283 に反映する。

コード変更を長時間ローカルだけに保持しない。コミットとプッシュは一組の保存操作として扱う。push が失敗した場合は remote head を再確認し、未保存の変更を破棄・上書きしない。force push は使用しない。

## 5. 安全性と完了判定

- 不明な公開分類は公開不可として扱う。
- 公開前検査は差分だけでなく、追加・変更・復元するファイルの全内容を対象とする。
- 認証、認可、承認、秘密情報、scope、予算、期限、二重実行、不可逆操作、実行状態不明は fail-closed とする。
- CI 成功はコード検査の結果であり、実モデルの協調成功を意味しない。
- PR は Draft のまま維持し、利用者の明示承認なしに merge しない。
- フェーズの exit gate を満たさない場合、後続フェーズへ進んだ扱いにしない。


### Domain invariant enforcement (ADR-0079)

Job completion は、判定関数の存在だけでは完了条件を実装したことにならない事例として扱う。現在は `transitionJob` の遷移 gate と `validateJobModel` の persisted Graph validation の両方で completion readiness を強制する。今後、Job approval・Task scope・RoleExecution・output/evidence の各不変条件についても、状態変更、永続化、復元、Controller の privileged operation 直前検査を対応表に記録し、未確認経路を完了扱いしない。
