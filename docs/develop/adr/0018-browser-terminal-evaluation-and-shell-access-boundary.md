# 18. ブラウザ内ターミナルの方式評価と shell のアクセス境界

Date: 2026-10-10

## Status

Accepted

## Context

Issue #21 は、SSH トンネル越しに開発コンテナの shell をブラウザから操作する方式の検討を求めている。#26 では libghostty と xterm.js＋Go PTY を比較したが、ブラウザから shell に到達する場合の脅威モデルは未記録だった。この ADR は方式と実装前の条件を決める。ターミナルの実装は含まない。

現在の Web はサーバー側のトークンを Go API に代理送信し、ブラウザには渡さない。これは上流 API の認証であり、Web に接続した利用者の認証ではない。Ledger の task・dispatch・review を扱う M5 の構成を、そのまま shell の権限に拡張できない。

## Decision

将来実装する場合は xterm.js による表示と、Go 側の PTY・WebSocket bridge を第一候補とする。Linux 向けの creack/pty は `/dev/ptmx` と syscall で PTY を開く実装を持つため、Zig や cgo を必須にせず評価できる。PTY と WebSocket の依存は、その実装 Issue でバージョン、ライセンス、対応 OS、保守状況を確認して選ぶ。今回は追加しない。

libghostty の直接組み込みは採用しない。参照した現在の公式 README は C/Zig の組み込みライブラリとして紹介しており、#26 の「API が不安定」という記述を現在の事実として繰り返さない。ただし、今回確認した資料では Taxion のブラウザ表示に必要な配布方式を確定できず、既存の TypeScript 構成への組み込み評価も必要になる。xterm.js はブラウザ向けの表示と WebSocket 接続時の注意事項を公開しているため、今回の用途では先に評価する。

現行の操作は SSH とエディタを使う[自宅から開発用 Docker を使う手順](../../userguide/06-remote-access.md#自宅から開発用-docker-を使う場合)を維持する。ブラウザ内ターミナルの実装は保留とし、以下の境界と受入条件を具体化した新規 Issue で着手判断する。

## Threat model

想定する利用者は、自分の開発コンテナを操作する単一の operator である。保護対象はコンテナ内のソース、credentials、入力したコマンド、端末出力、接続先の shell 権限。悪意のある Web ページ、許可されていない接続元、端末出力に含まれる制御文字、切断後に残るプロセスを脅威として扱う。

| 境界・脅威 | 実装時に必要な対策 | 残る制約 |
| --- | --- | --- |
| ブラウザ→Web：任意の接続元による shell の操作 | ターミナルは既定で無効。利用者を認証してから短時間・単一用途の session を発行し、接続・再接続・操作ごとに所有者を検証する | 現在の上流トークン代理送信は利用者認証を代替しない |
| 別サイト→WebSocket：利用者の権限を使った接続 | 許可した Host・Origin のみ受け付け、Origin 欠落も拒否する。session 発行は認証付き POST とし、CSRF 対策を行う | loopback や SSH トンネルだけでは別サイトからの接続試行を防げない |
| 通信の盗聴・session の再利用 | 接続は SSH 転送か TLS/WSS の保護下に限定する。共有 token を URL・ログに入れず、session の失効と期限を検証する | ADR-0016 の trusted-network 向け plain HTTP は shell の公開条件に使わない |
| Web/Go bridge→コンテナ：権限昇格・対象の取り違え | サーバー設定で許可した非 root のユーザー・コンテナ・作業ディレクトリ・shell に固定する。ブラウザから任意の起動引数を受け付けない | shell はその OS ユーザーの権限を持つ。Ledger の guard は shell コマンドを制限しない |
| コンテナ→host：host や他コンテナの操作 | bridge に Docker socket、host の credentials、不要な mount を渡さない。root や privileged container を使用しない | コンテナだけで任意コードの完全な隔離を保証しない |
| 端末出力→ブラウザ：XSS・リンク・clipboard 操作 | 端末用ページを分離し、固定したローカル assets と CSP を使う。出力を HTML に挿入しない。clipboard・リンク・独自 escape handler は既定で無効とする | 同一ページの script や browser extension が侵害された場合、入力・出力を保護できない |
| 切断・過負荷→PTY：孤立プロセス・資源枯渇 | session 数、入力サイズ、buffer、idle 時間を制限する。切断・失効時にプロセス群を停止し、回収する。無制限の出力蓄積は行わない | 再接続を認める場合は猶予と所有者検証を別途定義する |
| 操作→audit：credentials やソースの漏出 | 接続・切断・拒否と対象・session ID の metadata を記録し、端末入出力を既定で記録しない | shared token と自己申告の X-Operator だけでは本人情報を証明できない |

利用者端末、コンテナの OS ユーザー、ブラウザ自体が侵害済みの場合の防御は、この構成の保証に含めない。入力と出力を扱うサーバーは機密情報にアクセスできるため、第三者の中継サービスを導入しない。

## Consequences

ADR-0011 の zero-dependency 方針は現在の Web に引き続き適用する。実装時にはブラウザ用 assets と PTY/WebSocket の依存追加を同 ADR の改正として記録する。言語分割は変更せず、Go がプロセス管理と認証・権限制御を担当し、TypeScript は表示と入力に限定する。実装時に ADR-0013/0014 の再評価条件も確認する。

実装の受入条件には、未認証接続、異なる Origin、別 session の操作、期限切れ・再利用、任意対象の指定を拒否する検査を含める。切断後のプロセス回収、buffer 上限、端末出力の安全な表示、audit に入出力が残らないことも確認する。これらが満たされるまでは shell の endpoint を公開しない。

Issue #21 の方式検討と脅威モデルの記録は、この ADR で完了する。ブラウザ内ターミナルが実装済みであることは意味しない。

## References

方式の資料確認であり、依存を導入した build や接続試験は行っていない。

- [xterm.js security guide](https://xtermjs.org/docs/guides/security/)：script の権限、端末出力、WebSocket の Origin・認証に関する注意事項。
- [Ghostty README](https://github.com/ghostty-org/ghostty/blob/246f702876b924a1cb7cade1e99274d1470302fc/README.md)：libghostty の C/Zig 向け位置づけ。
- [creack/pty Linux 実装](https://github.com/creack/pty/blob/9246436fffe85773950f49dcc73de333da5212e6/pty_linux.go)：PTY の開き方。README の例は production 向けではないと明記されている。
- [ADR-0016](0016-serve-modes-loopback-remote-with-token-opt-in-idp-delegation-deferred.md)：既存 serve mode と IdP 委譲の境界。
