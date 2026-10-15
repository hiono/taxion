# プロジェクトの設立時の方針

この文書は、設立時の Foundation から Local までの対象範囲を記録したものです。現在の機能一覧ではありません。利用と開発の入口は [README](../../README.md)、追跡中の作業は[ロードマップ](roadmap.md)、現在の動作は README から参照する各仕様文書で確認してください。

## 設立の方針

Taxion は、非公開の先行プロジェクトから実装を移植せずに開発する clean-room プロジェクトです。コード、Git 履歴、Issue、名称、path、credential は移植しません。入力に使うのは、個人情報と出典を確認して一般化した要件、ガードレール、手順、失敗の知見だけです。

## 設立時の対象範囲

- SDK に依存しない管理処理（`internal/core`）：Task・Dispatch・Evidence・Checkpoint。
- Runtime 契約（`internal/runtime`）：`Start / Resume / Stop / Events`。
- ローカルを基本とする CLI：`init / task create / dispatch / review / resume`。
- DSP-001、SHA-001、CHK-001、ROLE-001 の適合テスト。

## 設立時の対象外

- Server・ACP・Web UI。Server と Platform の段階で扱う。
- Copilot SDK の採用。Adapter 段階の評価対象であり、この時点の採用決定ではない。
- 複数 provider の orchestration、fleet、workflow。
- provider 固有の前提や内部 host など、非公開リポジトリに固有の動作。

## 段階別の計画

計画には DayN 形式の番号ではなく段階名を使います。次の記述は設立時の計画です。

- Foundation：リポジトリ、CI、設立方針、ガードレール。この方針を記録した commit で完了。
- Core：Go の管理処理と適合テスト。
- Local：ローカル CLI の操作と `--repo` の識別確認。
- Adapter：plugin としての Runtime adapter の実現性評価。最初の候補は Go 用 Copilot SDK。
- Server：保存と API の設計。Task Graph と Ledger を判断の元にする。
- Platform：複数 client で使う ACP server と Web UI。
- Future：契約が安定した場合だけ Rust core への分割を検討する。候補は Rust の `taxion-server`・`taxion-acp`、Go の `taxion-runtime-copilot`、TS の `taxion-web`。

## 要件の追跡

公開する要件には、PRIV-xxx、ROLE-xxx、SHA-xxx、CHK-xxx、DSP-xxx、SDK-xxx の ID を付けます。[ガードレール](guardrails.md)を参照してください。
