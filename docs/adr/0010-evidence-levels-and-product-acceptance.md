# ADR-0010: 検証証拠の水準とJob完遂の受入れを分離する

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` のADR-0107（Six-Actor PoCを候補実装ベースラインとして比較評価する判断）とADR-0085（仕様・履歴・検証証拠の分離）を参照し、新Taxionの製品受入れ条件として再構成した。Linux / Windows、GoReleaser cross-build、Commit前Secret Scan等の品質ゲートは現在の開発行動規約に従う。

## Context

Unit test、synthetic harness、SDK型とのcompile、実Provider実行、永続化・再起動復旧、実Job完遂は、それぞれ証明する対象が異なる。低い証拠水準の成功を、実環境での製品能力の証明へ拡張解釈してはならない。

また、CI成功やAPIの存在だけでは、人間の曖昧な意図を保ちながらJobを完遂できることを証明しない。製品の完成は、受入条件と出所が確認できる成果に基づいて判断する必要がある。

## Decision

検証結果は証明した境界を明示し、Taxionの製品受入れは実際のJob完遂能力で判断する。

### 1. 証拠水準

1. **Static / unit:** 局所ロジック、型、不変条件を検証する。
2. **Synthetic integration:** fake Providerやin-memory harness内の契約、順序、異常系を検証する。
3. **SDK compatibility:** 実SDKの型、API、protocol handshake等との互換性を検証する。
4. **Live Provider:** 実Provider接続、実callback、identity / attribution、turn、usage、stop semanticsを検証する。
5. **Durable recovery:** 実プロセス再起動・crash後にJob facts、Provider operations、成果、Usage、Worktree状態を安全に照合できることを検証する。
6. **Product E2E:** 人間の曖昧な依頼から、計画、質問・回答、承認、複数Role実行、レビュー、修正、証拠確認、Job完了までを検証する。

上位の証拠水準は下位の成功だけから推定しない。各結果は対象HEADの完全なSHA、実行環境、Provider / SDK version、条件、観測値と対応付ける。CI結果はそのRunが実際に検証したSHAにのみ有効とする。

### 2. 第一段階の製品シナリオ

- 人間が曖昧な依頼を入力する。
- Plannerが目的・制約・受入条件を整理し、不足情報を質問する。
- 人間または別Roleが回答し、同じJobへ記録する。
- Plannerが回答を反映して計画を更新する。
- 人間がJobContractの正確なrevisionを承認する。
- Coderが承認済み範囲で成果を作成する。
- Reviewerが固定対象を独立したContext / sessionで評価する。
- 指摘があればCoderへ戻し、修正・再レビューする。
- 必要な人間判断を挟み、Evidenceと成果を確認してJobを完了する。
- 中断・再起動後も同じJobを安全に再開する。
- 外部Agentとの協調が必要な場合はA2Aを用い、外部結果をJobの承認・認可・成果へ暗黙変換しない。

### 3. Job完了判定

Job完了には、次をすべて要求する。

- Jobの受入条件を満たす成果が存在する。
- 成果とEvidenceが対象digest、Job / Task / RoleExecution、producer、verifierへ結び付いている。
- 必須受入条件が現在有効なprovenance-valid Evidenceによって満たされている。
- UsageとProvider operationの結果が確定している。
- 未解決Provider operation、実行状態UNKNOWN、未完了RoleExecutionが存在しない。
- Provider quiescenceとWorktree releaseが確認されている。
- 未解決の安全・認可・データ整合性問題が存在しない。

CI成功、Reviewerの自己申告、A2Aのterminal state、RoleExecutionのterminal statusだけでJob完了を宣言しない。

### 4. 必須品質ゲート

- LinuxとWindowsの検証を必須とする。
- GoReleaserのcross-buildはmacOS targetを含めて必須とする。
- macOS native検証は任意とし、通常開発の停止条件にしない。
- すべてのCommit前に秘密情報検査を実施する。
- CI結果は対象HEAD SHAとRun IDを照合する。
- Docs-only changeではbuildを必須としない。
- 未実施の実Provider / E2E検証を成功扱いしない。
- 外部CLI・実Providerの検証は、実行主体、環境、Provider / SDK version、結果を証拠として記録し、未実施の場合は未確認と明記する。

### 5. 旧候補実装の評価

旧Taxion-PoCのテスト・実行結果は、その実行環境と検証境界の証拠として扱う。旧PoCが候補実装ベースラインであることは、実Provider、再起動復旧、製品E2Eの成功を意味しない。新Taxionへの直接採用・部分採用・改修・再実装は、provenance、設計適合性、安全性、保守性および新Taxionの受入れ証拠で個別に判断する。

## Consequences

実装済み、syntheticで確認済み、SDK互換性確認済み、実Provider確認済み、再起動復旧確認済み、実Job完遂確認済みを明確に区別できる。完成判定をAPI数、Actor数、テスト数、CI成功だけに置かない。

## Security and operational impact

検証証拠のprovenanceと対象を固定する。秘密情報をログや成果物へ出さない。失敗・UNKNOWN・未実施を成功に読み替えない。証拠が不足する場合はJob完了を許可しない。

## Alternatives considered

- Test passを製品受入れと同一視する方式は、実ProviderとJob完遂を証明しないため採用しない。
- 実Provider未検証を理由にsynthetic testを無価値とする方式は、局所契約の決定的検証を捨てるため採用しない。
- CI成功をユーザー価値の代替指標とする方式は採用しない。
- 旧PoCの結果を新Taxionの実行証拠として直接流用する方式は、環境・対象・provenanceが異なるため採用しない。

## Relationship to other ADRs

- ADR-0001は製品目的とJob完遂による価値を定義する。
- ADR-0003はJobAggregate、成果、証拠、Provider operation、Job完了Commandの正本を定義する。
- ADR-0004はJobApprovalとExecution Permissionを定義する。
- ADR-0005は全状態変更経路での不変条件強制を定義する。
- ADR-0006は内部6 Actor通信、ADR-0007は外部Agent A2A境界を定義する。
- ADR-0008はWorktree所有権とRoleExecutionの安全な移転を定義する。
- ADR-0009は現行仕様、計画、履歴、検証証拠の正本を分離する。
