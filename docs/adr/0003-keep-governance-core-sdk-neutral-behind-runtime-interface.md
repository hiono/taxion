# 3. Keep governance core SDK-neutral behind Runtime interface

Date: 2026-10-09

## Status

Accepted

## Context

The first runtime candidate ships a Go SDK, which tempts direct use of SDK
types throughout the codebase. That would collapse Taxion into a Copilot SDK
wrapper and block future runtimes (ACP, other providers).

## Decision

`internal/core` depends only on the stdlib and on the minimal interface
(`internal/runtime`: `Start / Resume / Stop / Events`). Concrete adapters live
outside core as plugins. Enforced by the `core-purity` CI job (SDK-001).

## Consequences

- Core stays portable; swapping runtimes never touches governance logic.
- Adapter authors map foreign event/type models to neutral `{Type, Payload}`.
- Slight indirection cost; SDK features must prove themselves through the
  Adapter-phase evaluation before adoption.
