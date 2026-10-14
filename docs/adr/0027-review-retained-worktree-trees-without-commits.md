# 27. commit を作らない成果物の同一 tree レビュー

Date: 2026-10-14

## Status

Accepted

## Context

文書作業の dogfood では、Coder が指定 worktree に成果物を作成した。
しかし、Task の commit 禁止と、成功結果・独立 Reviewer が commit SHA を要求する契約が
衝突し、正式な成功結果からレビューへ進めなかった。
別の版へのレビュー承認は、作成された成果物の同一性を証明しない。

Human は例外的な commit 許可ではなく、commit なしで同一内容をレビューする方式を採択した。
単なるファイルコピーでは、対象の識別、変更範囲、再起動後の照合と Git GC 後の保持を
既存の Git 証拠と同じ条件で扱えない。

## Decision

既定の commit mode は維持し、Task で `outputMode: worktree-tree-snapshot` を
明示承認した場合だけ、Controller が一時 Git index から成果物の tree SHA を算出する。
`outputSha` は開始 commit、`outputTreeSha` は成果物 tree として区別し、
モデルが提示した tree SHA は Controller の証拠として採用しない。

tree は `refs/taxion/snapshots/` の immutable な内部 ref に保持する。
branch、commit、実 index、remote は変更しないが、保持 ref の追加はローカル Git metadata の変更である。
Reviewer は別 worktree に対象 tree を適用し、開始時、検証前後、完了時に同一 tree を照合する。
検証済みの Controller 管理 Reviewer worktree だけを解放し、ref は監査証拠として保持する。

Task 承認、handoff、完了 proof、checkpoint、部分成果と成功結果の successor 検査に
baseline commit、tree SHA、変更 path を通す。停止済み Flow の予算は変更せず、
継続には同じ成果物を持つ別 worktree と、新しい Task・予算の承認を要求する。
旧 Dispatch の成果物方式を遡って変更して成功扱いする方式は採らない。

## Consequences

commit 禁止の作業でも、同じ内容を独立 Reviewer へ渡せる。
nonignored な untracked ファイルは tree に含むが、ignored ファイルの内容は保証しない。
既存 commit mode の clean worktree と exact commit SHA の検査は維持する。
commit mode の部分成果を、承認なしで snapshot mode の継続として扱うことはできない。

同一 tree、改変拒否、GC 後の保持、修正と再レビュー、停止後の successor は、
実 Git と fake runtime の回帰で確認する。この結果だけで実 SDK の coding・review を
end-to-end 受け入れたことにはしない。
現在の契約は [Runtime contract](../03-runtime-contract.md)、
継続手順は [Task Graph](../10-task-graph.md)、
保存証拠は [実行記録](../11-execution-records.md) が所有する。
