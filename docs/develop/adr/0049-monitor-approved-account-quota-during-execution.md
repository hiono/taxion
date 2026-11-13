# ADR-0049: 実行中も承認済み account quota を確認する

## Status

開発版で実装。合成 runtime の回帰検証と、モデルを呼ばない実 account の残量照会を行った。
Issue #155 全体の受入れ完了は意味しない。
正式配布、実 account での継続実行、効果測定の完了を意味しない。

新規既定値と申告した課金条件は
[ADR-0051](0051-centralize-declared-billing-conditions.md)で更新した。
以下の `account` 必須監視は、その方式を承認した保存済み契約と新規の明示選択で維持する。

## Context

送信前の quota 判定と利用量イベントでの残量表示だけでは、
長い tool 実行中や出力のない session が予約枠を消費し続ける。
残量表示の成功も、実行継続の認可を証明しない。

## Decision

承認済み `account` mode の Coder・Reviewer は、共通の event 監視で
約1分ごとと最終結果の採用前に選択 account を再取得する。
既存の5%予約率と UTC 日割り判定を再利用し、観測を receipt に保存する。
Copilot の残量が取得不能・古い・不完全な場合は fail-closed とする。
送信時の観測がない実行、既知の reset cycle・resource basis が変わった実行は止める。

Copilot の既定 quota 読取りは cache を返す場合があるため、SDK の account 一覧から
選択済み account と一致する資格情報を一意に照合し、その token を SDK の
quota request にだけ渡して再取得する。token は永続化・表示しない。
照合失敗や資格情報欠落では、他 account や cache へ fallback しない。
provider の `resetDate` は観測時刻として扱い、欠落・1分超の古さ・未来時刻を拒否する。
subscription の reset 日時は認証済み account の別 field から取得する。
ローカルの取得時刻で古い snapshot を新しいものに見せない。

新規利用の標準方式 `budgetGuidance.accountAllocation = "reset-daily"` では、
固定5%予約の代わりに、保存した開始残量をリセットまでの24時間区間で均等配分する。
開始時の区間数を N、現在の残区間数を M とし、残量下限を
開始残り率 × (M − 1) / N とする。最終24時間は下限0とし、残量ゼロの送信は拒否する。
月初・月中の早期枯渇を抑えつつ、失効直前の利用を許す人間の採択に基づく。
十分な controller 残量を実測で保証する方式とは区別する。
既存契約の配分は変えず、新しい field は承認の budget digest に含める。
同じ account・resource・unit・scope・reset の保存済み開始基準を使い、
残量増加や新しい Dispatch によって配分を作り直さない。
基準と下限は既存の receipt に保存する。改変された記録や reset 不明は拒否する。
独立した計画・質問・検索処理でも、無通信時に約1分ごとと最終回答の採用前に
残量を再取得する。送信時の観測は変更せず、以後の観測を結果の
`quotaObservations` に保持する。質問・計画の観測は session と内容 hash を結び付け、
既存の計画記録にも保存する。検索は既存の SDK 検索証跡に結果全体を保存する。
同じ初期基準で予約率・日割り下限・周期・資源の一致を確認し、不明・古い観測を
完了として採用しない。停止時は既存の独立 session の終了処理を使用する。
停止失敗は成功に置き換えず、元の残量停止エラーと併せて返す。
開始時に取得できた残量が途中で非対応になった場合も停止する。
非対応の時間制限のみの例外は、最初から非対応だった旧方式に限る。

新たに承認した `account`・`reset-daily` の Task で `maxNanoAiu` を省略した場合、
FlowBudget に `accountQuotaOnly: true` を保存する。nano-AIU の既定上限や換算値は
作らず、残量の取得・下限判定・継続監視を停止条件にする。正の明示上限は従来どおり守る。
日割り配分のない旧方式では省略を拒否し、実行時の `off`・`flow` への切替も拒否する。
native 使用量が不明でもこの方式では続行できるが、不明という記録は変更しない。
`request` で省略すると、既定・明示・設定の `account` モードを前提に
`reset-daily` を計画 Task と実行 Task に設定して表示・承認する。
保存 operation・承認 digest・再開照合にもこのモードを含める。
Planner 使用量を0の上限から引く処理は行わず、正の明示上限がある場合にだけ差し引く。

Flow は既存の `BUDGET_STOP` と handoff 保留を使用し、session 停止と
partial Git evidence の取得も既存の経路を使う。停止失敗を成功へ置き換えない。
新しい Dispatch の当日開始率は同じ reset cycle の観測から選ぶ。
`off`・`flow` と quota 非対応 runtime に account 確認済みという意味を加えない。

## Alternatives

新規 proposal の既定を旧 `flow` に残して新方式を追記するだけでは、
利用者に上限の手入力と方式選択を引き続き要求する。そのため新規生成時は
`account`・`reset-daily` を既定とし、設定・依頼別の明示選択を優先する。
残量読取り権限は proposal に含め、既定値の選択だけで実行を認可しない。
保存済み Task・Dispatch・request checkpoint は過去の資産としてそのまま引き継ぐ。
旧 checkpoint の option に方式指定がなくても、保存した Task policy を使い、
現在の既定値から再構成しない。新たな後続 Task では元の方式を明示して承認できる。
残量非対応 runtime を理由に新規の標準方式から旧方式へ自動で切り替える案は採用しない。

usage event だけの確認は無通信中に働かない。新たな監視 scheduler は設けず、
既存の1秒 ticker を使う。reset 後の増加を既存実行の追加予算とは解釈しない。

## Limitations and rollback

定期観測と停止要求は提供元での厳密な請求上限ではない。実 account での継続検証、
親 Controller の単独消費実測は行っていない。
合成テストの成功で実作業による検証を完了としない。
実測による絶対量の controller 予約は導入せず、承認された日割り残量下限を使用する。
request 枠、AI credits、
nano-AIU の単位を換算して不足した契約を補わない。
`reset-daily` の最終24時間には controller 用の残量も保証しない。
省略可能な契約は新たに承認した残量方式だけであり、既存上限の自動削除は行わない。
旧版は新しい承認 field を理解しないため、その契約を旧版で再利用しない。
旧版は今回の継続監視を行わないため、切替前に稼働 session の停止を確認する。
保存観測や停止履歴は削除せず、停止済み Flow を書き換えて復帰させない。

## Validation

`TestBudgetGuidanceSelectionApprovalAndLegacyShape` は新規の既定と明示選択を、
`TestRequestAccountQuotaOmitsNativeCeilingAndResumes` は方式・上限の指定なしでの
計画・実装・独立レビュー・再開を確認する。
`TestRequestOldDefaultCheckpointRetainsFlowPolicy` は旧既定値のまま保存した
checkpoint に新規の既定値を遡及適用せず、元の明示上限で再開することを確認する。

2026-10-25 の既定方式変更では、`go build ./...`、`go vet ./...`、
`go test ./...`、変更範囲の `go test -race` と文書の実行例・リンク検査が成功した。
合成 runtime による検証であり、実モデルや外部製品の作業は再実行していない。
開発版の変更であり、配布済み v0.7.15 の動作は変更しない。

`TestAccountQuotaOnly*`・`TestOmittedFlowCeiling*`・`TestExplicitFlowCeiling*` は、
省略の承認条件、未知の native 使用量、期限・回数制限、明示上限の維持、
監視を無効化する指定の拒否と残量停止を確認する。
`TestRequestAccountQuotaOmitsNativeCeilingAndResumes` は実モデルを使わず、
上限省略、未知の Planner 使用量、実装・独立レビュー・中断後の再開、
同じ成果 tree の照合と保存を通して確認する。

`TestIsolated*` は質問・Planner・検索の正常完了、最終結果直前の残量不足、
古い値、読取り失敗、周期・資源の変更、無通信時の監視、停止失敗、
初期観測の保持と計画記録の保存、`off`・`flow` の残量照会なしを確認する。
独立レビューで見つかった実行中の残量対応喪失による迂回を修正し、
開始時に取得できた値が非対応になる場合の拒否も確認する。
新しい下限方式の開始基準が実行中に作り直されないことも確認する。

`reset-daily` は `TestResetDaily*` で月初・月中の下限、区間境界、最終24時間の
0%下限とゼロ残量の拒否、増加後も同じ開始基準を使うことを確認する。
保存基準の欠落・改変・競合、reset 不明、残量非対応・取得不能・古い観測、
無制限契約の拒否、送信と実行中監視、変更時の再承認も確認する。
独立レビューで見つかった残量非対応時の時間制限のみへの切替を修正し、
新方式では拒否する回帰を追加した。全 Go 検証と対象を絞った race 検証を行う。

`go build ./...`、`go vet ./...`、`go test ./...` と、変更した
Controller の quota・guidance・停止テストの `go test -race` を実行した。
合成 runtime で無通信中と最終結果前の停止、予約率・日割り枠、取得失敗、
stale・identity・resource・cycle の変更、送信観測の欠落を確認する。
Flow の `BUDGET_STOP`、後続 role の抑止、未コミット file と partial Git evidence
の保持も回帰テストに含む。実 account の継続実行と独立レビューは未実施。
