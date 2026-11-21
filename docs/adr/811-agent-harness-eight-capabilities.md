# ADR-811: Agent Harnessの8要素を製品能力要求として維持する

Date: 2026-11-20

## Status

Accepted

## Context

Taxionはハーネス自体の再実装を目的としませんが、隔離、並列委譲、継続、回復などの能力を製品要求から失ってはなりません。Taxion内部とSDK・CLI・OS・Provider・外部実行環境の責務を区別しながら、システム全体の充足を評価する基準が必要です。

## Decision

H1〜H8を製品能力要求として維持し、仕様・実装・検証証拠の対応に使用します。

| 要素 | 必須能力 |
|---|---|
| H1 ループ | ModelへのContext提供、Tool実行、結果の次Stepへの反映、Task終了判定 |
| H2 ToolとSandbox | 必要なTool利用、Taskごとの独立Computer、Sandboxによる被害封じ込め |
| H3 Sub-agent | Side Taskの並行委譲、独立Context、Main Agentへの要約返却 |
| H4 Context管理 | TurnごとのContext選択、Skill / Fileの必要時ロード、履歴圧縮 |
| H5 Memoryと再開 | 進捗・履歴の永続化、新Sessionからの参照、同じJobの再開 |
| H6 Guardrails | 高リスク操作承認、認証情報保護、Prompt Injection耐性 |
| H7 Verification | Summaryだけに依存しない検証、独立評価、必須Check後の完了 |
| H8 Observability | Model / Tool Call追跡、失敗反復検知、別Approachへの誘導 |

Taxion内部での再実装を一律に要求しません。SDK、CLI、OS、Provider、外部実行環境へ委譲する場合は、対応構成と能力を確認する証拠を保持します。

Computer隔離はWorktree、Session、Processの分離とは別の能力として評価します。Task間の独立Context、provenance、予算・期限を維持します。

Context圧縮と再開ではJobAggregateを正本とし、Job Contract、承認、重要事実、未解決状態を保持します。GuardrailsはJobApprovalと実行基盤のSandbox / Credential境界で強制します。Verificationは成果identity、受入条件、独立Verifier、再現可能なEvidenceに基づきます。Observabilityは追跡できない呼出しやevent欠落を明示し、停滞検知から権限・予算を拡張しません。

## Consequences

H1〜H8の充足状況をProvider / Runtimeごとに評価し、未対応・未検証の能力を明示します。本ADRは8要素の実装完了を宣言しません。

## Relationship to other ADRs

ADR-201はJobと再開情報、ADR-300は内部協調、ADR-408はWorktree、ADR-500は権限・Guardrails、ADR-800は検証証拠と製品受入れを定義します。
