# ADR-0077: Job 中心の Role 協調モデルへ再設計する

Date: 2026-11-13

## Status

Accepted

## Context

従来の実行モデルは、Planner・Coder・Reviewer を固定された工程と protocol に当てはめ、情報不足や受渡し条件の不一致を Controller 側の接続拒否へ結び付けていた。実際の開発フローでは接続拒否により協調が成立せず、機能不全となっていた。

既存の Clarification や Dispatch の不足箇所を補修し、既存 protocol を維持することを優先しても、Job を中心に担当者同士が判断・相談しながら仕事を進めるという目的には到達しない。

## Decision

Taxion の中心を固定工程や質問 protocol ではなく、Job を継続単位とする Role 協調へ置く。

### Job と Role

- Job は目的、制約、承認範囲、成果、検証証拠、協調履歴を保持する継続単位とする。
- Planner、Coder、Reviewer は Job を進める担当 Role とする。Role の実行回数、順序、session 寿命を Job の寿命と同一視しない。
- Role は Job の Context と成果を参照し、担当領域の専門判断を行う。
- 固定された Planner → Coder → Reviewer の直列順序を必須としない。必要な Role へ仕事を委ね、結果を同じ Job に戻せるようにする。

### 不完全な情報の扱い

- 情報不足、任意 metadata の欠落、自然言語の表現差だけを理由に handoff や Job を拒否しない。
- 受信 Role は既存 Context と成果物から判断し、可能な範囲を進める。
- 可逆で影響の小さい不足は、仮定と不確実性を必要に応じて記録して進めてよい。
- 判断に不可欠な情報だけを適切な相手 Role または人へ質問する。
- 回答、判断、部分成果は元の Job に結び付け、担当 Role がその情報を使って続行する。
- 質問の category、reason、answer format、reference list 等を一律の必須 schema としない。自然言語のやり取りを基本とする。

### Controller の責務

Controller は意味的な情報の完全性を判定して Role の判断を代行しない。Job の同一性、状態、出所、実行権限、承認、予算、期限、競合、データ整合性、安全性を管理する。

認証・権限・秘密情報・禁止操作、人間承認、承認範囲変更、予算・期限変更、二重実行、不可逆操作、実行状態不明などの安全境界は fail-closed とする。Role の回答や仮定によって解除しない。

### 既存実装と protocol

既存の ReviewFlow、Clarification、Dispatch、role packet、固定 schema は新設計の制約としない。新しい設計を満たす部分のみ再利用し、満たさない部分は変更または廃止する。互換性維持だけを理由に旧 protocol を残さない。

一方、必要性が実証されていない汎用 scheduler、常駐 Role、質問専用エンジンを先行導入しない。実際の Job 協調を成立させる最小の実行・保存モデルから構築する。

### Job authority and approval boundary

Job approval is the sole source of authority for work performed under a first-class Job. It is distinct from Job conversation, Task planning, and RoleExecution records.

- A Job approval binds an immutable Job revision: request, goal, constraints, acceptance conditions, explicit execution scope, allowed operations, repository/resource boundaries, budget ceilings, deadline, security restrictions, and effective configuration references.
- The approval record is keyed by JobID and records the approved revision/proposal hash, canonicalization version, approver identity/source/reason, and approval time. The record is append-only; a changed approval basis requires a new revision and explicit approval.
- A Task is an implementation decomposition, not an authority source. Its effective scope and permissions must be a subset of the currently approved Job scope. A Task may narrow scope, budget, or deadline, but may not widen them.
- Each RoleExecution is independently checked against the current Job approval and the Task's narrowed scope, Role policy, runtime configuration, and remaining budget immediately before every privileged Runtime operation. A previously created RoleExecution or Job message is not a grant.
- Task-level approvals may remain only as additional, narrower gates where a concrete operation requires them. They cannot replace, widen, or imply Job approval.
- Legacy Task approval bundles, authorization decisions, packet receipts, and dispatch audit records remain historical evidence for the legacy Task. They are not migrated into Job approval and cannot authorize a new Job or a new RoleExecution.
- A legacy Task without JobID remains unchanged. A new Job that supersedes work from that Task must receive its own approval; references to the legacy Task are provenance only.

The canonical Job approval proposal is a versioned value, not a digest of mutable Task records. Its explicit scope must be inspectable before confirmation. It must not contain resolved credentials; configuration is represented by approved selection and source references/digests.

### Job outputs and evidence

Job owns the durable index of accepted outputs and evidence references; the underlying bytes may remain in repository objects or existing artifact storage.

- An output reference identifies the Job, originating Task (if any), producing RoleExecution, repository/resource, immutable content identity (commit/tree/blob or equivalent), and creation time.
- Evidence references identify the claim or acceptance condition they support, evidence type, immutable content identity, producer, and verification status/result. Evidence records distinguish observed, passed, failed, and unverified states.
- A Role may propose outputs/evidence, but cannot mark its own output accepted or turn a message into verification evidence. Controller records provenance; an independent Reviewer or configured verifier records acceptance.
- Job completion requires all mandatory acceptance conditions to be satisfied by current, provenance-valid evidence, and no unresolved safety or execution-state uncertainty.
- Superseding or replacing an output appends a new reference and preserves prior history; it does not rewrite the provenance of prior results.

JobApproval, JobOutput, and JobEvidence are conceptual boundaries to implement. Exact storage structs and migration details are settled in the implementation plan before runtime authorization is changed.

## Alternatives considered

- 既存 Clarification の宛先と successor Dispatch だけを拡張する: 固定工程・固定 protocol に設計が拘束されるため却下。
- SDK の root / child attribution に依存する inbox を有効化する: provider 固有の event attribution が未確認であり、Job 協調の一般解にならないため却下。
- すべての Role を常駐させる汎用 scheduler を導入する: 現時点で必要性が実証されていないため却下。
- 不足情報を完全に構造化してから実行する: Taxion の目的に反するため却下。

## Consequences

- Issue #283 と関連 PR は、本 ADR を基準に目的・受入れ条件・実装を再定義する。
- Job の継続と Role の一回の実行を分離する。
- 質問・回答は独立した機能ではなく、Job 協調の一形態として扱う。
- 既存実装は移行対象であり、互換性維持を優先しない。
- 合成テストは構造と安全境界の確認に使い、実モデルでの協調成立とは区別する。

## Security and operational impact

Job の文脈を Role 間で共有する場合も、Role ごとの file access、tool policy、repository 権限、承認範囲を拡張しない。Reviewer には独立した実行環境を与える。会話本文を実行権限や承認として解釈しない。秘密情報の redaction と公開前調査は維持する。

## Compatibility

旧 Clarification / ReviewFlow protocol の互換性維持を目的としない。既存 workspace の読み込み・移行で必要な互換処理は、データ損失と安全性を避けるためのものに限定し、旧 protocol を新しい設計の中心へ残さない。

## Status

Accepted. Implementation is in progress; no end-to-end real-model acceptance is claimed.
