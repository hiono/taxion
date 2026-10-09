# 5. Stay on Go until contracts stabilize; defer language split

Date: 2026-10-09

## Status

Accepted

## Context

A server/CLI/ACP/Web platform suggests polyglot options (notably Rust for the
server core), but the first runtime candidate's SDK is Go, making Go the
fastest path to validate control-plane value.

## Decision

Implement Foundation → Platform in Go. Revisit a split
(`taxion-server` / `taxion-acp` in Rust, `taxion-web` in TS) only in the Future
phase and only if core contracts have stabilized.

## Consequences

- Single toolchain and fast Adapter-phase evaluation.
- No premature FFI/build complexity.
- If Go limits appear, the SDK-neutral core (ADR-0003) keeps a rewrite bounded.
