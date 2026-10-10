# 16. serve modes loopback/remote with token opt-in; IdP delegation deferred

Date: 2026-10-10

## Status

Accepted

## Context

M5 added `taxion serve` token auth (ADR-0015), but mode selection was implicit: `--addr
0.0.0.0:3938` without `--token` was refused at startup (good), yet the
refusal depended on the interaction between flags. The interface did not state that
"remote serving is a deliberate, named choice". A non-technical operator
copying a LAN example could land on plain-HTTP bearer auth without
understanding the exposure, and a security review would rightly call the
posture ambiguous: is this tool loopback-first or LAN-ready?

Evaluated alternatives (issue #25 discussion): audit operator-ID (D), 401
rate limit + lockout (E), hash-chained audit (G), delegating auth to OS/IdP
reverse proxy (H). H is the architecturally ideal end state but premature:
validated demand is single-user-over-SSH-tunnel; multi-user LAN demand is
hypothetical. H would add identity-provider integration and maintenance
requirements while making single-user setup more complex.

## Decision

F-lite: two named modes on `taxion serve`.

- `loopback` (default): any non-loopback bind is refused, token or not.
  The tool cannot be exposed by accident.
- `remote`: requires `--token` (the flag is the opt-in signature).
  Plain-HTTP bearer auth is explicitly a trusted-network-only posture
  (same residual risks as ADR-0015: single shared token, no TLS,
  unaudited per-user identity).

Unknown modes are rejected at startup. Logic is a pure function
(`checkServeMode`) covered by unit tests.

H (IdP/OS delegation) is deferred with a numeric trigger: **3 external
multi-user requests** (issues or discussions from non-maintainer users
asking for shared LAN use). On the third, this ADR is reopened.

## Consequences

- Operators get a loopback-only default and an explicit remote mode
  requiring a token; docs/06 documents SSH forwarding before remote mode.
- The expert-visible residual risks (single token, no TLS, no rate limit)
  are unchanged but now sit behind an explicit opt-in instead of a flag
  interaction. The mode switch clarifies configuration without changing
  the authentication mechanism.
- D/E/G remain open as independent follow-ups; none is a precondition.

## Implementation status (M8)

The consequences above describe the initial F-lite implementation.
The D/E/G follow-ups are now implemented: self-asserted `who`, IP-based
failure counting and lockout, and audit hash-chain verification. M8
acceptance is recorded in Issues #41–#43; current fields, fixed thresholds,
and verification limits belong in [the audit reference](../07-audit-log.md).
The original "no rate limit" statement no longer describes current behavior.
Shared-token identity, lack of built-in TLS, and deferred IdP delegation
remain unchanged.
