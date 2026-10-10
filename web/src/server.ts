// Local order counter. Serves one HTML page with order forms (create task,
// dispatch, review) plus a JSON API proxied to `taxion serve` (M4).
// Guarantees: GET + the three order POSTs exist; every other method gets
// 405; this slice never reads ledger files and never mutates on its own —
// all writes go to the upstream API, and upstream failure surfaces as 502,
// never as a silent success (fail-closed display).
//
// Localhost scope (M4): no auth. Do not expose beyond loopback (auth is M5).
//
// M5: when the upstream API requires a Bearer token, pass --token (or
// TAXION_TOKEN env). The token lives server-side only: it is attached to
// upstream fetches and never sent to the browser.
//
// Usage: TAXION_API=http://127.0.0.1:3938 node src/server.ts [--port N] [--api URL] [--host ADDR] [--token TOK]
//
// Usage: TAXION_API=http://127.0.0.1:3938 node src/server.ts [--port N] [--api URL] [--host ADDR]
// Security default: listens on 127.0.0.1 only. Pass --host 0.0.0.0 to expose
// (see docs/06-remote-access.md — beyond-loopback needs care until M5 auth).
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

// Order POSTs are passed through to the upstream API untouched, so guard
// semantics (400/404/409) stay in one place: the Go server.
const PAGE = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>Taxion — Task Graph (localhost console)</title>
<style>body{font-family:system-ui,sans-serif;max-width:960px;margin:2rem auto;padding:0 1rem}
table{border-collapse:collapse;width:100%;margin-bottom:2rem}
th,td{border:1px solid #ccc;padding:.4rem .6rem;text-align:left;font-size:.9rem}
th{background:#f4f4f4}.active{font-weight:bold;color:#0a6b0a}
.badge{display:inline-block;font-size:.75rem;background:#eee;border-radius:4px;padding:.1rem .4rem;margin-left:.5rem}
form{margin-bottom:1.5rem}input{margin-right:.5rem;padding:.25rem .4rem}
#msg{min-height:1.5em;font-weight:bold}</style>
</head><body>
<h1>Taxion Task Graph<span class="badge">localhost console</span></h1>
<div id="msg"></div>
<h2>Order — create task</h2>
<form id="f-task"><input name="title" placeholder="title" required><input name="repo" placeholder="repo (required)" required><button>Create</button></form>
<h2>Order — dispatch</h2>
<form id="f-dispatch"><input name="task" placeholder="task id" required><input name="repo" placeholder="repo (required)" required><button>Dispatch</button></form>
<h2>Order — review</h2>
<form id="f-review"><input name="task" placeholder="task id" required><input name="repo" placeholder="repo (required)" required><input name="sha" placeholder="sha (required)" required><button>Bind evidence</button></form>
<div id="app">Loading…</div>
<script>
const msg=(t)=>{document.getElementById("msg").textContent=t};
async function order(path,body){
  const r=await fetch(path,{method:"POST",headers:{"content-type":"application/json"},body:JSON.stringify(body)});
  const j=await r.json();
  if(!r.ok) throw new Error(j.error??("HTTP "+r.status));
  return j;
}
const form=(id,path,names)=>{
  document.getElementById(id).addEventListener("submit",async e=>{
    e.preventDefault();
    const fd=new FormData(e.target),body={};
    for(const n of names) body[n]=String(fd.get(n)??"");
    try{const j=await order(path,body);msg("OK: "+JSON.stringify(j));load();}
    catch(err){msg("Failed: "+err);}
  });
};
form("f-task","/api/tasks",["title","repo"]);
form("f-dispatch","/api/dispatches",["task","repo"]);
form("f-review","/api/reviews",["task","repo","sha"]);
function load(){
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
}
load();
</script></body></html>`;

export function apiToken(argv: string[] = process.argv.slice(2)): string {
  return flagValue(argv, "--token") ?? process.env["TAXION_TOKEN"] ?? "";
}

function authHeaders(token: string): Record<string, string> {
  const h: Record<string, string> = { "content-type": "application/json" };
  if (token !== "") h["authorization"] = `Bearer ${token}`;
  return h;
}

async function proxyGet(upstream: string, token: string): Promise<{ status: number; body: string }> {
  try {
    const res = await fetch(`${upstream}/api/ledger`, {
      headers: token !== "" ? { authorization: `Bearer ${token}` } : {},
    });
    const body = await res.text();
    return { status: res.status, body };
  } catch {
    return { status: 502, body: JSON.stringify({ error: "upstream unavailable" }) };
  }
}

async function proxyOrder(
  upstream: string,
  token: string,
  path: string,
  req: import("node:http").IncomingMessage,
): Promise<{ status: number; body: string }> {
  try {
    const chunks: Buffer[] = [];
    for await (const c of req) chunks.push(c as Buffer);
    const res = await fetch(`${upstream}${path}`, {
      method: "POST",
      headers: authHeaders(token),
      body: Buffer.concat(chunks),
    });
    return { status: res.status, body: await res.text() };
  } catch {
    return { status: 502, body: JSON.stringify({ error: "upstream unavailable" }) };
  }
}

export function createApp(upstream: string, token = "") {
  return createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    if (url.pathname === "/" && req.method === "GET") {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(PAGE);
      return;
    }
    if (url.pathname === "/api/ledger" && req.method === "GET") {
      proxyGet(upstream, token).then(({ status, body }) => {
        res.writeHead(status, { "content-type": "application/json" });
        res.end(body);
      });
      return;
    }
    if (req.method === "POST" && url.pathname.startsWith("/api/")) {
      proxyOrder(upstream, token, url.pathname + url.search, req).then(({ status, body }) => {
        res.writeHead(status, { "content-type": "application/json" });
        res.end(body);
      });
      return;
    }
    if (req.method !== "GET" && req.method !== "POST") {
      res.writeHead(405, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: "GET + order POSTs only" }));
      return;
    }
    res.writeHead(404, { "content-type": "application/json" });
    res.end(JSON.stringify({ error: "not found" }));
  });
}

export function listenHost(argv: string[] = process.argv.slice(2)): string {
  return (
    flagValue(argv, "--host") ??
    process.env["TAXION_HOST"] ??
    "127.0.0.1"
  );
}

export type { Ledger };

const isMain = process.argv[1]?.endsWith("server.ts") ?? false;
if (isMain) {
  const argv = process.argv.slice(2);
  const port = Number(flagValue(argv, "--port") ?? 3939);
  const host = listenHost(argv);
  const server = createApp(apiBase(argv), apiToken(argv));
  server.listen(port, host, () => console.log(`taxion web (order counter) on ${host}:${port}`));
}
