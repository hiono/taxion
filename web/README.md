# @taxion/web — localhost console

Separate directory, zero dependencies, no build step (Node ≥22.18 strips
types natively). One HTML page with task operation forms (create task, dispatch,
review) plus a JSON API proxied to `taxion serve` (M4).

Request handling: the page GETs; forms POST to `/api/*`, which this
slice passes through to the upstream API untouched — guard semantics
(400/404/409) live in exactly one place (the Go server). Upstream failure
surfaces as 502, never as a silent success. This slice never reads ledger
files and never mutates on its own. All writes land in the CLI-owned ledger
via the API.

Approved [Memory checks](../docs/08-memory-model.md#approval-and-dispatch-checks)
are evaluated by Go before dispatch. The console preserves policy denials
(409, MEM-001) and storage/configuration failures (500); it does not interpret
guidelines or add conditions to the dispatch request.

Localhost scope with M5 auth: when the upstream API requires a Bearer token,
pass `--token` or `TAXION_TOKEN` — it is attached server-side to upstream
fetches and never sent to the browser. Listens on 127.0.0.1 by default;
`--host` or `TAXION_HOST` overrides. Remote access goes over SSH tunnels,
see `../docs/06-remote-access.md`.

```sh
npm test
taxion gen-token                          # once: store it outside the repo
TAXION_TOKEN=<tok> taxion serve --addr 127.0.0.1:3938
TAXION_API=http://127.0.0.1:3938 TAXION_TOKEN=<tok> npm start -- --port 3939
```
