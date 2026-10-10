# 05 — Ledger wire format (FROZEN since M2)

The JSON shapes below are the frozen contract between the Go core/CLI/API
(`internal/core`, `internal/server`) and the TypeScript view (`web/`).
Frozen on 2026-10-10 at the close of milestone M2 (issues #9–#11).

## Change rule

Any addition, removal, or rename of a key requires ALL of:

1. an ADR superseding/amending this document,
2. an update to this document,
3. an update to `internal/core/wire_test.go` (CI pins the exact key sets),
4. a synchronized update of `web/src/types.ts`.

Rationale: the wire format churned as late as #7 (lowercase tags); freezing
it is the precondition for the M3/M4 work and for revisiting ADR-0012.

## Top-level ledger object

Keys (all present, arrays, insertion order significant):

| key           | element |
| ------------- | ------- |
| `tasks`       | Task |
| `dispatches`  | Dispatch |
| `evidence`    | Evidence |
| `checkpoints` | Checkpoint |

## Task

| key         | type   |
| ----------- | ------ |
| `id`        | string |
| `title`     | string |
| `repo`      | string |
| `createdAt` | string (RFC3339Nano, UTC) |

## Dispatch

| key         | type   |
| ----------- | ------ |
| `id`        | string |
| `taskId`    | string |
| `repo`      | string |
| `active`    | boolean |
| `createdAt` | string (RFC3339Nano, UTC) |

## Evidence

| key         | type   |
| ----------- | ------ |
| `sha`       | string (exact coder-output SHA, never `"latest"`) |
| `taskId`    | string |
| `createdAt` | string (RFC3339Nano, UTC) |

## Checkpoint

| key    | type    |
| ------ | ------- |
| `name` | string  |
| `done` | boolean |

## Naming rule

Keys are lowercase camelCase (`taskId`, `createdAt`). No new key may use any
other convention.

## HTTP surface (served by `taxion serve`, read/written via `web/` since M4)

Reads:

- `GET /api/ledger` → full ledger object above
- `GET /api/tasks[?repo=]` → Task array
- `GET /api/dispatches[?repo=]` → Dispatch array
- `GET /api/evidence[?task=]` → Evidence array
- non-GET on a read route → 405; unknown path → 404; unreadable ledger → 500 (fail-closed)

Writes (v1, added M4; localhost only, no auth — auth is M5):

- `POST /api/tasks` `{title, repo}` → 201 Task. Missing title/repo → 400 (REP-001).
- `POST /api/dispatches` `{task, repo}` → 201 Dispatch. Unknown task → 404;
  another dispatch active → 409 (DSP-001). Missing task/repo → 400.
- `POST /api/reviews` `{task, repo, sha}` → 201 Evidence. Empty sha → 400
  (SHA-001). Missing task/repo → 400.
- Request bodies share the CLI guards exactly; web passes POST requests through
  untouched. No ledger keys were added for writes (freeze intact).
