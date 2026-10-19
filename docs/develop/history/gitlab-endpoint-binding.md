# GitLab endpoint binding の修正履歴

Date: 2026-10-20

- Issue: [#105](https://github.com/hiono/taxion/issues/105)
- Milestone: [#31](https://github.com/hiono/taxion/milestone/31)
- Target release: v0.6.0
- Decision: [ADR-0042](../adr/0042-gitlab-endpoint-binding-separates-api-clone-and-ssh-identities.md)

この文書は、判断が変わった理由と最終状態を記録する。Revision ごとの逐語の
やり取りは残さない。

## v0.5.0 の legacy host/path モデル

v0.5.0 は repository を `repository.host` と `repository.path` で表していた。
Git remote から repository identity を作るときは URL の path 全体を使い、scheme の
ない `scp` 形式では最初の `:` より後ろを使っていた。この方式は、GitLab が
reverse proxy 配下の subpath で公開され、SSH が標準外の port を使う構成を表現
できない。

## 問題の発見と再現

対象環境は、API が `https://gitlab.example.test/git/api/v4`、HTTPS clone が
`https://gitlab.example.test/git/group/project.git`、SSH clone が
`ssh://git@gitlab.example.test:2222/group/project.git` だった。

API と glab は動作した。SSH と HTTPS の `git ls-remote` は同じ `HEAD` を返した。
それでも Taxion の snapshot binding は `Git repository path differs` で停止した。
同じ repository を二つとして数えていた。

## 根本原因

1. identity を URL の path 全体から作るため、clone prefix の `/git` が namespace の
   一部として読まれる。
2. SSH の port を host 比較に含めるため、`:2222` が host の一部として扱われる。
3. 設定に API base、Web base、clone prefix、SSH host、SSH port、provider の
   project identity を分けて持つ場所がない。
4. 安全な origin を再構成する経路が clone prefix を落とすため、snapshot の origin
   照合が一致しない。

prefix を推定で取り除く方法は使えない。`/git` は namespace の一部にもなり得るため、
`https://gitlab.example.test/git/git/group/project.git` のような URL では prefix の
個数を決められない。

## 設計 Revision の変遷と最終判断

- Revision 1 は短縮した SHA の証拠と最小の `endpoints` 案で提案し、受け入れられ
  なかった。evidence の完全性、identity の種類の分離、同一 API 応答の利用、path
  segment 単位の prefix 照合、`scp` 形式の回帰、旧 binary での実測、既存 code と
  衝突しない診断 code、実行可能な単位の上限が要求された。
- Revision 2 で、完全な 40 桁 SHA、別々の identity 型、同一 API 応答の5項目、
  path segment の照合、`scp` 形式の扱いを反映した。
- Revision 3 で、S5 を必須にして fail closed とし、`projectId` を実測値で固定し、
  host と path の大文字小文字の扱いを分け、snapshot の version 契約と実行上限を
  定めた。
- Revision 4 で、v2 snapshot の保存値を正本とし、再検証を stage A と stage B に
  分け、drift の診断 code として REP-007 を追加した。version 1 の snapshot は
  従来の検証器だけを使い、migration は別 Task に分けた。
- Revision 5 で、`webBase` を必須にし、stage B の比較を優先順位を適用した後の
  実効値で行うと決めた。設定の採用元だけの違いは drift としない。
- 最終判断は Revision 5 とする。

判断を変えた理由は、v0.5.0 の互換性を保ったまま subpath と標準外 port を表現する
には、prefix の推定をやめて設定に明示させ、照合の段階を分ける必要があったため。

## Revision 5 の最終契約

- `endpoints` の6項目（`apiBase`、`webBase`、`httpsCloneBase`、`sshHost`、
  `sshPort`、`projectId`）を単位で扱う。
- `apiBase`、`webBase`、`httpsCloneBase`、`sshHost` は同じ hostname を大文字小文字を
  区別せず共有し、3つの URL は同じ port を持つ。https だけを許す。
- 照合は S1〜S5 の5段階とし、失敗した段階を診断 code で示す。
  - S1（REP-002）: 設定の契約。
  - S2（REP-003）: Git remote の解析。先頭 segment が10進数だけの `scp` 形式、
    percent encoding、`.`、`..`、backslash、credential を拒否する。
  - S3（REP-004）: identity と `repository.path` の照合。path は大文字小文字を
    区別する。
  - S4（REP-005）: clone prefix、host、port の照合。標準外の port は `ssh://` URL
    だけが表現できる。
  - S5（REP-006）: 同じ API 応答の5項目の照合。未認証と到達不能も失敗とする。
- snapshot は version 1 と version 2 に分ける。version 1 は従来の検証器だけを使う。
  version 2 は保存値を正本として検証する。
- 再検証は stage A（内部整合性）と stage B（実効設定との比較）に分ける。実効値が
  異なる場合は REP-007 で停止し、自動変換・自動適用・現行設定による再 binding を
  行わない。
- 実行上限（Dispatch 数、session と Task 全体の AI credits と時間、修正回数）を
  契約で定め、上限を管理できない場合は新しい Dispatch を発行しない。

## 実装概要

- 設定: `endpoints` block と契約検査（REP-002）を追加し、layer 解決に組み込んだ。
  legacy 設定の JSON 形式は変えない。
- Git remote の解析: scheme、host、port、path を分離し（REP-003）、identity
  （REP-004）と prefix・host・port（REP-005）を照合する。
- 証拠の binding: dispatch の selection から `endpoints` を取り出し、remote の
  照合に使う。`endpoints` がない場合は従来の照合を続ける。
- snapshot: `endpoints` を記録したとき version 2 とし、origin の再構成に clone
  prefix を含める。version 1 の byte は変えない。
- 再検証: `VerifySnapshot` が stage A と stage B を行い、実効設定との drift を
  REP-007 で示す。
- forge: 同じ API 応答の5項目を照合する（REP-006）。未認証と到達不能も失敗にする。
- 診断: `taxion config check --sources` が identity mode と採用元を示す。診断に
  endpoint の値と credential を出力しない。

## 独立 Reviewer で解消した5項目

1. stage B が保存値の `repository.path` を実効設定と比較していなかった。
2. version 2 で保存値が欠けている場合に panic し得た。
3. S5 の意味上の失敗（account の不一致、解析できない応答、権限）が REP-006 に
   なっていなかった。
4. port 22 を承認済みとする場合に、port を省略した `ssh://` URL を拒否していた。
5. ADR の `ssh://` URL の user 制約の記述が曖昧だった。

## 現在の状態

- branch は `fix/gitlab-endpoint-binding`。実装は実装開始時点の `main` を基点に
  している。
- local 試験は gofmt 差分なし、go vet 指摘なし、`go test ./...` 1286 passed /
  20 packages、`go test -race` 205 passed。
- Reviewer の判定は APPROVE であり、重大な未解決の指摘はない。
- PR、CI、merge、tag、Release は未実施。GitHub 上には Issue と milestone だけが
  ある。CI の結果は PR 作成後に確認する。

## 対象外

- API、Web、HTTPS clone、SSH で hostname が異なる reverse proxy 構成。
- snapshot の endpoint metadata への暗号署名。
- 複数 repository を持つ version 2 snapshot。
- endpoint 形式への migration の自動化。
