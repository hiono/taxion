# 04 ロードマップ

段階は Foundation、Core、Local、Adapter、Server、Platform、Future の順とする。

checked の項目は各段階で完了した作業を記録し、現在の機能一覧を表すものではない。当初の read-only Web と localhost 限定 API は、その後に範囲を拡張した。詳細な実装結果は Issue、現在の動作は [README](../../README.md) から参照する仕様文書に記載する。[判断・Milestone 対応表](14-decision-traceability.md)で、段階名、ADR、Issue、現在の仕様を対応付ける。

- [x] Foundation / `bootstrap`：リポジトリ、CI、設立方針、ガードレール。
- [x] Core / `core-conformance`：順序付き Sequence（CHK-002）、JSON Ledger（LED-001）。
- [x] Local / `cli-ux`：`task list`、`dispatch list`、JSON 出力、`--repo` の確認（REP-001）。
- [x] Adapter / `runtime-wiring`：process 内の local Runtime。不明・失敗時は処理を許可しない。
- [x] Server / `sqlite-ledger`：pure-Go SQLite 保存と outbox（ADR-0009）。
- [x] Platform / `acp-spike`：stdio 上の最小 ACP v1 server（dispatch・review）。
- [x] Platform / `web-slice`：read-only Task Graph 表示（`web/`、TS、依存なし）。
- [x] Future / `language-split-eval`：評価し、core 固定まで延期（ADR-0012）。
- [x] M2 / `http-read-api` (#9)：Ledger の read-only HTTP API（GET のみ、localhost）。
- [x] M2 / `web-api-client` (#10)：Web が `taxion serve` 経由で読み取る。接続失敗は 502。
- [x] M2 / `wire-freeze` (#11)：schema 文書（05）、CI のキー固定、ADR-0012 の固定補足。
- [x] M3 / `proc-runtime` (#12)：process を使う Runtime と worker process の監督。標準ライブラリのみ。
- [x] M3 / `acp-methods` (#13)：明示的な taxion/dispatch・taxion/review。慣例形式は非推奨。
- [x] M3 / `split-reeval-2` (#14)：ADR-0013 で再び DEFER と判断し、新しい再検討条件を記録。
- [x] M4 / `write-api` (#15)：POST tasks・dispatches・reviews。CLI と同じ検査。
- [x] M4 / `web-console` (#16)：Task・Dispatch・review form と POST の転送。接続失敗は 502。
- [x] M4 / `adr-revisits` (#17)：ADR-0011 の改訂、ADR-0014 の分割しない判断、schema の書込み節。
- [x] M5 / `token-auth` (#22)：token 認証、gen-token、Web 転送、loopback 外公開の拒否。
- [x] M5 / `audit-trail` (#23)：変更・拒否の追記専用 JSONL。操作者がローテーションする。
- [x] M5 / `public-prep` (#24)：ADR-0015 の保護設計、履歴 scan の合格、外部公開。
- [x] M5 / `mode-switch` (#25)：F-lite の `--mode loopback|remote`、ADR-0016、IdP の延期。
- [x] M6 / `memory-model` (#27)：ADR-0017 の層・範囲・昇格。[Memory](../userguide/08-memory-model.md)に記載。
- [x] M6 / `guideline-promotion` (#29)：設定可能な警告閾値、明示的な構造化承認、共通 Dispatch 検査（MEM-001、ADR-0019）。
- [x] M7 / `memory-export-and-scope` (#39–#40)：sanitizer を使う標準出力だけの export と、明示的な global 書込み。現在の操作は [Memory](../userguide/08-memory-model.md)に記載。
- [x] M8 / `server-hardening` (#41–#43)：操作者の自己申告ラベル、既定5回失敗の lockout、制約を明記した audit chain 照合。
- [x] Future / `browser-terminal-eval` (#21)：手法と shell access の脅威モデルを ADR-0018 に記録。実装は延期中で、[SSH・editor 接続](../userguide/06-remote-access.md#自宅から開発用-docker-を使う場合)を使う。terminal 実装には別 Issue と browser 利用者の認証が必要で、接続先の M5 token だけでは足りない。
- [x] Adapter / `copilot-sdk-eval`：Go SDK の対応確認と adapter 実装は
  下記 M11 の #49〜#51 で追跡する。SDK 採用と役割別 Session の目的は
  ADR-0020 に記録した。非対応は blocker として扱う。

各作業は小さな PR とし、`gofmt + vet + test`、公開可能な文書、SHA を固定したレビューを確認する。

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
| [M15](https://github.com/hiono/taxion/milestone/21) | CLI と ACP サーバーを共通 controller に接続し、仕様検索から復旧・外部記録まで受け入れる | #62・#79 |
| [M16](https://github.com/hiono/taxion/milestone/22) | Planner 採用と Rust 導入の評価 | #64〜#65 |

M14 は 2026-10-13 に完了した。#60 の最終 Reviewer は保存済み Coder SHA を独立に承認し、公開 worktree と実 SDK 受入れも確認した。#58〜#61 はすべて closed である。

[ADR-0024](adr/0024-human-handoff-control-and-side-questions.md) に基づき、
M12 の #73 は受け渡し保留・途中中断・指示案の改訂、#74 は本流を変更しない質問と
出典付き反映を実装する。#53 の自動進行と #67 の計画・Memory を再利用し、
#54 の実 SDK 受入れに両方を含める。M13 の #55 は再起動と競合の復旧、
M15 の #62 は CLI 発行済み Dispatch の ACP 実行・観測を受け入れた。
[要件と到達条件](09-orchestration-requirements.md)の段階別判定条件にある仕様検索・予算停止後の復旧・最終報告・外部記録までの
end-to-end 受入れは #79 で追跡し、完了まで M15 を open とする。#63 は M15 の完了条件ではなく、
実作業が発生した場合に追加で確認する後続 Issue である。
追加 forge は M15 の完了条件に含めない。#75 は GitLab Issue API の実験結果を記録し、
MR を使う実環境が用意された場合に残りを検証する任意の後続 issue とする。
Web UI、複数利用者の project 権限・session 所有権は必要性が具体化した時点で再評価する。
Bitbucket は利用契約と検証環境がないため、M15 の対象に含めない。
質問の観測は保留機能から独立して着手し、本流への反映は #73 と接続する。

M12 の実 SDK 受入れを 2026-10-12 に完了した。
修正と新 SHA の独立再レビューについては、既存の実行証拠を保持した。
最終検証では、依頼受付、変更契約の再承認、Memory に基づく Planner 判断を確認した。
質問・追加調査で選択した事項の反映、独立承認、SDK の中断も確認した。
利用量と残る制約は [M12 の実 SDK 受入れ結果](09-orchestration-requirements.md#m12-の実-sdk-受入れ結果) を参照する。

M13 は実行・完了 checkpoint の再検証、同一 transaction の receipt・外部公開、
出典付き失敗分析、全未公開 commit の秘密検査を実装した。
SQLite と Git の障害回帰に加え、Human が承認した本文を新しい CLI で実 GitHub に公開し、
再送時に保存済み結果を再利用することを確認した。
手順と証拠は [実行記録](../userguide/11-execution-records.md)、
公式仕様と能力差は [開発基盤の対応評価](12-forge-capabilities.md) を参照する。
Bitbucket の adapter は #76 で後続候補として追跡する。
現在の CLI・ACP サーバーの基本利用は追加 forge adapter に依存しない。

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
| project と利用者 | 複数利用者で運用する場合の参加者、依頼者、承認者、実行・レビュー・停止の権限 |
| repository | provider、host、path、Git 操作主体とアクセス権 |
| 作業・レビュー記録 | 接続先、Issue・PR・MR 等の種別と ID、公開・更新の権限 |
| AI runtime | 認証主体、利用権、契約参照、費用帰属、取得可能な利用量と上限 |
| 実行環境 | 端末・共有サーバー・CI・worker、credential 保管先、実行の分離 |
| 再開と担当交代 | 複数利用者で運用する場合の session 所有権、権限・契約の再検査、停止・引継ぎの承認 |

M12 #69 で共通契約と既存 GitHub＋Copilot の明示的な主体選択を実装し、
#54 の実 SDK 受入れで確認する。M13 #70 は GitHub・GitLab・Bitbucket の
対応能力と外部記録 adapter の境界を評価し、追加実装を承認された後続 Issue に分ける。
複数利用者の project 参加と契約選択は #71 に後続候補として保持する。
GitLab Self-Managed の追加検証は #75 で追跡する。M15 の受入れに GitLab は要求しない。

GitLab・Bitbucket を含む基盤・利用者・契約・worker の組合せは、
具体的な利用計画が決まった時点で後続の受入れ範囲を定める。
Bitbucket は利用契約と検証環境がないため #76 に後続候補として保持する。
契約上の費用負担者と実測できる利用量は別々に記録し、
提供側から取得できない課金情報は取得不能として明示する。

実装の順序は #69 → #54、#69・#56 → #70、
#55・#57・#60・#61 → #62 → #79 とする。#63 は承認済みの実作業が発生した場合に行う
後続確認であり、M15 の完了条件ではない。#75 は M15 の完了をブロックしない。
再開時の主体・接続先・契約の照合は #55、
最初の GitHub 外部記録 adapter は #56、
CLI と ACP サーバーの共通 controller 接続は #62 が担当する。
