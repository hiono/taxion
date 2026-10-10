# 21. 永続的な Task Graph と Dispatch の変更管理

Date: 2026-10-10

## Status

Accepted

[ADR-0020](0020-layered-memory-and-role-sessions-for-orchestration.md)
の情報保持と実行契約を具体化する。以下は採択した設計であり、
現在の Ledger schema や実装済み API を表すものではない。

## Context

会話に作業を記載するだけでは、保留した懸念、以前の許可、実行中の指示が
次の担当へ引き継がれない。Task を記録しても、対象の repository、
依存する作業、必要な証拠、指示の変更関係が不明なら安全に実行できない。

元の会話では、保守性の懸念に対して「解決策が未決なので Task を作らない」
という扱いが提示され、ユーザーが、後で忘れて対応しなくなる扱いだと指摘した。
問題は実装を今すぐ行わないことではなく、確認された懸念に Task ID を付与せず、
追跡から外したことにある。この軌道修正から、scope out は実行範囲や優先順位の
判断であって、Task の保持を解除する判断ではない、という規則に至った。

要件の確認には、元の会話のユーザー発言と、その後に追加された規則を用いる。
初期のテンプレートや提案をそのまま最終仕様と扱わない。
公開文書には一般化した決定を記録し、非公開の会話本文、識別情報、
資料の参照先を転記しない。

| 要件の変化 | 採択する内容 |
| --- | --- |
| scope out した懸念に Task ID を付けず、追跡から失う扱いへの指摘 | 実行許可や解決策が未決でも Task ID を付け、判断・理由と再開条件を残す |
| 改訂された運用規則で Task schema と状態を定義 | 作業、依存関係、権限、acceptance、証拠を構造化する |
| 内部登録だけでは作業を確認できないという追加規則 | 必要な Issue または既存 Issue 内の Task 記録を実行前に用意する |
| 後続の指示で以前の指示が不明になるという追加規則 | Task ID と Dispatch ID を分け、変更関係と実行状況を永続化する |
| 古いレビュー結果が再提示された実例 | 結果を Dispatch・対象 SHA と照合し、状態を巻き戻さない |

## Decision

### Task は成果物と判断を保持する単位にする

すべての実行可能な作業と、確認された重要な懸念を Task Graph に登録する。
Task ID は不変で再利用しない。通常の retry は同じ Task ID を使い、
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
EVIDENCE_COLLECTION を区別する。調査や設計の許可だけで実装へ進めない。
原因が不明な懸念は INVESTIGATION、選択肢が必要な懸念は DESIGN として残す。
今は実行しないこと、親の scope 外であること、永久に却下すること、
cancel することを同じ扱いにしない。

Task を追加する前に、完了・保留・却下・cancel・置換済みも含めて同等の作業を探す。
既存 Task を使う場合は証拠と判断の履歴を保持する。
Created From から起点となる依頼、懸念、指摘を参照できるようにする。

### scope out による Task lost を防ぐ

確認された重要な懸念に Task ID がない状態は、Task Graph の整合性エラーとする。
「今回は対象外」「後で検討する」と会話や Memory に書くだけでは追跡を満たさない。
懸念の保存と実装の許可を分け、原因や解決策が未決でも調査・設計 Task を作成できる。
Task の作成は実装権限の付与を意味しない。

scope out する際は、既存 Task との重複を確認したうえで Task ID、出典、
対象外とした範囲と理由、現在の状態、判断待ちや依存、次の行動と再開条件を残す。
親の scope 外なら適切な親へ関連づけるか独立した作業として保持し、
現在の Critical Path から外れても検索と後続の計画の対象から除外しない。
却下や cancel は明示的な判断と理由を必要とし、単なる保留から推測しない。

検証では、懸念の提示、scope out、別の作業の進行、Session の再作成または再起動、
後続の計画作成を順に通す。同じ Task ID と出典、保留理由、再開条件を復元でき、
ユーザーが同じ懸念を再説明せずに追跡・再評価できることを確認する。
Task の一覧表示だけでなく、未付与の検出と後続計画での利用も検証対象にする。

### 依存関係と完了条件を検査する

Depends On は Task ID と必要な状態を組にする。兄弟関係だけでは依存や順序を作らない。
Blocks は依存関係から整合するように管理し、状態変更時に後続 Task を再評価する。
循環依存や未充足の必須依存を持つ作業は実行しない。
安全な準備作業は、未決の判断を先取りしない範囲で明示的に許可する。

Task の状態は DRAFT、READY、ACTIVE、WAITING_FOR_EVIDENCE、
WAITING_FOR_REVIEW、REQUEST_CHANGES、BLOCKED、HUMAN_DECISION_REQUIRED、
APPROVED、COMPLETED、REJECTED、CANCELLED、SUPERSEDED とする。
APPROVED と COMPLETED は別であり、プロセスの終了だけで完了にしない。
COMPLETED には実施、必要な証拠、独立レビュー、acceptance の充足または
該当する明示承認済みの例外、依存 Task の再評価を必要とする。

親 Task の完了には必須の子 Task の結果を照合する。
子の REJECTED・CANCELLED・SUPERSEDED を無条件の達成とみなさず、
有効な判断、代替 Task の結果、または承認された免除によって
親の acceptance が満たされることを確認する。

blocker は TECHNICAL、PROCESS、HUMAN_DECISION、SCOPE_BOUNDARY、
GUIDANCE_VIOLATION、EVIDENCE_GAP、DEPENDENCY_BLOCKED を区別し、
原因と解消条件を保持する。複数の blocker は ID で識別する。
権限がないことを、権限を迂回・取得するための作業に置き換えない。

### 必要な外部記録を実行前に用意する

独立した成果物、保留作業、複数回の実行、独立レビュー、重要なリスク、
Human の判断を必要とする作業は、Issue で追跡できるようにする。
独立した lifecycle や acceptance があれば専用 Issue を使う。
小さく、親と同じ lifecycle で、別の承認や保留を必要としない作業だけは、
既存 Issue 内の Task ID 付き記録を使える。

Issue の値は専用参照、既存 Issue からの継承、登録 Task を示す PENDING、
または理由付きの NOT_REQUIRED に分類する。
初期の「値がなければ NONE」という一般規則を、実行可能な Task の Issue には適用しない。
NOT_REQUIRED は永続的な後続作業も独立レビューも不要な場合に限る。
必要な Issue が未登録なら登録 Task を前提に置き、本作業を開始しない。
実装後の報告だけでは実行前の確認機会を満たさない。

外部更新の主体と権限は controller が管理する。
元の指示形式にあった Issue 登録担当の表記を、
Coder に無制限の外部更新権限を与える理由にしない。

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
CONTINUES を重複実行の許可には使わない。
AMENDS の新しい指示が正常に発行されるまでは、以前の指示を置換済みにしない。

Dispatch には ID、Task ID、Repository、Recipient、Status、Relationship、
Related Dispatch ID、Instruction Summary、Issued Evidence、Acknowledged、
Execution Started、Execution Result、Last Updated By、Last Transition を保持する。
実行契約には worktree、SHA、指示の版、許可・禁止操作、必要な証拠も含める。
受信、開始、停止、結果取得を別の事実として扱う。

Human の質問・コメント・懸念は原則 NO_CHANGE とする。
明示的な変更要求、無効になった証拠、scope 違反、必須の制約違反などがなければ
active Dispatch を維持し、懸念は別 Task として保存する。
実行中または開始状況が不明な作業を、即時 cancel 済みとは扱わない。
必要な場合は安全な停止の証拠を確認してから競合する作業を発行する。
完了した Dispatch は遡って取り消さず、是正・後続 Task を作る。

後続の停止規則には Task の SUSPENDED という表記もあるが、
上記の Task 状態一覧には含まれない。
本設計では停止中の Dispatch を区別し、Task は BLOCKED と停止・再開条件で表す。
wire の実装時に無断で別の Task 状態を追加しない。

### 指示と返却結果を自己完結させる

最終的な指示の順序は次のとおりとする。初期形式より後に追加された
Dispatch の識別・変更関係を省略しない。

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
置換指示を理解するために過去の会話を組み合わせる必要をなくす。

返却結果には Task・Dispatch・role・repository、status、開始・生成・検証対象の SHA、
変更ファイル、検証コマンドと exit code、証拠の参照、blocker、
要求する次の遷移を含める。Reviewer は verdict、findings、
acceptance ごとの判断、worktree の変更有無を返す。
controller が Git と保存済み契約に照合し、古い結果、途中出力、
自己申告だけの承認を現在の完了へ変換しない。

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
既存の小さい Task・Dispatch 型を、この設計に対応済みとは扱わない。
