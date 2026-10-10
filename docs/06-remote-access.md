# 06 — Remote access (SSH tunnel first)

Security and personal-information protection are top priority. Until M5
(auth + audit) is complete, **no Taxion surface listens beyond loopback
without a token**:

- `taxion serve` — default `--addr 127.0.0.1:3938`; `--token` / `TAXION_TOKEN`
  env enables Bearer auth on every route (missing/wrong → identical 401).
  Binding beyond loopback without a token is refused outright.
- web order counter — default `--host 127.0.0.1:3939`; `--token` /
  `TAXION_TOKEN` is forwarded server-side to the upstream API, never to the
  browser.

Mint a token once and keep it outside the repo (env file, secret store):

```sh
taxion gen-token
```

The ledger file holds task titles and repo paths. Treat it as sensitive:
never place it on a shared volume unencrypted, and never expose the API
without a tunnel until M5.

## Rule

> Keep the API on localhost. Pull it to your machine with `ssh -L`.
> Direct `0.0.0.0` exposure without auth is forbidden before M5
> (beyond-loopback serving needs a new ADR per the ADR-0011 amendment).

## Pattern A — API on a remote host, web + browser local (recommended)

On your machine, forward the remote API port to local loopback:

```sh
ssh -L 3938:localhost:3938 user@remote-host
# now http://127.0.0.1:3938 responds with the remote ledger
```

Then run the web counter locally against the tunnel (defaults are
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
  taxion taxion serve --addr 0.0.0.0:3938 --store /data/ledger.json
```

`-p 127.0.0.1:…` keeps the published port off the LAN; the container itself
must listen on `0.0.0.0` to receive docker-forwarded traffic, but that socket
is reachable only via the host loopback. Then apply Pattern A:

```sh
ssh -L 3938:localhost:3938 user@remote-host
```

Alternative without publishing a port at all — reach the container over SSH:

```sh
ssh user@remote-host docker exec taxion taxion --store /data/ledger.json task list
```

## Pattern C — web counter also remote

Keep both ends on loopback and forward twice:

```sh
ssh -L 3938:localhost:3938 -L 3939:localhost:3939 user@remote-host
# API on remote-host:  taxion serve  (default localhost)
# web on remote-host:  npm start     (default --host 127.0.0.1)
# browser (local):     http://127.0.0.1:3939
```

## Pattern D — home client develops in a work docker container (combined)

Patterns A+B combined into one flow: from home you drive the Taxion ledger
(order/approve) in the browser while doing the real work (edit/terminal) over
a second SSH channel. Two rails, both inside SSH:

```sh
# 1. tunnels (home): API + web + your editor's channel
ssh -L 3938:localhost:3938 -L 3939:localhost:3939 user@work-host
# 2. API in docker (work-host): Pattern B, loopback-published only
docker run -d --name taxion \
  -p 127.0.0.1:3938:3938 \
  -v taxion-data:/data \
  taxion taxion serve --addr 0.0.0.0:3938 --store /data/ledger.json
# 3. web counter (either end, localhost-only): Pattern A or C
TAXION_API=http://127.0.0.1:3938 npm start -- --port 3939
# 4. browser (home): http://127.0.0.1:3939 — create/dispatch/review
# 5. real work (home): VS Code Remote SSH to work-host, then
#    `docker exec -it <dev-container> bash` — edit, build, test as usual
```

What each rail covers: the browser rail operates the ledger (what to do,
who approved); the editor rail does the work (code, build, test). Taxion
never sees your source — only task titles, repo paths, and SHAs in the
ledger, which stay inside the tunnel.

## Explicit opt-out (advanced, M5 territory)
`taxion serve --addr 0.0.0.0:3938` or web `--host 0.0.0.0` exposes the full
API — including order POSTs (task/dispatch/review) — to anyone who can reach
the port, with no authentication. Do this only behind a firewall/VPN you
control, and record a new ADR first (required by the ADR-0011 amendment).
M5 (auth + audit + public release) removes this restriction properly.
