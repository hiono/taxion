# 25. 承認したローカル仕様検索と役割別 tools

Date: 2026-10-12

## Status

Accepted

## Context

M14 は、仕様書の取得から出典確認・Coder・Reviewer への受け渡しまでを自動化する。
Human は、公開・合成資料をローカルで検索し、承認した合成本文を役割へ送信する方式を採択した。
M365 接続は、組織が利用環境と許可範囲を提示した場合に評価する条件付きの後続拡張として、
[#77](https://github.com/hiono/taxion/issues/77) に記録する。

M365 Copilot の検索、SharePoint の検索 API、組織の connector は、
それぞれ license・権限継承・出典取得・データ転送先を確認して採否を決める。
OfficeCLI の [公式 README](https://github.com/iOfficeAI/OfficeCLI) は構造付き Office 読取りと MCP を提供する。
今回の環境では Go 標準ライブラリと既存の `pdfminer` による exact corpus の読取りを採用する。
これにより、承認した原本と parser を固定して、ローカルで自動検索できる。

## Decision

検索方式は、承認済み PDF・DOCX・PPTX の literal AND 検索とする。
scope は exact asset、版、閲覧範囲、用途別利用許可と private evidence directory を保持する。
host が取得した引用を共通の仕様入力として Task に添付し、
Orchestrator の `taxion_search` と read-only な stdio MCP から同じ reader を利用する。
承認は信頼されたローカル管理者の出典付き判断記録として扱う。

roles の tools は、Task decision に保存した `taxion.tools.v1` の承認で選ぶ。
skills・skill-only plugins・固定 MCP request・RTK を内容 hash に結び付け、
SDK create/resume の設定と通常の role permission を照合する。
MCP executable は信頼された host code として承認する。
原本と failure は private evidence に保存し、RTK は成功 plain text の表示を圧縮する。

## Consequences

公開・合成資料の検索から仕様入力への自動受け渡しを、ローカル環境で検証できる。
権限不足・取得失敗・原本変更は error、検索結果なしは空配列として扱う。
引用・tool 原本の外部公開は、用途別の明示承認に従う。

通常 CI は無料回帰を実行し、有料 SDK 受入れは session 数と上限を Human が承認する。
SDK credit は soft cap、tokens は usage event による事後観測の停止条件である。
実 SDK 独立 Reviewer は #60 で受入れを完了した。
実装手順と受入れ結果は [仕様入力](../specification-input.md)、
tools の権限と証拠保持は [Runtime contract](../runtime-contract.md) に記録する。
この token 閾値による停止判断は、新しい実行について ADR-0036 で改訂した。

### 2026-10-15 の実装補足

文書 dogfood では、skill directory の接続だけでは実呼出しの証拠にならなかった。
承認済み skills / skill-only plugins がある role に限り `skill` tool を接続し、
Reviewer の通常の custom-tool permission も扱う。外部ファイルの読み取り、
shell、managed approval の境界は維持する。将来の Reviewer asset も
Coder の有料 turn 前に照合する。

OSS の通常テストは独自の合成 skill と一時 directory を使う。
特定の外部 skill・個人の home・認証・有料モデルを要求せず、
native plugin discovery は無料の明示 opt-in とする。
実 skill ロードと品質工程は別の受入れであり、Taxion 配下のログ、
検査結果と同一成果物の独立レビューを要求する。
指定の日本語文書用 skill は dogfood 入力であり、製品の固定依存にはしない。

## 標準探索と doctor に関する後続判断

上記の内容固定は通常利用の前提にしない。ユーザーは、Copilot 単体で使う skill が
Taxion では探索されず、専用 directory や hash 承認を要求される挙動を不親切と判断した。
通常は設定したバックエンドの標準探索を使い、未使用バックエンドの配置は混ぜない。
明示した `taxion.tools.v1` は内容固定・監査の opt-in として残す。

`taxion doctor` は実行と同じ worktree・CLI・ユーザー設定で検索先と検出結果、
無効化・上書き・利用不能の理由を示す。Copilot の native な一覧を優先し、
doctor だけの別の優先順位を作らない。診断でモデル・hooks・MCP を起動せず、
検出成功を実 skill 品質工程の成功とはしない。host 設定の信頼と、
担当の file permission・成果物検証を分けて説明する。

以前の「承認済み asset がある role に限る」は固定方式の条件として維持し、
通常の native skill 利用には適用しない。現行動作は [Runtime 契約](../runtime-contract.md)を参照する。

## References

- [Copilot の標準 skill 配置](https://docs.github.com/en/copilot/concepts/agents/about-agent-skills)
- [Copilot の標準指示探索](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions)
- [OfficeCLI](https://github.com/iOfficeAI/OfficeCLI)
- [pdfminer.six](https://pdfminersix.readthedocs.io/)
- [Agent Plugins](https://docs.github.com/en/copilot/concepts/agents/about-plugins)
- [MCP tools](https://modelcontextprotocol.io/specification/2024-11-05/server/tools)
- [RTK](https://github.com/rtk-ai/rtk)
