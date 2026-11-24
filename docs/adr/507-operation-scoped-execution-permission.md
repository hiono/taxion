# ADR-507: 実行権限をoperation単位に限定し、観測・適合・登録と分離する

Date: 2026-11-23

## Status

Accepted

## Context

Jobの承認、対象の観測、契約への適合評価、次工程への登録可否は異なる判断です。これらを一つの許可状態へまとめると、観測や受入れの成功が外部副作用の実行権限へ誤って昇格します。

## Decision

**Execution Permissionは、現在有効なJobApprovalと承認済みscopeに束縛した単一operationの実行許可とします。**

Execution Permissionはeffect、resource、Provider identityおよび実行要求に対して発行し、副作用の直前に再検証します。Observation、Conformance、Admissionはそれぞれ独立した事実・評価として扱い、いずれもExecution Permissionを代替しません。禁止された副作用と、人間承認を要する操作は通常のExecution Permissionで許可しません。

## Consequences

Jobの受入れ判断と外部副作用の認可を分離し、scope・承認・実行要求の変更による権限流用を防止します。具体的な評価入力、保存形式、再検証手順は製品仕様と詳細設計で定義します。

## Relationship to other ADRs

ADR-500はAuthorization & Securityの基本方針、ADR-504はJobApproval、ADR-505およびADR-506はread-only grant、ADR-201はJobAggregateの保存境界を定義します。
