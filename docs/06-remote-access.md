# 06 — Remote access (SSH tunnel first)

Security and personal-information protection are top priority. Since M5
(ADR-0015), the rule is: **loopback-only by default, token required beyond
loopback**:

- `taxion serve` — default `--addr 127.0.0.1:3938`; `--token` / `TAXION_TOKEN`
  env enables Bearer auth on every route (missing/wrong → identical 401).
  `--mode loopback` (default) refuses any non-loopback bind, token or not;
  `--mode remote` is the named opt-in for LAN/docker use and requires
  `--token` (ADR-0016). Binding beyond loopback without a token is refused
  outright.
- web console — default `--host 127.0.0.1` and `--port 3939`; `--token` /
  `TAXION_TOKEN` is forwarded server-side to the upstream API, never to the
  browser.

Generate a token once and keep it outside the repo (env file, secret store):

```sh
taxion gen-token
```

## Token hygiene (best practice)

`gen-token` is stateless: running it twice generates two independent values
and does not revoke previous values. Only the token supplied to `serve` is
accepted:

- Issue once. Re-issue only to **rotate** (suspected leak, member change).
- Never pass the token inline (`TAXION_TOKEN=<tok> …` stays in shell
  history). Load it from a `0600` env file outside the repo, or `read -s`:
  `read -rs TAXION_TOKEN < /dev/tty; export TAXION_TOKEN`.
- Rotate by restarting `serve` with the new value. The previous token is no
  longer accepted after restart. Notify everyone sharing it before restarting.
- Treat unused generated values as active credentials until you are
  sure they were never deployed: delete them from history/notes.
- Known limit (ADR-0015): one shared token, no per-operator keys yet. If the
  number of operators grows, evaluate per-user authentication rather than
  generating more shared tokens.

The ledger file holds task titles and repo paths. Treat it as sensitive:
never place it on a shared volume unencrypted. Plain HTTP stays inside
trusted networks (loopback, SSH tunnel, VPN, docker bridge); public-internet
exposure without TLS termination in front is out of scope (ADR-0015).

## Rule

> Prefer SSH forwarding: keep the API on localhost and connect with `ssh -L`.
> Token-authenticated serving beyond loopback is authorized by ADR-0015
> and selected with ADR-0016's `--mode remote` (token mandatory).
> Untokened `0.0.0.0` exposure is still forbidden — refused at startup.

## Pattern A — API on a remote host, web + browser local (recommended)

On your machine, forward the remote API port to local loopback:

```sh
ssh -L 3938:localhost:3938 user@remote-host
# now http://127.0.0.1:3938 responds with the remote ledger
```

Then run the web console locally against the tunnel (defaults are
localhost-only on both ends, nothing is exposed):

```sh
taxion serve --addr 127.0.0.1:3938   # on remote-host (or already running)
TAXION_API=http://127.0.0.1:3938 npm start -- --port 3939   # local
# open http://127.0.0.1:3939
```

No firewall changes, no new listening sockets on any public interface.

## Pattern B — API inside docker on the remote host

Bind the container port to the *host's loopback only* (never bare `-p 3938:3938`):

```sh
# on remote-host
docker run -d --name taxion \
  -p 127.0.0.1:3938:3938 \
  -v taxion-data:/data \
  -e TAXION_TOKEN \
  taxion taxion serve --mode remote --addr 0.0.0.0:3938 --store /data/ledger.json
```

`-p 127.0.0.1:…` keeps the published port off the LAN; the container itself
must listen on `0.0.0.0` to receive docker-forwarded traffic, so `--mode
remote` plus a token is required even here (the socket also touches the
docker bridge — other containers could reach it). Reachable only via the
host loopback in practice. Then apply Pattern A, adding the token:

```sh
ssh -L 3938:localhost:3938 user@remote-host
# web/curl need the same token the remote serve was started with:
export TAXION_TOKEN   # value from the operator, never from chat/logs
```

Alternative without publishing a port at all — reach the container over SSH:

```sh
ssh user@remote-host docker exec taxion taxion --store /data/ledger.json task list
```

## Pattern C — web console also remote

Keep both ends on loopback and forward twice:

```sh
ssh -L 3938:localhost:3938 -L 3939:localhost:3939 user@remote-host
# API on remote-host:  taxion serve  (default localhost)
# web on remote-host:  npm start     (default --host 127.0.0.1)
# browser (local):     http://127.0.0.1:3939
```

## Pattern D — home client develops in a work docker container (combined)

Patterns A+B combined: use the browser for task creation, dispatch, and
evidence binding, and a separate SSH channel for editing, building, and
testing in the development container:

```sh
# 1. tunnels (home): API + web; the editor uses a separate SSH connection
ssh -L 3938:localhost:3938 -L 3939:localhost:3939 user@work-host
# 2. API in docker (work-host): Pattern B, loopback-published only
docker run -d --name taxion \
  -p 127.0.0.1:3938:3938 \
  -v taxion-data:/data \
  -e TAXION_TOKEN \
  taxion taxion serve --mode remote --addr 0.0.0.0:3938 --store /data/ledger.json
# 3. web console (either end, localhost-only): Pattern A or C
TAXION_API=http://127.0.0.1:3938 npm start -- --port 3939
# 4. browser (home): http://127.0.0.1:3939 — create/dispatch/review
# 5. development (home): VS Code Remote SSH to work-host, then
#    `docker exec -it <dev-container> bash` — edit, build, test as usual
```

The browser operates on the ledger's tasks, dispatches, and evidence.
The editor and terminal are used to modify code, build, and test. These
ledger API operations transmit task titles, repo paths, and SHAs rather
than source files. Both connections use SSH.

## Explicit remote mode (advanced, ADR-0016)

`taxion serve --mode remote --addr 0.0.0.0:3938 --token …` is the named,
ADR-0016-authorized way to serve the LAN: token gate on every route, audit
log on every write. Operators must account for residual risks (single shared
token, plain HTTP — trusted networks only, never the open internet without
TLS in front). Web `--host 0.0.0.0` only widens the page/proxy; the upstream
API stays token-authenticated. Additional authentication features (IdP,
per-user keys) require a new ADR — see ADR-0016's deferred H with its numeric
trigger.
