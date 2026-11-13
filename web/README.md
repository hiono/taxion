# @taxion/web ローカル console

独立した directory で、依存 package と build 手順はない。Node ≥22.18 が native に型を除去する。Task 作成・Dispatch・review の form を持つ一つの HTML page と、`taxion serve` へ転送する JSON API からなる（M4）。

page は GET を使い、form は `/api/*` に POST する。POST は接続先 API に変更せず渡す。400・404・409 の検査条件は Go server 一か所で定義する。接続先の失敗は 502 として報告し、通知のない成功にはしない。この console は Ledger file を直接読まず、独自の変更処理も行わない。全書込みは API を通して CLI 管理の Ledger に保存する。

承認済み [Memory 検査](../docs/userguide/memory-model.md#承認と-dispatch-前の検査)は、Dispatch 前に Go が評価する。console は policy 拒否（409、MEM-001）と保存・設定の失敗（500）をそのまま返し、指針の解釈や Dispatch request への条件追加は行わない。

M5 の認証付き localhost 構成では、接続先 API が token を要求する場合に `--token` または `TAXION_TOKEN` を指定する。token は server 側の API 呼出しだけに付け、browser には送らない。既定は `127.0.0.1` で待ち受け、`--host`・`TAXION_HOST` で変更する。

リモート接続には SSH tunnel を使う。[リモート接続](../docs/userguide/remote-access.md)に、token の保管・読込みと接続手順を記載している。

```sh
npm test
taxion gen-token                          # once: store it outside the repo
TAXION_TOKEN=<tok> taxion serve --addr 127.0.0.1:3938
TAXION_API=http://127.0.0.1:3938 TAXION_TOKEN=<tok> npm start -- --port 3939
```
