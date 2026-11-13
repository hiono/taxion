# 7. 作業履歴を Issue・PR・Milestone、判断理由を ADR に記録する

Date: 2026-10-09

## Status

Accepted

## Context

分担を明示しないと、何をいつ実施したかという履歴と、なぜ決めたかという理由が、source comment、chat log、文書で混ざる。

## Decision

- source tree は現在の状態だけを記録する。
- GitHub Issue・PR・Milestone は作業履歴を記録する。ADR-0004 の段階別 Milestone を使い、roadmap の項目を追跡する Issue にする。
- `docs/develop/adr` は永続的な判断を記録する。`mise use cargo:adrs` で導入する `adrs` で管理し、動作に影響する変更は必ず ADR を参照する。

## Consequences

- review は Issue の履歴、ADR の理由、diff の状態を確認する。
- ADR は増え続ける。置換済み ADR は状態を更新して残し、削除しない。
- `mise.toml` で `adrs` を開発用必須 tool にする。
