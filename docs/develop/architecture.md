# アーキテクチャ

Taxion は、承認した作業を複数の役割へ渡し、実行と成果の対応をローカルに保存する CLI である。
モデルが作業の内容を考え、Controller が指示の発行、実行条件、検証と結果受理を制御する。
モデルの成功申告だけでは完了とせず、Git の対象と検証・独立レビューの証拠を照合する。
この文書は目標アーキテクチャと現行実装との差を区別して説明する。現行コードに残る旧 Task / ReviewFlow 経路は移行上の残存実装であり、目標モデルや互換性要件を定義しない。

## 入口と役割

操作の入口は二つある。`ready` は外側の coding agent に操作手順を渡す静的な出力で、
その agent が利用者と相談して Taxion を呼ぶ。`request` は CLI 内の Planner 対話から
計画と承認を受け、同じ Controller の実行処理へ接続する。
JSON を生成する主体が違っても、利用者の承認と実行条件は共通である。

Planner、Coder、Reviewer は役割であって、常駐サービスではない。
各 Role は Job の目的と共有 Context を受け取り、担当領域の判断を行う。
Role 間の協調は固定された Planner → Coder → Reviewer の直列手順に限定しない。
必要に応じて別 Role へ仕事を渡し、質問・回答・部分成果を同じ Job に蓄積して続行する。
実行 session は Role ごとに分離し、Reviewer の独立性も維持する。
人のコードや途中成果の独立レビューは、Coder 成功を前提にせず持込み入力から開始する。

## Job 中心の Role 協調

Job は作業の継続単位であり、Task は Job 内の作業分解、RoleExecution は担当 Role の一回の実行単位である。Dispatch / ReviewFlow は旧実行モデルの概念であり、Job の寿命や新しい協調経路の正本にしない。
Job の意味的な完全性を Controller が審査して Role の受渡しを拒否してはならない。
受信 Role は自然言語、既存 Context、成果物、過去のやり取りから判断し、可能な範囲を進める。
可逆で影響の小さい不足は仮定と不確実性を示して進め、判断に必要な情報だけを相手 Role に尋ねる。
回答・判断・部分成果は元の Job に結び付け、担当 Role がその情報を使って作業を再開する。

Role 間の会話は固定 schema の質問票ではなく、Job に属する追記型の協調記録として扱う。
質問の分類、理由、回答形式、参照一覧などを一律の必須項目にしない。
Controller が検証するのは Job と出所の対応、実行状態、権限、承認、安全性であり、
質問の意味や情報の十分性は担当 Role が判断する。

既存の ReviewFlow、Clarification、Dispatch の形はこのモデルを制約しない。
必要な場合は役割・実行・保存モデルを再設計し、既存 protocol の互換性を維持するためだけの分岐を増やさない。
設計の正本は [Job 中心の Role 協調](role-collaboration.md) と [ADR-0077](adr/0077-job-centered-role-collaboration.md) とする。

## 処理と依存関係

通常の処理は次のように接続する。

```text
利用者と外側 agent                  利用者と内部 Planner
        | ready と CLI 操作                 | request
        +------------------+-----------------+
                           |
                   CLI / Controller
                    |             |
         承認・状態・監査の保存    役割ごとの実行
                    |             |
           SQLite workspace      Runtime interface
                                  |             |
                             Copilot SDK    OpenCode ACP
                                  +------+------+
                                         |
                              隔離した役割と Git worktree
```

このほか、Memory と仕様検索は許可された情報を指示へ供給し、
開発基盤 adapter は repository access と承認済み外部記録の操作を担当する。
HTTP API と Web console は記録の参照・操作の別入口であり、モデル実行の中核ではない。

| 実装 | 責務 |
|---|---|
| `cmd/taxion` | 引数・入力、設定選択、利用者への表示、Controller の呼出し |
| `internal/core` | Task・Dispatch・Flow、承認束、状態とデータの整合規則。標準ライブラリだけに依存 |
| `internal/runtime/controller` | 実行前検査、runner 所有権、工程進行、検証、結果受理、停止と継続 |
| `internal/runtime` と各 adapter | SDK 非依存の実行契約と、Copilot・OpenCode 固有の接続・イベント処理 |
| `internal/store` | SQLite transaction、追記履歴の検証、実行・復旧記録 |
| `internal/gitproof`・`gitguard`・`tools` | Git 対象と変更の照合、公開前保護、担当へ許可した操作 |
| `internal/config`・`billing`・`provenance` | 設定、課金方式、承認した配布物と運用モードの照合 |
| `internal/memory`・`specsearch` | 情報の保存・検索と用途別の利用許可 |
| `internal/forge`・`publictext` | 開発基盤との接続、公開する文章と承認の照合 |
| `internal/portable`・`server`・`acp`・`web/` | 状態移送、HTTP・ACP の入口、Web console |

実際の呼出しを追う入口は `cmd/taxion/request_checkpoint.go` と
`internal/runtime/controller/run.go`、保存境界は `internal/store/sqlite.go` である。

## 契約・実行・成果は別の記録

Task Graph の Task は、目的、範囲、受入れ条件、役割、対象 SHA と承認を表す。
ReviewFlow は、その Task の実装・検証・独立レビューを進める工程で、
期限、予算、修正回数、発行した Dispatch、runner 所有権を保持する。
Dispatch は一人の担当への固定した指示と実行単位で、runtime session と結果へ結び付く。

ApprovalBundle は承認根拠の照合に使う。role packet は指示・証拠の固定した受渡しであり、
gate report は次の遷移が許可されるかの判定である。
これらの hash は承認内容や対象の一致を確認するもので、利用者の認証を代替しない。
Contract・Execution・Publication の判定と、その後の実行・公開操作は分かれている。

成果物は Git commit または Controller が保持する tree snapshot として対象を固定する。
モデルの出力、入力検証の合格、レビュー結果、結果受理の検証は別の証拠である。
診断レビューの完了も製品品質の合格とは区別する。

持込みレビューの v2 契約は、目的と検証方針を別々に承認する。
必須コマンドの合格、失敗結果の観測、コマンドなしの対象照合を明示し、
入力発行・結果受理・保存監査・停止後の引継ぎで同じ判定を使う。
診断の完了には読める報告を要求し、製品テストの失敗を合格へ変換しない。
判断理由と旧版の扱いは [ADR-0065](adr/0065-separate-review-purpose-from-validation-success.md)を参照する。

## 保存は一つの workspace を中心に行う

`store.Workspace` は `Ledger`、`Graph`、`Records` を同じ SQLite transaction で扱う。
Graph が現在の作業契約と工程を持ち、Records が利用量・runtime receipt・公開・復旧などの証拠を持つ。
Ledger は既存 API と互換な基本記録であり、現在の全実行状態を表す唯一の形式ではない。

保存は `workspace_documents` に記録単位で分割し、長い共通文字列を
`workspace_strings` へ共有保存する。古い一括 JSON 保存からの移行も store が担当する。
Memory は別の SQLite DB、成果物と保持 ref は Git に存在するため、
workspace DB 一つのコピーだけで移送や完全復旧が済むとは扱わない。
正式な一ファイル移送は `internal/portable` が必要な DB と Git object をまとめる。

詳しい保存形式は [Ledger と workspace のデータ形式](ledger-schema.md)、
Task と Flow の操作は [Task Graph](task-graph.md)を参照する。

期限と受信中の工程確認は、変更番号が同じ取引の検証済み状態を読み取り専用で再利用する。
時刻と外部 Memory は毎回評価し、変更後は全体を再検証する。
通常取得・更新・正式結果受理の検証は省略しない。
責務と計測は [ADR-0064](adr/0064-observe-unchanged-workspaces-without-reloading-history.md) と
[イベント観測の検証](event-observation-validation.md)を参照する。

## 実行を維持する変更と、停止後の継続

runner は Flow を単独所有し、同じ Dispatch の重複実行を防ぐ。
実行中は契約、対象、工程、利用量と期限を検査し、必要な停止は担当 session の停止証拠まで確認する。
停止と成果の保存は別の処理であり、取消要求だけで停止完了とは扱わない。

実行中の回答受信と保存 session の観測は、同じ結果確定処理を使う。
Controller が回答の対象・受入れ条件・Git 状態を検証し、runtime 終了、
管理 worktree の解放、正式結果の保存を担当する。
runner が終了して回答の受信が途切れた場合、`workflow recover` は
同じ session の終了済み回答と保存観測を照合して、この結果確定処理を使う。
回答を人が転記して成功状態へ変えたり、レビューを再送したりはしない。
元期限内の実行完了と、新たに許可した host 検証時間を分けて確認する。
host 検証済み結果と runtime 終了を順に保存し、途中終了後も完了済み検証を繰り返さない。
worktree 解放後に停止した場合は、保存した検証結果と Git の実在状態を再照合して確定する。
Reviewer の `SUCCEEDED` はレビュー完了を表し、`REQUEST_CHANGES` や `BLOCKED` の指摘も保持する。
指摘があるレビューを製品の承認には使わない。

時間と費用の不足は停止前に一括判断する。稼働中の `workflow extend` は、
元の Flow・工程計画を変えず、明示承認済みの上限を追記して同じ context と session を維持する。
正式停止後の `recover-review` は旧実行を再送せず、同じ候補・合格入力検証・保存解析を
使う後続を作る。期限切れ後の新期限は同じ要求の再契約に含める。
いずれも消費や成果をリセットする操作ではない。

入力形式と条件は [CLI リファレンス](../userguide/cli-reference.md)、
利用者の手順は[復旧ガイド](../userguide/failure-recovery.md)、
判断理由は [ADR-0058](adr/0058-recover-unstarted-artifact-review.md)が正本である。
