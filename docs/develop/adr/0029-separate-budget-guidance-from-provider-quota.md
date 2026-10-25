# 29. 残予算の指示と provider quota の取得を分離する

Date: 2026-10-15

## Status

Accepted

新規 proposal の既定方式と実行中の監視は
[ADR-0049](0049-monitor-approved-account-quota-during-execution.md)で更新した。
以下の `flow` 既定と固定5%方式は当時の決定であり、保存済み契約の引継ぎには維持する。

設定、依頼ごとの選択、送信補足、送信記録と Copilot SDK quota 読取りを実装した。
合成 runtime と SDK protocol fixture に加え、選択済み実アカウントから
製品 adapter の読取りが成功することを確認した。session 作成・model 指示は0回。
実モデルが補足を使って作業配分を改善するか、新しい入口で coding・review を
完了できるかは未確認であり、読取り成功と区別する。新規実行の token 閾値停止は
後続の ADR-0036 で廃止した。

## Context

少額の予算では、実行を停止するだけでなく、残りの作業を優先し、途中の成果と
未確認事項を残すよう Coder に伝える必要がある。ただし、Taxion が承認した
Flow の残予算と provider アカウントの残量は対象も単位も異なる。
現在の Flow は観測した input / output tokens と期限を判定し、
Copilot の `MaxAiCredits` は session 単位の soft ceiling として設定している。
context window の `currentTokens` / `tokenLimit` は課金残量ではない。

固定した Copilot Go SDK v1.0.19 には、実験的な
`RPC.Account.GetQuota`、call ごとの `CopilotUsage.TotalNanoAiu` と
`AiCreditsStatus` がある。quota は種類別の snapshot であり、
すべての契約で同じ単位の絶対 credit 残量を取得できるとは確認していない。
Taxion はこれらを account 残量として取得・永続化していない。

`rpc.AccountQuotaSnapshot` は `EntitlementRequests`、`UsedRequests`、
`RemainingPercentage`、`ResetDate` と超過利用の条件を返す。
`premium_interactions` などの resource 名と返却された単位を保持し、
この requests / percentage を絶対 AI Credit 残量へ暗黙に換算しない。
`session.RPC.Usage.GetMetrics` には session 累積の `TotalNanoAiu` と
`AiCreditsStatus` があるが、account の残量ではない。累積値を
call ごとの usage receipt に再加算せず、数値の completeness を別に記録する。
両 API は固定 SDK の実験的 surface である。

公式 Billing REST API の `/organizations/{org}/settings/billing/ai_credit/usage` と
`/users/{username}/settings/billing/ai_credit/usage` は期間別の使用量を返す。
個人 endpoint は個人に直接課金されるプラン向けであり、組織・enterprise が
管理・課金するライセンスの利用量は個人 report に含まれない。
`grossQuantity` / `netQuantity` は残量 field ではない。同じ課金主体・期間・
単位・付与量・割引や超過の扱いを確認せず「付与量 − netQuantity」を
残量として採用しない。管理者用 report の権限を runtime に暗黙要求しない。
Copilot usage metrics の日次 report も、処理済み期間の利用統計であり、
送信直前の account 残量の代替にはしない。

非公開の `copilot_internal/user` を使う外部ツールの存在は、
Taxion の安定 API 契約の根拠とはしない。SDK quota の取得不能を理由に
internal endpoint、別 token、editor を装う header へ自動 fallback しない。
2026-10-15 の照合では公開資料とローカル SDK 型だけを読み、実 account の quota、
Billing API、internal endpoint への接続は行っていない。

OpenCode は利用する model provider と credential によって課金主体が変わる。
公式 SDK ガイドから、全 provider 共通の account 残量取得契約は確認できない。
Taxion の現在の runtime 設定は Copilot のみを受け付け、OpenCode adapter はない。
この差を core の Copilot 固有 field や、取得不能をゼロに変える処理で吸収しない。

## Decision

### 既定値と指示ごとの選択

`execution.budgetGuidance` に `off` / `flow` / `account` の既定値を
設ける。既存の Global → Project → 環境変数の優先順位を維持し、最後に
その実行依頼で明示された選択を適用する。指示ごとの未指定は解決済み既定値を
継承し、すべて未指定なら `flow` とする。不正な値は実行前に error にする。
config を使った一律固定か毎回必須選択かの二択にはしない。
この既定値は新規に承認する実行へ適用する。旧 Task / Dispatch に設定が
保存されていない場合は従来の無補足を維持し、旧 session への継続時に
新方式を使うなら明示した再承認を要求する。

| 選択 | 指示に含める情報 | account 外部照会 |
| --- | --- | --- |
| `off` | 残量の補足指示を付けない | しない |
| `flow` | 承認済み Flow の上限、観測済み利用量、残り、期限、未完了 role | しない |
| `account` | `flow` の情報と、選択した provider account の quota 観測 | 明示承認された読取りだけ |

この選択は助言の方式であり、上限・停止・利用量記録・scope・権限を変えない。
`off` でも予算検査は維持する。Flow がない実行で `flow` を選んだ場合は、
Flow 予算を「対象なし」と表示し、承認済み session ceiling があれば別項目で示す。
account 残量も、承認済み Task の予算を増やす根拠にはしない。

解決した方式、採用元、許可する指示ごとの選択範囲と account 読取り範囲を
Task / Dispatch の承認と runtime の設定 identity に結び付ける。
実行中の config 編集で方式を変えず、指示ごとの変更は承認済み範囲内に限る。
新たな account 読取り権限や予算を要求する変更は再承認する。
毎回、同じ選択を確認させる UI は要求しない。

`request --budget-guidance` は今回の Planner と実装 Task の選択を表示し、
`/plan` と `/approve` に結び付ける。`task track` と `conversation intake` も
新規 proposal の未指定値を固定設定から解決する。Task の `budgetGuidance` は
`mode`、`source`、`allowedModes`、`accountReadApproved` を持ち、Dispatch に
複製する。生成時の許可範囲は `off` / `flow`、選択が `account` のときだけ
`account` を加える。`runtime start|send --budget-guidance` はこの範囲内で
一回の送信を変更でき、許可範囲の追加は新しい Task 承認を必要とする。

### 観測値と停止判定

残量は Controller が保存済み receipt と provider の読取り結果から作る。
モデルに総量を調べさせたり、その推測値を採用したりしない。
`Send` の直前に別の補足として付け、追加の model turn は発行しない。
role の system prompt を毎回書き換えて session identity を変える方式は採らない。
動的な観測は送信ごとの receipt に保存し、固定設定の hash と区別する。

Controller の `budget-guidance` receipt は provider の event stream と区別した
stream ID に、観測値、実際の補足文、送信準備・送信終了・拒否を保存する。
`records show` と runtime の event 出力で確認する。Planner・質問・SDK検索の
隔離 turn も同じ観測処理を使い、結果 JSON と既存の private planning / search
記録に補足と観測を残す。Flow 外の隔離 turn は session soft credit ceiling と
期限を別項目にする。旧 Task の省略値は読取り・補足を追加しない。

Flow の残りは、同じ単位で既知の利用量を重複なく合算できる場合だけ算出する。
未知の call があれば「未確認」、上限超過なら超過量と停止理由を残す。
tokens、context capacity、requests、credits、通貨を根拠なく換算しない。
model multiplier や累積 counter を、そのまま call ごとの credit 使用量にしない。
会計単位と増分 / 累積の意味は adapter ごとに確認する。

指示には、承認済み acceptance を優先し、任意の追加作業や既に保存した調査の
反復を避け、未完了 role と復旧に必要な成果・証拠を残すことを含める。
残量が少ないことを理由に検査や独立レビューを省略して完成扱いしない。
「あと何 turn 完了できる」といった未検証の予測は含めない。
実行前の停止判断は Controller が行い、助言に従うかをモデルへ委ねない。
一 turn の超過を必ず防げる hard cap とも説明しない。

### provider ごとの観測契約

quota 観測は runtime-neutral な結果として、provider、account の非秘密識別子、
resource、適用範囲、unit、取得元、観測時刻、reset / expiry、既知の値を保持する。
状態は `available` / `unsupported` / `unavailable` / `stale` を区別する。
取得不能をゼロ、無制限、成功へ置き換えない。無制限は provider が明示した
entitlement として記録し、Taxion の実行予算には適用しない。

`account` の外部読取りは model 使用なし、追加課金や quota 購入なしで
行う。選択済み credential を使い、暗黙の別 account、Git 用 token、
全 account の列挙、非公開 billing endpoint の推測は使わない。
未承認の読取り要求や account 不一致は実行前に拒否する。

各送信で新しい観測を要求し、過去の snapshot を現在値として再利用しない。
照会は一回だけ、timeout は5秒と残りの承認済み実行時間の短い方とし、自動 retry はしない。
取得から送信まで60秒を超えた結果、または provider が明示した有効期限を過ぎた
結果は `stale` とする。reset 時刻は有効期限と同一視せず、対象課金期間を別に保持する。
これらの初期値は通常の読取り遅延でモデル実行を長く待たせず、次の指示への
古い残量の流用を防ぐために固定する。hard cap の保証には使わない。
新しい観測でも並行利用や課金反映の遅延があるため、送信時点の残量保証にはしない。

承認済みの照会が unsupported、通信失敗、期限切れの場合は、理由と状態を
利用者・送信内容・receipt に明示し、既存の Flow 制御の下で続行する。
これは成功-shaped fallback ではなく、account 観測が助言であるという契約である。
既知の `hasQuota=false` が provider の実行拒否を意味する場合は新規送信を止める。
quota 超過時の有料追加利用を暗黙に許可しない。
将来「残量取得成功を実行の必須条件にする」要件が出た場合は別の承認済み
実行 gate として扱い、`account` の意味を黙って変えない。

#155 で、`account` が取得に成功した場合の送信条件を追加した。取得失敗時に
送信を止めない契約は変えない。残り率が5%以下の場合と、その UTC 日の減少が
（その日最初の残り率 − 5%）÷ リセットまでの UTC 日数に達した場合は、新しい
Dispatch を送らない。5%は、Taxion の記録に含まれない親の Controller の利用分として
保守者が決めた暫定値であり、Task を動かさない日の残り率の減り方を実測して見直す。
残り率だけを比較するため、上記の単位の非換算は維持される。SDK の `ResetDate` には
観測時刻が入ることを 2026-10-24 に実測したため、リセット日時は認証情報の
`quota_reset_date_utc` から取得し、取得できない場合は日割りを行わない。

## Consequences

通常は外部照会なしで Flow の残りを伝えられ、操作者は必要な実行でだけ
account 情報を追加できる。provider ごとの機能差と取得失敗は可視化される。
quota の取得が成功しても支払主体・契約適格性や model turn の料金保証にはならない。

実装は、まず無料の合成 usage / quota と fake runtime で、全モード、未指定と優先順位、
取得不能・stale・account 不一致・単位違い・重複・並行観測・上限到達を検証する。
quota 観測のための model turn が0回であることと、stop-only 接続から照会しないことも
確認する。実 account の確認は、権限と実験的 API の版を確定した後の opt-in とする。
外部 account 残量の全面取得や OpenCode 対応まで実装済みとは主張しない。

現行動作と将来契約を [設定ガイド](../../userguide/configuration.md)、
[runtime 契約](../runtime-contract.md)、[利用者ガイド](../../userguide/user-guide.md)、
[開発者ガイド](../../../CONTRIBUTING.md) で分けて示す。
未実装分は Release の受入れ記録から消さず、実装・検証済みになるまで未確認とする。

## References

- 固定した `github.com/github/copilot-sdk/go` v1.0.19 の
  `rpc.ServerAccountAPI.GetQuota`、`rpc.AccountGetQuotaResult`、
  `rpc.AccountQuotaSnapshot`、`rpc.UsageAPI.GetMetrics`、
  `rpc.UsageGetMetricsResult`、`rpc.AssistantUsageData`、`rpc.SessionUsageInfoData`。
- [GitHub Billing usage REST API](https://docs.github.com/en/rest/billing/usage?apiVersion=2026-03-10)。
- [GitHub Copilot usage metrics REST API](https://docs.github.com/en/rest/copilot/copilot-usage-metrics?apiVersion=2026-03-10)。
- [OpenCode SDK の公開 API](https://opencode.ai/docs/sdk/)。
- [ADR-0003: SDK-neutral runtime](0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md)。
- [ADR-0023: 独立した設定と実行主体](0023-independent-project-forge-runtime-and-configuration-contracts.md)。
- [ADR-0028: 停止専用接続](0028-cancel-saved-sessions-without-execution-reconfiguration.md)。
