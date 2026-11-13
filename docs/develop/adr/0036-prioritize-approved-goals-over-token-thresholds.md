# 36. 観測不能な token 上限で承認済み目標を止めない

Date: 2026-10-16

## Status

Accepted

## Context

Taxion は model turn が始まる前に、その turn の token 消費量を正確に予測できない。
turn 終了後に利用量を確認して上限と比較する方式では、直前の turn が上限を超える
ことを防げず、承認済みの作業を途中で止める一方、超過分の料金は既に発生し得る。
推測した token 上限を、総費用の hard cap のように扱う根拠はない。

一方、現在取得できる数値は課金単位が揃っていない。SDK の呼出し別利用量は
nano-AIU、quota snapshot は resource 固有の request 数・残り割合、
session ceiling は別個の soft ceiling である。月間 AI Credit allowance と
同じ単位で取得できる残量がない限り、これらから残高や利用率を算出できない。

## Decision

新しい Task・Flow・隔離 turn では、観測 token 数を閾値とする実行停止 gate を
設けない。token 利用量は各 model call の観測記録として保存し、次の処理を止める
条件には使わない。期限、明示承認、repair 回数、契約・権限違反、安全停止、
provider による実行拒否は従来どおり適用する。SDK の session soft credit ceiling は
token 閾値と混同せず、account 全体の上限や請求額の保証として説明しない。

各 usage event の後に、取得できた call 別 nano-AIU と完全性状態を報告する。
利用可能な account allowance と usage が同じ課金単位・期間・課金主体であることを
検証できない場合、残高・使用率・残り率を `unavailable` と表示する。token、
request percentage、session ceiling、累積量から換算・推定しない。後日、同じ単位の
検証済み allowance が得られた場合に限り、対応する割合を算出できる。

既存 workspace の `maxTokens`、`BUDGET_LIMIT` と停止結果は履歴互換のため保持する。
それらは新しい実行の入力や停止条件として使わない。停止済みの旧 Flow とその
成果は変更せず、既存の承認済み継続手順で扱う。

## Consequences

Taxion は予測できない turn token 数で承認済みの目標を打ち切らない。利用者は
各 call の観測量を確認できる一方、account 残高・割合は同単位の allowance を取得
できない間は確認できない。料金が発生する操作には引き続き利用者の承認と既存の
SDK session ceiling を使うが、いずれも総請求額の厳密な停止保証にはならない。

回帰テストは、token 利用量が多くても新規 Flow が進むこと、call 別の使用量と
状態が報告されること、未検証の残高・割合が unavailable となること、旧停止記録が
読み取れることを確認する。これらは実 account の月間残量や実モデル受入れを証明しない。

## Alternatives

より小さい token threshold を選ぶ案は、turn ごとの予測誤差をなくさず、完了前の停止を
残すため採用しない。nano-AIU を quota request percentage や AI Credit に換算する案は、
同じ課金単位と対象期間を保証できないため採用しない。
