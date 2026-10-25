# ADR-0049: 実行中も承認済み account quota を確認する

## Status

開発版で実装。合成 runtime の回帰検証と、モデルを呼ばない実 account の残量照会を行った。
Issue #155 全体の受入れ完了は意味しない。
正式配布、実 account での継続実行、効果測定の完了を意味しない。

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

Flow は既存の `BUDGET_STOP` と handoff 保留を使用し、session 停止と
partial Git evidence の取得も既存の経路を使う。停止失敗を成功へ置き換えない。
新しい Dispatch の当日開始率は同じ reset cycle の観測から選ぶ。
`off`・`flow` と quota 非対応 runtime に account 確認済みという意味を加えない。

## Alternatives

usage event だけの確認は無通信中に働かない。新たな監視 scheduler は設けず、
既存の1秒 ticker を使う。reset 後の増加を既存実行の追加予算とは解釈しない。

## Limitations and rollback

定期観測と停止要求は提供元での厳密な請求上限ではない。実 account での継続検証、
親 Controller の単独消費実測、独立した Planner・質問 session の監視は残る。
合成テストの成功で Issue #155 や実作業による検証を完了としない。
自動の Task-wide nano-AIU ceiling と実測による絶対量の controller 予約は未実装であり、
既存の明示 ceiling と割合による予約を維持する。request 枠、AI credits、
nano-AIU の単位を換算して不足した契約を補わない。
旧版は今回の継続監視を行わないため、切替前に稼働 session の停止を確認する。
保存観測や停止履歴は削除せず、停止済み Flow を書き換えて復帰させない。

## Validation

`go build ./...`、`go vet ./...`、`go test ./...` と、変更した
Controller の quota・guidance・停止テストの `go test -race` を実行した。
合成 runtime で無通信中と最終結果前の停止、予約率・日割り枠、取得失敗、
stale・identity・resource・cycle の変更、送信観測の欠落を確認する。
Flow の `BUDGET_STOP`、後続 role の抑止、未コミット file と partial Git evidence
の保持も回帰テストに含む。実 account の継続実行と独立レビューは未実施。
