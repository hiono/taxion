import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { createApp } from "../src/server.ts";

const LEDGER = {
  tasks: [{ id: "t-1", title: "demo", repo: "/repo/demo" }],
  dispatches: [{ id: "d-1", taskId: "t-1", active: true }],
  evidence: [{ taskId: "t-1", sha: "deadbeef" }],
};

let base = "";
let server: ReturnType<typeof createApp>;
let upstream: ReturnType<typeof createServer>;
let upstreamBase = "";

before(async () => {
  // Mock `taxion serve`: the web slice must only ever fetch the API.
  upstream = createServer((req, res) => {
    if (req.method === "GET" && req.url === "/api/ledger") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify(LEDGER));
      return;
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found" }));
  });
  await new Promise<void>((resolve) => upstream.listen(0, resolve));
  upstreamBase = `http://localhost:${(upstream.address() as AddressInfo).port}`;
  server = createApp(upstreamBase);
  await new Promise<void>((resolve) => server.listen(0, resolve));
  base = `http://localhost:${(server.address() as AddressInfo).port}`;
});

after(
  () =>
    new Promise<void>((resolve, reject) =>
      server.close((e) => (e ? reject(e) : resolve())),
    ),
);

after(
  () =>
    new Promise<void>((resolve, reject) =>
      upstream.close((e) => (e ? reject(e) : resolve())),
    ),
);

describe("read-only Task Graph view (API-backed)", () => {
  it("serves the HTML page", async () => {
    const res = await fetch(`${base}/`);
    assert.equal(res.status, 200);
    assert.match(res.headers.get("content-type") ?? "", /text\/html/);
    assert.match(await res.text(), /read-only/);
  });

  it("proxies the ledger as JSON from the upstream API", async () => {
    const res = await fetch(`${base}/api/ledger`);
    assert.equal(res.status, 200);
    assert.deepEqual(await res.json(), LEDGER);
  });

  it("fails closed (502) when the upstream API is down", async () => {
    const dead = createApp("http://127.0.0.1:1");
    await new Promise<void>((resolve) => dead.listen(0, resolve));
    const port = (dead.address() as AddressInfo).port;
    try {
      const res = await fetch(`http://localhost:${port}/api/ledger`);
      assert.equal(res.status, 502);
    } finally {
      await new Promise<void>((resolve) => dead.close(() => resolve()));
    }
  });

  it("rejects every non-GET method (no mutation surface)", async () => {
    for (const method of ["POST", "PUT", "PATCH", "DELETE"]) {
      const res = await fetch(`${base}/api/ledger`, { method });
      assert.equal(res.status, 405, method);
      const home = await fetch(`${base}/`, { method });
      assert.equal(home.status, 405, `${method} /`);
    }
  });

  it("returns 404 for unknown paths", async () => {
    const res = await fetch(`${base}/nope`);
    assert.equal(res.status, 404);
  });
});
