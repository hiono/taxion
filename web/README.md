# @taxion/web — read-only Task Graph view

Separate directory, zero dependencies, no build step (Node ≥22.18 strips
types natively). Reads the CLI-owned ledger file and serves one HTML page
plus `GET /api/ledger`.

Read-only guarantee: only GET routes exist; every other method gets 405; the
ledger file is opened read-only and never written. All mutations stay in the
Go CLI (`taxion task/dispatch/review`).

```sh
npm test
TAXION_STORE=/path/to/ledger.json npm start -- --port 3939
```
