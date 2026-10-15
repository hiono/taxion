# リモート接続

安全性と個人情報の保護を最優先にする。M5（ADR-0015）以降は、既定で loopback のみに公開し、loopback 外では token を必須とする。

`taxion serve` の既定 address は `127.0.0.1:3938`。`--token` または環境変数 `TAXION_TOKEN` を指定すると、全 route で token 認証を有効にする。未指定・不一致の request は同じ 401 を返す。既定の `--mode loopback` は、token の有無を問わず loopback 外への bind を拒否する。LAN・Docker で使う場合は、`--mode remote` を明示選択し、token を指定する（ADR-0016）。token なしの loopback 外 bind は起動時に拒否する。

Web console の既定は `--host 127.0.0.1`、`--port 3939`。`--token`・`TAXION_TOKEN` は server 側から接続先 API へ渡し、browser には渡さない。

token は一度生成し、リポジトリ外の環境変数ファイルや secret store に保管する。

```sh
taxion gen-token
```

## token の管理

`gen-token` は状態を保存しない。二回実行すると独立した二つの値を生成し、前の値を失効させない。`serve` に渡した token だけを受け付ける。

- 発行は一回とし、漏えいの疑いやメンバー変更によるローテーション時だけ再発行する。
- token を command 行に直接書かない。`TAXION_TOKEN=<tok> …` は shell 履歴に残る。リポジトリ外の `0600` の環境変数ファイルから読み込むか、`read -rs TAXION_TOKEN < /dev/tty; export TAXION_TOKEN` を使う。
- 新しい値で `serve` を再起動して切り替える。再起動後は旧 token を受け付けない。共有する全員に事前通知する。
- 未使用の生成値も、配備していないと確信できるまでは有効な credential として扱い、履歴・メモから削除する。
- 現在は単一の共有 token であり、操作者別 key はない（ADR-0015）。操作者が増えた場合は、共有 token を増やすより、利用者別の認証を評価する。

Ledger には Task title と repo path が含まれる。機密情報として扱い、暗号化せずに共有 volume に置かない。平文 HTTP は loopback、SSH tunnel、VPN、Docker bridge など信頼するネットワーク内で使う。前段の TLS 終端なしに公開インターネットへ公開することは対象外（ADR-0015）。

## SSH 転送の方針

API を localhost に保ち、`ssh -L` で接続する方法を優先する。token 認証付きの loopback 外公開は ADR-0015 で許可し、ADR-0016 の `--mode remote` で選択する。token は必須。token なしの `0.0.0.0` 公開は引き続き禁止し、起動時に拒否する。

## リモート API とローカル Web console

推奨する構成では、操作者の機器でリモート API port をローカル loopback へ転送する。

```sh
ssh -L 3938:localhost:3938 user@remote-host
# now http://127.0.0.1:3938 responds with the remote ledger
```

Web console はローカルで起動し、tunnel に接続する。両端の既定は localhost のみで、外部には公開しない。

```sh
taxion serve --addr 127.0.0.1:3938   # on remote-host (or already running)
TAXION_API=http://127.0.0.1:3938 npm start -- --port 3939   # local
# open http://127.0.0.1:3939
```

firewall の変更や、公開 interface 上の新しい待受 socket は不要。

## リモート Docker 内の API

container の port は host の loopback だけに bind する。単独の `-p 3938:3938` は使わない。

```sh
# on remote-host
docker run -d --name taxion \
  -p 127.0.0.1:3938:3938 \
  -v taxion-data:/data \
  -e TAXION_TOKEN \
  taxion taxion serve --mode remote --addr 0.0.0.0:3938 --store /data/ledger.json
```

`-p 127.0.0.1:…` は公開 port を LAN から隔離する。ただし、Docker が転送する通信を受けるため、container 自体は `0.0.0.0` で待ち受ける必要がある。この socket は Docker bridge にも接続し、他の container から届く可能性があるため、この構成でも `--mode remote` と token が必要。host に公開した port は loopback からだけ接続できる。前節の SSH 転送を使い、token も設定する。

```sh
ssh -L 3938:localhost:3938 user@remote-host
# web/curl need the same token the remote serve was started with:
export TAXION_TOKEN   # value from the operator, never from chat/logs
```

port を公開せず、SSH 経由で container に接続する方法もある。

```sh
ssh user@remote-host docker exec taxion taxion --store /data/ledger.json task list
```

## Web console もリモートに置く場合

両方を loopback に保ち、二つの port を転送する。

```sh
ssh -L 3938:localhost:3938 -L 3939:localhost:3939 user@remote-host
# API on remote-host:  taxion serve  (default localhost)
# web on remote-host:  npm start     (default --host 127.0.0.1)
# browser (local):     http://127.0.0.1:3939
```

## 自宅から開発用 Docker を使う場合

リモート Docker 内 API と SSH 転送を組み合わせる。browser では Task 作成、Dispatch、証拠の対応付けを行い、編集・ビルド・テストは別の SSH 接続で開発 container 内へアクセスする。

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

browser は Ledger の Task・Dispatch・Evidence を操作する。editor と terminal はコード変更、ビルド、テストに使う。Ledger API のこれらの操作は、source file ではなく Task title、repo path、SHA を送信する。両方の接続に SSH を使う。

## LAN への明示公開

`taxion serve --mode remote --addr 0.0.0.0:3938 --token …` は、ADR-0016 で採択した LAN 公開の方法。全 route で token を確認し、全書込みを監査ログに記録する。操作者は、単一共有 token と平文 HTTP のリスクを考慮する。信頼するネットワークに限り、TLS 終端なしで公開インターネットには出さない。

Web の `--host 0.0.0.0` は page・proxy の公開範囲だけを広げ、接続先 API の token 認証は維持する。IdP・利用者別 key などの認証追加には、新しい ADR が必要。ADR-0016 で延期した H と、その数値による着手条件を参照する。
