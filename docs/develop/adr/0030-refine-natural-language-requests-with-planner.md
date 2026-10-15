# 30. Planner との対話からコピペなしで実装を依頼する

Date: 2026-10-15

## Status

Accepted

CLI と無料の通し検証を実装した。実 Copilot モデルでの入口の受入れは未確認。

## Context

Copilot SDK の導入目的は、人のことばで作業を依頼し、role 間の指示や成果物を手作業でコピーしないことだった。しかし従来の入口には Task、confirmation、Flow の JSON と role prompt ファイルの作成が残っていた。保存済み Task の助言を返す Planner だけでは、新しい依頼を相談してから実装する体験にならない。

## Decision

`taxion request` を入口にする。Planner は依頼を読み取り、必要な確認質問を返し、会話に応じて目的・範囲・完了条件・検証コマンドを修正する。利用者に契約 JSON の作成を求めない。

`/plan` は上限付きの有料相談、`/approve` は表示した計画と残り上限での実装・検証・独立レビューを選ぶ。CLI が既存の intake、confirmation、flow を組み立てる。新しい実行 engine や自動承認は作らない。

Planner は tool-free とし、対話、commit 内の path 一覧、利用者が選んだ commit 内のテキストを読む。未読の source や未実施の検証を確認済みとしない。承認後は bundled prompt と snapshot mode を使い、worktree 内の新規ファイルを許可する。既存の権限・成果物検証・停止機構は維持する。

固定設定の `approvalRef` を繰り返し使うため、設定 decision ID は `REF@task:PRIMARY_TASK_ID` でも識別できるようにする。承認 tuple と exact Task 一覧は変えず、旧同名 decision との互換性を維持する。承認履歴を上書きせず、依頼のたびに設定ファイルを編集させない。

## Consequences

### 標準カスタマイズを使う後続補足

通常実行の Planner / Orchestrator でも、バックエンドの標準 skill・指示を探索する。
元判断の tool-free は現在の通常実行では file・shell tool を与えないという条件に改める。
通常の skill 利用には個別承認 JSON や専用 directory を要求せず、
`taxion doctor` から実行と同じ探索先と利用不能の理由を確認する。
内容固定が必要な場合の手動承認方式と、未確認の実 skill 品質工程は別に維持する。

利用者は依頼と Planner への回答、実行の確認に集中できる。相談の全 token を実行と共有し、期限を延長せず、usage と対話を private receipt に残す。

既存の個別 CLI 操作は高度な設定に使える。外部 skill の承認、相談の自動再開、実行中のこの console への入力は今回追加しない。OpenCode ACP 接続は後続 Issue #80 とし、初回体験の実装に混ぜない。無料の合成 runtime 検証と、実モデルによる計画の妥当性・実行の受入れを区別する。
