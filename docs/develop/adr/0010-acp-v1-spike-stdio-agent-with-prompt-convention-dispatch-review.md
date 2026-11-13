# 10. stdio の ACP v1 agent で Dispatch・review を試作する

Date: 2026-10-09

## Status

Accepted

## Context

Platform 段階では editor など agent client の tool から Taxion へ接続する手段が必要となる。ACP v1（agentclientprotocol.com）は改行区切り stdio 上の JSON-RPC 2.0 を使う公開標準で、initialize、session/new、session/prompt、session/cancel の順に操作し、session/update 通知と end_turn・cancelled の停止理由を持つ。

## Decision

`internal/acp` を試作し、Taxion を agent とする。`session/new` は Task を作り、cwd を明示 repository identity として（REP-001）、Coder adapter session を始める。`session/prompt` の通常文は Dispatch（DSP-001）、`review <sha>` は Evidence の対応付け（SHA-001）に使い、`session/cancel` は失敗時に処理を許可せず停止する。core は変えず、標準ライブラリだけを使う。LLM は接続せず、管理操作の receipt と計画更新を返す。

## Consequences

- ACP v1 の editor は `taxion acp` で Dispatch・review を操作できる。
- `review <sha>` の慣例による routing は試作段階で、release 前に実 turn engine または明示 method へ置き換える。
- v2 は草案のため v1 を維持し、v2 が安定したら再評価する。

## M15 の実装補足

上記の Ledger 操作だけの試作は、Runtime 実行について置き換えた。`session/new` は CLI 発行の新 Dispatch ID を要求し、ACP 自体は Task・Dispatch を作れない。`session/prompt` は共通 Runtime Controller で1 turn を実行し、`session/cancel` は Controller の safe stop を要求する。保存 Dispatch 契約、role 専用 system prompt、tool policy、設定、credit 上限、timeout が実行を制限する。ACP session ID は process 内だけで有効。再接続は保存済み実行中 Dispatch を指定し、prompt を再送せず既存 turn を観測する。その他の復旧には CLI runtime command を使う。
