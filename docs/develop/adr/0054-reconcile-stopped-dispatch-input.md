# 0054: 停止済み初回指示の取消と承認済み入力の継承

## 状態

採用。Issue #194。開発版の変更であり、配布と製品データへの適用は別途承認する。

## 背景

初回指示の発行後、実行開始の記録がないまま安全停止すると、通常取消はできても、承認済み入力を後続契約へ渡す根拠が不足する場合がある。未開始の再契約に見せるために停止 code を変更したり、以前からある入力 tree を新しい実装成果として記録したりする修復は採用しない。

## 決定

`workflow reconcile-stopped` を、引継ぎ保留・runner 不在の `STOPPED / SAFETY_STOP` と、初回の `ISSUED` Coder 1件に限定する。入力は保持済みの snapshot 形式とし、ACK・開始・session・結果・runtime receipt の存在、契約や入力 tree の変更、他の稼働中操作を拒否する。

人間は継続中の実行がないことの確認根拠と、配送が不明なままであることの受入れを提示する。記録がないことだけでは実行停止を証明できない。preview の完全ハッシュは保存状態と判断を束縛し、適用時にも照合する。Dispatch 取消、Task の `BLOCKED` 遷移、監査記録の追記を同じ transaction で保存する。元の Flow・停止理由・承認・期限・予算は変えない。

後続 Task には、元の承認済み入力という出典を付ける。同一 repository の別 worktree で baseline・対象 commit・tree・変更 path を再照合し、元と後続の両入力を検査する。`reconciliationHash` は提案の decision に含め、提案ハッシュと正式確認にも束縛する。確認時は取消後の正確な feedback target を要求する。元 Task を supersede し、新 Task を READY にするが、新しい指示やモデル実行は発行しない。

## 制約と旧版へ戻す手順

人間の確認は外部実行の機械的な停止証拠ではなく、取消も未配信・未実行・無課金の証明ではない。配送は `unknown`、利用量は `unavailable` のまま扱う。取消結果の既存必須欄 `startedSha` は instruction が期待する開始 SHA であり、実際の実行開始観測ではない。実行開始の各フィールドは未設定のまま保つ。

初回以外・commit 形式・session 記録あり・入力が変わった状態は対象外とする。新しい実行の承認束・役割委譲・工程計画・runtime gate は省略しない。一時的な repository API エラーの原因調査、DB の正本集約、新しい配布版の公開はこの変更に含めない。

監査記録の変更・削除、元停止記録の変更は拒否する。旧版でこの監査を含む DB を更新しない。旧版へ戻す際は現状と作業場所を保持し、適用前の整合バックアップを別配置で使う。

## 検証と申し送り

2026-10-27 に `go build ./...`、`go vet ./...`、`go test ./...` が成功した。`TestStoppedDispatchReconciliation` は安定した preview と状態不変、変更ハッシュの拒否、取消と保留の保存、正規化保存の読戻し、監査と旧停止の改変拒否、配送不明の保持、同一入力の後続承認を検査する。欠けた人間の確認、開始・session・receipt、tree・path・worktree・対象の不一致、重複適用、競合する後続承認も拒否した。

次の対象限定 race 検証は成功した。store と Git 検査の新経路は controller の統合試験から呼び出す。全体 race 検証の成功は主張しない。

```sh
go test -race ./internal/runtime/controller ./internal/store ./internal/gitproof ./cmd/taxion \
  -run 'Test(StoppedDispatchReconciliation|Intake|Reapproval|BudgetStoppedFlowContinues|BudgetContinuation|SafetyStopContinuation|SavedReviewAcceptance|ReadySkill|UsageGroups)' -count=1
```

独立した読取り専用コードレビューで重大な指摘はなかった。最初の全体試験では、CLI コマンド数を固定した既存試験が新コマンド追加を拒否したため、期待数とカテゴリの上限を1件分更新し、新コマンドの表示も必須にして全体試験を再実行した。補完は実 CLI から再生成した。管理スキルは新しい復旧契約を含む 1.3.5 とし、製品側への更新はまだ行っていない。

Issue #194 の開発 branch は `fix/stopped-dispatch-reconciliation`。製品の DB・旧 Flow・部分成果は操作しておらず、有料実行も追加配布も行っていない。公開版での実操作には、配布と対象操作の別途承認が必要である。
