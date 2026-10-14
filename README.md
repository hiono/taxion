# Taxion

Taxion は coding agent の作業を Task として承認し、Dispatch、検証結果、Git の証拠、独立レビューを記録する CLI-first の実行管理ツールです。Human が作業範囲と実行予算を承認し、controller が保存した契約・SHA・検証条件に沿って進行を確認します。

Taxion は coding agent や Git の認証主体を提供しません。token 上限は各実行の利用量を観測して後続作業を止める上限であり、実行中の一 turn の費用を必ず抑える provider 側の hard cap ではありません。実装・制約は[利用者ガイド](docs/user-guide.md)と[CLI リファレンス](docs/cli-reference.md)を参照してください。

## 使う

- [利用者ガイド](docs/user-guide.md) — 導入、設定、依頼の承認から独立レビューまで
- [設定ガイド](docs/configuration.md) — Global／Project 設定、環境変数、確認コマンド
- [CLI リファレンス](docs/cli-reference.md) — コマンド、入力条件、運用上の制約
- [Task Graph と実行契約](docs/10-task-graph.md) — Task・Dispatch の状態、証拠、予算停止後の復旧
- [実行記録](docs/11-execution-records.md) — checkpoint と外部公開の手順
- [判断・Milestone 対応表](docs/14-decision-traceability.md) — ADR、実装 Issue、現在の仕様の対応

## 開発する

- [CONTRIBUTING.md](CONTRIBUTING.md) — 開発環境、検証、文書の責務、変更の進め方
- [Roadmap](docs/04-roadmap.md) — 採択した到達条件と Issue の対応
- [ADR](docs/adr/) — 設計判断とその根拠

**v0.1.0 はまだ公開されていません。** 現在のソースから試す手順は[利用者ガイド](docs/user-guide.md)を参照してください。release tag と配布手順は、実際の公開後にここへ反映します。
