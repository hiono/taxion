# Taxion

Taxion は、人間の曖昧な思考を対話によって整理し、実行可能な計画へ昇華し、複数の Agent / Role の協調とガードレールによって、人間が意図した成果物と Job の完遂を支援するソフトウェアです。

Taxion は、最初から完全な要求仕様や実装手順を利用者へ要求しません。断片的な考え、希望、疑問、違和感、未整理の制約を対話の出発点として扱います。目的・制約・受入条件を明確にし、計画、実行、質問、回答、レビュー、修正、中断、再開を同じ Job の文脈へ結び付けます。

## 製品が実現すること

- 曖昧な依頼から目的、制約、受入条件を整理する。
- 新しい情報や成果に応じて計画を見直す。
- 複数の Agent / Role が調査、計画、実装、レビュー、修正を協調する。
- 情報不足を質問、回答、明示した仮定、部分作業によって解消する。
- 人間が判断・承認・介入する境界を保持する。
- Job の目的、判断、実行状態、成果、証拠を永続化し、同じ Job を安全に再開する。
- 実行状態や成果の出所が不明な場合に、成功を推測せず安全に停止・照合する。

## 設計の基本原則

1. 製品目的と利用者価値を最上位の評価基準とする。
2. 旧設計の制約を温存せず、新しいドメイン不変条件から再構築する。
3. Job の同一性、承認、対象・資源境界、禁止副作用、認証・認可、データ整合性を強制する。
4. 会話、提案、Role の判断だけで実行権限を拡張しない。
5. ドメイン不変条件は生成・変更・保存・復元・外部入力の全経路で強制する。
6. Taxion内部6 Actor通信はJobMessage、外部Agent相互運用はA2Aとし、両者を混同しない。
7. Observation、Conformance、Admission、Execution Permissionを分離し、観測や受入れから実行権限を推論しない。
8. 合成テスト、SDK互換性、実Provider実行、実Job完遂の証拠を混同しない。

## 現在利用できる機能

- `taxion auth login`：OIDC Authorization Code Flow with PKCEでHUMAN Principalを検証します。ログインSessionやtokenは保存しません。
- `taxion job create --request "..."`：OIDC認証後、原文依頼とIntakeProvenanceを同一Job transactionで保存したDRAFT Jobを作成します。
- `taxion job authorize-planning ... <job-id>`：OIDC再認証後、DRAFT Jobに束縛したbounded read-only PlanningAuthorizationを発行・永続化します。
- `taxion job start-planning --authorization-id <grant-id> <job-id>`：OIDC再認証後、grantを再検証し、Taskless PLANNER RoleExecutionとProvider Start intentを永続化します。Providerは起動しません。
- `taxion job message --body "..." <job-id>`：OIDC再認証後、Jobの所有者からControllerへの補足を保存します。`--to <role-execution-id>` で宛先、`--reply-to <message-id>` で返信先を指定できます。保存のみで、Agentへの自動配送は行いません。
- `taxion dev job create --request "..."`：開発・永続化テスト専用の未認証ローカルDRAFT Jobを作成します。
- `taxion dev provider login`：Copilot CLIをTaxion専用のローカルプロファイルへログインします。
- `taxion dev provider run --prompt "..."`：Copilot CLIをACP stdioで起動し、自然言語promptを1回送信して応答を表示します。ツール権限要求はすべて拒否します。ACP AdapterはTask実行を受け付けません。これはProvider接続のスモークテストであり、承認済みJobのCoder実行ではありません。
- `taxion dev provider run --provider opencode --prompt "..."`：OpenCodeの`opencode/big-pickle`を一時プロファイルで起動するスモークテストです。Jobには接続されません。
- `taxion dev provider run --provider opencode --job <local-job-id> --prompt "..."`：未認証のローカルDRAFT Jobに限り、依頼と過去の会話を自然言語でProviderへ渡し、Controller / Orchestrator間の記録として同じJobへ保存します。各実行は新しいSessionを開始し、履歴のコピーで文脈を再構成します。Provider失敗時も送信前に保存した入力は残ります。取得した応答は保存してからProviderを終了します。認可済みJob実行やAgent間自動配送ではありません。
- 上記に `--consult` を付けると、主Agentの質問を別Sessionへ渡し、その回答を同じ主Agent Sessionへ戻します。質問・回答はOrchestratorによる記録として返信関係をつけて順に保存し、失敗時も保存済みの内容は残ります。質問を必ず一回求める開発用の実験であり、RoleExecution間の配送や終了後のSession再開ではありません。
- 上記に `--review` を付けると、Plannerの計画をCoderへ、CoderのテキストをReviewerへ渡し、指摘を同じCoder Sessionへ戻した後、同じReviewer Sessionで再レビューします。`--consult` も付けると、Coderに質問を一つ求め、同じPlanner Sessionの回答を同じCoder Sessionへ戻してから作業を続けます。各応答はOrchestratorの記録として順に保存します。Coderの出力を作業ファイルに適用する機能はありません。ツール権限要求は拒否しますが、OS隔離は保証せず、正式なRoleExecution間の配送でもありません。
- `taxion job show <job-id>`：Jobの現在の内容をJSONで表示します。
- `taxion job list`：保存済みJobを一覧表示します。

ローカルJobで計画・レビューを一巡させる例（`JOB_ID` は作成時に表示されたIDに置き換えます）：

```sh
taxion dev job create --request "小さなCLIツールの案を作る"
taxion dev provider run --provider opencode --job JOB_ID --prompt "実装案を作ってください" --review
```

### Copilot CLI接続のスモークテスト

```sh
taxion dev provider login
taxion dev provider run --prompt "この依頼を一文で要約してください"
```

Copilot CLIはTaxion専用のprivate profileで認証します。Providerのtool permission requestはすべて拒否されます。このコマンドはACP接続と応答取得の確認用であり、Jobに束縛したPlanner / Coder実行ではありません。GitHub Actions artifactから取得した場合は、ZIP展開後に`chmod +x ./taxion`を実行してください。

OpenCodeでは送信前にACP Sessionが`opencode/big-pickle`を選択中と報告したことを確認します。プロジェクト等の設定がこれを上書きした場合や選択を確認できない場合は送信せずに失敗します。

JobはSQLiteへtransactionで保存し、Command receiptとpayload hashを照合します。既定の保存先はOSのユーザー設定ディレクトリ配下の taxion/jobs.sqlite です。`--store` で保存先を明示できます。

UnixではSQLiteを開く前にデータベースと既存のjournalファイルの権限を所有者のみの読み書きに制限します。保存先には、ほかのユーザーがファイルを置き換えられないディレクトリを指定してください。

PlanningAuthorizationの発行・永続化と、Taskless PLANNERのRoleExecution / Provider Start intentの記録まで利用できます。開発用ProviderスモークテストではCopilot CLIの実起動と応答取得を確認できますが、結果はJobへ結び付かず、承認済みJobのPlanner / Coder実行ではありません。Provider turn / effect / resume、JobContractの承認、Coder / Reviewer実行、Handoff、Job完了は未実装です。Start intentの記録をProvider実行やplanning完了の証拠として扱いません。機能の実装状態はコード、テスト、対象HEADのCIによって確認します。

## 仕様・設計・受入れ

- [Product Contract](docs/specifications/product-contract.md): 利用者シナリオ、Job lifecycle、権限、内部JobMessage、外部A2A、永続化、Worktreeの製品契約。
- [Runtime Boundaries](docs/design/runtime-boundaries.md): 論理コンポーネント、信頼境界、Command / Provider / A2A / Recoveryのデータフロー。
- [Product Acceptance Test Specification](docs/test-specs/product-acceptance.md): 証拠水準とJob完遂を確認する受入れシナリオ。

## 開発・運用

開発時の品質基準は[Development Quality Gates](docs/development/quality-gates.md)を参照してください。

## 設計判断

採用済み設計判断は [ADR index](docs/adr/README.md) を参照してください。製品目的と再構築原則の根拠となった旧設計判断は、アーカイブ済みリポジトリの原本を参照して再構成しています。旧Git履歴をこのリポジトリへ移植していません。
