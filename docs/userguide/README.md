# 利用者向け文書

初めて使う場合は、下の「最初の依頼を進める」だけを順に読みます。文書一覧の並びやファイル名は読む順序を表していません。

## 最初の依頼を進める

1. [利用者ガイドの冒頭](user-guide.md)で Taxion と実行ハーネスの関係を確認し、[導入方法](user-guide.md#導入方法)で CLI を用意します。公開済みの版と、ソースからの導入を区別します。
2. coding agent と対話している場合は、[今の coding agent に操作を任せる](user-guide.md#今の-coding-agent-に操作を任せる)から `taxion ready` を実行させ、出力を読ませます。設定前でも使える、外側の agent 向けの操作手順です。
3. agent が[設定と実行前の確認](user-guide.md#設定と実行前の確認)を進め、利用者が範囲・検証コマンド・設定・予算を確認して承認すると、Coder・検証・独立レビューへ進みます。外側の agent を使わない場合は、[内部 Planner と相談する](user-guide.md#人のことばで依頼して計画を相談する) `taxion request` が別の入口です。設定値を調べる必要がある場合だけ[設定ガイド](configuration.md)を読みます。
4. [結果の確認と停止後の継続](user-guide.md#結果の確認と停止後の継続)で、実行状態、成果物の SHA、検証とレビューの証拠を確認します。停止した場合は次の手順へ進みます。

作業条件や skill 内容の固定・監査を個別に指定する場合は、オーケストレータが[依頼から独立レビューまで](user-guide.md#依頼から独立レビューまで)の操作例を使います。通常の利用者が Task・担当ごとの指示や JSON を手作業で作成・転記する必要はありません。

## 止まった作業を続ける

最初からやり直す前に、[保存結果と停止後の継続](user-guide.md#保存結果と停止後の継続)で停止理由、保存成果、session の停止証拠を確認します。続行には後続 Task と新しい予算の承認が必要です。成功した Coder の成果が残っていれば、同じ成果物の Reviewer から続けます。

保存状態と receipt の意味は[実行の復旧](execution-records.md#復旧時の照合)、承認する後続 Task と Git 操作の詳細は[停止した作業の継続](../develop/task-graph.md#停止した作業の継続)で確認します。`workflow recover` は残った runner 所有権を解放する操作であり、停止条件を解除して再実行する操作ではありません。

## 必要なときに参照する

以下は全員の必須通読ではありません。option を調べる、記録を公開する、Memory を移すなど、必要な作業から選びます。

| 文書 | 内容 |
| --- | --- |
| [利用者ガイド](user-guide.md) | 導入、計画の相談、承認、Coder・検証・独立レビュー、停止後の確認 |
| [設定ガイド](configuration.md) | 設定ファイル、優先順位、環境変数、認証と承認の確認範囲 |
| [CLI リファレンス](cli-reference.md) | コマンドと option、入力条件、実行上の制約 |
| [承認 bundle](approval-bundle.md) | 7 dimension、proposalHash・recordHash、確認 token、保存順序 |
| [三つの gate](gates.md) | contract・execution・publication の判定、JSON report、終了 code |
| [ロール間の handoff packet](role-handoff-packets.md) | 参照だけの immutable packet、canonical hash、受信時の照合と制限 |
| [Task の validate と authorize](task-validate-and-authorize.md) | manifest の検査、承認の記録、再利用と再承認 |
| [drift と legacy の分類](drift-and-legacy.md) | 差分の dimension、authorization impact、verified-legacy の扱い |
| [失敗からの復旧](failure-recovery.md) | entry code、blocked・fail、停止後の確認、rollback |
| [v0.6 からの更新](upgrading-from-v0.6.md) | 保存形式、legacy 承認、移行と戻し方 |
| [v0.7.0 の既知の制限](v0.7.0-known-limitations.md) | 対象外の機能、未確認事項、v0.7.1 候補 |
| [実行の復旧と外部記録](execution-records.md) | 保存結果の照合、復旧、承認済み記録の公開、失敗の記録 |
| [Memory](memory-model.md) | 保存範囲、承認、検索、export、ローカル移行 |
| [リモート接続](remote-access.md) | SSH 転送、token、公開接続時の設定 |
| [監査ログ](audit-log.md) | 記録内容、照合の限界、保存とローテーション |
| [GitHub Releases](https://github.com/hiono/taxion/releases) | 公開 version と各版の変更内容 |

実装の仕様と設計判断は[開発者向け文書](../develop/README.md)に分けています。Task の状態と承認の詳細は[Task Graph](../develop/task-graph.md)、仕様資料の利用許可は[仕様入力](../develop/specification-input.md)を参照してください。
