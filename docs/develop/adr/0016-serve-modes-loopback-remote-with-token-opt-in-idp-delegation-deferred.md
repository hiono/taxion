# 16. serve の loopback・remote を明示し IdP 委譲を延期する

Date: 2026-10-10

## Status

Accepted

## Context

M5 は `taxion serve` に token 認証を追加した（ADR-0015）が、mode 選択は暗黙だった。`--addr 0.0.0.0:3938` の token なし起動は適切に拒否するものの、flag の組合せに依存し、remote 公開が明示的な選択だと分からない。技術に詳しくない操作者が LAN の例をコピーすると、公開範囲を理解せず平文 HTTP の token 認証を使いかねない。loopback 優先か LAN 対応かが曖昧な設計は、security review でも指摘される。

Issue #25 で、audit operator-ID（D）、401 rate limit・lockout（E）、audit hash chain（G）、OS・IdP reverse proxy への認証委譲（H）を評価した。H は設計上の理想的な到達点だが、まだ早い。確認済みの需要は単一利用者の SSH tunnel で、複数利用者の LAN は仮説にとどまる。H は IdP 統合と保守を増やし、単一利用者の setup も複雑にする。

## Decision

F-lite として `taxion serve` に2つの名前付き mode を設ける。

- 既定の `loopback` は token の有無によらず loopback 外 bind を拒否し、誤操作で公開できないようにする。
- `remote` は明示選択として `--token` を要求する。平文 HTTP の token 認証は信頼するネットワーク専用とし、ADR-0015 と同じ単一 token、TLS なし、利用者別 identity の監査なしという risk を保つ。

未知の mode は起動時に拒否する。logic は pure function の `checkServeMode` とし unit test で検証する。

H の IdP・OS 委譲は、**外部利用者から複数人での利用を求める依頼が3件** を再検討条件として延期する。maintainer 以外の利用者が共有 LAN 用途を求める Issue・Discussion を数え、3件目で本 ADR を再開する。

## Consequences

- 既定の loopback 限定と token 必須の明示 remote を提供する。[リモート接続](../../userguide/remote-access.md)は remote mode より先に SSH 転送を案内する。
- 専門家が確認する単一 token、TLS なし、rate limit なしの risk は変えず、flag の組合せでなく明示選択にする。mode 切替は設定を明確にし、認証方式は変えない。
- D・E・G は独立した後続作業に残し、前提条件にはしない。

## M8 の実装補足

上記は初期 F-lite の影響を記録している。後続 D・E・G は実装済みで、自己申告 `who`、IP 別失敗件数・lockout、audit hash chain の照合を提供する。M8 受入れは Issue #41–#43、現在の field、固定閾値、照合の制約は[監査ログ](../../userguide/audit-log.md)に記録する。元の「rate limit なし」は現在の動作ではない。共有 token による identity、組込み TLS なし、IdP 委譲の延期は変えない。
