# Taxion

Taxion は、人のことばでコードの変更を依頼し、Planner と計画を相談してから、Coder・検証・独立レビューへ進める CLI ツールです。JSON や担当ごとの指示をコピーして渡す必要はありません。

利用者が作業範囲と予算を承認すると、Taxion が担当への指示、実行結果、Git の証拠、レビュー結果をローカルに記録します。独立レビューでは、Coder が作成したものと同じ内容を確認します。

**v0.1.0 はまだ公開されていません**。自然言語の依頼から計画・コード作成・独立レビューまでの実モデルでの通し受入れは未確認です。

## 使い始める

[利用者ガイド](docs/userguide/user-guide.md)に、現在のソースからの導入、設定、`taxion request` で依頼して承認する手順を記載しています。必要な option は[CLI リファレンス](docs/userguide/cli-reference.md)、設定ファイルと環境変数は[設定ガイド](docs/userguide/configuration.md)で確認できます。復旧・Memory・リモート接続などの文書は[利用者向け文書の一覧](docs/userguide/README.md)から参照できます。

初回利用は[導入から結果確認までの順序](docs/userguide/README.md#最初の依頼を進める)、作業が停止した場合は[保存成果からの継続](docs/userguide/README.md#止まった作業を続ける)をたどってください。参照資料をすべて読む必要はありません。

Taxion は coding agent や Git の認証を提供しません。token 上限は観測した利用量から後続作業を止めるための上限であり、実行中の一 turn の費用を必ず抑える provider 側の制限ではありません。

## 開発する

[開発への参加方法](CONTRIBUTING.md)に、開発環境と変更時の確認事項を記載しています。[開発者向け文書の一覧](docs/develop/README.md)から、要件、現在の仕様、[ロードマップ](docs/develop/roadmap.md)、[設計判断の記録（ADR）](docs/develop/adr/)を参照できます。[脆弱性の報告](SECURITY.md)は公開 Issue ではなく、案内に従って非公開で行ってください。
