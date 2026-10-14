# 5. 契約の安定まで Go を使い言語分割を延期する

Date: 2026-10-09

## Status

Accepted

## Context

server・CLI・ACP・Web の構成では、特に server core の Rust など複数言語が候補となる。しかし最初の Runtime 候補の SDK が Go のため、Go が管理機能の価値を最も早く検証できる。

## Decision

Foundation から Platform までは Go で実装する。Rust の `taxion-server`・`taxion-acp` と TS の `taxion-web` への分割は、Future 段階かつ core 契約が安定した場合だけ再検討する。

## Consequences

- 単一 toolchain で Adapter 段階を早く評価できる。
- FFI・build の複雑さを早期に増やさない。
- Go の制約が現れても、SDK 非依存 core（ADR-0003）で書き直す範囲を限定できる。
