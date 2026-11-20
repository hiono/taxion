# ADR-500: Job承認境界と禁止副作用を中心に実行権限を強制する

Date: 2026-11-20

## Status

Accepted

## Context

Agentに通常操作の許可リストを与え、事前列挙されていない操作を拒否する方式は、承認済みJobの境界内で必要となった通常作業まで停止させます。一方、Agentの判断や自然言語の回答に権限を委ねると、Jobの対象境界、禁止副作用、人間の承認、安全性を維持できません。

Taxionは、承認されたJobの範囲内でAgentが手段を選択できることと、Controllerが禁止された副作用を確実に防ぐことを両立する必要があります。また、観測された事実、契約への適合、次の仕事への受入れ、実行許可を同じ判定に混在させてはなりません。

## Decision

**Jobの承認範囲と禁止副作用を権限モデルの中心に置き、承認範囲内の通常操作はAgentに委ね、Controllerは対象境界・明示的禁止事項・実行基盤の強制境界を検証します。**

### 1. JobApprovalは完全なJobContract revisionに束縛する

JobApprovalはimmutable JobContract revisionのcanonical hashに束縛し、canonicalization version、approver identity / source / reason / timeをappend-onlyで記録します。少なくとも次を保持します。

- Jobの目的、制約、受入条件、必要な証拠
- 対象repository / resourceと作業領域の境界
- file accessの上限とWorktree / sandboxの境界
- aggregate budget、concurrency ceiling、absolute deadline
- セキュリティ制約、禁止事項、人間承認が必要な副作用
- 秘密を含まないRuntime configurationの選択参照

TaskはJobの対象・作業範囲・資源上限を狭められますが、Job境界を拡張できません。JobContractの対象境界、承認条件、予算、期限などを実質的に変更する場合は、新しいrevisionと明示的な人間承認を要求します。新revisionは新規operationに対する旧Approvalを無効化し、稼働中Provider sessionとWorktree ownershipを照合するまで再実行を許可しません。Task-level approvalを設ける場合も、具体的なoperationに対する追加の狭いgateに限り、JobApprovalを置換・拡張しません。

### 2. 通常操作のallowlistとRole-to-operation ACLを権限根拠にしない

- JobApprovalに通常操作のallowlistを要求しません。
- Roleごとのoperation allowlistやTool allowlistを、Jobの実行権限の正本にしません。
- 承認済みJobの対象・資源・作業領域内で、可逆かつ通常の作業に属する操作は、個別の許可登録なしで実行できることを原則とします。
- Planner / Coder / Reviewerは作業上の責務であり、静的なRole-to-operation ACLを定義する主体ではありません。
- Providerが提示するAvailableToolsやToolPolicyIdentityは、Providerの能力・構成を観測する情報です。JobApprovalの許可リストと同一視しません。

### 3. Controllerは明示的な禁止副作用を強制する

次の副作用は明示的に禁止し、Roleの判断や会話で解除しません。

- Jobが承認したrepository / resource / Worktreeの境界外へのアクセス
- 他Jobや他実行主体のデータ・作業領域への不正アクセス
- 秘密情報・資格情報・個人情報の不正な取得、表示、外部送信
- 権限昇格、sandbox bypass、承認されていないcredentialの利用
- 承認されていない本番環境・外部サービスへの変更
- 承認対象外のcommit / push / release / publish等の外部副作用
- データ破損、履歴改変、provenanceや検証結果の捏造
- 同一作業環境の二重所有、依存関係を無視した競合実行、実行状態不明のままの再送
- 上位の安全ポリシーまたはJobApprovalで明示的に禁止された副作用

不可逆操作、外部副作用、対象や承認範囲を実質的に変更する操作は、既存の人間承認経路へ送ります。承認要求では対象、差分、影響範囲を具体化します。

### 4. 未知のOperation / Provider toolは副作用と封じ込め可能性で判断する

未知のOperation IDやProvider toolが事前リストにないという理由だけで、一律に拒否しません。

- sandbox / OS / filesystem / network / credentialの強制境界内で副作用を封じ込められ、禁止事項に該当しない通常操作は実行可能とします。
- 外部副作用、高リスク、不可逆性がある場合は人間承認へ分岐します。
- 操作の影響を判定できず、実行基盤でも封じ込められない場合は、無条件許可せず承認要求または拒否とします。
- 未知であること自体を拒否理由にせず、判定できない具体的な影響・境界・不足証拠を理由として示します。
- Validation commandなど、人間が値そのものを承認した項目は完全一致で照合します。汎用的なprocess execution能力だけで任意commandを許可しません。

### 5. JobApproval前のPlanningAuthorizationを限定的に分離する

利用者が明示的に開始したrequest intakeでは、JobをDRAFTのまま保持し、Taskless PLANNER RoleExecutionだけに限定したPlanningAuthorizationを発行できます。

PlanningAuthorizationは次を満たします。

- JobIDと原文依頼hashに束縛する。
- 認証済みhuman actor、intake source、時刻を記録する。
- Provider / account identity、canonical configuration digest、repository identityを固定する。
- 実際に強制可能なread-only context boundaryを固定する。
- permitted effect、planning budget / turns、deadlineを固定する。
- immutableかつcontent-addressedとし、変更は新しいgrantとして記録する。
- Provider start、各turn、各effect、resumeの前にControllerが再検証する。
- expiryまたはcancellation後のresumeを拒否する。

PlanningAuthorizationはJobApprovalではなく、JobをACTIVEにしません。実装Task、Coder / Reviewer RoleExecution、repository write、validation execution、commit、publication、credential use、network access、external mutationを許可しません。Plannerの提案、会話、human answerによってgrantを拡張しません。

Providerがread-onlyを実際に強制できない場合はimmutable snapshotまたは明示的context documentsだけを渡します。いずれも強制できない場合、repository contextへのアクセスを許可しません。完全なJobContractの提案と、その正確なrevision hashに対する明示的なJobApprovalだけが実装権限を与えます。

### 5.1 承認済みJob内のJobQuestionAuthorization

承認済みJob内でRoleからTaskless PLANNERへ質問し、read-only context acquisitionを伴う個別回答実行が必要な場合は、immutable JobQuestionAuthorizationを発行できます。このgrantはJobApproval、Planner RoleExecution、元Question JobMessage、Provider / account / configuration、read-only context、budget、deadlineへ束縛します。QUESTION本文やMessageKindだけから自動生成せず、JobApproval・PlanningAuthorization・Task scope・Effect authorityを拡張しません。発行、実行、失効、復旧はJobAggregate Commandで記録し、各turn / effect / resumeの前に再検証します。

### 6. Observation・Conformance・Admission・Execution Permissionを分離する

- **Observation:** 対象が現在どの状態にあるかを、対象identity、provenance、取得時刻、read scope、元Jobへの参照とともに記録します。
- **Conformance:** 観測事実がどの契約・受入条件を満たすかを、副作用のない型付き判定で評価します。
- **Admission:** 対象、provenance、証拠不足、稼働所有権、実行不明、適用ルール、阻害理由、次に必要な操作を評価し、次の仕事の入力にできるかを判断します。
- **Execution Permission:** 現在のJob、approval revision、scope、budget、deadline、Provider、effectを再検証し、何を実行できるかを判断します。

Admissionのacceptは、契約確認の入力にできるという意味であり、実行・品質合格・公開の許可ではありません。契約確定と実行開始では、対象・承認・予算・期限をそれぞれ再確認します。観測自体にmodel、検証command、修正の権限を与えません。

### 7. 情報不足だけを理由にRole間協調を拒否しない

- 受け手Roleは自然言語、部分的な説明、既存Context、成果物、証拠を使って最初に自分で判断します。
- 判断可能な範囲は、必要に応じて明示した仮定と不確実性を添えて進めます。
- 判断に必要な情報が本当に不足している場合だけ質問します。質問は仕事を止めるためではなく、仕事を成立させるために使います。
- 既存Contextや記録から分かる事項を聞き直さず、固定schemaの全項目を質問で埋めることを要求しません。
- 仮定が可逆で影響が小さい場合は、仮定と影響を示して進められます。不可逆、重大、権限に関わる判断は既存の承認経路へ送ります。

次の条件は引き続きfail-closedとし、Roleの判断や自然言語回答で迂回しません。

- 認証・権限・秘密情報・禁止操作に関する違反または不明確さ
- 人間承認が必要な操作、承認範囲・対象・予算・期限の実質的変更
- Job / Task / RoleExecution / 成果物のidentityやprovenanceを安全に確認できない場合
- データ破損、二重実行、競合する実行、不可逆な副作用の危険
- 実行状態が不明で、安全に再開・再送できない場合

これら以外の通常の説明不足、任意metadataの欠落、自然言語の表現差、未確定の実装詳細は、それだけで拒否理由にしません。

### 8. RoleとControllerの責務

- Plannerは目的・背景・制約・未確定事項を実用的なJobとして提案します。
- Coderは情報不足だけで作業を拒否せず、実装可能な範囲を判断し、必要な場合に質問します。
- ReviewerはCoderの説明だけを根拠にせず、依頼原文、承認条件、固定された成果物、検証証拠を独立して評価します。
- Controllerは意味情報の完全性を一律審査してRoleの判断を代行せず、Job identity、進行、記録、権限、承認、安全性を管理します。
- Reviewerの独立性は実行主体、Context / session、固定対象、provenanceおよびread-only execution boundaryで保証します。Role名だけのACLを独立性の代替にしません。

## Security and operational impact

JobApprovalはimmutable JobContract revisionの正確なhashに束縛し、scope、budget、deadline、configurationまたは禁止事項の変更で再承認を要求します。Controllerと実行基盤は対象境界、禁止副作用、credential隔離、sandbox境界を強制します。通常操作のallowlistを権限の正本にせず、影響を判定・封じ込めできない未知の操作は無条件に許可しません。PlanningAuthorizationとJobQuestionAuthorizationはread-onlyの限定grantとして扱い、会話やMessageKindで拡張しません。identity、provenance、実行状態が不明な場合はfail-closedとします。

## Alternatives considered

- **通常操作をJob / Role単位の静的allowlistで認可する:** 承認範囲内の通常作業を不要に制限し、未知toolの意味を評価できないため採用しません。
- **未知のOperationを名称だけで一律拒否する:** 未知であることと危険な副作用であることは同一でないため採用しません。
- **PlanningAuthorizationとJobApprovalを一つのgrantに統合する:** DRAFTのread-only planningが実装権限へ誤って拡張されるため採用しません。
- **QUESTION / JobMessageからJobQuestionAuthorizationを自動発行する:** 通信本文やMessageKindが権限を作成するため採用しません。
- **Observation / Conformance / Admission / Execution Permissionを一つの判定へまとめる:** 観測・適合・受入れ・実行許可の境界を失うため採用しません。
## Consequences

- Roleは承認済みJobの範囲内で、事前に列挙されていない通常の作業手段を選択できます。
- Controllerは操作名の網羅的な許可集合ではなく、対象境界、禁止副作用、承認境界、実行基盤の隔離を中心に強制します。
- 観測、適合判定、受入れ、実行許可の意味を混同しません。
- 不完全情報の協調を許容しつつ、identity、認可、provenance、実行不明、データ整合性の境界はfail-closedで維持します。
- 合成テストは型付き判定と安全境界の証拠に限定し、意味評価や実Providerの実行成功の証拠へ拡張しません。

## Acceptance criteria

- JobApprovalがimmutable JobContract revisionのexact hashに束縛されます。
- 通常操作のallowlistやRole-to-operation ACLが実行権限の根拠になっていません。
- 未知toolが、未知であるという理由だけで拒否されず、具体的な副作用・境界・封じ込め可能性で判定されます。
- PlanningAuthorizationがTaskless Plannerのread-only planningに限定され、JobApprovalや実装権限へ昇格しません。
- Observation、Conformance、Admission、Execution Permissionが別の責務としてテストされています。
- 情報不足だけで通常のRole協調を拒否せず、認証・権限・秘密情報・scope・実行不明は迂回できません。
- Reviewerが固定された同一成果物を独立した実行主体とread-only境界で検証します。

## Relationship to other ADRs

- ADR-100は製品目的を定義します。
- ADR-101は旧設計の制約を温存しない再構築原則を定義します。
- ADR-201はJobAggregate、PlanningAuthorization、JobApproval、JobMessage、Provider / Worktree factsとCommand境界を定義します。
- 具体的な全状態変更経路の不変条件、6 Actor通信、Worktree並行性、外部Agent protocolは下位ADRで定義します。
