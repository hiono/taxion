# 04 — Roadmap

Phases: Foundation → Core → Local → Adapter → Server → Platform → Future.

Checked items record completed work at that phase; they are not a current
feature inventory. In particular, the original read-only Web and
localhost-only API scopes were extended later. Issues hold detailed
implementation results; the reference documents linked from
[README](../README.md) describe current behavior.

- [x] Foundation / `bootstrap` — repo, CI, charter, guardrails.
- [x] Core / `core-conformance` — ordered Sequence (CHK-002), JSON Ledger (LED-001).
- [x] Local / `cli-ux` — `task list`, `dispatch list`, JSON output, `--repo` gate (REP-001).
- [x] Adapter / `runtime-wiring` — in-process local Runtime, fail-closed.
- [x] Server / `sqlite-ledger` — sqlite store + outbox, pure-Go (ADR-0009).
- [x] Platform / `acp-spike` — minimal ACP v1 server over stdio (dispatch/review).
- [x] Platform / `web-slice` — read-only Task Graph view (`web/`, TS, zero-dep).
- [x] Future / `language-split-eval` — evaluated, deferred until core freeze (ADR-0012).
- [x] M2 / `http-read-api` (#9) — read-only HTTP API over the ledger (GET only, localhost).
- [x] M2 / `web-api-client` (#10) — web reads via `taxion serve`, 502 fail-closed.
- [x] M2 / `wire-freeze` (#11) — schema doc (05) + CI key pinning + ADR-0012 freeze note.
- [x] M3 / `proc-runtime` (#12) — process-backed Runtime, worker process supervision (stdlib only).
- [x] M3 / `acp-methods` (#13) — explicit taxion/dispatch + taxion/review, convention deprecated.
- [x] M3 / `split-reeval-2` (#14) — ADR-0013: DEFER again, new triggers recorded.
- [x] M4 / `write-api` (#15) — POST tasks/dispatches/reviews, CLI-identical guards.
- [x] M4 / `web-console` (#16) — task/dispatch/review forms + POST passthrough, 502 fail-closed.
- [x] M4 / `adr-revisits` (#17) — ADR-0011 amended, ADR-0014 stay-split-free, schema write section.
- [x] M5 / `token-auth` (#22) — Bearer gate, gen-token, web forwarding, loopback refusal.
- [x] M5 / `audit-trail` (#23) — append-only JSONL of mutations + denials, operator rotates.
- [x] M5 / `public-prep` (#24) — ADR-0015 security model, history scan clean, public release.
- [x] M5 / `mode-switch` (#25) — F-lite: --mode loopback|remote, ADR-0016, IdP deferred.
- [x] M6 / `memory-model` (#27) — ADR-0017 layers×ranges×promotion, docs/08.
- [x] M6 / `guideline-promotion` (#29) — configurable warning threshold,
  explicit structured approval, and shared dispatch checks (MEM-001, ADR-0019).
- [x] M7 / `memory-export-and-scope` (#39–#40) — sanitized stdout-only
  export and explicit global writes; current commands are in docs/08.
- [x] M8 / `server-hardening` (#41–#43) — self-asserted operator labels,
  default five-failure lockout, and audit-chain verification with documented limits.
- [x] Future / `browser-terminal-eval` (#21) — approach and shell-access threat
  model recorded in ADR-0018. Implementation remains deferred; use SSH/editor
  access in docs/06 Pattern D. A terminal implementation needs its own Issue
  and browser-user authentication, not only the upstream M5 token.
- [ ] Adapter / `copilot-sdk-eval` — Go SDK の対応確認と adapter 実装は
  下記 M11 の #49〜#51 で追跡する。SDK 採用と役割別 Session の目的は
  ADR-0020 に記録した。非対応は blocker として扱う。

Each step: small PR, `gofmt + vet + test`, sanitized docs, SHA-bound review.

## 階層化した Memory と自動実行の計画

[ADR-0020](adr/0020-layered-memory-and-role-sessions-for-orchestration.md)
を採択した実装方針とする。既存の完了記録は個別の成果であり、
この到達条件の達成を意味しない。
要件と検証条件は [Taxion の要件と到達条件](09-orchestration-requirements.md) に定義する。
Memory の保存・参照・指示への反映は M10〜M12 に含め、
M14 まで延期しない。各 Issue の前提と完了条件は
[#46](https://github.com/hiono/taxion/issues/46) から参照できる。

| 段階 | 対象 | Issue |
| --- | --- | --- |
| [M10](https://github.com/hiono/taxion/milestone/16) | 階層化した Memory、Task Graph、Task lost の防止、実行契約 | #46〜#48・#66 |
| [M11](https://github.com/hiono/taxion/milestone/17) | Go Copilot SDK、役割別 Session、system prompt・履歴の分離 | #49〜#51 |
| [M12](https://github.com/hiono/taxion/milestone/18) | Memory を指示に反映する自動実装・独立レビュー、実行主体・接続先・AI 契約の分離、commit 前の混入防止、Human の途中介入と状況・根拠の質問 | #52〜#54・#67・#69・#72〜#74 |
| [M13](https://github.com/hiono/taxion/milestone/19) | 再起動からの復旧、永続的な作業記録、外部基盤 adapter の分離・対応評価 | #55〜#57・#70 |
| [M14](https://github.com/hiono/taxion/milestone/20) | 仕様検索、Memory の移行、skills・tools 連携 | #58〜#61 |
| [M15](https://github.com/hiono/taxion/milestone/21) | ACP・Web、project 参加者の権限・契約・session 所有権、組合せ別の到達条件 | #62〜#63・#71 |
| [M16](https://github.com/hiono/taxion/milestone/22) | Planner 採用と Rust 導入の評価 | #64〜#65 |

[ADR-0024](adr/0024-human-handoff-control-and-side-questions.md) に基づき、
M12 の #73 は受け渡し保留・途中中断・指示案の改訂、#74 は本流を変更しない質問と
出典付き反映を実装する。#53 の自動進行と #67 の計画・Memory を再利用し、
#54 の実 SDK 受入れに両方を含める。M13 の #55 は再起動と競合の復旧、
M15 の #62・#63 は ACP・Web の共通操作と組合せ別の受入れを担当する。
質問の観測は保留機能から独立して着手し、本流への反映は #73 と接続する。

## project・開発基盤・AI 契約の独立した管理

[ADR-0023](adr/0023-independent-project-forge-runtime-and-configuration-contracts.md)
をこの分離と設定契約の採択判断とする。
初期の設定入力は YAML とし、TOML・JSON の設定入力は将来拡張とする。
Go の型付き設定を共通契約として維持する。
設定は固定 Global／Project YAML と環境変数を用い、
優先順位を環境変数 > Project > Global とする。
`config check` は任意ファイル指定 option を廃止し、
`.taxion/` 全体と cwd の実 `.env` の Git 状態を検査する。
共有 Memory の配置と runtime 接続は #69 の後続作業とする。
mise・CI は環境変数を注入し、
実 `.env` はローカルの未追跡・ignore 対象として扱う。
commit 前の index・秘密検査と明示 hook 導入は M12 #72、
未公開コミットと外部記録の公開前検査は M13 #56、
基盤側の保護と漏えい対応の評価は #70 で実施する。

Taxion の core は Task・Dispatch・判断・証拠・承認を管理し、
外部クライアント、開発基盤、AI runtime はそれぞれの adapter を通じて接続する。
この境界により、同じ実行契約を異なる基盤と利用者の組合せに適用する。

| 管理対象 | 独立して選択・確認する情報 |
| --- | --- |
| project と利用者 | 参加者、依頼者、承認者、実行・レビュー・停止の権限 |
| repository | provider、host、path、Git 操作主体とアクセス権 |
| 作業・レビュー記録 | 接続先、Issue・PR・MR 等の種別と ID、公開・更新の権限 |
| AI runtime | 認証主体、利用権、契約参照、費用帰属、取得可能な利用量と上限 |
| 実行環境 | 端末・共有サーバー・CI・worker、credential 保管先、実行の分離 |
| 再開と担当交代 | session 所有権、権限・契約の再検査、停止・引継ぎの承認 |

M12 #69 で共通契約と既存 GitHub＋Copilot の明示的な主体選択を実装し、
#54 の実 SDK 受入れで確認する。M13 #70 は GitHub・GitLab・Bitbucket の
対応能力と外部記録 adapter の境界を評価し、追加実装を承認された後続 Issue に分ける。
M15 #71 は複数開発者の project 参加と契約選択を管理し、
#63 で採択した基盤・利用者・契約・worker の組合せを検証する。

組合せには「GitLab repository＋外部協力者＋組織が許可した Copilot 契約＋専用 worker」
や「Bitbucket repository＋別サービスの作業管理＋個人の Copilot 契約」を含める。
実装済み能力、未確認の能力、後続計画を対応表に保持する。
契約上の費用負担者と実測できる利用量は別々に記録し、
提供側から取得できない課金情報は取得不能として明示する。

実装の順序は #69 → #54、#69・#56 → #70、
#69・#55・#62 → #71、#70・#71 → #63 とする。
再開時の主体・接続先・契約の照合は #55、
最初の GitHub 外部記録 adapter は #56、
入口ごとの利用者認証は #62 が担当する。
