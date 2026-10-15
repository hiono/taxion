# Taxion 0.1.0 リリースノート案

この文書は草案です。M15 #79 と Release #78 の受入れ条件を満たすまで公開しません。

## 対象機能

Taxion 0.1.0 は、利用者が承認した coding agent の作業を進める CLI ツールです。Task・Dispatch の実行条件、Runtime の結果、検証の証拠、独立レビューをローカル workspace に記録します。

`taxion ready` は、今の coding agent が Taxion を操作するメタプロンプトを出力します。内部 Planner を使う `request` は別の入口です。外側の agent による契約生成・明示承認・実行・復旧を案内し、利用者による JSON・role prompt の転記を前提にしません。ready 自体はモデルを呼ばず、環境や状態を変更しません。

Task で出力方式 `worktree-tree-snapshot` を選ぶと、commit 前の作業をレビューできます。controller が tree SHA を記録し、ローカルの `refs/taxion/snapshots/` に保持して、独立した review worktree に同じ tree を再現します。既定は commit 方式のままです。

## 既知の制約

- `doctor` は選択中バックエンドの標準探索を診断します。通常の skill 利用に専用 directory や個別 hash 承認は不要です。検出と実ロード・品質確認は区別します。`ready` の出力だけで agent の判断・継続を保証せず、無料の操作回帰を実 agent の受入れの代わりにはしません。設定と Runtime 認証の確認範囲は、それぞれの文書に記載した範囲に限ります。
- AI Credit 上限は soft ceiling です。Taxion が利用量を観測して後続作業を止める前に、実行中のモデル turn が設定値を超える場合があります。
- 初回導入のガイド例と snapshot の継続は、実 Git を使うローカル CLI・controller テストで確認しています。有料モデルは呼びません。この成功は login 済み SDK の通し受入れを証明せず、旧 Dispatch に欠けている証拠も補いません。
- `runtime stop` は、保存済み session の対応情報と transport option だけを使い、元の prompt や tool を要求しません。実行操作を拒否し、pending work の継続を無効にします。suspension と cancellation の receipt は別々に保持します。`workflow run|step` は session 作成前に、今後使う可能性のある全 Reviewer の設定承認を確認します。
- この Release は、GitLab merge request、Bitbucket 接続、Web UI、複数利用者の管理、無人運用を約束しません。

## 公開後の導入

Go 1.26.0 が必要です。`v0.1.0` tag の公開後に、mise の [Go backend](https://mise.jdx.dev/dev-tools/backends/go.html) と [`mise use`](https://mise.jdx.dev/cli/use.html) で導入します。

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@v0.1.0
mise exec -- taxion --help
```

## 更新

`v0.1.0` を、導入する公開済みバージョンに置き換えます。

```sh
mise use -g go@1.26.0 go:github.com/hiono/taxion/cmd/taxion@<VERSION>
```

## 削除

[`mise unuse`](https://mise.jdx.dev/cli/unuse.html) で global mise 設定から Go tool を外します。他の設定で使っていなければ、mise がインストールを削除します。

```sh
mise unuse -g go:github.com/hiono/taxion/cmd/taxion
```

特定のインストールが残り、他のプロジェクトでも使っていなければ、[`mise uninstall`](https://mise.jdx.dev/cli/uninstall.html) でそのバージョンを指定して削除します。

```sh
mise uninstall go:github.com/hiono/taxion/cmd/taxion@v0.1.0
```
