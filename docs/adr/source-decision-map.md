# Historical ADR Source Decision Map

Date: 2026-11-20

本書は、アーカイブ済み旧TaxionのAccepted ADRから、新Taxionで再確立した設計判断への対応を追跡するための索引です。本書自体は新しい製品判断を追加しません。規範となる判断は対応先の新ADRを参照してください。

旧リポジトリのADR、Git履歴、Issue、PR、コメントは読み取り専用の歴史資料です。旧資料の文章・Issue番号・作業計画・実装状態を新Taxionへ移植しません。旧PoCの初期READMEと後続ADRによる位置付け変更の時系列も維持します。

| 旧ADR | 新Taxionの対応先 | 再確立した判断 | 引き継がないもの・明示した扱い |
|---|---|---|---|
| 旧ADR-000 | [ADR-000](000-adr-governance.md) | ADR体系、採番、判断記録の統治を管理する。 | 旧リポジトリ固有の番号移行作業と履歴は移植しない。 |
| ADR-0001 | [ADR-000](000-adr-governance.md) | アーキテクチャ判断をADRへ記録し、現行判断の正本とする。 | 旧ファイル形式・旧番号体系をそのまま採用しない。 |
| ADR-0002 | [ADR-101](101-rebuild-over-legacy-constraints.md) | 旧コード・旧履歴を一括移植せず、製品目的と新しい不変条件から再構築する。 | 旧clean-room運用の進捗・milestone・手順は移植しない。 |
| ADR-0007 | [ADR-004](004-specification-plan-history-and-evidence.md) | 設計判断、作業履歴、計画、CI証拠の責務を分離する。 | 旧Issue / PR / Milestoneの履歴は移植しない。 |
| ADR-0008 | [ADR-000](000-adr-governance.md) | ADRの必須構成、判断理由、代替案、他ADRとの関係を統一する。 | 旧ADR本文の一括コピーや旧Nygardテンプレートの機械適用は行わない。 |
| ADR-0015 | [ADR-500](500-job-authority-and-prohibited-effects.md) | 認証済み主体、権限境界、監査可能な認可判断を維持する。 | 旧Token API、旧HTTP認証実装、旧監査schemaは移植しない。 |
| ADR-0026 | [ADR-500](500-job-authority-and-prohibited-effects.md) | ファイルアクセスはJob承認対象と実行境界に束縛し、Task単位でscopeを拡張しない。 | 旧TaskApproval / file access level型は移植しない。 |
| ADR-0029 | [ADR-201](201-job-aggregate-and-command-boundary.md)、[ADR-500](500-job-authority-and-prohibited-effects.md) | Job budgetとProvider quota / usageを別の事実として記録し、残量を推測で権限へ変換しない。 | 旧quota provider、旧budget guidance、旧Flow計算式は移植しない。 |
| ADR-0036 | [ADR-500](500-job-authority-and-prohibited-effects.md) | token thresholdや費用最適化で、承認済みJobの目的・受入条件を暗黙に弱めない。 | 旧token thresholdや旧実行回数制御は移植しない。 |
| ADR-0047 | [ADR-500](500-job-authority-and-prohibited-effects.md) | 実行操作はJob scopeと強制可能なsandbox / OS境界内で許可し、外部副作用は明示承認へ分岐する。 | 旧Coder shell allowlistや旧CLI権限モデルは移植しない。 |
| ADR-0040 | [ADR-800](800-evidence-levels-and-product-acceptance.md) | 効率・通信量・費用の改善は、成果品質、安全性、必須検証を維持したうえで評価する。 | 旧workflow evaluate、旧評価原稿、実験結果・進捗は移植しない。 |
| ADR-0043 | [ADR-500](500-job-authority-and-prohibited-effects.md) | Job承認境界、read-onlyの強制、権限と観測の分離を維持する。 | 旧ApprovalBundle、旧Flow gate、旧Task専用認可は移植しない。 |
| ADR-0050 | [ADR-201](201-job-aggregate-and-command-boundary.md) | 進捗・継続に必要な記録はJobへ出所付きappend-only factとして保持し、自己申告を検証済み証拠へ昇格させない。 | Copilot Reviewer専用progress tool、旧review continuation実装は移植しない。 |
| ADR-0055 | [ADR-101](101-rebuild-over-legacy-constraints.md)、[ADR-500](500-job-authority-and-prohibited-effects.md) | 承認済みJobの目的・scope内で手段を適応させ、実質的な目的・scope・権限変更は再承認する。 | 旧Flow・旧repair枠・旧CLIの具体的な実装規則は移植しない。 |
| ADR-0056 | [ADR-201](201-job-aggregate-and-command-boundary.md)、[ADR-800](800-evidence-levels-and-product-acceptance.md) | 入力Artifactのidentity、検証証拠、Coderの実行成功を別の事実として扱う。 | 旧ArtifactTarget型、旧workflow command、旧Dispatchの不存在を現行APIとして移植しない。 |
| ADR-0057 | [ADR-500](500-job-authority-and-prohibited-effects.md) | 既存の承認・証拠で解決できる事項はControllerが照合し、新しい人間判断が必要な場合に限り差分をまとめてエスカレーションする。 | 旧Controller/Flowの差戻し経路、旧CLI、旧実装進捗は移植しない。 |
| ADR-0061 | [ADR-800](800-evidence-levels-and-product-acceptance.md) | Modelを使わない決定的な検証も証拠として扱い、実行主体と検証範囲を明示する。 | 旧Model-free Task / Dispatch実装は移植しない。 |
| ADR-0065 | [ADR-500](500-job-authority-and-prohibited-effects.md)、[ADR-800](800-evidence-levels-and-product-acceptance.md) | 診断目的の観測結果と品質検証の合格を分離し、診断の完了を品質PASSにしない。 | 旧review-input.v2、validationMode、旧workflowのstatus値は移植しない。 |
| ADR-0070 | [ADR-500](500-job-authority-and-prohibited-effects.md) | Credentialを伴う外部操作は正確な対象・権限・実行境界に束縛し、Agentの自己申告で資格情報を扱わない。 | 旧credential operation registryや旧provider実装を移植しない。 |
| ADR-0071 | [ADR-201](201-job-aggregate-and-command-boundary.md)、[ADR-800](800-evidence-levels-and-product-acceptance.md) | Coderの受理済み成果、immutable target identity、Reviewerの独立評価と証拠の来歴を分離して保持する。 | A/B/T/Pの旧Flow固有schema、reconcile-stopped、旧Reviewer再開経路は移植しない。 |
| ADR-0072 | [ADR-201](201-job-aggregate-and-command-boundary.md)、[ADR-800](800-evidence-levels-and-product-acceptance.md) | Reviewerの正式な失敗結果を成功へ書き換えず、後から得た負の指摘は別のappend-only factとして扱う。 | 旧workflow record-failed-review、旧Dispatch / Session時刻判定は移植しない。 |
| ADR-0078 | [ADR-106](106-constrained-design-decision-method.md) | 複数候補の責務割当を制約付き最適化で比較し、セキュリティ・認可・整合性等の必須条件をハード制約として維持する。 | 旧Task/Dispatch/ReviewFlowの責務マッピング表や旧分析ログは移植しない。 |
| ADR-0062 | [ADR-811](811-agent-harness-eight-capabilities.md)、[Product Contract](../specifications/product-contract.md)、[Acceptance Tests](../test-specs/product-acceptance.md) | H1〜H8およびH1.1〜H8.3を採択済み製品能力要求として維持。実装委譲先・対応構成・実動作証拠を確認する。 | 旧コード上の充足評価、旧Issue番号、旧進捗・未達記録は移植しない。 |
| ADR-0066 | [ADR-500](500-job-authority-and-prohibited-effects.md)、[ADR-202](202-domain-invariants-across-state-paths.md) | Observation / Conformance / Admission / Execution Permissionを分離し、判定・強制・検証を区別する。 | 旧CLI workflow、旧Task / Dispatch専用型、旧実装の完了状態は新Taxionの仕様にしない。 |
| ADR-0067 | [ADR-500](500-job-authority-and-prohibited-effects.md) | 情報不足や任意metadata欠落だけでRole協調を拒否しない。必要な質問と仮定を用い、安全・identity・認可境界はfail-closedとする。 | 旧Clarification / Dispatch実装や固定質問schemaは再導入しない。 |
| ADR-0067（対象固定の品質証拠） | [ADR-800](800-evidence-levels-and-product-acceptance.md) | 成果対象を固定し、品質判断を検証可能な証拠へ結び付ける。 | 旧Review progressや旧実装状態は移植しない。 |
| ADR-0073 | [ADR-500](500-job-authority-and-prohibited-effects.md) | 承認範囲内の手段選択を通常操作と副作用の境界で判断し、実行状態UNKNOWNや未確認の外部副作用を推測で成功・失敗にしない。 | 旧Dispatch固有の継続判定、CLI、保存形式、旧実行経路は移植しない。 |
| ADR-0077 | [ADR-201](201-job-aggregate-and-command-boundary.md)、[ADR-500](500-job-authority-and-prohibited-effects.md)、[ADR-300](300-jobmessage-six-actor-communication.md)、[ADR-408](408-worktree-concurrency-and-ownership.md) | Job中心のRole協調、JobApproval、Task / RoleExecution、成果・証拠provenanceを維持する。 | 操作allowlistの衝突部分は旧ADR-0080により置換済み。Reviewerの別Worktree要件はJob-nativeについて旧ADR-0082により逐次引継ぎへ置換済み。旧Task / ReviewFlow経路は維持しない。 |
| ADR-0079 | [ADR-202](202-domain-invariants-across-state-paths.md) | 不変条件を生成・変更・保存・復元・migration・外部入力・recoveryの全該当経路で強制し、判定・強制・検証を分離する。 | 旧実装のvalidatorやテストをそのまま移植しない。 |
| ADR-0080 | [ADR-500](500-job-authority-and-prohibited-effects.md) | JobApproval、通常操作のallowlistを権限根拠にしないこと、禁止副作用、未知Operationの副作用判定、PlanningAuthorizationを維持する。 | 旧Task / ReviewFlow認可、旧ToolPolicyの権限正本化は持ち込まない。 |
| ADR-0081 | [ADR-101](101-rebuild-over-legacy-constraints.md) | 旧設計の制約を温存せず、製品目的と新しい不変条件から再構築する。 | 旧コード・API・データ形式・Git履歴の一括移植、互換性だけのfallbackは行わない。 |
| ADR-0082 | [ADR-408](408-worktree-concurrency-and-ownership.md) | Git WorktreeをTask単位で割り当て、同一Worktreeのmutating Roleを逐次実行し、独立Taskのみ並行させる。 | 旧doctor / ready / Skill / CLIの具体的な実装状態は新Taxionの現行仕様として引き継がない。必要な新CLI契約は別途現行仕様で定義する。 |
| ADR-0083 | [ADR-201](201-job-aggregate-and-command-boundary.md) | JobAggregate、Command境界、TaskとRole協調関係、Provider operation / observation、Worktree ownership、成果・証拠をJobの正本とする。 | 旧GraphTask / TaskGraph、履歴projection、旧workspace decoder、migration reader、旧Task-era history readerをread-only名目でも残さない。 |
| ADR-0084 §5 | [ADR-201](201-job-aggregate-and-command-boundary.md) | 旧Memory DBを独立保持する例外は旧ADR-0086によって superseded とする。 | 旧Memory DBとSnapshot / Restoreを新Taxionへ残さない。 |
| ADR-0085 | [ADR-000](000-adr-governance.md)、[ADR-004](004-specification-plan-history-and-evidence.md) | ADR、現行仕様、テスト、計画、Git履歴、Issue / PR、CI証拠の責務を分離する。 | 特定外部サービス・アカウント・フォルダー・ファイルを計画情報の恒久的正本に固定しない。旧文書の保存先・参照先は指示なく変更しない。 |
| ADR-0086 | [ADR-100](100-product-purpose.md)、[ADR-201](201-job-aggregate-and-command-boundary.md) | 旧Memory subsystemを退役させ、Jobの目的、計画、質問・回答、判断、成果、証拠、Usage、Provider観測をJobAggregateへ集約する。 | 旧Memory DB / schema / CLI / API / migration / Snapshot / Restore、旧Memory dataの自動変換・復元を行わない。 |
| ADR-0087 | [ADR-300](300-jobmessage-six-actor-communication.md)、[ADR-201](201-job-aggregate-and-command-boundary.md)、[ADR-500](500-job-authority-and-prohibited-effects.md) | 内部6 Actorの自然言語Bodyとsystem-managed Envelope、MessageKind、配信観測、Handoff、JobQuestionAuthorizationを維持する。 | 独立Message DB、旧Clarification runtime、Task / Dispatch通信経路は再導入しない。 |
| ADR-0088 | [ADR-100](100-product-purpose.md)、[ADR-811](811-agent-harness-eight-capabilities.md) | 人間の曖昧な思考を対話で整理し、複数Agent / RoleとガードレールによりJob完遂を支援する製品目的を維持する。 | 旧PoCとしての当時の実装状態や旧Issue / PRを現行の製品仕様にしない。 |
| ADR-0107 | [ADR-101](101-rebuild-over-legacy-constraints.md)、[ADR-800](800-evidence-levels-and-product-acceptance.md) | Six-Actor PoCを候補実装ベースラインとして評価し、直接採用・部分採用・追加検証／修正・再実装を証拠に基づき判断する。 | 候補実装であることを理由に新Taxionへ自動採用・直接移植しない。旧PoC初期READMEの実験専用という当初の記述を遡及的に書き換えない。 |
| ADR-0108 | [ADR-700](700-a2a-external-agent-interoperability.md) | 外部Agentの必須相互運用protocolをA2Aに一本化し、内部6 ActorのJobMessageと分離する。 | MCP / ACP / Copilot SDK / Provider固有protocolを初期必須化しない。A2Aの十分性は未検証の設計仮説として扱う。 |

## 適用上の注意

- 新ADRが旧ADRの全文章を複製することを要求しません。引き継ぐのは、現在の製品目的に必要で、適用範囲と根拠を確認できた設計判断です。
- 旧ADR内の実装計画、旧Issue番号、CI結果、当時のファイル名、旧API、旧CLI、旧schemaは、現在の新Taxionの仕様を構成しません。
- 旧ADRの一部判断が後続ADRで置換されている場合は、置換後の判断を適用します。例として、ADR-0077のallowlistとReviewer Worktreeに関する規定は、それぞれADR-0080とADR-0082の後続判断に従います。
- 対応先が見つからない新たな永続要件を確認した場合は、本書へ曖昧に追記せず、原典と適用範囲を確認したうえで新ADRまたは既存ADRの明示的改訂を行います。
