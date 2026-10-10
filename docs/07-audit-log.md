# 07 — Audit log (M5-2, M8-D adds `who`, M8-E adds `reason` + 429)

The serve API keeps an append-only audit log: the "camera" next to the M5-1
"key". Default location is next to the ledger (`.taxion/audit.jsonl`);
override with `serve --audit PATH`, disable with `serve --audit ""`.

## What is recorded

One JSON object per line:

```json
{"ts":"2026-10-10T...Z","method":"POST","path":"/api/tasks","repo":"demo","who":"hiono","status":201,"authed":true}
```

| field    | content |
| -------- | ------- |
| `ts`     | UTC, RFC3339Nano — when the response was sent |
| `method` | HTTP method |
| `path`   | request path only (no query string) |
| `repo`   | explicit repo identity from the POST body, if present |
| `who`    | self-asserted operator label from `X-Operator`, if present (M8-D) |
| `status` | response code — including guard denials (400/404/409) and lockout (429) |
| `authed` | whether this request presented the valid token |
| `reason` | why a denial happened — `"lockout"` on 429s only (M8-E) |

Logged: every mutation attempt (POST/PUT/PATCH/DELETE, any outcome) and
every auth denial (401/403, any method) plus every rate-limit lockout
(429, any method). Successful reads are noise and are
not logged — a read-heavy day leaves the log quiet.

## Brute-force lockout (M8-E)

Fixed policy, no flags: 5 failed logins from one IP within 1 minute locks
that IP out for 5 minutes. Locked-out requests answer 429 (with
`"reason":"lockout"` in the log) — even with the right token, until the
lockout expires. One success resets the IP's counter, so typos do not
accumulate. The key is the connection's RemoteAddr host; no
`X-Forwarded-For` is trusted.

## What is NEVER recorded

Tokens, `Authorization` headers, request bodies (task titles, SHAs), and
query strings. The log proves *what was done*, never *with which secret*.

`who` is a claim, not a proof: any caller holding the shared token can send
any `X-Operator` value. It distinguishes operators by declaration so a
two-person day is readable — it does not authenticate them. Per-operator
identity is the deferred IdP work (ADR-0016).

## Fail-closed behavior

- `serve` probes the audit destination at startup (`CheckAudit`) and refuses
  to start when it is unwritable — a camera that cannot record stops the
  opening, it does not wave traffic through.
- A mid-run write failure is reported on server stderr. Operators must treat
  a missing line as a gap, never as quiet success.

## Retention policy (local-first)

- The log is append-only: Taxion never rotates, truncates, or deletes it.
  Deleting audit history would destroy the evidence the log exists to keep.
- File mode is 0600 on creation. Back it up alongside the ledger file.
- Operators rotate externally when it grows (e.g. move aside + `logrotate`
  with `copytruncate`, or monthly archival). Rotation is an operator act,
  recorded outside Taxion — same as ledger backups.
- Keep retention aligned with the ledger: as long as a ledger generation is
  kept, its audit lines are kept.
