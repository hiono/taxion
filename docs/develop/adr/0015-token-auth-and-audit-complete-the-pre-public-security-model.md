# 15. 公開前の token 認証と audit の保護設計を決める

Date: 2026-10-10

## Status

Accepted

## Context

M5 の Issue #22（token 認証）と #23（audit trail）は実装済み。M5 までは全操作が loopback 限定で認証はなく、ADR-0011 の改訂で loopback 外公開には新 ADR を要求した。外部公開（#24）には現在の保護設計と、その保証対象外を明示する必要がある。

M5 で実装した内容は次のとおり。

- `taxion serve --token`・`TAXION_TOKEN` による全 route の token 認証。constant-time 比較を使い、欠落・不一致は同じ401にして判別情報を返さない。token なしの loopback 外 bind は起動時に拒否する。
- `taxion gen-token` は暗号学的乱数32 bytes を16進文字列にし、log に記録しない。
- Web console は server 側だけで token を転送し、page に含めない。
- 全変更の試行と拒否を追記専用 JSONL audit（`{ts,method,path,repo,status,authed}`）に記録する。token、body、query は含めず、起動時に検査して失敗を即座に報告する。Taxion は削除せず、操作者がローテーションする。
- Ledger wire format は M2-3 で固定済み。M5-3 のための履歴 scan は合格した。

## Decision

公開前の設計を十分と判断し、**token 付き公開だけ** loopback 限定を解除する。

1. `serve` の `--token` 指定では、LAN、Docker の `-p`、SSH 転送先など loopback 外への bind を許し、token 認証と audit を使う。
2. token なしは loopback 限定を保ち、起動時に強制する。
3. 平文 HTTP の token 認証は loopback、SSH tunnel、VPN、Docker bridge の信頼するネットワーク内だけで受け入れ、制約を文書で明かす。前段 TLS 終端なしの公開インターネットへの公開は対象外とし、必要になったら将来の ADR で判断する。

## Consequences

- この ADR で ADR-0011 改訂の loopback 外公開条件を満たす。token 付き公開を明示的に認可し、配置ごとの ADR は不要とする。
- SECURITY.md を M5 の対策へ更新し、報告先は非公開 advisory のまま保つ。
- 単一共有 token、利用者別 identity なし、Taxion 内の TLS なし、ローテーションまで audit が無制限に増える risk を明示して引き受ける。token 失効は server 再起動によるローテーションで行う。
