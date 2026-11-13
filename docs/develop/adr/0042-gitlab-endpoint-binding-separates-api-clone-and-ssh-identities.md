# 42. GitLab endpoint binding は API・clone・SSH の識別情報を分離する

Date: 2026-10-20

## Status

Accepted

## Context

GitLab の self-managed 環境では、reverse proxy の配下に GitLab を置き、API と
clone を別の URL prefix で公開することがある。対象環境では API が
`https://host/git/api/v4`、HTTPS clone が `https://host/git/<namespace>/<project>.git`、
SSH clone が `ssh://git@host:2222/<namespace>/<project>.git` になる。

v0.5.0 は `repository.host` と `repository.path` だけを持ち、Git remote から
repository の識別情報を URL の path 全体から作っていた。この方式では HTTPS の
`/git` prefix が namespace の一部として読まれ、SSH の `:2222` が host 比較に
含まれ、どちらも `Git repository path differs` または
`Git origin host differs from approved host` で停止する。

prefix を heuristic に取り除く方法は採れない。`/git` は namespace の一部でも
あり得るため、`https://host/git/git/<project>.git` のような URL で prefix の
個数を決められない。どの表記が prefix かを設定に書かせる必要がある。

この制約を解消するため、endpoint を個別に設定し、remote と API 応答の両方で照合する。

## Decision

設定に `endpoints` block を追加し、6 つの値を明示する。

- `apiBase`: API の URL。`/api/v4` で終わる https URL。
- `webBase`: Web UI の URL。https URL。
- `httpsCloneBase`: HTTPS clone の prefix。https URL。
- `sshHost`: SSH の hostname。port を含まない。
- `sshPort`: SSH の port。10 進数の 1〜65535。
- `projectId`: GitLab の数値 project ID。

`apiBase`・`webBase`・`httpsCloneBase`・`sshHost` は同じ hostname を大文字小文字を
区別せずに共有し、3 つの URL は同じ port を持つ。URL に credential・query・
fragment・percent encoding を含めない。違反は REP-002 として拒否する。

`endpoints` を書かない設定は `repository.host` と `repository.path` の従来動作を
続ける。6 項目は単位で扱い、一部だけを書いた解決結果は拒否する。

照合は5段階に分け、失敗した段階を診断 code で示す。

- S1 設定の検査（REP-002）
- S2 Git remote の解析（REP-003）
- S3 repository identity の照合（REP-004）
- S4 clone prefix・host・port の照合（REP-005）
- S5 API 応答による identity の照合（REP-006）

S5 は同じ API 応答の `id`・`path_with_namespace`・`ssh_url_to_repo`・
`http_url_to_repo`・`web_url` を同時に照合する。数値 ID だけ、名前だけ、
`HEAD` の SHA だけでは同一性を主張しない。API が未認証または到達不能な場合も
REP-006 として停止する。

remote の解析では、`ssh://` URL の port を明示値として扱い、`scp` 形式
(`git@host:path`) は port 22 として扱う。標準外の port は `ssh://` URL だけが
表現できる。port を省略した `ssh://` URL は 22 として扱う。path の先頭 segment が
10 進数だけの `scp` 形式は、port との区別が曖昧なため REP-003 で拒否する。
`ssh://` URL の password と `git` 以外の user は拒否する。

snapshot は version で検証器を分ける。version 1 は従来の検証器だけを使い、
書き換え・backfill・migration をしない。`endpoints` を記録した snapshot は
version 2 とし、保存値 (projectId・apiBase・webBase・httpsCloneBase・ssh.host・
ssh.port・repository.path・origin) を正本として検証する。version 3 以上は拒否する。

再検証は2段階にする。stage A は snapshot 内部の整合性だけを確認し、証拠を
書き換えない。stage B は保存値と、優先順位適用後の実効設定を比較する。設定の
採用元だけが異なり実効値が同じ場合は drift としない。実効値が異なる場合は
REP-007 として停止し、自動変換・自動適用・現行設定による再 binding を行わない。

## Consequences

`/git` prefix と `:2222` を持つ環境でも、namespace と host を分離して照合できる。
`repository.host` と `repository.path` だけの設定は変更なしで動き続ける。

`endpoints` を持つ snapshot は version 2 になり、値を理解しない binary では
未対応の証拠として残る。migration はこの判断に含めず、別 Task で扱う。
hostname が API と clone で異なる reverse proxy 構成は対象外とし、必要になった
時点で別の ADR を起こす。

## Security

診断は credential、token、認証 header、request body を出力しない。REP-002〜REP-007 の
メッセージには、失敗した段階と照合した対象の種類だけを含める。password を含む URL は
設定の検査でも照合でも拒否する。

URL の解析では、credential 付きの URL、`git` 以外の SSH user、password 付きの `ssh://`
URL を拒否する。percent encoding、`.`、`..`、backslash を拒否し、path の正規化で別の
repository を同一視できないようにする。path は大文字小文字を区別し、host は区別しない。

S5 は同じ API 応答の5項目を同時に照合する。数値 ID だけ、名前だけ、`HEAD` の SHA
だけでは同一性を主張しない。未認証または到達不能な API も失敗として停止する。

判定できない場合は停止する。v2 snapshot の自動変換、自動適用、現行設定による
再 binding を行わない。

## Compatibility and migration

`endpoints` を書かない設定と version 1 の snapshot は、従来の検証器と動作を変えない。
version 1 の snapshot は書き換えず、backfill も migration もしない。

`endpoints` を持つ設定は v0.5.0 では読めない。未知の field として拒否され、設定
ファイルは変更されない。rollback では binary と設定を同時に戻す。

`endpoints` を記録した snapshot は version 2 になる。version 2 を理解しない binary では
未対応の証拠として残り、削除も変換もしない。endpoint 形式への migration はこの判断に
含めず、別 Task で扱う。

設定 layer の解決順序と採用元の記録は既存の仕組みをそのまま使う。`endpoints` の値は
この版では環境変数で上書きしない。

## Out of scope

API、Web、HTTPS clone、SSH で hostname が異なる reverse proxy 構成は対象外とする。
必要になった時点で別の ADR を起こす。snapshot の endpoint metadata への暗号署名、
複数 repository を持つ version 2 snapshot、endpoint 形式への migration の自動化も
対象外とする。

## Alternatives

URL から prefix を推定する方法は、`/git` が namespace にもなり得るため採用しない。
provider ごとの URL テンプレートを code に埋め込む方法は、self-managed の prefix
を表現できないため採用しない。設定ファイルを複数に分ける方法は、既存の layer
解決と採用元の記録を二重にするため採用しない。既存 snapshot を endpoint 形式へ
書き換える方法は、過去の証拠の意味を変えるため採用しない。
