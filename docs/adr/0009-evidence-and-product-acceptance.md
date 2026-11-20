# ADR-0009: 検証証拠の水準とJob完遂の受入れ

Date: 2026-11-20

## Status

Accepted

## Source decision

アーカイブ済み `hiono/obsolete-taxion` のADR-0107、ADR-0085および関連Issue / PR検証記録を参照し、証拠水準と製品受入れを新Taxion向けに再構成した。

## Context

Unit test、synthetic harness、SDK型とのcompile、実Provider実行、永続化・再起動復旧、実Job完遂は、それぞれ証明する対象が異なる。低い証拠水準の成功を、実環境での製品能力の証明へ拡張解釈してはならない。

## Decision

検証結果は証明した境界を明示し、Taxionの受入れは実際のJob完遂能力で判断する。

### 1. 証拠水準

1. Static / unit: 局所ロジック、型、不変条件を検証する。
2. Synthetic integration: fake Providerやin-memory harness内の契約・順序・異常系を検証する。
3. SDK compatibility: 実SDKの型、API、protocol handshake等との互換性を検証する。
4. Live Provider: 実Provider接続、実callback、identity / attribution、turn、usage、stop semanticsを検証する。
5. Durable recovery: 実プロセス再起動・crash後にJob facts、Provider operations、成果、Usage、Worktree状態を安全に照合できることを検証する。
6. Product E2E: 人間の曖昧な依頼から、計画、質問・回答、承認、複数Role実行、レビュー、修正、証拠確認、Job完了までを検証する。

上位の証拠水準は下位の成功だけから推定しない。各結果は対象SHA、実行環境、Provider / SDK version、条件、観測値と対応付ける。

### 2. 第一段階の製品シナリオ

- 人間が曖昧な依頼を入力する。
- Plannerが目的・制約・受入条件を整理し、不足情報を質問する。
- 人間または別Roleが回答し、同じJobへ記録する。
- Plannerが回答を反映して計画を更新する。
- 人間がJobContractの正確なrevisionを承認する。
- Coderが成果を作成する。
- Reviewerが固定対象を独立に評価する。
- 指摘があればCoderへ戻し、修正・再レビューする。
- 必要な人間判断を挟み、Evidenceと成果を確認してJobを完了する。
- 中断・再起動後も同じJobを安全に再開する。

### 3. 完了判定

Job完了には、Jobの受入条件を満たす成果、対象digestへ結び付いた検証Evidence、Usageの確定、未解決Provider operationの不存在、Provider quiescence、Worktree解放、未完了RoleExecutionの不存在を要求する。CI成功だけでJob完了や製品受入れを宣言しない。

### 4. 必須品質ゲート

- LinuxとWindowsの検証を必須とする。
- GoReleaserのクロスビルドはmacOS targetを含めて必須とする。
- macOS native検証は任意とし、未完了を通常開発の停止条件にしない。
- Commit前に秘密情報検査を行う。
- 対象HEADのSHAとCI runを照合する。
- Docs-only changeではbuildを必須としない。
- 未実施の実Provider / E2E検証を成功扱いしない。

## Consequences

実装済み、syntheticで確認済み、SDK互換性確認済み、実Provider確認済み、再起動復旧確認済み、実Job完遂確認済みを明確に区別できる。完成判定をAPI数やテスト数に置かない。

## Security and operational impact

検証証拠のprovenanceと対象を固定する。秘密情報をログや成果物へ出さない。失敗・UNKNOWN・未実施を成功に読み替えない。

## Alternatives considered

- Test passを製品受入れと同一視する方式は、実ProviderとJob完遂を証明しないため採用しない。
- 実Provider未検証を理由にsynthetic testを無価値とする方式は、局所契約の決定的検証を捨てるため採用しない。
- CI成功をユーザー価値の代替指標とする方式は採用しない。

## Relationship to other ADRs

ADR-0001の利用者価値、ADR-0003のJob完了条件とdurable Provider facts、ADR-0005の全経路不変条件、ADR-0008のWorktree ownershipを製品受入れの証拠へ結び付ける。
