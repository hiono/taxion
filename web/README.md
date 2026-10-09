# @taxion/web — localhost order counter

Separate directory, zero dependencies, no build step (Node ≥22.18 strips
types natively). One HTML page with order forms (create task, dispatch,
review) plus a JSON API proxied to `taxion serve` (M4).

Counter guarantee: the page GETs; order forms POST to `/api/*`, which this
slice passes through to the upstream API untouched — guard semantics
(400/404/409) live in exactly one place (the Go server). Upstream failure
surfaces as 502, never as a silent success. This slice never reads ledger
files and never mutates on its own. All writes land in the CLI-owned ledger
via the API.

Localhost scope (M4): no auth. Listens on 127.0.0.1 by default; `--host`
or `TAXION_HOST` overrides (e.g. `--host 0.0.0.0`). Do not expose beyond
loopback (auth is M5) — remote access goes over SSH tunnels, see
`../docs/06-remote-access.md`.

```sh
npm test
taxion serve --addr 127.0.0.1:3938
TAXION_API=http://127.0.0.1:3938 npm start -- --port 3939
```
