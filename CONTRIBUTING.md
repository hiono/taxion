# 開発への参加方法

Taxion は、依頼と承認から実装・検証・独立レビューまでを進めるための CLI です。
変更では、利用者の作業が完了することを重視します。既存の成果を再利用し、
承認や保存の安全性を守りながら、不要な操作とやり直しを減らしてください。

初めての変更は、環境の準備、対象箇所の修正と検証、PR の順で進めます。
使い方を確認したい場合は[利用者ガイド](docs/userguide/user-guide.md)、
実装の構成を調べる場合は[開発者向け索引](docs/develop/README.md)を参照してください。

## 開発環境

Go の必要版は `go.mod`、開発ツールは `mise.toml` が正本です。

```bash
mise install
mise exec -- go version
mise exec -- go build ./...
```

Taxion CLI 自体を検証するときは、[development mode の手順](docs/userguide/binary-provenance.md#明示-policy-と初回設定)に従い、専用の一時 store と development policy を用意します。`storeDirectory` には store の実体 path を指定し、開発中の source build に同じ store を明示して `provenance configure`、`provenance verify`、`ready --check-skill`、`ready` を実行します。利用者の product store や policy は使いません。この設定はモデル実行や外部サービス利用の承認を与えません。

修正中は変更した package のテストを実行し、PR 前に全体を確認します。

```bash
mise exec -- go test ./internal/core
mise run check
```

`mise run check` は format、vet、Go の test/build、文書リンク、Web のテストを確認します。
有料モデルや実サービスのテストは起動しません。Web は Node 22.18.0 以降を使い、
通常検査に追加 package のインストールは不要です。
CLI のコマンド・flag を変えた場合は `mise exec -- go generate ./cmd/taxion` で補完も更新します。

## 変更から PR まで

取り組む問題と利用者にとっての完了条件を Issue または PR に書きます。
関連する仕様と呼出し元を確認し、目的を満たす最小の変更にしてください。
`internal/core` は標準ライブラリだけに依存し、外部サービスとの接続は runtime などの adapter に置きます。

動作を変える場合は、その変更がなければ失敗する回帰テストを追加します。
承認、予算、成果保全に関わる変更では、正常時だけでなく拒否・中断時にも
保存成果と履歴が失われないことを確認します。詳しい実行・保存仕様は
[Runtime 契約](docs/develop/runtime-contract.md)と[Ledger](docs/develop/ledger-schema.md)を参照してください。

PR には問題、変更の理由、確認した結果、未確認の範囲を示します。
利用者の操作や互換性が変わる場合は文書も同じ PR で更新し、設計の選択が変わる場合は ADR を残します。
push 前に差分を確認し、秘密情報、個人情報、private な DB や生ログを含めないでください。

## 検証する範囲を選ぶ

通常テストは一時 Git repository、合成応答、ローカルの protocol peer を使います。
認証・モデル・外部サービスがなくても実行できることが、日常の開発の前提です。
共通の実行制御を変える場合は、Copilot と OpenCode の両 adapter と Controller を確認します。

```bash
mise exec -- go test -race ./internal/runtime/controller ./internal/runtime/copilot ./internal/runtime/openacp
```

実サービスでの受入れは、通常検査とは別に対象、アカウント、モデル、権限、予算、期限を確認して行います。
ローカル試験の成功を実サービスや文書品質の実証とは扱いません。
停止時は成果・検証記録・累計消費を保持し、再実行が必要な範囲を判断します。
継続の操作は[復旧ガイド](docs/userguide/failure-recovery.md)を参照してください。

実サービス用の入力と実行条件は、対象テストの近くに記載しています。
[Copilot のテスト](internal/runtime/copilot/)、
[OpenCode のテスト](internal/runtime/openacp/)、
[文書作業の通し受入れ](cmd/taxion/opencode_live_test.go)から対象を選びます。
個別試行の結果は対応する Issue・PR に記録し、この参加ガイドへ蓄積しません。

## 文書の配置と責務

読者が知りたいことに応じて更新先を選び、同じ説明は複製せずリンクします。

| 読者の目的 | 更新先 |
|---|---|
| 概要を知り、導入する | `README.md`、[利用者向け索引](docs/userguide/README.md) |
| 依頼・承認・実行・復旧を進める | 利用者ガイド、設定ガイド、復旧ガイド |
| コマンドと入力形式を調べる | CLI リファレンス |
| 仕組みと変更の影響を理解する | [開発者向け索引](docs/develop/README.md)から担当する仕様へ |
| 設計を選んだ理由を知る | [ADR](docs/develop/adr/) |
| 個別の変更と確認結果を調べる | Issue・PR、リリースごとの検証記録 |

現在の使い方と、過去の判断・検証結果は区別してください。
旧仕様を現行の規則として残さず、ADR では後続判断への参照を示します。
ファイル名は内容を表す名前にし、ADR 以外の連番で通読順を作りません。

## 説明文とコードコメント

公開説明は日本語で、利用者と貢献者に向けて変更と理由を直接書きます。
コマンド、API、正式な機能名、引用原文の表記は保ちます。
未確認のことは未確認とし、内部の作業通信や非公開の証拠を転記しません。
公開内容の確認方法は[公開コミュニケーション方針](docs/develop/public-communication-policy.md)を参照してください。

コメントはコードだけでは分からない理由や API の契約を説明します。
実装の逐語説明と作業履歴は置かず、長い設計理由は ADR へ残します。

## 実行ハーネスと SDK の更新

SDK の依存版は `go.mod` と `go.sum` で管理します。
更新では上流の互換性と、接続・権限・tool・取消・利用量の変更を確認し、
両 adapter の回帰と影響する実サービス受入れを分けて行います。
CLI の製品版と通信 protocol の版は区別します。現在の接続条件は
[Runtime 契約](docs/develop/runtime-contract.md)、OpenCode 固有の判断は
[ADR-0038](docs/develop/adr/0038-run-opencode-acp-with-sealed-role-boundaries.md)が正本です。

バックエンドに影響する変更では [バックエンド別の確認記録](docs/develop/backend-acceptance.md)を参照し、
Copilot と OpenCode の合成確認、過去の実証、現在の実受入れの未達を区別する。
通常試験で実モデルを開始せず、追加実証に必要な構成・費用・権限の承認を先に確認する。

## リリース

正式な公開手順は[Binary release](docs/develop/binary-release.md)に一本化しています。
候補と独立レビューの対象を一致させ、操作・更新・復旧の文書をコードと揃えてから公開します。
公開済み tag と配布物は移動・置換せず、修正が必要なら新しい版を作ります。
公開の結果と未達条件は対応する Issue・PR に残し、
[GitHub Releases](https://github.com/hiono/taxion/releases)では利用者に影響する変更を説明します。
