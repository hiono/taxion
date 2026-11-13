# Taxion

Taxion は、coding agent と相談した依頼を、承認済みの実装・検証・独立レビューへ進める CLI です。
担当の使い分けと指示・成果の受け渡しを自動化し、手動コピーと不要なやり直しを減らします。
利用者は目的・範囲・実行上限を確認して承認し、Taxion が実行と成果の証拠を保存します。

Planner は計画、Coder は実装、Reviewer は独立レビューを担当します。
実行ハーネスには Copilot CLI（Copilot SDK 経由）または OpenCode（ACP 経由）を選びます。
GitHub・GitLab などの開発基盤とは別の設定で、GitLab のコードを Copilot で開発することもできます。
対応する契約と利用枠の選択は[課金ガイド](docs/userguide/billing.md)を参照してください。

## 使い始める

[利用者ガイド](docs/userguide/user-guide.md#導入方法)から正式バイナリを導入し、
今の coding agent に `taxion ready` を実行して出力を読むよう伝えます。
その agent が操作を担当するため、利用者が JSON や担当別 prompt を転記する必要はありません。
`ready` 自体はモデルを呼ばず、設定や workspace も変更しません。

`taxion init` で実行環境を準備し、利用者が提案と検証・レビュー・上限を承認して進めます。
外側の coding agent を使わない場合は `taxion request` から内部 Planner に相談できます。
導入から結果確認までの順序は[最初の依頼を進める](docs/userguide/README.md#最初の依頼を進める)にまとめています。

## 保存した成果から続ける

Taxion は作業条件、担当への指示、Git の対象、検証・レビュー結果、観測した利用量を保存します。
独立レビューは実装と同じ成果物を対象にし、合格した検証を根拠なく繰り返しません。
人が用意した成果物や途中の作業も、目的と実施済みの検査を明示してレビューできます。

時間と費用が不足する見込みは、停止前にまとめて判断します。
稼働実行の正式な上限変更は同じ session を維持し、正式停止後は成果と記録を引き継ぐ後続を作ります。
操作と適用条件は[保存成果からの継続](docs/userguide/README.md#止まった作業を続ける)と[復旧ガイド](docs/userguide/failure-recovery.md)を参照してください。
同じ承認内の内部遷移と、上限・権限の追加承認は区別します。

## 仕組みを理解する・開発する

[アーキテクチャ](docs/develop/architecture.md)は、CLI、Controller、実行ハーネス、
保存状態と Git 成果物の関係を説明します。
[開発への参加方法](CONTRIBUTING.md)から環境の準備と PR の手順へ、
[開発者向け索引](docs/develop/README.md)から変更箇所の仕様へ進めます。

公開版の変更と検証結果は[GitHub Releases](https://github.com/hiono/taxion/releases)にあります。
過去の判断は [ADR](docs/develop/adr/)に保存し、現在の使い方とは分けて扱います。
脆弱性は [SECURITY.md](SECURITY.md) に従って非公開で報告してください。
