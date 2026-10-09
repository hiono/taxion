// Read-only Task Graph view. Serves one HTML page plus a JSON API proxied
// from `taxion serve` (M2). Guarantees: only GET routes exist; non-GET
// methods get 405; this slice never reads ledger files and never mutates —
// upstream failure surfaces as 502, never as an empty ledger pretending to
// be the truth (fail-closed display).
//
// Usage: TAXION_API=http://127.0.0.1:3938 node src/server.ts [--port N] [--api URL]
import { createServer } from "node:http";
import type { Ledger } from "./types.ts";

function flagValue(argv: string[], name: string): string | undefined {
  const i = argv.indexOf(name);
  return i >= 0 && i + 1 < argv.length ? argv[i + 1] : undefined;
}

export function apiBase(argv: string[] = process.argv.slice(2)): string {
  return (
    flagValue(argv, "--api") ??
    process.env["TAXION_API"] ??
    "http://127.0.0.1:3938"
  );
}

const PAGE = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>Taxion — Task Graph (read-only)</title>
<style>body{font-family:system-ui,sans-serif;max-width:960px;margin:2rem auto;padding:0 1rem}
table{border-collapse:collapse;width:100%;margin-bottom:2rem}
th,td{border:1px solid #ccc;padding:.4rem .6rem;text-align:left;font-size:.9rem}
th{background:#f4f4f4}.active{font-weight:bold;color:#0a6b0a}
.badge{display:inline-block;font-size:.75rem;background:#eee;border-radius:4px;padding:.1rem .4rem;margin-left:.5rem}</style>
</head><body>
<h1>Taxion Task Graph<span class="badge">read-only</span></h1>
<div id="app">Loading…</div>
<script>
fetch("/api/ledger").then(r=>r.json()).then(l=>{
  const t=(o)=>Object.values(o??{}).length?o:[];
  const esc=(s)=>String(s??"").replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));
  const tbl=(heads,rows)=>"<table><tr>"+heads.map(h=>"<th>"+h+"</th>").join("")+"</tr>"+
    rows.map(r=>"<tr>"+r.map(c=>"<td>"+c+"</td>").join("")+"</tr>").join("")+"</table>";
  document.getElementById("app").innerHTML =
    "<h2>Tasks</h2>"+tbl(["ID","Title","Repo"],t(l.tasks).map(x=>[esc(x.id),esc(x.title),esc(x.repo)]))+
    "<h2>Dispatches</h2>"+tbl(["ID","Task","Active"],t(l.dispatches).map(x=>[esc(x.id),esc(x.taskId),x.active?'<span class="active">yes</span>':"no"]))+
    "<h2>Evidence</h2>"+tbl(["Task","SHA"],t(l.evidence).map(x=>[esc(x.taskId),esc(x.sha)]));
}).catch(e=>{document.getElementById("app").textContent="Failed to load ledger: "+e});
</script></body></html>`;

async function proxyLedger(upstream: string): Promise<{ status: number; body: string }> {
  try {
    const res = await fetch(`${upstream}/api/ledger`);
    const body = await res.text();
    return { status: res.status, body };
  } catch {
    return { status: 502, body: JSON.stringify({ error: "upstream unavailable" }) };
  }
}

export function createApp(upstream: string) {
  return createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    if (req.method !== "GET") {
      res.writeHead(405, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "read-only: GET only" }));
      return;
    }
    if (url.pathname === "/") {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(PAGE);
      return;
    }
    if (url.pathname === "/api/ledger") {
      proxyLedger(upstream).then(({ status, body }) => {
        res.writeHead(status, { "content-type": "application/json" });
        res.end(body);
      });
      return;
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found" }));
  });
}

export type { Ledger };

const isMain = process.argv[1]?.endsWith("server.ts") ?? false;
if (isMain) {
  const argv = process.argv.slice(2);
  const port = Number(flagValue(argv, "--port") ?? 3939);
  const server = createApp(apiBase(argv));
  server.listen(port, () => console.log(`taxion web (read-only) on :${port}`));
}
