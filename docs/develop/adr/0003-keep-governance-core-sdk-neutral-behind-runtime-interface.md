# 3. 管理 core を SDK 非依存の Runtime interface に分離する

Date: 2026-10-09

## Status

Accepted

## Context

最初の Runtime 候補は Go SDK を提供し、その型を code 全体で直接使いたくなる。しかし、それでは Taxion が Copilot SDK wrapper となり、将来の ACP・他 provider の Runtime を妨げる。

## Decision

`internal/core` は標準ライブラリと最小 interface（`internal/runtime` の `Start / Send / Resume / Stop / Events`）だけに依存する。具体 adapter は core 外の plugin とし、`core-purity` CI job で強制する（SDK-001）。

## Consequences

- core の移植性を保ち、Runtime を替えても管理 logic を変更しない。
- adapter 作者は外部の event・型を中立な `{Type, Payload}` に変換する。
- 間接参照の負担は少し増える。SDK 機能は採用前に Adapter 段階で評価する。
