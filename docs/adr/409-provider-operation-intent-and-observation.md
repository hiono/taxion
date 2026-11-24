# ADR-409: Provider operationの意図と観測をJob内で分離して管理する

Date: 2026-11-23

## Status

Accepted

## Context

Providerの実行状態をRoleExecutionやJobの状態だけで表すと、外部operationの受付、実行結果、停止、利用量の確定を区別できません。通信断や再起動で結果が不明な場合に、新しいoperationを発行すると二重実行や資源の早期解放につながります。

## Decision

**Provider operationは、JobAggregateに先行記録した意図と、そのidentityに束縛した追記専用の観測事実によって管理します。**

- Provider呼出しの前にTaxionが発行したoperation identityとintentを確定します。
- Providerの応答・event・result・usage・stopは、対応するoperation identityへ束縛した独立した事実として保存します。
- 不明な結果は不明のまま保持し、同じidentityによる照合なしに新しいoperationとして再送しません。
- Providerの停止、最終結果、利用量が照合されるまで、関連する実行資源の所有権を維持します。
- Task-bound Startは同一OperationIDの並行dispatchをProvider側で重複排除するため、E4 Start Idempotencyを必須とします。Start Lookup単独は既存operationの照合にのみ使用し、新規Startのdispatch根拠にしません。RuntimeはRequireIdempotency要求をcredential取得前に検証します。
- Stop dispatchはJobAggregate内のClaimとDispatchStartで直列化し、ClaimのJob revisionを単調増加するfencing tokenとしてProviderへ渡します。
- DispatchStart後の再起動復旧では、E4 Stop LookupのNOT_FOUNDとE4 Stop Dispatch Fencingの両方を要求します。ProviderはOperationID / SessionIDごとにfencing tokenを原子的に比較し、古いtokenを拒否し、同一operationの重複要求を一つのoperationへ収束させます。
- fencing capabilityが未検証の場合、DispatchStart済みClaimをNOT_FOUNDだけで解放せず、Providerの確定証拠が得られるまで保持します。Claimの経過時間による自動解放は行いません。
- Provider capabilityはProvider構成と検証証拠に基づいて扱い、実行権限の根拠として使用しません。

## Consequences

Providerの実行事実とTaxion内部のRole / Job状態を混同せず、再起動・再接続・引継ぎ時の二重実行と早期解放を防止します。capabilityの具体的な検証項目と操作手順はProvider Adapter要求仕様・詳細設計・テスト仕様で定義します。

## Relationship to other ADRs

ADR-400はExecution & Runtimeの基本方針、ADR-201はJobAggregateの保存境界、ADR-408はWorktree所有権、ADR-500およびADR-507は実行権限、ADR-801は検証証拠の水準を定義します。
