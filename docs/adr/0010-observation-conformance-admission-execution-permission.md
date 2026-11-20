# ADR-0010: Observation・Conformance・Admission・Execution Permissionを分離する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` のAccepted ADR-0066を根拠とし、新TaxionのJobAggregate、JobEvidence、JobApproval、Provider operationおよびA2A ingressに適用する形で再構成した。

## Context

外部Provider、外部Agent、filesystem、Git、検証環境から得られる観測事実と、その事実がJob契約に適合するか、Jobの入力として受け入れられるか、次の副作用を実行してよいかは異なる判断である。これらを一つの成功状態や一つのvalidatorへまとめると、観測しただけで成果を受け入れたり、契約に適合しただけで実行権限を付与したりする危険がある。

情報が不足していることだけを理由に、観測記録やRole間の情報交換を拒否してはならない。一方、認証・認可・同一性・provenance・秘密情報・実行状態・データ整合性の不確実性は、安全境界として扱う必要がある。

## Decision

Observation、Conformance、Admission、Execution Permissionを別々の概念・判定・状態として実装する。各段階は明示的な根拠を持ち、前段階の成功から後段階の許可を推論しない。

### 1. Observation — 何が観測されたか

Observationは、対象について実際に取得した事実を記録する。

- 対象の完全なidentity、Job / Task / RoleExecution / Operation等との相関
- 観測元、認証済み主体またはProvider identity、取得時刻
- 明示された読取り範囲、観測手段、原データまたはimmutable content identity
- provenance、schema / protocol version、関連するrequest / operation identity
- 観測の完全性、欠落、取得失敗、UNKNOWN等の状態

Observationは、その内容が正しい、Job契約に適合する、成果として受入れ可能、または実行許可済みであることを意味しない。観測の欠落や不一致を、観測そのものの成功・失敗へ混ぜない。停止後の新規観測を過去の実行時点へ遡及させず、作者・変更時刻・成功を推測しない。

### 2. Conformance — 何の条件に適合するか

Conformanceは、Observationと固定されたJobContract revision、JobApproval、受入条件、対象identity、検証規則を比較する副作用のない判定である。

- 判定対象と参照revision / digestを明示する。
- PASS、FAIL、INCONCLUSIVE等を区別し、阻害理由と不足Evidenceを返す。
- 判定根拠には観測のprovenance、対象一致、鮮度、完全性を含める。
- 意味的な品質評価は担当Roleまたは独立Reviewer / verifierが行い、構造・identity・安全性の検査と混同しない。
- 判定結果を保存する場合は、対象Observationとルールversionへ束縛する。

ConformanceがPASSであることは、成果のAdmission、Job完了、外部副作用、次のRoleExecutionを許可しない。

### 3. Admission — Jobの入力として受け入れられるか

Admissionは、Observation、外部AgentのMessage / Artifact、Roleが提案したOutput / Evidence等をJobAggregateの正規入力・履歴として登録できるかを判定する。

Admissionでは少なくとも次を検査する。

- Job、Task、RoleExecution、Message、Operation等のidentityと所属
- trusted ingress、認証済み送信元、provenance、対象content identity
- schema / protocol version、相関関係、順序、期限、重複・idempotency
- JobAggregate revisionとドメイン不変条件
- 秘密情報、禁止データ、対象範囲、必要な承認条件
- 既存事実との矛盾、破損、UNKNOWN、未解決の所有権・実行状態

Admissionは、入力・観測・成果の記録をJobへ関連付ける判断である。受け入れたことは、その内容の品質合格、作業の実行許可、外部への公開許可を意味しない。受け入れられない場合も原データや失敗の観測を捏造・消去せず、隔離または明示的な拒否理由を記録する。

### 4. Execution Permission — 次に何を実行できるか

Execution Permissionは、特定のJob、承認revision、Task / RoleExecution、実行主体、対象resource、effect、Runtime configuration、budget、deadlineに対して、次の副作用を実行してよいかを判定する。

- ADR-0004のJobApprovalと、必要な場合のPlanningAuthorizationを正本とする。
- Job scope、Taskが狭めたscope、対象identity、Provider / account / configuration、実行基盤のcontainment、禁止副作用を実行直前に再検証する。
- permissionは対象effectと実行範囲を限定し、別のJob、対象、revision、effectへ転用しない。
- 実行後のProvider resultやfilesystem observationは別のObservationとして記録し、permissionの根拠へ遡及変換しない。
- UNKNOWN、期限切れ、承認revision不一致、対象identity不一致、provenance不明、containment不能の場合はfail-closedとする。

Conformance PASSやAdmission successからExecution Permissionを自動生成しない。JobMessage、A2A Message、Agent Card、Provider AvailableTools、Role名、自然言語の承認表現からExecution Permissionを生成しない。

### 5. 四つの責務を混ぜない

| 概念 | 問い | 成立しても意味しないこと |
| --- | --- | --- |
| Observation | 何が観測されたか | 正しさ、受入れ、実行許可 |
| Conformance | どの契約・規則に適合するか | Admission、Job完了、実行許可 |
| Admission | Jobの記録・入力として受け入れられるか | 品質合格、次の副作用の許可 |
| Execution Permission | 特定の副作用を実行できるか | 実行成功、成果受入れ、Job完了 |

各判定は共通の型付き結果を利用してもよいが、判定の責務、根拠、保存対象、呼出し境界を統合してはならない。各入口は同じ規範的判定を再利用し、入口ごとに異なる認可や受入れ基準を作らない。

### 6. 情報不足と安全上の不明確さを分離する

通常の説明不足、任意metadataの欠落、自然言語の表現差、未確定の実装詳細だけを理由にObservationやRole間の情報交換を拒否しない。受け手は既存Contextから判断し、可逆で影響の小さい仮定を明示して進め、不可欠な情報だけを質問する。

ただし、認証・認可・Job / Task / Artifact identity・provenance・秘密情報・承認・禁止副作用・二重実行・データ破損・Provider停止・Worktree所有権・UNKNOWNの安全境界が不明な場合は、情報不足として通常処理へ降格しない。必要なEvidenceが揃うまで隔離・保留・拒否とする。

## Consequences

- 外部Agentから受け取ったA2A Message / ArtifactはObservationとして取得し、ConformanceとAdmissionを経てJobへ関連付ける。A2A上の完了状態だけで成果受入れや次の実行を許可しない。
- Reviewerの評価はJobEvidenceとして出所と対象を固定する。ReviewerのPASSも、JobApprovalやExecution Permissionを置き換えない。
- Provider operationの開始・結果・停止は別々のObservationとし、結果不明を成功・失敗へ推測変換しない。
- 受入れ判定と実行許可を分けることで、既存証拠を活用しながら、承認済み範囲外の副作用を防止する。

## Security and operational impact

ObservationやAdmissionを実行権限として扱わない。Conformance結果の対象・version・provenanceを固定する。Execution PermissionはJobApprovalと実行基盤の強制境界を組み合わせ、各副作用直前に再検証する。安全上のUNKNOWNを成功扱いしない。

## Alternatives considered

- 4概念を一つの成功 / 失敗状態へ統合する方式は、観測・受入れ・認可の意味を混同するため採用しない。
- Observationの成功から成果受入れを推論する方式は、品質・契約適合性を証明しないため採用しない。
- Conformance PASSから実行権限を付与する方式は、JobApprovalと副作用境界を迂回するため採用しない。
- Admissionを権限付与と兼用する方式は、外部入力や成果記録が実行能力を拡張するため採用しない。
- すべての不完全な情報を拒否する方式は、TaxionのRole協調目的に反するため採用しない。

## Relationship to other ADRs

- ADR-0001の製品目的とADR-0002の再構築原則に従う。
- ADR-0003はJobAggregate、JobEvidence、Provider operation、Command境界の正本を定義する。
- ADR-0004はExecution Permissionの権限根拠を定義する。
- ADR-0005は4概念に関わるドメイン不変条件をすべての状態変更経路で強制する。
- ADR-0006は内部JobMessageを定義し、本文から認可を生成しない。
- ADR-0007は外部A2Aの境界を定義し、外部状態をJobの権限や完了状態へ暗黙変換しない。
- ADR-0009は検証Evidenceの水準とJob完了の受入れを定義する。
