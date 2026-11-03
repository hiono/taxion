# 課金契約と利用方針

工程計画の時間枠と、提供元の課金枠は別の制御です。`workflow plan` の `stageBudget` は後続工程の時間を確保しますが、提供元がクレジットを予約するわけではありません。`coderSessionCredits` と `reviewerSessionCredits` は各セッションのソフト上限で、全体の課金上限ではありません。未指定はゼロ、すなわち設定なしです。

任意の `reviewNanoAiu` と `repairNanoAiu` は、正の `maxNanoAiu` を承認した場合だけ使う内部の残額枠です。同じ設定の完全な保存使用量があれば、その実績に20%を加えた見積もりを提案します。値がゼロなら未見積もりで、無料や確保済みを意味しません。使用量イベントで監視するため、提供元が報告する前の超過まで防ぐものではありません。トークン・クレジット・`nano-AIU` は相互換算しません。

この文書は現在の課金条件と実行時の扱いを説明します。旧版での利用条件は当該リリースの記録を参照してください。
設定は、選択した実行先の契約条件を Taxion に申告するものです。
提供元の契約、超過課金、自動チャージを API で変更する機能ではありません。

## Runtimeと利用枠ごとの選択

残量確認の必要性は、runtime の名前だけでは決まりません。Copilot と OpenCode を同じ数値残量必須ルールで扱わず、提供元・アカウント・モデル・利用枠と、Task で承認した方式を確認します。外側の操作担当が使う Copilot の枠と、Taxion が起動する OpenCode の枠も別です。片方の残量で他方を許可・停止しません。

| 実行先・利用枠 | 選択と残量不明時の扱い |
| --- | --- |
| Copilot | `account` または別途承認した残量必須条件があれば、承認済みの SDK account quota 読取りを使います。取得不能なら停止します。`provider` 契約なら数値残量不明だけでは止めません。 |
| OpenCode の確認済み Free モデル | Go の残量を要求しません。無料経路、提供元の制限、有料 fallback がないことを確認し、承認した方式で実行します。無料利用制限に達した場合は停止し、自動再送・有料切替えを行いません。 |
| OpenCode Go | Task ごとに、事前残量確認を必須にするか、追加課金なしの提供元上限停止へ委ねるかを明示します。前者で必要な数値を取得できなければ停止します。後者は `provider` の4条件を確認・承認した場合に限り、数値残量不明だけでは止めません。 |
| その他の OpenCode 接続先 | OpenCode の名前から無料・Go契約とは推定せず、その接続 provider の課金条件を確認します。 |

OpenCode は実行 runtime、Free / Go は利用枠の区別です。Go には5時間・週・月の利用制限があり、月の残量だけでは現在利用可能とは判断できません。提供元が実際に拒否した場合は、その理由と観測できた範囲を保存します。数値を事前取得できる操作がない状態で、数値必須の契約を満たしたことにはできません。

既存の `billing` 条件、`execution.budgetGuidance` と Task 承認を使い、新しい設定キーは追加しません。上表は [ADR-0063](../develop/adr/0063-apply-runtime-specific-billing-contracts.md) の方針です。OpenCode の account quota 読取りは未対応であり、Task ごとの選択・案内・バックエンド別受入れに残る改善は同 ADR の追跡先で管理します。この文書の更新によって未対応機能が利用可能になるわけではありません。

数値確認が必要なときは、許可された既存の非推論観測を先に使い、取得元・アカウント・単位・対象期間・観測時刻を照合します。既存の観測で解決できる情報を利用者へ繰り返し転記させません。`taxion runtime quota --dispatch ID` は、保存済みの設定承認と `account` 方式を持つ active Dispatch の Copilot quota を、モデル session を作らずに照会します。任意アカウントの照会や OpenCode quota の取得ではありません。

LiteLLM は通過したモデル API リクエストの利用量・算定費用と独自予算を管理する候補です。Copilot provider 対応はありますが、その Proxy の残予算を Copilot の契約残量や Go の rolling allowance として使う根拠にはなりません。モデル API の接続を統一しても Coding Agent の session・ツール・権限・成果受け渡しは別です。Taxion への LiteLLM 導入と自動費用最適化 scheduler は未実装で、この方針で導入を決定しません。

外側の Copilot 操作担当は、既に許可された全体残量の観測があれば、それと承認済みの追加消費上限から次の作業単位を完了できるか判断します。厳密な call／session 別の課金帰属は前提にせず、同じ単位でのアカウント使用量の増分を保守的な消費上限として使えます。全体残量は全額を使う許可ではありません。停止・保存の余裕を取り、既に取得できる値を利用者へ再入力させません。

既存観測がなければ、読取りが許可されている外側のアカウントに `taxion runtime quota --account ACCOUNT --approve-account-read` を使えます。ログイン済み Copilot のアカウント一致を検査し、Dispatch・Task・workspace・モデル session を作らずに照会します。この flag は既存の読取り許可を操作へ明示するもので、毎回利用者へ再承認を要求する指示ではありません。worker の `provider` 契約とは別の観測であり、worker の方式を変更しません。取得不能・鮮度切れ・利用拒否は非ゼロ終了し、エラーをゼロ残量や実行許可へ読み替えません。

## 契約条件を設定する

既存の Global または Project の `config.yaml` に次を追加します。
`runtime.provider`・`runtime.account`・`runtime.contractRef` は、その条件が適用される
提供元・アカウント・契約参照として併せて確認してください。
OpenCode は接続するモデル提供元の条件を確認します。

```yaml
billing:
  contract: provider-limit
  overage: denied
  autoRecharge: disabled
  paidFallback: disabled
execution:
  budgetGuidance: provider
```

これは部分例です。既存の `execution` の他の項目を消さず、同じキーの下へ追加します。
新規提案では、この条件が既定になります。既定値も人間が承認する提案であり、
利用者の実際の契約を確認済みにするものではありません。

| 設定 | 値と意味 |
| --- | --- |
| `billing.contract` | `provider-limit`: 提供元の契約上の枠で利用が停止する申告。`metered`: 従量課金。`unknown`: 未確認。省略時は新規提案で `provider-limit`。 |
| `billing.overage` | `denied`・`allowed`・`unknown`。超過課金の申告。省略時は `provider-limit` なら `denied`、それ以外は `unknown`。 |
| `billing.autoRecharge` | `disabled`・`enabled`・`unknown`。自動チャージの申告。新規提案の省略時は `disabled`。 |
| `billing.paidFallback` | `disabled`・`enabled`・`unknown`。別アカウント・別提供元への有料の自動切替えの申告。新規提案の省略時は `disabled`。 |

値は文字列です。通知だけの予算を「提供元で停止する」と申告しないでください。
超過課金の許可、自動チャージ、有料切替えのいずれかが有効・未確認なら、
`provider` 方式は拒否します。従量課金や条件未確認も同様です。
条件だけを設定して方式を省略した場合は `flow` を選び、正の利用額上限が必要です。
`allowed` や `enabled` の申告は Taxion に購入・切替えを許可する操作ではありません。

設定方法と優先順位は既存の設定と共通です。Global → Project → 環境変数の順で上書きし、
依頼ごとの `--budget-guidance` は方式だけを選びます。契約条件の検査は迂回できません。
環境変数は `TAXION_BILLING_CONTRACT`、`TAXION_BILLING_OVERAGE`、
`TAXION_BILLING_AUTO_RECHARGE`、`TAXION_BILLING_PAID_FALLBACK` です。
初回の不足項目は `init` または `doctor fix --set billing.overage=denied` などでも指定できます。
既存値を変える場合は採用元の設定ファイルを編集します。設定処理は既存値を勝手に置換しません。

```sh
taxion config check --billing
taxion config check --sources
```

`--billing` は新規提案用の解決済み条件、方式、適用先、設定元を JSON で表示します。
未指定条件は既定として解決され、設定元一覧にない項目は明示設定ではありません。
`verification` は `human-declaration-not-provider-verified` です。
この表示はローカル設定の検査だけで、残量照会、モデル起動、提供元への設定変更を行いません。
`--sources`・`--capabilities`・`bind` とは併用しません。

保存済み契約の方式を表示する場合は対象を明示します。

```sh
taxion config check --billing --task TASK_ID
taxion config check --billing --dispatch DISPATCH_ID
```

現在の設定を既存の承認照合で確認した後、Task または不変の Dispatch 指示の
`policy` を返します。Task で選んだ `account` を現在の新規提案用 `provider` に
置換しません。`contractBasis` は `saved-task`／`saved-dispatch`、対象なしなら
`new-proposal` です。保存契約の表示では、現在の設定元を保存ポリシーの由来と誤解しないよう
`sources` を省略し、`policy.source` は保存値を維持します。
現在の設定が保存承認と異なれば非ゼロ終了し、保存済みの条件も書き換えません。

対象には既存の承認照合と同じ有効状態が必要です。終端・停止済みの契約の参照には
`workflow show` などの保存表示を使います。旧契約に方式がなければ欠落として拒否し、
新規提案の既定値を補いません。DB・WAL・SHM の一時コピーから読み、元ファイルは変更しません。
残量・認証・契約資格を照会せず、モデルも起動しません。
`executionAuthorized:false` であり、表示成功は実行許可や残量取得成功ではありません。

OpenCode の Free／Go は `runtime.contractRef` に対応する操作者の契約確認から区別し、
参照名だけで資格を保証しません。Go の Task で数値必須の `account` を選んだ場合、
この表示は取得能力を追加しないため、未対応の残量観測を満たしたことにはできません。
確認済みの `provider` 契約なら数値残量だけを理由に停止せず、既存の4条件と別途指定した上限を維持します。

## 実行中の扱い

| 方式 | 外部残量照会 | 利用額上限の省略 |
| --- | --- | --- |
| `provider` | 自動では行わない。提供元の上限停止に任せる | 上記の4条件を含む提案を承認した場合だけ可能 |
| `account` | 開始前・実行中・最終結果採用前に必須。新規選択は `reset-daily` | 残量下限と継続監視を承認した場合だけ可能 |
| `flow` | 行わない。保存済みの期限・利用量を補足 | 不可。正の `maxNanoAiu` が必要 |
| `off` | 行わない。補足指示も付けない | 不可。正の `maxNanoAiu` が必要 |

提供元の上限停止と月内の利用配分は別です。`provider` は月内の残量を温存せず、
残量10%などの閾値も設けません。`account` は月内配分を厳守したい場合に明示的に選びます。
`runtime quota --dispatch ID` は保存済みの `account` 契約を照合して照会する明示操作です。`--system-prompt-file` は不要で、`--timeout` は既定5秒・最大5秒、再試行しません。停止済み Flow と期限切れ Flow は拒否します。観測可能な場合は quota と `executionAuthorized:false` を JSON で返し、取得不能・古い観測・利用拒否では非ゼロ終了します。Dispatch、承認、Flow、session は変更せず、実行時の検査も代替しません。`provider` 方式ではこの照会を要求せず、数値必須契約を満たせない場合に方式を自動変更することもありません。

`provider`／`provider-stop` では、現在残量が不明という理由だけで実行を止めたり、
利用者に credits の手入力を必須にしたりしません。ただし、明示した正の利用額上限や、
別途承認した「残量確認を必須とする」条件は維持します。提供元の利用資格検査と数値残量の
取得は別です。残量確認を追加するために、無断で `account` へ切り替えてはいけません。
この区別は `--help`、`ready` と現在の管理スキルにも明記します。インストール済みスキルは利用者の許可なく更新しません。
`config check --billing` は新規提案用の解決済み設定の表示であり、残量照会ではありません。
保存済み作業では、現在の設定だけでなく Task／Dispatch と Flow の課金契約を確認します。

`account` の残量照会は従来どおり5秒と実行残時間の短い方で打ち切り、再試行しません。
取得不能・未対応・古い観測・リセット不明は停止条件です。通常は1分ごとと最終回答採用前に
照合し、この方式の障害時動作を `provider` へ自動で切り替えることはありません。

`provider` は残量照会をしないため、そのタイムアウトを実行停止条件にしません。
提供元が実際にモデル利用を拒否した場合は通常の実行失敗・停止処理を行い、
結果や途中成果を保存します。別課金先へ自動で切り替えたり、停止した作業を再送したりしません。
停止・保存処理自体が失敗する可能性は残り、停止証跡がない状態を成功とは扱いません。

どの方式でも期限、実行数、修正回数、権限、検証、独立レビューを維持します。
正の利用額上限を明示した場合も維持し、使用量不明時はその上限の下で停止します。
Copilot の session 別 soft ceiling、call 別 nano-AIU、SDK の requests 残量は別単位です。
相互に換算せず、残量未取得をゼロ・無料・無制限と表示しません。

## 承認・移行・復旧

解決済みの課金条件は Task と Dispatch の指示、設定承認、実行設定に保存します。
新しい設定は新規提案だけに適用し、保存済みの `account`・`flow`・省略された旧契約、
正の利用額上限、期限、観測記録を変更しません。
課金方式や認証先を変える場合は既存実行を停止・照合し、新しい契約として承認します。
同じ課金条件のまま、稼働中の期限・正の利用額上限だけを正式に増やす場合は、
[停止前の追加承認](cli-reference.md#実行を維持した期限予算の延長)で同じ実行を維持します。
条件変更後も成功済み成果を再利用できるかは、既存の継続条件で別途確認します。

`provider` で利用額上限を省略した Flow・依頼には `budgetSource: provider-stop` を保存します。
旧 `accountQuotaOnly` は従来の意味のまま維持します。
新フィールドがない記録を読んだだけでは `provider` の権限を追加しません。
新しい課金条件・方式を含む store を v0.7.18 以前へ戻して再利用しないでください。
戻す場合は停止状態を確認したうえで、更新前に保存した整合したバックアップを使います。
停止履歴・承認・残量基準を削除する移行はしません。

人間の申告と SDK の表示が異なる場合、Taxion は申告を API による保証へ読み替えません。
`provider` は自動照会しないので、この不一致を自動発見する機能もありません。
実際の契約範囲、組織負担、並行利用、自動チャージ等は提供元の管理画面で確認してください。
請求額の保証、決済情報の管理、契約の購入・変更は対象外です。
