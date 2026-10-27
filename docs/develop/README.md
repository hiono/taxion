# 開発者向け文書

初めて変更する場合は、[開発への参加方法](../../CONTRIBUTING.md#開発環境)で環境を準備し、[ロードマップ](roadmap.md)と対応 Issue から作業を選びます。利用者として動作を確認する手順は[利用者向け文書](../userguide/README.md)にあります。

## 変更箇所の仕様から検証へ進む

全機能の設計・運用では、[承認済みの目的に沿って柔軟に対応する方針](adr/0055-adapt-within-approved-goals.md)を適用します。目的・受入れ条件・権限・残予算・期限を守り、手段は状況に応じて選びます。内部の形式や版の違いだけで同じ承認を繰り返さず、実際に新しい判断が必要な差分を示します。

Task の登録・承認・状態を変える場合は [Task Graph](task-graph.md)、実行担当や SDK 接続を変える場合は [Runtime 契約](runtime-contract.md)、保存形式を変える場合は [Ledger のデータ形式](ledger-schema.md)を読みます。接続する基盤の範囲は[開発基盤の対応評価](forge-capabilities.md)、資料の取扱いは[仕様入力と利用許可](specification-input.md)で確認します。

対応する[ガードレール](guardrails.md)と回帰テストを確認して変更し、[開発環境の検証手順](../../CONTRIBUTING.md#開発環境)を実行します。操作の説明は利用者ガイド、現在の動作は所有する仕様書へ反映します。[文書の配置と責務](../../CONTRIBUTING.md#文書の配置と責務)で更新先を選びます。

## 判断と実施結果を残して公開へ進む

変更の結果と証拠は Issue・PR に記録します。設計上の選択や制約が変わる場合は [ADR](adr/)に判断理由を残し、[判断・Milestone 対応表](decision-traceability.md)から現在の仕様へ案内します。ADR の採択だけで機能の受入れ完了にはしません。

公開作業は[リリース手順](../../CONTRIBUTING.md#リリース)に従います。受入れ条件への例外は Issue に記録し、利用者向けの公開 version と各版の変更内容は[GitHub Releases](https://github.com/hiono/taxion/releases)で確認します。

## 仕様・要件・履歴を必要に応じて参照する

一覧を上から通読する必要はありません。設立時の方針と設計判断の履歴は、現在の操作手順と分けて扱います。

| 文書 | 内容 |
| --- | --- |
| [プロジェクトの設立時の方針](charter.md) | 設立時の対象範囲と制約 |
| [ガードレール](guardrails.md) | 必要な不変条件と実装での確認箇所 |
| [公開情報と出典の方針](privacy-provenance.md) | 公開資料に含めない情報と確認手順 |
| [Runtime 契約](runtime-contract.md) | SDK 共通インターフェースと現在の adapter の制約 |
| [ロードマップ](roadmap.md) | 段階別の到達条件と追跡 Issue |
| [Ledger のデータ形式](ledger-schema.md) | キー、API の入出力、変更条件 |
| [要件と到達条件](orchestration-requirements.md) | 採択済み要件、段階別の受入れ、実装との差分 |
| [Task Graph](task-graph.md) | 登録、状態遷移、実行条件、保存と互換性 |
| [開発基盤の対応評価](forge-capabilities.md) | 対応する API、実装・受入れの区別、後続作業 |
| [仕様入力と利用許可](specification-input.md) | 出典・バージョン・用途別承認と送信時の照合 |
| [判断・Milestone 対応表](decision-traceability.md) | ADR、実装 Issue、現在の仕様の対応 |
| [ADR](adr/) | 設計判断、選択肢、理由、後続の補足 |

ADR の採択と機能の実装・受入れ完了は別々に確認します。過去の要件や結果は残し、現在の仕様と異なる場合は補足を記載します。
