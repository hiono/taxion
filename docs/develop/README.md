# 開発者向け文書

[開発への参加方法](../../CONTRIBUTING.md)に、環境の準備と変更時の確認事項を記載しています。操作手順は[利用者向け文書](../userguide/README.md)、現在の仕様と設計判断は次の文書で確認できます。

| 文書 | 内容 |
| --- | --- |
| [プロジェクトの設立時の方針](00-charter.md) | 設立時の対象範囲と制約 |
| [ガードレール](01-guardrails.md) | 必要な不変条件と実装での確認箇所 |
| [公開情報と出典の方針](02-privacy-provenance.md) | 公開資料に含めない情報と確認手順 |
| [Runtime 契約](03-runtime-contract.md) | SDK 共通インターフェースと現在の adapter の制約 |
| [ロードマップ](04-roadmap.md) | 段階別の到達条件と追跡 Issue |
| [Ledger のデータ形式](05-ledger-schema.md) | キー、API の入出力、変更条件 |
| [要件と到達条件](09-orchestration-requirements.md) | 採択済み要件、段階別の受入れ、実装との差分 |
| [Task Graph](10-task-graph.md) | 登録、状態遷移、実行条件、保存と互換性 |
| [開発基盤の対応評価](12-forge-capabilities.md) | 対応する API、実装・受入れの区別、後続作業 |
| [仕様入力と利用許可](13-specification-input.md) | 出典・バージョン・用途別承認と送信時の照合 |
| [判断・Milestone 対応表](14-decision-traceability.md) | ADR、実装 Issue、現在の仕様の対応 |
| [ADR](adr/) | 設計判断、選択肢、理由、後続の補足 |

ADR の採択と機能の実装・受入れ完了は別々に確認します。過去の要件や結果は残し、現在の仕様と異なる場合は補足を記載します。
