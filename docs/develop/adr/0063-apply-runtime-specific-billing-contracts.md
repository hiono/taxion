# 63. Runtime・利用枠とTask契約に応じて残量確認を選ぶ

Date: 2026-11-01

## Context

Taxion の開発動機は、オーケストレーションによるモデル消費を抑え、既存 Coding Agent の使い分けと、人間がコピーしていた指示・成果の受け渡しを自動化することにある。外側の相談役から Copilot CLI や OpenCode の Coder・Reviewer へ依頼する構成も含む。Taxion 自体を新しい Coding Agent として作り直すことや、管理工程を増やすことが目的ではない。

Copilot の Fusion は提供元によるモデル選択・実行方式であり、常に最安とは保証できない。OpenCode は実行 runtime であり、接続する provider とモデルによって Free、Go、その他の契約を使う。したがって runtime 名だけでは費用も必要な残量確認も決まらない。

ADR-0051 は、提供元の上限停止へ委ねる `provider` と、アカウント残量を監視する `account` を既に分離した。しかし操作時には、一律の「残量不明なら停止」が再導入され、Copilot の既存 quota 読取りを使わず転記を求めた。ADR-0060 に残る「有料月次枠なら必ず事前残量確認」という記述も、Task ごとの選択を妨げていた。

## Decision

共通の課金判定は ADR-0051 のまま維持し、必要な観測は選択した runtime、provider、アカウント、モデル、利用枠と Task の承認済み契約から決める。全 runtime に数値残量を必須とする新しい規則は作らない。手順の正本は[課金ガイド](../../userguide/billing.md#runtimeと利用枠ごとの選択)とする。

Copilot は、`account` または別途承認された残量必須条件がある場合、既存の SDK account quota 読取りを使う。承認された非推論観測で解決できる情報を利用者へ繰り返し転記させない。quota 読取りが可能であっても、既存の `provider` 契約へ数値必須条件を追加しない。

OpenCode は Free と Go を区別する。確認済み Free モデルに Go の残量を要求しない。Go は Task ごとに、事前数値確認を必須とするか、追加課金なしの provider 上限停止へ委ねるかを明示する。前者は必要な観測がなければ停止する。後者は既存の4条件を確認・承認した場合に限り、数値残量不明だけでは停止しない。有料・無料を問わず、未知の経路や fallback を無償と推定しない。

利用量、提供元の契約残量、利用資格・容量、Proxy 内予算は別々に記録・解釈する。取得元・対象アカウント・resource・単位・期間・時刻・完全性を保持し、Copilot request 枠、session AI credit、token、Go の割合を推測で換算しない。外側の操作担当の利用枠と、Taxion が起動する Dispatch の利用枠も分ける。

LiteLLM は model API の会計・独自予算の候補であり、Coding Agent runtime の接続契約の代替ではない。LiteLLM の Copilot provider 対応だけでは契約残量の取得を保証できず、Proxy 外の利用も Proxy の spend には含まれない。導入と自動費用最適化 scheduler は別途判断し、今回の方針から必須機能へ昇格させない。

安さだけで実行先を決めず、必須検証と同一対象の独立レビューを完了条件として維持する。費用の比較には再試行・修正・レビューも含め、通貨や課金単位が異なる値は無理に合算しない。必要な指示・成果・証拠を受け渡し、全会話の再送や計画の重複生成を避ける。

## Alternatives considered

全 runtime で残量を必須にする案は採らない。数値の取得能力と追加課金防止を混同し、承認済み provider-stop や Free 利用を不必要に止めるからである。逆に、全契約を provider 停止へ委ねる案も採らない。承認した残量下限や利用量上限を守る条件を失うからである。

LiteLLM へ全面置換する案は採らない。モデル API の集計だけでは、既存 SDK/ACP の session、tool permission、成果保存、provider の契約残量を代替できない。自動分類・最安選択は今回実装せず、まず既存契約と観測の利用を一貫させる。

## Consequences

操作担当は保存済み契約と既存証拠から次の認可済み操作を選び、技術的な取得経路を使わずに残量入力や再承認へ戻さない。人間へ返すのは、新しい利用枠・課金方式・予算など未解決の実質的な選択だけとする。API の存在は利用可能な CLI の存在やバックエンド別受入れの完了を意味しない。

OpenCode Go の数値確認が必須で、利用可能な観測経路がない場合は停止する。`provider` は月内配分や進行中 call の厳密な費用上限を保証しない。事後 usage しか得られない制限を、提供元が call 前に拒否する hard ceiling と説明しない。

## Security and operational impact

課金方式は権限、期限、実行数、修正数、検証、独立レビューを緩和しない。明示的な正の利用額上限と別途承認した残量必須条件も維持する。提供元拒否・取消・通信終了・残量取得失敗を区別し、原因未取得や使用量不明をゼロ・無料・成功にしない。自動再送、有料 fallback、超過課金の利用、上限引上げを追加承認なしに行わず、停止時は途中成果と履歴を保持する。

## Compatibility

既存の `billing` 条件と `budgetGuidance` を使い、新しい設定キー、DB migration、SDK 更新は追加しない。保存済み Task/Dispatch/Flow の承認・上限・停止理由を再解釈せず、条件変更には既存の停止・照合・新契約承認を使う。ADR-0060 の有料枠に関する一律事前確認だけを本決定で改訂し、最終稼働日の既承認作業を進める方針は維持する。

## Status

v0.7.21-rc.9 では、外側の操作担当向けに `runtime quota --account ACCOUNT --approve-account-read` を追加する。既存の選択アカウント SDK 読取りを共有し、Task や Dispatch の作成、worker の課金方式変更を要求しない。`ready` と管理スキル1.3.15は、全体残量・承認済み追加上限・次の作業単位・停止余裕から継続を判断する。厳密な session 別帰属や専用会計エンジンを前提にしない。同じ単位のアカウント使用量増分を保守的な上限として使い、取得済みの値を利用者へ再入力させない。外側と worker の予算の区別、明示上限と取得不能時の停止は維持する。

候補 CLI のログイン済みアカウント読取りを、存在しない store を明示して実施し、workspace 作成なしで native quota と `executionAuthorized:false` が返ることを確認した。これは非推論 API の実接続確認であり、モデル実行や OpenCode の契約受入れではない。全 Go test、vet、build と対象経路の race test、独立した code／公開文レビューを実施した。配布の完了は公開 RC の workflow とダウンロードした実物の `ready`・help・スキル整合・残量読取りで確認し、結果を #234 に残す。

Accepted. 2026-11-01 の利用者判断で runtime/provider 別の扱いと OpenCode Go の Task 別指定を採択した。今回の変更は文書と追跡の改善であり、runtime 動作の変更や新しいモデル実行を含まない。既存の課金判定・Copilot quota adapter を再利用し、案内・操作経路・バックエンド別受入れの残る改善は [Issue #234](https://github.com/hiono/taxion/issues/234) で追跡する。Fusion の実装は #216、OpenCode 診断と実証は #231、呼出し追跡は #227、品質付き費用評価は #90 を参照する。

PR #235 後の開発版では、保存済み `account` Dispatch 向けの `runtime quota --dispatch ID` を追加した。既存の Copilot SDK と Controller 検査を再利用し、モデルを呼ばずに観測する。`ready`・help・管理スキル1.3.14には Copilot／OpenCode Free／Go、Task 別の数値必須条件、外側の操作担当の利用枠との分離を反映する。これは OpenCode account quota や Free／Go 実契約での停止動作を実装・実証したものではなく、バックエンド別受入れは #234 に残す。

同変更では `go test ./...`、`go vet ./...`、`go build ./...` と変更経路の race test を実施した。`TestObserveAccountQuotaWithoutSessionOrWorkspaceMutation` は成功・取得不能・鮮度切れ・利用拒否・非 account 契約・読取り中の Dispatch 変更を確認し、session 開始・送信・再接続と workspace 変更がないことを検査する。独立レビューで通常の workspace open による作成・migration を指摘され、既存 `OpenReadOnly` に変更し、`TestRuntimeQuotaDoesNotInitializeWorkspace` を追加した。SDK protocol fixture と合成検査は実契約でのモデル利用受入れと区別する。

文書のローカルリンク試験と公開文・path・secret-pattern の検査を実施した。ローカルでは非公開識別子一覧による照合を実施していないため、その成功を主張しない。これらの文書検査は実契約での上限停止やモデル品質の受入れ証拠ではない。最終 commit の必須検査は対応 PR を参照する。

## References

保存契約の表示に関する後続は [ADR-0069](0069-show-saved-task-billing-policy.md) と
[課金判断の確認記録](../billing-validation.md)を参照する。
`config check --billing --task ID`／`--dispatch ID` は保存方式を表示するが、
OpenCode の残量取得能力や実契約の受入れを追加するものではない。

- [ADR-0051](0051-centralize-declared-billing-conditions.md): 共通の課金判定
- [ADR-0059](0059-treat-copilot-fusion-as-provider-routing.md): Fusion 対応
- [ADR-0060](0060-use-remaining-plan-quota-on-final-workday.md): 最終稼働日の利用枠
- [ADR-0040](0040-constrain-goal-oriented-efficiency-by-quality-evidence.md): 品質条件付き費用評価
- [Copilot SDK usage and billing](https://docs.github.com/en/copilot/how-tos/copilot-sdk/features/usage-and-billing)
- [OpenCode Go](https://opencode.ai/ja/go): Free モデルと Go の rolling-window・月次制限
- [LiteLLM GitHub Copilot provider](https://docs.litellm.ai/docs/providers/github_copilot)
- [LiteLLM spend tracking](https://docs.litellm.ai/docs/proxy/cost_tracking)
