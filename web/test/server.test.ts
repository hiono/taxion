import { describe, it, before, after } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { AddressInfo } from "node:net";
import { createApp } from "../src/server.ts";

const LEDGER = {
  tasks: [{ id: "t-1", title: "demo", repo: "/repo/demo" }],
  dispatches: [{ id: "d-1", taskId: "t-1", active: true }],
  evidence: [{ taskId: "t-1", sha: "deadbeef" }],
};

let base = "";
let server: ReturnType<typeof createApp>;

before(async () => {
  const dir = mkdtempSync(join(tmpdir(), "taxion-web-"));
  const path = join(dir, "ledger.json");
  writeFileSync(path, JSON.stringify(LEDGER));
  server = createApp(path);
  await new Promise<void>((resolve) => server.listen(0, resolve));
  base = `http://localhost:${(server.address() as AddressInfo).port}`;
});

after(() => new Promise<void>((resolve, reject) => server.close((e) => (e ? reject(e) : resolve()))));

describe("read-only Task Graph view", () => {
  it("serves the HTML page", async () => {
    const res = await fetch(`${base}/`);
    assert.equal(res.status, 200);
    assert.match(res.headers.get("content-type") ?? "", /text\/html/);
    assert.match(await res.text(), /read-only/);
  });

  it("exposes the ledger as JSON", async () => {
    const res = await fetch(`${base}/api/ledger`);
    assert.equal(res.status, 200);
    assert.deepEqual(await res.json(), LEDGER);
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
