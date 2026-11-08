# ADR-0075: 認可と復旧の判定を明示的な拒否条件に統一する

## Context

Controller は認可と復旧の判断に、許可する状態・操作の組合せを列挙する方式を使っている。この方式では、利用者が承認した操作でも、状態の組合せが列挙されていないと実行経路がなく、拒否される。

実際に、期限切れ後の停止済み Flow と未開始 Coder Dispatch を取り消す場面で、既存の取消し・整合回復操作は適用対象外だった。汎用 Dispatch 遷移も、未開始作業の取消しに worker result を要求して拒否した。利用者が取消しと新しい期限を承認しても、状態を安全に更新する Controller 経路がなかった。

許可判定と状態の整合性検査は別の責務である。未知の状態を無条件に許可する必要はないが、許可操作を列挙しきれないことだけを理由に、承認済みの復旧を拒否するべきではない。

## Decision

認可と復旧の入場判定を、明示的な拒否条件に基づく方式へ統一する。利用者の要求が保存済み承認の対象内にあり、明示的な拒否条件に当たらない場合は、許可操作の一覧にないことだけを理由に拒否しない。

Controller は、承認済み操作を現在の状態から適用できる型付き遷移へ変換する。特に未開始 Dispatch の取消しでは、worker の実行結果を要求せず、利用者の取消し承認と Controller が確認した未開始状態から取消し記録を作る。Task Graph、Flow、Dispatch、usage ledger の整合性は一つの Controller 操作で保つ。過去の指示、結果、承認、監査記録は書き換えない。

次の条件は明示的な拒否条件として維持する。

- 認証済み主体、対象 Task、repository、権限のいずれかが承認と一致しない。
- 要求が承認済み scope、実行枠、利用額上限、または固定 deadline を超える。期限切れ後の作業には新しい固定期限を含む後続承認を要求する。
- 正の利用額上限があるのに、必要な usage evidence が不明または不完全で、残枠を確認できない。
- 要求または保存記録が不正である、あるいは Graph・Dispatch・ledger の整合性を保てない。
- 活動中または配送状態が不明な実行を重複させる、成果を失わせる、または実行履歴を書き換える。
- credential の開示、承認されていないファイル・repository へのアクセス、未承認の外部操作につながる。

拒否条件に当たらないのに遷移を適用できない場合は、Controller の未実装経路として具体的な不足を報告する。一般的な `rejected` に置き換えず、同じ承認のまま使える安全な復旧操作を提供する。期限や予算の承認が必要な場合は、必要な差分を提示して承認を求める。

この決定は、認証だけで期限や予算を解除するものではない。認証は主体を確認し、承認は対象となる操作と上限を定める。稼働中 Flow の期限延長と、停止後の後続作成は、それぞれ既存の明示承認契約に従う。

## Alternatives considered

- 許可する状態・操作の組合せを列挙し続ける。列挙漏れが承認済み操作の停止につながるため採用しない。
- 状態遷移をすべて無条件に許可する。Graph・ledger の整合性、利用上限、認証、履歴の保護を損なうため採用しない。
- 認可を拒否条件で判断し、型・証拠・整合性の検査を別に維持する。承認済み操作を不必要に止めず、必須制約も保てるため採用する。

## Consequences

認可・復旧の拒否理由を、明示的な拒否条件と構造検証の失敗に分けて記録する。許可操作の列挙漏れを理由にした停止は、Controller に復旧遷移を追加して解消する。

未開始 Dispatch の取消し、停止済み Flow の期限再契約、および Graph と ledger の原子的な更新に対する回帰試験が必要になる。未知の状態や不整合を成功扱いすることは、この決定に含まれない。

## Security and operational impact

この決定は認可判定の既定値を変更するが、必須の安全条件を省略しない。主体と対象の照合、入力形式、usage evidence、固定上限、期限、credential redaction、append-only の履歴を維持する。新しい scope、権限、budget、deadline、公開許可は、従来どおり利用者の明示承認を要する。

## Compatibility

既存の Task、Flow、Dispatch、approval hash と監査履歴は変更しない。旧記録は旧版の契約で読み取り、復旧には新たな Controller 遷移と必要な後続承認を使う。

[ADR-0043](0043-approval-gates-and-read-only-lanes.md) の承認 gate、[ADR-0053](0053-enforce-approved-flow-stage-boundaries.md) の工程期限、[ADR-0057](0057-resolve-evidence-before-user-escalation.md) の証拠照合と差分承認は維持する。特に未知の schema や policy version の拒否は変更しない。この ADR が変更するのは、承認済みの認可・復旧操作を、許可一覧にないという理由だけで拒否する扱いである。

## Status

Accepted 2026-11-08 by explicit user decision. 未開始 Dispatch の取消について部分実装。
全認可入口の移行、独立レビューとリリースは未完了。

今回の実装対象は未開始 Dispatch の `workflow reconcile-stopped` と保存監査の照合、
承認済み baseline-only 入力の後続検証である。停止 code による許可一覧を初回 Coder と
未開始 Reviewer の分類から除去し、Graph schema、契約と入力、実行・配送の証拠を検査する。
取消は期限切れ後にも旧 Flow を変更せず行える。新たな実行の期限・予算承認を代替しない。
実行済み session の復旧や全認可入口の移行、独立レビュー、配布はこの変更の完了範囲に含めない。

### 検証と申し送り

2026-11-08 の合成試験では、初回 Coder の明示 snapshot と baseline-only 入力について、
`SAFETY_STOP`、`BUDGET_STOP`、`BUDGET_LIMIT`、`RECONTRACT` からの preview と取消を確認した。
旧期限の経過後も Flow・承認・予算・期限を維持し、Task と Dispatch と ledger を整合して保存する。
通常・成果物 Reviewer の `RECONTRACT` 取消、監査再読込み、元入力と後続 worktree の同一性も検査する。
未知の schema、ACK・開始・session・receipt・usage の存在、入力 drift、
Graph/ledger 不整合、静止確認と配送不明の受入れ不足は拒否する。

`go build ./...`、`go vet ./...`、`go test ./...`、文書リンク試験、
`gofmt` と `git diff --check` は成功。
`go test -race -timeout 30m ./internal/core ./internal/gitproof ./internal/store ./internal/runtime/controller ./cmd/taxion`
も成功。Controller package は約23分、CLI package は約3分半を要した。
公開識別子の repository 検査は、必要な保護済み policy がローカルにないため未完了。
policy を省略して合格にせず、公開前に正規の CI で検査する。
実サービス・既存 store への適用・新モデル実行は行っていない。

取消を適用した DB はこの監査を理解する版で扱う。旧版への rollback では現状を保全し、
適用前の整合バックアップを別配置で使う。[ADR-0054](0054-reconcile-stopped-dispatch-input.md)
の配送・利用量の不確実性と rollback 制約は維持する。
