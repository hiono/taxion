# ADR-0080: Job 境界内の手段選択と禁止副作用の強制\n\nProject-wide redesign principle: [ADR-0081](0081-rebuild-over-legacy-constraints.md). This authorization decision is one application of that principle; it does not authorize preserving obsolete authorization or execution models elsewhere.

Date: 2026-11-14

## Status

Accepted

## Context

ADR-0073 は、承認された対象・権限・資源・受入条件の範囲内では Agent が達成手段を選び、Controller は想定手順との一致ではなく禁止された副作用を防止する、と決定している。

ADR-0066 は観測、適合判定、受入れ、実行許可を分離し、ADR-0067 は情報不足だけを理由に Role 間の仕事を拒絶しないと決定している。

一方、ADR-0077 の Job authority 節は、Job Contract の Operations allowlist、Role policy との積集合、未知 Operation の fail-closed、Role ごとの操作許可を要求している。PR #292 の Job-native 実装にも RoleOperations と RoleToolPolicies が導入され、Intake が Role ごとの操作集合を固定している。

この方式は、承認済み Job の境界内で必要になった通常操作であっても、事前列挙されていないという理由だけで Role を停止させる。ADR-0073 の判断原則と矛盾し、旧来の許可リストによる接続拒否を Job-native 経路へ再導入する。

## Decision

Taxion の Job-native 権限モデルは、操作の許可リストを中心にせず、承認済み Job の対象・資源・作業領域・制約を境界として定義し、その境界内での通常の手段選択を Role に委ねる。Controller は明示的な禁止事項と禁止された副作用を強制する。

### 1. Job の承認対象

Job approval は引き続き immutable Job revision に束縛し、少なくとも次を保持する。

- Job の目的、制約、受入条件、必要な証拠
- 対象 repository / resource と作業領域の境界
- file access の上限と worktree / sandbox の境界
- aggregate budget、concurrency ceiling、absolute deadline
- セキュリティ制約、禁止事項、承認が必要な副作用
- 秘密を含まない Runtime configuration の選択参照

対象 repository や resource の境界は、Job が触れてよい対象を限定するために明示する。これは通常操作を列挙する許可リストとは異なる。

Job approval の権限根拠に、通常操作の allowlist や Role ごとの operation allowlist を要求しない。Task は Job の対象・作業範囲・資源上限を狭められるが、境界を拡張できない。

### 2. 通常操作と禁止事項

承認済み Job の作業領域内で、可逆かつ通常の開発作業に属する操作は、個別の許可登録なしで実行できることを原則とする。

次の副作用は明示的に禁止し、Role の判断や会話で解除しない。

- Job が承認した repository / resource / worktree の境界外へのアクセス
- 他 Job のデータや他実行主体の領域への不正アクセス
- 秘密情報・資格情報・個人情報の不正な取得、表示、外部送信
- 権限昇格、sandbox bypass、承認されていない credential の利用
- 承認されていない本番環境・外部サービスへの変更
- 承認対象外の commit / push / release / publish 等の外部副作用
- データ破損、履歴改変、出所や検証結果の捏造
- 二重実行、競合実行、実行状態不明のままの再送
- その他、Job approval または上位の安全ポリシーで明示的に禁止された副作用

不可逆操作、外部副作用、対象や承認範囲を実質的に変更する操作は、既存の承認経路へ送る。人間承認を要求する場合は、対象・差分・影響範囲を具体化する。

### 3. 未知の操作・Provider tool

未知の Operation ID や Provider tool が事前リストにないという理由だけで、一律に拒否してはならない。

Controller は、その操作が Job の対象・資源境界内に閉じ、禁止された副作用を発生させないことを、実行基盤の強制境界と利用可能な操作情報から評価する。

- sandbox / OS / filesystem / network / credential の強制境界内で副作用を封じ込められ、禁止事項に該当しない通常操作は実行可能とする。
- 外部副作用、高リスク、不可逆性がある場合は、人間承認へ分岐する。
- 操作の影響を判定できず、実行基盤でも封じ込められない場合は、無条件許可せず承認要求または拒否とする。
- 未知であること自体を拒否理由にせず、判定できない具体的な影響・境界・不足証拠を理由として示す。

Provider の利用可能 tool 一覧や ToolPolicyIdentity は、Runtime の能力・構成を観測する情報として扱う。Job approval の許可リストと同一視しない。Adapter が実際に提供する機能と、Job の禁止事項・実行境界は別々に検査し、どちらも他方を拡張しない。

### 4. Role の責務

Planner / Coder / Reviewer は作業上の責務であり、静的な Role-to-operation ACL を定義する主体ではない。

Role は Job の目的と自身の担当責務に従って、必要な調査・編集・検証・相談・委譲の手段を選択できる。Controller は Job の同一性、承認、対象境界、禁止事項、予算、期限、競合、実行状態、証拠の出所を強制する。

Reviewer の独立性は実行環境・対象の固定・証拠の provenance で保証する。単に Role 名に read-only の操作 allowlist を割り当てることを独立性の代替にしない。Reviewer に変更操作を許す場合も、承認済みの別 worktree 等に隔離し、レビュー対象を直接改変できない境界を維持する。

### 5. 個別に完全一致を維持する項目

本 ADR は、値そのものを人間が承認した項目の完全一致検査を廃止しない。

- ValidationCommands は承認済みの完全な command string と一致させる。generic な process.exec / test.run の能力だけで任意 command を許可しない。
- repository / resource identity、対象 commit/tree、worktree、configuration identity は現在の承認対象と照合する。
- commit / push / release / publish 等は明示された承認範囲と実行主体を検査する。
- 予算、期限、並行実行数、承認 revision は実行時にも再検証する。

これらは特定の対象・値・副作用を保護する境界であり、通常操作の包括的 allowlist ではない。

### 6. 実装・移行

以下を実施する。

1. ADR-0077 の Job authority 節にある Operations allowlist、Role policy との積集合、unknown Operation fail-closed を本 ADR に従って改訂する。
2. JobContract.Operations、RoleOperations、RoleToolPolicies、Provider AvailableTools / ToolPolicyIdentity の用途を再分類する。通常操作の静的許可リストとして使う実装を削除または置換する。
3. Job approval に対象境界、禁止事項、承認が必要な副作用、実行基盤の強制境界を結び付ける。
4. JobRoleController の Start / Resume / Send / tool preflight のすべてで、同じ Job revision と禁止事項・実行境界を再検証する。
5. Provider adapter の実際の tool 能力と Job policy の照合を維持する。ただし、事前列挙されていない通常 tool を自動的に拒否する用途には使わない。
6. ValidationCommands の完全一致、秘密情報保護、Job / Task / RoleExecution の同一性、budget / deadline、二重実行防止、不可逆操作の承認は維持する。
7. 通常の新規操作を事前登録せず実行できるケース、Job 境界外アクセス、秘密情報送信、未知 tool、高リスク操作、承認外 command、競合・再送の回帰試験を追加する。
8. 旧 Task / ReviewFlow の実行・認可経路を Job-native の並行 runtime として維持しない。Job-native の安全境界を成立させるために必要な domain model、runtime、adapter、test を置き換えたうえで、重複する旧経路と旧認可ロジックを削除する。旧データの保持・読取りが必要な場合は、実行・認可を行わない明示的かつ loss-aware な履歴移行境界に限定する。

## Relationship to existing ADRs

- ADR-0073 の「承認範囲内では Agent が手段を選び、Controller は禁止された副作用を防ぐ」を Job-native 権限モデルの基本原則として適用する。
- ADR-0066 の観測・適合判定・受入れ・実行許可の分離を維持する。
- ADR-0067 の情報不足だけでは受け渡しを拒絶しない原則を維持する。認証・権限・秘密情報・同一性・データ整合性の安全境界は緩和しない。
- ADR-0047 の Coder shell に対する具体的な拒否リストは、その対象範囲と限界を明示した局所的な防御として維持する。これを Job 全体の操作 allowlist の根拠に拡張しない。
- ADR-0077 の Job / Role collaboration、Job approval、成果物・証拠 provenance の他の判断は維持する。Job authority 節の操作 allowlist と Role allowlist に関する規定は本 ADR によって置き換える。

## Consequences

- Role は承認済み Job の範囲内で、事前に列挙されていない通常の作業手段を選べる。
- Controller は操作名の網羅的な許可集合ではなく、対象境界・禁止副作用・承認境界・実行基盤の隔離を中心に強制する。
- 単純な denylist だけで未知の副作用を防げるとは扱わない。実行基盤で封じ込められない不確実性は、具体的な承認要求または拒否へ分岐する。
- ToolPolicy の完全一致を権限の正本として使わず、Job approval と実行基盤の制約を独立して検証する。
- 通常操作の追加に伴う Job contract の再承認を減らし、Role 間の適応的な協調を妨げない。

## Security and operational impact

認証・認可、秘密情報、Job 境界、承認、予算、期限、二重実行、データ整合性、不可逆操作の安全境界は妥協しない。操作 allowlist を削除する変更は、OS / sandbox / filesystem / network / credential の強制境界と禁止事項の検査を同時に成立させる。実行基盤で制約できない能力を、プロンプトや Role の善意だけに依存して許可しない。

## Compatibility

本 ADR は [ADR-0081](0081-rebuild-over-legacy-constraints.md) の全体原則に従い、Job-nativeモデルを唯一の現行実行・認可モデルとして規定する。旧 Task / ReviewFlow の承認・tool allowlist・permission handler を並行 runtime として温存しない。既存データの移行が必要な場合は、旧承認を新しい権限根拠へ暗黙変換せず、移行不能・情報欠落を明示して拒否するか、新しい Job revision と明示承認を要求する。履歴参照のための読取り処理は、実行や認可を発生させてはならない。

## Status

Accepted. This ADR restores the accepted bounded-continuation principle from ADR-0073 for the Job-native authorization model and supersedes only the conflicting operation-allowlist provisions in ADR-0077.
