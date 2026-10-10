# 15. token auth and audit complete the pre-public security model

Date: 2026-10-10

## Status

Accepted

## Context

M5 issues #22 (token auth) and #23 (audit trail) are implemented. Until M5,
every Taxion surface was loopback-only with no authentication (the ADR-0011
amendment forbade beyond-loopback serving without a new ADR). Public release
(#24) needs an explicit statement of what the security model now is — and
is not.

What M5 built:

- `taxion serve --token` / `TAXION_TOKEN`: Bearer gate on every route,
  constant-time compare, identical 401 for missing/wrong (no oracle).
  Untokened binding beyond loopback is refused at startup.
- `taxion gen-token`: 32-byte crypto-random hex, never logged.
- Web console forwards the token server-side only; the page never contains it.
- Append-only JSONL audit of every mutation attempt and denial
  (`{ts,method,path,repo,status,authed}`); no tokens, bodies, or queries;
  fail-fast startup check; Taxion never deletes — the operator rotates.
- Ledger wire format frozen (M2-3); history scanned clean for M5-3.

## Decision

Declare the pre-public model sufficient and lift the loopback-only rule
**for tokened serving only**:

1. `serve` with `--token` may bind beyond loopback (LAN, docker `-p`,
   SSH-forwarded remotes), with token authentication and audit logging.
2. Untokened serving stays loopback-only, enforced by startup refusal.
3. Plain-HTTP Bearer is accepted on trusted networks (loopback, SSH tunnel,
   VPN, docker bridge) — documented, not hidden. Public-internet exposure
   without TLS termination in front remains out of scope (a future ADR when
   someone needs it).

## Consequences

- The ADR-0011 amendment's beyond-loopback clause is satisfied by this ADR:
  tokened exposure is now explicitly authorized, no per-deployment ADR needed.
- SECURITY.md is updated to the M5 control set; private advisories stay the
  disclosure channel.
- Residual risks, owned openly: single shared token (no per-user identity —
  rotation by server restart is the revocation mechanism), no TLS in Taxion
  itself, audit log grows unbounded until the operator rotates it.
