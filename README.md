# Taxion

Taxion は、coding agent と相談した依頼を、承認済みの Coder・検証・独立レビューへ進める CLI ツールです。今の agent に `taxion ready` を実行させると、その agent が Taxion を操作するためのメタプロンプトを受け取ります。JSON や担当ごとの指示を利用者がコピーして渡す必要はありません。

利用者が作業範囲と予算を承認すると、Taxion が担当への指示、実行結果、Git の証拠、レビュー結果をローカルに記録します。独立レビューでは、Coder が作成したものと同じ内容を確認します。

v0.2.0 は、v0.1.0 に OpenCode ACP runtime と停止後の復旧修正を追加した版です。実モデルによる文書作業、同一成果物の独立レビュー、部分成果からの復旧を [Issue #78](https://github.com/hiono/taxion/issues/78)・[Issue #79](https://github.com/hiono/taxion/issues/79)・[Issue #80](https://github.com/hiono/taxion/issues/80)で確認しました。版ごとの機能と制約は [GitHub Releases](https://github.com/hiono/taxion/releases) を参照してください。

## 使い始める

[利用者ガイド](docs/userguide/user-guide.md)に、現在のソースからの導入、今の agent への `taxion ready` による操作手順の出力、設定・承認から結果確認までを記載しています。外側の coding agent を使わず、Taxion 内部の Planner と計画を相談する `taxion request` も別の入口として使えます。必要な option は[CLI リファレンス](docs/userguide/cli-reference.md)、設定ファイルと環境変数は[設定ガイド](docs/userguide/configuration.md)で確認できます。復旧・Memory・リモート接続などの文書は[利用者向け文書の一覧](docs/userguide/README.md)から参照できます。

初回利用は[導入から結果確認までの順序](docs/userguide/README.md#最初の依頼を進める)、作業が停止した場合は[保存成果からの継続](docs/userguide/README.md#止まった作業を続ける)をたどってください。参照資料をすべて読む必要はありません。

`taxion doctor` で、設定したバックエンドと同じ検索先から見つかる skills・plugins・指示等を確認できます。通常はバックエンドの標準配置を使い、Taxion 用 directory へのコピーや skill ごとの hash 承認は不要です。

引数なしの `ready` はモデルを呼ばず、設定や workspace も変更しません。明示的な `ready --install-skill` は標準の home 配下に版付き操作 skill を配置し、`ready --check-skill` はその互換性と内容を JSON で確認します。更新は `ready --update-skill` で明示し、利用者の編集は上書きしません。ただし、配置や出力だけで agent の実ロード・判断・操作品質を保証するコマンドではありません。外側の agent の費用と、その後の Taxion によるモデル実行の費用は別です。

Taxion は coding agent や Git の認証を提供しません。新しい実行は予測不能な token 閾値で停止せず、各 model call で取得した利用量を報告します。account 残高・利用率は同じ課金単位の allowance を確認できない場合に unavailable と表示します。SDK session ceiling は別の soft ceiling で、総請求額を保証しません。

## 開発する

[開発への参加方法](CONTRIBUTING.md)に、開発環境と変更時の確認事項を記載しています。[開発者向け文書の一覧](docs/develop/README.md)から、要件、現在の仕様、[ロードマップ](docs/develop/roadmap.md)、[設計判断の記録（ADR）](docs/develop/adr/)を参照できます。[脆弱性の報告](SECURITY.md)は公開 Issue ではなく、案内に従って非公開で行ってください。
