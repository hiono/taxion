# ADR-811: Agent Harnessの8要素を製品能力要求として維持する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み hiono/obsolete-taxion の旧ADR-0062「エージェントハーネスの8要素を判断基準として維持する」を原典とする。旧ADR-0088が製品能力の評価基準として明示的に引き継いだH1〜H8を、新Taxionの製品能力要求として再確立する。旧リポジトリのIssue、実装評価、進捗、未達記録は移植しない。

## Context

Taxionは製品開発を支援するための協調システムであり、ハーネス自体の再実装を目的としません。一方、隔離、並列委譲、継続、停滞からの回復など、利用者に必要な能力を「上位の承認管理が主な役割である」という理由だけで対象外にすると、製品要求が追跡から失われます。

Taxion内部で実装する責務と、SDK、CLI、OS、Provider、外部実行環境へ委譲する責務を区別しつつ、システム全体として何を満たし、何が不足しているかを判断する共通基準が必要です。

## Decision

H1〜H8の各判定項目を採択済みの製品能力要求として保持します。IDは仕様・実装・検証証拠の対応に使用し、既存機能の名称を変えただけで充足したとは判定しません。

| 要素 | 必須能力 |
|---|---|
| H1 ループ | H1.1 ContextをModelへ送り、要求されたToolを実行する。H1.2 Tool結果を次のStepへ返す。H1.3 Taskの終了を判断する。 |
| H2 ToolとSandbox | H2.1 Terminal、File、Browser、接続済みApplicationを必要な範囲で利用できる。H2.2 すべてのTaskを独立したComputer内で実行する。H2.3 不適切なCommandの被害をSandbox内に封じ込める。 |
| H3 Sub-agent | H3.1 Main AgentからSide Taskを引き取り並行実行する。H3.2 各Sub-agentを新しいContext Windowで開始する。H3.3 Main Contextを圧迫しない短いSummaryを返す。 |
| H4 Context管理 | H4.1 Modelが毎Turn参照するContextを選択する。H4.2 Taskが必要とする場合にのみSkillやFileをロードする。H4.3 Context Windowが満杯になる前に古い履歴を要約する。 |
| H5 Memoryと再開 | H5.1 Progress Note、Git履歴、Skillを永続化する。H5.2 新しいSessionから前回作業を読める。H5.3 停止した位置から同じJobを再開する。 |
| H6 Guardrails | H6.1 Email、Payment、Delete等の高リスク操作を承認待ちにする。H6.2 実認証情報をSandbox外に保護する。H6.3 Prompt Injectionに盗取可能な認証情報を与えない。 |
| H7 Verification | H7.1 成功を装ったSummaryだけで完了させない。H7.2 Test、Browser Check、独立評価を先に実行する。H7.3 必須Checkの合格後にのみTask完了を許可する。 |
| H8 Observability | H8.1 すべてのModel CallとTool Callを追跡する。H8.2 同じ失敗した編集の反復を検知する。H8.3 別のApproachを試すよう促す。 |

### 1. TaxionとRuntimeの責任分担

- Taxion内部での再実装を一律に要求しません。SDK、CLI、OS、Provider、外部実行環境へ委譲できます。
- 委譲する場合は、Provider / Runtime、対応構成、能力の確認方法、検証証拠、失敗時の挙動を特定します。APIが存在するだけでは機能が有効・動作している証拠になりません。
- 委譲先が未対応または未検証の能力は未対応・未確認として残し、Taxion全体の充足を宣言しません。
- 8要素の一部実装や外部委譲は、残りの要求を対象外にしません。延期・分割は要求の削除を意味しません。

### 2. SandboxとWorktreeの分離

H2.2の独立Computerは、別Session、Process、Git Worktreeだけでは満たしません。Task間とHostのfilesystem、process、credential、network、外部副作用の境界を明示し、隔離要件を満たす証拠を要求します。

H2.1は全Taskへ全Toolを無条件に付与する要求ではありません。承認済みJob scopeで必要な能力を利用できることと、不要な能力を与えないことを両立します。Networkや接続Applicationの副作用はfilesystem隔離だけでは封じ込められないため、ADR-500の権限・認証・操作承認と併せて判定します。

### 3. Sub-agentの独立性

H3の並列委譲は、逐次Coder / ReviewerやModelを使わない並列Taskと区別します。独立したContext、Session、provenance、親子の総予算・期限、二重実行防止を維持します。Worktreeの並行性はADR-408に従い、独立Taskのみ別Worktreeで実行します。

### 4. ContextとMemory

Context選択、Skill / Fileのon-demand load、履歴圧縮は、現在のJob Contract、承認、必須制約、未解決事項、実行状態を失わないように行います。圧縮前後で重要な事実のprovenanceと参照可能性を検証します。

Memoryと再開はJobAggregateを正本とし、独立した二重のJob / Memory正本を作りません。Progress Note、Git履歴、Skill等の保存形式と運用上の所在は、ADR-000 / ADR-004の文書・情報統治に従います。可変計画や作業進捗の保存先を特定の外部サービスへ恒久固定しません。

### 5. GuardrailsとVerification

H6の高リスク操作承認、認証情報保護、Prompt Injection対策は、JobApproval、禁止副作用、実行基盤のSandbox / Credential境界で強制します。PromptやSkillの指示だけを安全境界としません。

H7は成果のimmutable identity、必須受入条件、独立Verifier、再現可能な証拠に基づきます。Model-free Taskの利用者申告やAgent Summaryは、Taxionが実行・独立検証した完了と区別します。

### 6. Observabilityと停滞への対応

H8.1ではModel CallとTool Callの対象、実行主体、時刻、相関ID、結果、証拠の出所を追跡します。Providerが報告しない呼出しがある場合、その欠落を明示し「全呼出し追跡済み」と判定しません。

H8.2 / H8.3では同じ失敗した編集の反復を検知し、別のApproachを提案できるようにします。停滞検知や提案は新しい実行権限、修正枠、予算、期限を付与・拡張しません。自動再試行はOperationの冪等性とJob authorityを別途検証します。

## Alternatives considered

- **8要素をTaxion内部へ直ちにすべて再実装する:** SDK / OS / Providerの責務を重複実装し、製品開発よりハーネス開発を優先するため採用しません。
- **Taxionを承認管理だけに限定し、不足項目を対象外にする:** 隔離・委譲・継続・適応という採択済み利用者要求が失われるため採用しません。
- **Worktreeや別Processだけで独立Computerの隔離を満たしたとする:** Host filesystem、credential、network、process境界の証拠が不足するため採用しません。
- **ProviderのAPI存在や自己申告を能力の証拠とする:** 実際の対応構成・動作・失敗時の挙動を確認できないため採用しません。
- **停滞検知から自動的に権限や予算を拡張する:** JobApprovalと安全境界を迂回するため採用しません。

## Consequences

- 製品設計、Runtime選定、実装、受入れでH1〜H8を共通基準として使用します。
- 未充足要求は独立した未達条件として追跡し、一部実装や外部委譲を全体完成と混同しません。
- Provider / Runtime別の能力差、対応構成、検証証拠、未対応範囲を明示します。
- 本ADRは8要素の実装完了を宣言せず、実際の充足はテスト仕様と対象SHAに結び付いた証拠で判定します。

## Security and operational impact

Worktree分離、Computer Sandbox、Permission Callback、Credential Redaction、OS隔離は別の対策として評価します。認証情報をModelへ渡さない設計と、必要な外部操作を仲介する仕組みを検証します。Observabilityの記録にも秘密情報を残しません。停滞検知は新しい権限・予算・期限の解除を意味しません。

## Acceptance criteria

- H1〜H8および各判定項目H1.1〜H8.3を、仕様・実装・証拠へ追跡できます。
- 委譲先のProvider / Runtime、対応構成、能力確認方法、失敗時挙動が明示されています。
- H2.2のComputer隔離をWorktree / Session / Process分離だけで充足扱いしません。
- H3のSub-agentが独立Contextと予算・期限・provenanceの境界を持ちます。
- Context圧縮後もJob Contract、承認、重要事実、未解決状態が保たれます。
- 新Sessionから同じJobを再開し、UNKNOWNを推測で解消しません。
- 高リスク操作承認、Credential隔離、Prompt Injection耐性を実行境界で検証します。
- Task完了は必須Checkと独立Evidenceに基づきます。
- Model / Tool Callの追跡欠落を明示し、停滞検知が権限・予算を拡張しません。

## Relationship to other ADRs

- ADR-100は製品目的を定義します。
- ADR-201はJobAggregate、永続化、Job再開、成果・Evidenceの正本を定義します。
- ADR-500はJob authority、禁止副作用、Credential / Effect境界を定義します。
- ADR-202は各能力に関わる不変条件を全状態変更経路で強制します。
- ADR-300は内部6 Actor通信、ADR-700は外部Agent A2A境界を定義します。
- ADR-408はTask / Worktreeの分離と並行性を定義します。
- ADR-004は現行仕様、計画、履歴、CI証拠の責務を分離します。
- ADR-800は検証証拠の水準とJob完遂の製品受入れを定義します。
