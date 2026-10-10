# 07 — Audit log (M5-2, M8-D adds `who`, M8-E adds `reason` + 429, M8-G adds `prev` chain)

The serve API keeps an append-only audit log of mutation attempts and
access denials. Default location is next to the ledger (`.taxion/audit.jsonl`);
override with `serve --audit PATH`, disable with `serve --audit ""`.

## What is recorded

One JSON object per line:

```json
{"ts":"2026-10-10T...Z","method":"POST","path":"/api/tasks","repo":"demo","who":"operator-1","status":201,"authed":true}
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
| `prev`   | hex SHA-256 of the exact previous line bytes; omitted on the first line (M8-G) |

Logged: every mutation attempt (POST/PUT/PATCH/DELETE, any outcome) and
every auth denial (401/403, any method) plus every rate-limit lockout
(429, any method). Successful reads are not logged.

## Brute-force lockout (M8-E)

Fixed policy, no flags: 5 failed logins from one IP within 1 minute locks
that IP out for 5 minutes. Locked-out requests answer 429 (with
`"reason":"lockout"` in the log) — even with the right token, until the
lockout expires. One success resets the IP's counter, so typos do not
accumulate. The key is the connection's RemoteAddr host; no
`X-Forwarded-For` is trusted.

## What is NEVER recorded

Tokens, `Authorization` headers, request bodies (task titles, SHAs), and
query strings. The log records the operation and result without recording
the credential used.

`who` is a claim, not a proof: any caller holding the shared token can send
any `X-Operator` value. It labels callers by self-declaration but does not
authenticate their identities. Per-operator
identity is the deferred IdP work (ADR-0016).

## Fail-closed behavior

- `serve` probes the audit destination at startup (`CheckAudit`) and refuses
  to start when it is unwritable.
- A mid-run write failure is reported on server stderr. Operators must treat
  a missing line as an audit gap.

## Tamper-evidence hash chain (M8-G)

Every line links to the previous one: `prev` is the hex SHA-256 of the
exact previous line bytes. The first line omits `prev` (genesis). A
restarted server continues the chain from the existing tail — it never
forks a second genesis.

Verify any time:

```sh
taxion audit verify [--audit PATH]   # default: next to the ledger
```

Exit 0 prints `chain OK`; exit 1 names the first broken line — treat every
line from there on as untrusted and investigate before continuing to append.

What the chain catches: modification, insertion, deletion, and reordering
of covered lines. What it does NOT catch: silent tail truncation (a cut
tail is still a valid shorter chain) — watch line counts externally, and
back the file up with the ledger.

Old-line compat: lines without `prev` are "legacy" and valid only as a
leading run (pre-M8-G logs keep verifying after upgrade; the first chained
line links onto the last legacy line). A legacy line after a chained line
is a gap and fails verification. Rotation starts a new genesis — keep the
archived file so its prefix stays verifiable.

## Retention policy (local-first)

- The log is append-only: Taxion never rotates, truncates, or deletes it.
  Deleting audit history would destroy the evidence the log exists to keep.
- File mode is 0600 on creation. Back it up alongside the ledger file.
- Operators rotate externally when it grows (e.g. move aside + `logrotate`
  with `copytruncate`, or monthly archival). Rotation is an operator act,
  recorded outside Taxion — same as ledger backups.
- Keep retention aligned with the ledger: as long as a ledger generation is
  kept, its audit lines are kept.
