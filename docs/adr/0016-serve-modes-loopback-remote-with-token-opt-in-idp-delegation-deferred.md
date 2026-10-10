# 16. serve modes loopback/remote with token opt-in; IdP delegation deferred

Date: 2026-10-10

## Status

Accepted

## Context

M5 gave `taxion serve` token auth (ADR-0015) but left a trap: `--addr
0.0.0.0:3938` without `--token` was refused at startup (good), yet the
refusal lived in an obscure flag interaction — nothing in the interface said
"remote serving is a deliberate, named choice". A non-technical operator
copying a LAN example could land on plain-HTTP bearer auth without
understanding the exposure, and a security review would rightly call the
posture ambiguous: is this tool loopback-first or LAN-ready?

Evaluated alternatives (issue #25 discussion): audit operator-ID (D), 401
rate limit + lockout (E), hash-chained audit (G), delegating auth to OS/IdP
reverse proxy (H). H is the architecturally ideal end state but premature:
validated demand is single-user-over-SSH-tunnel; multi-user LAN demand is
hypothetical, and H would saddle a single maintainer with identity-provider
promises while complicating the non-technical path.

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

- Non-technical operators get one safe default and one scary-named,
  token-mandatory option; docs/06 teaches tunnel-first, mode-second.
- The expert-visible residual risks (single token, no TLS, no rate limit)
  are unchanged but now sit behind an explicit opt-in instead of a flag
  accident — honest posture, not a stronger lock.
- D/E/G remain open as independent follow-ups; none is a precondition.
