import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { createApp, listenHost } from "../src/server.ts";

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
  // Mock `taxion serve`: the web slice must only ever talk to the API.
  upstream = createServer((req, res) => {
    const json = (code: number, v: unknown) => {
      res.writeHead(code, { "content-type": "application/json" });
      res.end(JSON.stringify(v));
    };
    if (req.method === "GET" && req.url === "/api/ledger") {
      json(200, LEDGER);
      return;
    }
    if (req.method === "POST" && req.url === "/api/tasks") {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        const p = JSON.parse(body);
        if (!p.title || !p.repo) {
          json(400, { error: "title and repo are required" });
          return;
        }
        json(201, { id: "t-9", title: p.title, repo: p.repo });
      });
      return;
    }
    if (req.method === "POST" && req.url === "/api/dispatches") {
      json(409, { error: "another dispatch is active" });
      return;
    }
    if (req.method !== "GET" && req.method !== "POST") {
      json(405, { error: "nope" });
      return;
    }
    json(404, { error: "not found" });
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

describe("localhost console (API-backed)", () => {
  it("serves the HTML page with order forms", async () => {
    const res = await fetch(`${base}/`);
    assert.equal(res.status, 200);
    assert.match(res.headers.get("content-type") ?? "", /text\/html/);
    const html = await res.text();
    assert.match(html, /f-task/);
    assert.match(html, /f-dispatch/);
    assert.match(html, /f-review/);
  });

  it("proxies the ledger as JSON from the upstream API", async () => {
    const res = await fetch(`${base}/api/ledger`);
    assert.equal(res.status, 200);
    assert.deepEqual(await res.json(), LEDGER);
  });

  it("passes order POSTs through with upstream status/body", async () => {
    const ok = await fetch(`${base}/api/tasks`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ title: "x", repo: "r" }),
    });
    assert.equal(ok.status, 201);
    assert.equal((await ok.json()).id, "t-9");

    const bad = await fetch(`${base}/api/tasks`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ title: "", repo: "" }),
    });
    assert.equal(bad.status, 400);

    const conflict = await fetch(`${base}/api/dispatches`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ task: "t-1", repo: "r" }),
    });
    assert.equal(conflict.status, 409);
  });

  it("fails closed (502) when the upstream API is down", async () => {
    const dead = createApp("http://127.0.0.1:1");
    await new Promise<void>((resolve) => dead.listen(0, resolve));
    const port = (dead.address() as AddressInfo).port;
    try {
      const get = await fetch(`http://localhost:${port}/api/ledger`);
      assert.equal(get.status, 502);
      const post = await fetch(`http://localhost:${port}/api/tasks`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: "{}",
      });
      assert.equal(post.status, 502);
    } finally {
      await new Promise<void>((resolve) => dead.close(() => resolve()));
    }
  });

  it("rejects PUT/PATCH/DELETE (only GET + order POSTs exist)", async () => {
    for (const method of ["PUT", "PATCH", "DELETE"]) {
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

describe("listen host (secure default)", () => {
  it("defaults to localhost", () => {
    assert.equal(listenHost([]), "127.0.0.1");
  });

  it("accepts --host override", () => {
    assert.equal(listenHost(["--host", "0.0.0.0"]), "0.0.0.0");
  });
});
