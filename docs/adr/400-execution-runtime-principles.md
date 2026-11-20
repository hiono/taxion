# ADR-400: Execution & Runtimeの基本設計方針

Date: 2026-11-21

## Status

Accepted

## Context

外部Agent / Providerの実行は、Jobの状態変更やWorktree等の資源操作を伴います。実行状態と外部実行の事実を分離し、権限・再試行・停止状態を一貫して扱う必要があります。

## Decision

**Execution & RuntimeはControllerが実行ライフサイクルを管理し、外部実行の意図と観測事実をJobへ結び付けます。**

- 外部Providerを呼び出す前に実行意図を記録し、結果・停止・利用量を対応するOperationへ結び付けます。
- 実行前と副作用の発生前に、Jobの承認範囲、対象、資源、予算、期限および禁止副作用を検証します。
- 実行環境の所有権と同時実行境界を明示し、停止や結果が不明な資源を推測で再利用しません。
- Provider capabilityはProvider / SDK / configurationごとに確認した証拠に基づいて扱います。
- Provider operationとその永続化はADR-201、WorktreeとRoleの実行引継ぎはADR-408、権限境界はADR-500で具体化します。

## Consequences

外部実行の状態とJobの状態を照合でき、再試行・停止・復旧時の二重実行や資源競合を抑止します。

## Relationship to other ADRs

ADR-201はProvider operationのJob内保存、ADR-408はWorktreeと実行所有権、ADR-500は実行権限と禁止副作用を定義します。
