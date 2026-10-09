# @taxion/web — read-only Task Graph view

Separate directory, zero dependencies, no build step (Node ≥22.18 strips
types natively). Fetches the ledger from `taxion serve` (M2 API) and serves
one HTML page plus `GET /api/ledger` (proxied).

Read-only guarantee: only GET routes exist; every other method gets 405;
this slice never reads ledger files and never writes. Upstream failure
surfaces as 502, never as an empty ledger. All mutations stay in the
Go CLI (`taxion task/dispatch/review`).

```sh
npm test
taxion serve --addr 127.0.0.1:3938
TAXION_API=http://127.0.0.1:3938 npm start -- --port 3939
```
