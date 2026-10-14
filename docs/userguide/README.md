# 利用者向け文書

最初に[利用者ガイド](user-guide.md)で導入と設定を済ませ、`taxion request` から依頼を相談します。個別の操作や停止後の対応は、次の文書で確認できます。

| 文書 | 内容 |
| --- | --- |
| [利用者ガイド](user-guide.md) | 導入、計画の相談、承認、Coder・検証・独立レビュー、停止後の確認 |
| [設定ガイド](configuration.md) | 設定ファイル、優先順位、環境変数、認証と承認の確認範囲 |
| [CLI リファレンス](cli-reference.md) | コマンドと option、入力条件、実行上の制約 |
| [実行の復旧と外部記録](11-execution-records.md) | 保存結果の照合、復旧、承認済み記録の公開、失敗の記録 |
| [Memory](08-memory-model.md) | 保存範囲、承認、検索、export、ローカル移行 |
| [リモート接続](06-remote-access.md) | SSH 転送、token、公開接続時の設定 |
| [監査ログ](07-audit-log.md) | 記録内容、照合の限界、保存とローテーション |
| [v0.1.0 リリースノート案](release-notes-0.1.0.md) | 公開前の受入れ条件、対象機能、制約、公開後の導入方法 |

実装の仕様と設計判断は[開発者向け文書](../develop/README.md)に分けています。Task の状態と承認の詳細は[Task Graph](../develop/10-task-graph.md)、仕様資料の利用許可は[仕様入力](../develop/13-specification-input.md)を参照してください。
