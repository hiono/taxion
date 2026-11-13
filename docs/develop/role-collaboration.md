# Job 中心の Role 協調設計

Status: Adopted design direction  
Decision: [ADR-0077](adr/0077-job-centered-role-collaboration.md)  
Tracking: [Issue #283](https://github.com/hiono/taxion/issues/283)

## 目的

Taxion は、完全な依頼情報を要求する検証装置ではない。不完全な情報を受け取った担当者同士が、判断・相談・補完を行い、Job を完了へ進めるための協調環境である。

既存実装の構造や protocol を維持することは目的としない。従来の開発フローが接続拒否により機能不全であった以上、既存実装を基準に不足箇所だけを継ぎ足す方法は採用しない。

## 中心概念

- **Job**: 利用者の目的、制約、受入れ条件、承認範囲、成果と協調履歴を保持する継続単位。
- **Role**: Planner、Coder、Reviewer など、Job の一部を判断・実行する担当者。常駐サービスである必要はない。
- **Execution**: Role が Job を進めるための一回の実行。session や provider の都合を Job の寿命と同一視しない。
- **Collaboration**: Role 間の依頼、質問、回答、判断、部分成果、引継ぎ。固定質問 schema ではなく自然言語を基本とする。
- **Controller**: Job の同一性、実行状態、出所、権限、承認、安全性、予算、期限、競合、復旧を管理する。

## 協調モデル

1. Job は不完全な情報でも受信 Role に渡す。
2. 受信 Role は既存 Context と成果物を読み、自分の専門領域で判断する。
3. 判断可能な範囲は作業を進める。必要に応じて仮定と不確実性を記録する。
4. 判断に不可欠な情報だけを、適切な相手 Role または人へ尋ねる。
5. 質問・回答は同じ Job に記録し、元の担当がその回答を参照して続行する。
6. Role は必要に応じて別 Role へ作業を委ねる。固定された直列順序を前提としない。
7. Reviewer は独立した判断を行い、Coder の自己申告や session に依存しない。
8. Job の完了は、成果・検証証拠・受入れ条件・承認範囲を照合して判断する。

## Job の承認・scope・成果物

Job の承認は JobID に結び付く独立した権限記録とし、Task approval や会話履歴から導出しない。

- 承認対象は Job の immutable revision とする。依頼、目的、制約、受入れ条件、明示的な実行 scope、許可操作、repository/resource 境界、budget 上限、deadline、安全制約、有効な設定参照を一つの proposal hash に束縛する。
- 承認 basis が変わった場合は revision を更新し、新しい明示承認を要求する。旧 approval を書き換えたり、自動的に再利用したりしない。
- Task は Job scope の内側で作業を分解する。Task の scope・budget・deadline は Job 承認範囲を狭めることはできるが、拡張できない。
- RoleExecution の作成時だけでなく、各 privileged Runtime 操作の直前に Job approval、Task の絞り込み、Role policy、Runtime config、残予算を再検証する。
- 旧 Task approval bundle、authorization decision、packet receipt、dispatch audit は旧 Task の履歴として保存する。新 Job の承認へ移行・継承しない。
- Jobless legacy Task は変更しない。そこから派生する新 Job は独立して承認し、旧 Task は provenance として参照する。

Job approval の scope は構造化して保持する。repository allowlist と各 repository の file-access ceiling、Controller が解釈する operation allowlist、aggregate budget / concurrency ceiling、absolute deadline、安全制約、secret を含まない configuration reference を proposal hash に含める。Task の Scope / OutOfScope は作業意図の記述であり、権限として解釈しない。Task は repository・file access・operation・budget・deadline を Job の許可範囲内に狭めることだけができる。比較不能または未知の operation は fail-closed とする。

Job は成果物と検証証拠の append-only index を保持する。参照は Job、Task（該当する場合）、RoleExecution、repository/resource、immutable content identity、producer、時刻を特定する。Role は成果・証拠を提案できるが、自身の成果を受入れ済みと判定できない。受入れ判定は独立 Reviewer または指定 verifier が記録する。Job 完了には必須 acceptance condition を裏付ける provenance-valid な証拠が必要である。

## Controller が拒否・停止する境界

意味的な情報不足、任意 metadata の欠落、表現差、未確定の実装詳細だけでは拒否しない。

次の条件は引き続き fail-closed とする。

- 認証、権限、秘密情報、禁止操作の違反または不明確さ
- 人間承認が必要な操作、承認範囲・対象・予算・期限の実質的変更
- Job、実行、成果物の同一性や出所を安全に確認できない場合
- データ破損、二重実行、競合実行、不可逆な副作用の危険
- 実行状態が不明で安全な再開・再送を判断できない場合

Role の回答や仮定によって、これらの制約を解除してはならない。

## 既存実装の扱い

既存の ReviewFlow、Clarification、Dispatch、role packet、固定 schema は設計上の前提ではない。再利用は新しい設計を満たす場合に限る。満たさない場合は、互換性維持のための追加分岐を作らず、責務とデータモデルを整理して置き換える。

新しい抽象化は、実際の Job 協調に必要な範囲に限定する。汎用 scheduler、常駐 Role、質問専用エンジンを先回りして作らない。

## 実装の到達条件

- Planner、Coder、Reviewer が同一 Job の共有 Context を使って独立して実行できる。
- Role が必要な場合に他 Role へ自然言語で依頼・質問できる。
- 回答・部分成果が元 Job に戻り、担当 Role が続行できる。
- 不足情報だけでは handoff が拒否されない。
- 権限・承認・予算・期限・同一性・安全性の停止条件は維持される。
- 実モデルで、仮定による続行と質問後の続行の両方を確認する。
- 合成テスト、保存された質問 receipt、Dispatch の発行だけを実モデル協調成功と扱わない。
