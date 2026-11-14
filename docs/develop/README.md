# 開発者向け文書

全体像は[アーキテクチャ](architecture.md)、環境の準備と PR は
[開発への参加方法](../../CONTRIBUTING.md#開発環境)から読みます。
使い方の確認は[利用者向け文書](../userguide/README.md)へ進んでください。

## 変更する責務の仕様を読む

| 変更したいこと | 参照先 |
|---|---|
| 依頼・契約・承認・工程進行 | [Task Graph](task-graph.md)、[承認束](../userguide/approval-bundle.md)、[gate](../userguide/gates.md) |
| 担当の実行・観測・停止・継続 | [Runtime 契約](runtime-contract.md)、[復旧ガイド](../userguide/failure-recovery.md) |
| 状態と証拠の保存・互換性 | [Ledger と workspace](ledger-schema.md)、[Memory](../userguide/memory-model.md) |
| 設定・認証・課金・配布物の照合 | [設定](../userguide/configuration.md)、[課金](../userguide/billing.md)、[provenance](../userguide/binary-provenance.md) |
| 外部資料・開発基盤・公開記録 | [仕様入力](specification-input.md)、[開発基盤](forge-capabilities.md)、[公開文章](public-communication-policy.md) |

変更に関係する[ガードレール](guardrails.md)と回帰テストを確認し、
[開発環境の検証手順](../../CONTRIBUTING.md#開発環境)を実行します。
現在の使い方はガイド、現在の契約は仕様、理由は ADR、個別の結果は Issue・PR に書きます。Taxion全体の再設計原則は [ADR-0081](adr/0081-rebuild-over-legacy-constraints.md) を正本とします。Job-native設計では [ADR-0077](adr/0077-job-centered-role-collaboration.md) と [ADR-0080](adr/0080-job-boundary-and-prohibited-effects.md) を併せて参照してください。旧Task / ReviewFlow / operation allowlist は現行設計の互換性要件ではなく、置換・削除対象です。履歴移行が必要な場合も、実行・認可を行わない明示的な境界に限定します。
更新先は[文書の配置と責務](../../CONTRIBUTING.md#文書の配置と責務)を参照してください。
全仕様の通読や、過去の障害経緯の暗記は変更の前提にしません。

## ドメイン不変条件を変更する

[ドメイン不変条件と実装・検証対応表](domain-invariants.md)で、論理条件・強制箇所・回帰検証の対応を確認します。判定関数の存在だけで完了とせず、状態変更・保存・復元・外部入力の各経路を確認します。原則は [ADR-0079](adr/0079-enforce-domain-invariants-across-state-paths.md) を参照してください。

## 判断・計画・検証履歴を調べる

| 資料 | 読む目的 |
|---|---|
| [ADR](adr/) | 設計を選んだ理由と後続判断 |
| [制約付き最適化による設計経路の導出](adr/0078-constrained-optimization-for-design-decisions.md) | 複数候補の責務対応・設計比較を有限の制約付き最適化問題として導く方法 |
| [判断・Milestone 対応表](decision-traceability.md) | 過去の判断から実装 Issue と現在の仕様をたどる |
| [要件と到達条件](orchestration-requirements.md) | 採択した要件と段階別の検証履歴 |
| [ロードマップ](roadmap.md) | 現在の未達事項と、過去の開発段階の参照 |
| [設立時の方針](charter.md) | 初期の対象範囲。現在の構成の代用にはしない |
| `*-validation.md` | 記載した候補と条件での検証結果。現在の全機能の保証ではない |
| [イベント観測の検証](event-observation-validation.md) | 合成履歴の監視コストと通常受信の回帰条件 |
| [持込みレビューの検証](artifact-review-validation.md) | 入力の出典・目的・検証方針と停止後の引継ぎ |
| [入力観測と受入れの設計](adr/0066-separate-observation-admission-and-execution.md) | 観測・旧方針への適合・新契約の入力受入れ・実行許可の分離 |

履歴には当時の版や制約を残しますが、現在の操作の正本とは分けます。
後続判断で条件が変わった ADR は参照先を示し、古い判断と現在の仕様を併記して選ばせません。

## 公開する

コードと利用者・開発者文書、必要な ADR、Issue・PR を同じ対象に揃えます。
公開作業は[リリース手順](../../CONTRIBUTING.md#リリース)から
[Binary release](binary-release.md)へ進みます。
公開版の内容は[GitHub Releases](https://github.com/hiono/taxion/releases)、
実施した検証と未達事項は対応する Issue・PR に記録します。
