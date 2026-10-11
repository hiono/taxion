# 21. 永続的な Task Graph と Dispatch の変更管理

Date: 2026-10-10

## Status

Accepted

[ADR-0020](0020-layered-memory-and-role-sessions-for-orchestration.md)
の情報保持と実行契約を具体化する。以下は採択した設計であり、
現在の Ledger schema と API の状態は、参照仕様と実装記録に記載する。

## Context

Task Graph と Dispatch に保留した懸念、以前の許可、実行中の指示を保存し、
repository、依存作業、必要な証拠、指示の変更関係を次の担当へ渡す。

元の会話では、保守性の懸念に解決策が未決でも Task ID を付けて記録するよう、
ユーザーが運用を修正した。scope out は実行範囲や優先順位を決める判断として扱い、
Task の状態と再開条件を保持する。

要件は、元の会話で示されたユーザーの判断と、その後に追加された運用規則から定める。
公開文書には一般化した決定を記録し、非公開情報は承認された範囲で管理する。

| 要件の変化 | 採択する内容 |
| --- | --- |
| scope out した懸念に Task ID を付けず、追跡から失う扱いへの指摘 | 実行許可や解決策が未決でも Task ID を付け、判断・理由と再開条件を残す |
| 改訂された運用規則で Task schema と状態を定義 | 作業、依存関係、権限、acceptance、証拠を構造化する |
| 作業を外部から確認する追加規則 | 必要な Issue または既存 Issue 内の Task 記録を実行前に用意する |
| 後続の指示で以前の指示が不明になるという追加規則 | Task ID と Dispatch ID を分け、変更関係と実行状況を永続化する |
| 古いレビュー結果が再提示された実例 | 結果を Dispatch・対象 SHA と照合し、状態を巻き戻さない |

## Decision

### Task は成果物と判断を保持する単位にする

すべての実行可能な作業と、確認された重要な懸念を Task Graph に登録する。
Task ID は作業ごとに一意とし、通常の retry では同じ ID を使う。
repository、実質的な scope、成果物、独立したレビューの単位が変わる場合は
別 Task として関連づける。置き換えた Task も履歴に残す。

採択する Task の情報は次のとおりとする。
保存上の型や wire のキーは、移行設計で別途定義する。

| 区分 | 情報 |
| --- | --- |
| 識別・出典 | Task ID、Task Type、Title、canonical Repository、Created From |
| 外部記録 | Issue、PR などの変更提案の参照 |
| 関係 | Parent Task ID、Child Task IDs、Sibling Task IDs、Depends On、Blocks、Supersedes、Superseded By |
| 担当・進行 | Owner、Reviewer、Status、Priority、Next Action |
| 実行条件 | Objective、Scope、Out of Scope、Acceptance Criteria、Required Evidence、Exit Condition |
| 対象・リスク | Branch、Baseline SHA、Target SHA、Risk、Exception IDs |

元の運用規則の Merge Request は、provider ごとの変更提案の参照として扱う。
GitHub では PR に対応づける。worktree、Session、指示の版と権限は
Dispatch に保持し、Task の対象と照合する。

Task Type は INVESTIGATION、DESIGN、IMPLEMENTATION、REVIEW、
EVIDENCE_COLLECTION を区別する。実装は IMPLEMENTATION の scope と Dispatch authorization に従って行う。
原因が不明な懸念は INVESTIGATION、選択肢が必要な懸念は DESIGN として残す。
保留、親の scope 外、却下、cancel はそれぞれ Task の状態と判断理由に記録する。

Task を追加する前に、完了・保留・却下・cancel・置換済みも含めて同等の作業を探す。
既存 Task を使う場合は証拠と判断の履歴を保持する。
Created From から起点となる依頼、懸念、指摘を参照できるようにする。

### scope out による Task lost を防ぐ

確認された重要な懸念に Task ID がない状態は、Task Graph の整合性エラーとする。
scope out や後日の検討を決めた懸念は、Task ID、出典、状態、再開条件とともに Task Graph に保存する。
懸念の保存と実装権限を分け、原因や解決策が未決の段階では INVESTIGATION または DESIGN として登録する。
Dispatch が許可する操作は Task の実装権限と scope から構成する。

scope out する際は、既存 Task との重複を確認したうえで Task ID、出典、
対象外とした範囲と理由、現在の状態、判断待ちや依存、次の行動と再開条件を残す。
親の scope 外の Task は適切な親へ関連づけるか、独立した作業として保持する。
Critical Path の外にある Task も検索と後続の計画で参照できる。
却下と cancel は明示的な判断・理由を付け、保留とは別の状態で記録する。

検証では、懸念の提示、scope out、別の作業の進行、Session の再作成または再起動、
後続の計画作成を順に通す。同じ Task ID と出典、保留理由、再開条件を復元でき、
ユーザーが同じ懸念を再説明せずに追跡・再評価できることを確認する。
Task の一覧表示だけでなく、未付与の検出と後続計画での利用も検証対象にする。

### 依存関係と完了条件を検査する

Depends On は Task ID と必要な状態を組にする。Sibling 関係は階層を示し、依存と実行順序は Depends On に記録する。
Blocks は依存関係から整合するように管理し、状態変更時に後続 Task を再評価する。
実行前に依存グラフの循環と必須状態を検査し、必須条件を満たす作業を実行する。
未決の判断がある場合は、その判断を保ったまま行える準備作業を明示的に許可する。

Task の状態は DRAFT、READY、ACTIVE、WAITING_FOR_EVIDENCE、
WAITING_FOR_REVIEW、REQUEST_CHANGES、BLOCKED、HUMAN_DECISION_REQUIRED、
APPROVED、COMPLETED、REJECTED、CANCELLED、SUPERSEDED とする。
APPROVED は受け入れ条件と review proof の承認を示し、COMPLETED は必要な証拠と子 Task を含む完了を示す。
COMPLETED には実施、必要な証拠、独立レビュー、acceptance の充足または
該当する明示承認済みの例外、依存 Task の再評価を必要とする。

親 Task の完了には必須の子 Task の結果を照合する。
子が REJECTED・CANCELLED・SUPERSEDED の場合、親の acceptance は有効な判断、
代替 Task の結果、または承認済みの waiver で満たす。

blocker は TECHNICAL、PROCESS、HUMAN_DECISION、SCOPE_BOUNDARY、
GUIDANCE_VIOLATION、EVIDENCE_GAP、DEPENDENCY_BLOCKED を区別し、
原因と解消条件を保持する。複数の blocker は ID で識別する。
権限を要する作業は、controller が認めた scope と承認経路に従って実行する。

### 必要な外部記録を実行前に用意する

独立した成果物、保留作業、複数回の実行、独立レビュー、重要なリスク、
Human の判断を必要とする作業は、Issue で追跡できるようにする。
独立した lifecycle や acceptance があれば専用 Issue を使う。
小さく、親と同じ lifecycle で、別の承認や保留を必要としない作業だけは、
既存 Issue 内の Task ID 付き記録を使える。

Issue の値は専用参照、既存 Issue からの継承、登録 Task を示す PENDING、
または理由付きの NOT_REQUIRED に分類する。
実行可能な Task は、専用参照、継承参照、PENDING、理由付き NOT_REQUIRED のいずれかを明示する。
NOT_REQUIRED は永続的な後続作業も独立レビューも不要な場合に限る。
必要な Issue がない場合は登録 Task を前提に置き、その完了後に実装へ進む。
実行前に作業記録を提示し、scope と acceptance を確認できる状態にする。

外部更新の主体と権限は controller が管理する。
Coder の外部更新は controller が明示した権限と scope に従う。

### Dispatch は一回の実行指示として管理する

Task ID は作業、Dispatch ID は特定の実行指示を識別する。
指示を改訂する場合は新しい Dispatch ID を作り、以前との関係を一つ明示する。

| Relationship | 意味 |
| --- | --- |
| NONE | 以前の Dispatch がない |
| CONTINUES | 以前の指示を変更せず、矛盾しない継続を記録する |
| AMENDS | 変更箇所、維持する箇所、理由を明示する |
| SUPERSEDES | 完全な指示で置き換え、以前の指示を履歴に残す |
| CANCELS | 代替の実行を発行せず、以前の指示を取り消す |
| SUSPENDS | 安全な停止点、停止の証拠、再開条件を指定する |
| RESUMES | 停止した作業を再開し、維持・変更する指示を明示する |
| INDEPENDENT | 別 Task の指示で、以前の指示を変更しない |

同じ Task の実行可能な active Dispatch は原則一つとする。
独立した並行作業は子 Task に分け、repository、branch、worktree、ファイル、
Issue・PR、依存関係、acceptance、資源の競合を検査する。
CONTINUES は同一作業の継続記録として使い、並行作業は独立した child Task に分ける。
新しい AMENDS が発行されるまで、以前の Dispatch を active 指示として保持する。

Dispatch には ID、Task ID、Repository、Recipient、Status、Relationship、
Related Dispatch ID、Instruction Summary、Issued Evidence、Acknowledged、
Execution Started、Execution Result、Last Updated By、Last Transition を保持する。
実行契約には worktree、SHA、指示の版、許可・禁止操作、必要な証拠も含める。
受信、開始、停止、結果取得を別の事実として扱う。

Human の質問・コメント・懸念は原則 NO_CHANGE とする。
明示的な変更要求、無効になった証拠、scope 違反、必須の制約違反などがなければ
active Dispatch を維持し、懸念は別 Task として保存する。
実行中または開始状況が不明な作業は状態を確認し、安全停止の証拠を得てから競合する作業を発行する。
完了した Dispatch は遡って取り消さず、是正・後続 Task を作る。

Dispatch の `SUSPENDED` result は停止 evidence と `resumeCondition` を保存し、
active 扱いで同じ Task の重複発行を防ぐ。Task は `BLOCKED` とし、
`nextAction`・`resumeCondition` に再開条件を記録する。

### 指示と返却結果を自己完結させる

最終的な指示の順序は次のとおりとし、Dispatch の識別と変更関係を先頭に含める。

```text
DISPATCH TO:
DISPATCH ID:
RELATIONSHIP:
RELATED DISPATCH ID:
TASK ID:
TASK TYPE:
TITLE:
REPOSITORY:
ISSUE:
PARENT TASK ID:
SIBLING TASK IDS:
DEPENDS ON:
BLOCKS:
OBJECTIVE:
DISPATCH EFFECT:
AUTHORIZED ACTIONS:
FORBIDDEN ACTIONS:
REQUIRED EVIDENCE:
EXIT CONDITION:
RETURN FORMAT:
```

受信者は最初の行で識別する。実装と独立レビューの受信者は Coder と Reviewer とし、
Planner の実行契約は別の採用判断で定義する。
baseline、対象 SHA、worktree は必要な証拠と操作条件に明示し、
構造化した実行契約と表示する指示を一致させる。
置換指示は Task と Dispatch の保存済み snapshot から単独で理解できる形にする。

返却結果には Task・Dispatch・role・repository、status、開始・生成・検証対象の SHA、
変更ファイル、検証コマンドと exit code、証拠の参照、blocker、
要求する次の遷移を含める。Reviewer は verdict、findings、
acceptance ごとの判断、worktree の変更有無を返す。
runtime は最終応答を保存済み契約に照合し、Task・Dispatch の遷移は構造化された result と proof を検査する。
Git object と検証結果の実物は repository と test record で確認し、その根拠を result に関連づける。

## Consequences

会話の変更を状態遷移へ反映する前に、既存の Task と active Dispatch を確認する。
既知の判断を再質問する代わりに永続記録から復元し、
新しい判断が必要な箇所だけを Human に提示する。
このため、データの保持だけでなく、関係の整合性と遷移を検査する実装が必要になる。

設計の実装は [#47](https://github.com/hiono/taxion/issues/47)、
[#48](https://github.com/hiono/taxion/issues/48) で追跡し、
結果の照合と復旧は M12・M13 で検証する。
関連する Memory の保存は [#66](https://github.com/hiono/taxion/issues/66)、
保留 Task と判断を次の指示で再利用する検証は
[#67](https://github.com/hiono/taxion/issues/67) で追跡する。
既存の [Ledger schema](../05-ledger-schema.md) は現在の wire の参照として維持し、
拡張時には migration と互換性を定義する。
現在の Task・Dispatch 型は実装時にこの設計へ拡張し、更新した wire と migration を参照する。
