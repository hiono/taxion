# Taxion 0.1.0 リリースノート案

この文書は草案です。M15 #79 と Release #78 の受入れ条件を満たすまで公開しません。

## 対象機能

Taxion 0.1.0 は、利用者が承認した coding agent の作業を進める CLI ツールです。Task・Dispatch の実行条件、Runtime の結果、検証の証拠、独立レビューをローカル workspace に記録します。

`taxion ready` は、今の coding agent が Taxion を操作するメタプロンプトを出力します。内部 Planner を使う `request` は別の入口です。外側の agent による契約生成・明示承認・実行・復旧を案内し、利用者による JSON・role prompt の転記を前提にしません。引数なしではモデルを呼ばず、環境や状態を変更しません。`--install-skill` と `--update-skill` は home の標準探索先へ版付き操作 skill を明示配置・更新し、`--check-skill` は許容版・CLI 契約・ハッシュを確認します。改変済みファイルは上書きせず、配置や互換性を実ロード成功と取り違えません。

Task で出力方式 `worktree-tree-snapshot` を選ぶと、commit 前の作業をレビューできます。controller が tree SHA を記録し、ローカルの `refs/taxion/snapshots/` に保持して、独立した review worktree に同じ tree を再現します。既定は commit 方式のままです。

workspace の既定保存先は Git 作業ツリールートの `.taxion/` です。Git の外ではカレントディレクトリを使い、linked worktree はそれぞれ独立した保存先を持ちます。別の正式状態を選ぶ例外用途では `--store` に旧 ledger のパスを指定します。`workspace.db` 自体を渡す指定は拒否します。

役割別 SDK session を維持し、過去の instruction を繰り返し展開する代わりに、保存済み Dispatch ID と instruction・result の hash で結果を参照します。目的・範囲・受入れ条件・証拠・予算・独立 Reviewer の指摘は文脈に残します。SQLite は変更レコードだけを保存し、長い同一文字列を共有・圧縮します。計画 receipt と実行先 Flow ID も DB に保存し、正式 event の二重保存を避けます。

`workspace snapshot` は停止・照合済み workspace、存在する Memory、必要な Git object と親履歴を非公開の SQLite 一ファイルへ収めます。`workspace inspect-snapshot` は隔離配置で DB と Git を再構築して照合します。`workspace restore` はファイル全体の SHA-256 と承認元を指定し、新規 directory へ復元します。旧契約・承認・結果・hash は保持し、新配置を別 receipt に記録します。旧 SDK session を再開せず、新しい配置・設定・予算を承認した後続 Task で継続します。具体的な操作は[一ファイル移送](user-guide.md#別マシンへの一ファイル移送)を参照してください。

## 既知の制約

- `doctor` は選択中バックエンドの標準探索を診断します。通常の skill 利用に専用 directory や個別 hash 承認は不要です。検出と実ロード・品質確認は区別します。`ready` の出力だけで agent の判断・継続を保証せず、無料の操作回帰を実 agent の受入れの代わりにはしません。設定と Runtime 認証の確認範囲は、それぞれの文書に記載した範囲に限ります。
- AI Credit 上限は soft ceiling です。Taxion が利用量を観測して後続作業を止める前に、実行中のモデル turn が設定値を超える場合があります。
- 初回導入のガイド例と snapshot の継続は、実 Git を使うローカル CLI・controller テストで確認しています。有料モデルは呼びません。この成功は login 済み SDK の通し受入れを証明せず、旧 Dispatch に欠けている証拠も補いません。
- `runtime stop` は、保存済み session の対応情報と transport option だけを使い、元の prompt や tool を要求しません。実行操作を拒否し、pending work の継続を無効にします。suspension と cancellation の receipt は別々に保持します。`workflow run|step` は session 作成前に、今後使う可能性のある全 Reviewer の設定承認を確認します。
- この Release は、GitLab merge request、Bitbucket 接続、Web UI、複数利用者の管理、無人運用を約束しません。

### 保存形式と移送の注意

書込み接続は旧 DB を transaction 内で移行します。read-only 接続は移行しません。移行後は新形式に対応する binary を使い、古い binary での読取り・更新を前提にしないでください。移行確認用に残した旧 JSON は正本ではなく、今回の更新でも自動削除しません。DB の空き page を自動で切り詰める処理もありません。

通常の `workspace.db` 単体コピーは Memory と Git の移送を代替しません。移送コマンドは不足 object、観測した未記録変更、履歴中の禁止 path、submodule、Git LFS、既知の token・秘密鍵パターンを拒否します。外部 skill／MCP の実体、SDK home、認証情報、Git config・hook は移送せず、移送先で必要な設定と承認を用意します。未知の秘密まで検出する保証はないため、archive は非公開で扱ってください。

Memory は既存の256 MiB上限を維持します。archive と Git object をメモリに読み込むため、必要なメモリと一時保存領域は移送量に応じて増えます。Windows は既存の Unix process-group 処理のため未対応です。

入力 context のバイト数減少と無料回帰は確認していますが、実推論 token・料金の削減率は未測定です。新しい結果 schema の実 SDK 推論受入れも未確認であり、無料 fixture の成功と区別します。

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
