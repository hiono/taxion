# ドメイン不変条件と実装・検証対応表

この文書は、ドメイン不変条件の規範、コード上の強制箇所、検証証拠を対応付ける。判断の原則は [ADR-0079](adr/0079-enforce-domain-invariants-across-state-paths.md)、Job の意味と完了要件は [ADR-0077](adr/0077-job-centered-role-collaboration.md) を参照する。

**判定関数の存在だけでは実装完了とみなさない。** 各不変条件について、状態変更・永続化・復元・外部入力などの該当経路で強制されることを確認する。

## 完了条件の論理定義

Job \(j\) が \`COMPLETED\` であるための条件を次のように定義する。

\[
Completed(j) \Rightarrow
Approved(j)
\land AcceptanceSatisfied(j)
\land RequiredEvidenceSatisfied(j)
\land BudgetWithinLimit(j)
\land NoUnresolvedExecution(j)
\]

ここで、

- `Approved(j)`: 現在の Job 契約が承認済みであり、承認対象と現在の契約が一致する。
- `AcceptanceSatisfied(j)`: すべての必須 Acceptance 条件に、provenance を検証できる `passed` Evidence が存在する。
- `RequiredEvidenceSatisfied(j)`: すべての必須 Evidence 種別に、provenance を検証できる `passed` Evidence が存在する。
- `BudgetWithinLimit(j)`: Job の利用量が承認済み上限内であり、外部利用量を監視できない場合は完了を許可しない。
- `NoUnresolvedExecution(j)`: 実行中・待機中・停止要求中・中断中の RoleExecution がなく、成功した RoleExecution の利用量が記録されている。

この述語は実装の現在の意味を示す。新しい安全条件・Evidence 種別・実行状態を追加する場合は、述語、判定ロジック、すべての強制箇所、テストを同じ変更で更新する。

## 現在の実装対応

| 条件 | 規範的な判定 | 強制箇所 | 回帰検証 | 確認状態 |
|---|---|---|---|---|
| Job completion readiness | `TaskGraph.jobCompletionReadiness` / `JobCompletionReady` | `transitionJob` の `COMPLETED` 遷移 gate、`validateJobModel`、`TaskGraph.UnmarshalJSON` の Job 復元検証 | `TestJobStateTransitionsAreAudited`（遷移拒否、保存状態検証、JSON 復元拒否） | このPRで実装・検証中 |
| Job / Task の所有整合性 | Task.JobID から Job を一意に解決 | `validateJobModel`、Task の追加・更新経路 | Job/Task ownership tests | 経路の継続監査 |
| RoleExecution の所属・履歴整合性 | JobID / TaskID / ParentExecutionID と履歴状態の整合性 | RoleExecution mutation と `validateJobModel` | RoleExecution lifecycle / cross-Job tests | 経路の継続監査 |
| Job output / evidence provenance | 出所、Job、Task、Execution、content identity、verification の整合性 | `AppendJobOutput` / `AppendJobEvidence`、`validateJobArtifacts` | Job artifact / evidence tests | 経路の継続監査 |
| Job approval scope | 承認済み Job 契約と Task / RoleExecution の scope 包含 | Controller の privileged operation 直前検査、Graph contract validation | approval / execution authorization tests | 経路の継続監査 |

「経路の継続監査」は、判定ロジックや一部の gate が存在することを示すだけで、生成・更新・保存・復元・実行の全経路で不変条件が強制されると認定した状態ではない。監査完了の根拠を得るまで、実装完了として扱わない。

## 変更時の必須手順

1. 不変条件を論理式または同等の検証可能な条件として定義する。
2. 状態を変更するすべての入口と、保存・復元・移行・外部入力の境界を列挙する。
3. ドメインに規範的な判定ロジックを置き、状態変更 gate と aggregate validation から共有する。
4. 拒否時に状態が変更されないことを確認する。
5. 正常系、違反系、永続化後の復元、旧形式移行、競合・再送など該当する経路をテストする。
6. コードレビューで、列挙した経路が実際に gate / validator を通ることを確認する。
7. この表、関連 ADR、実装計画、Issue の証拠を同じ変更単位で更新する。

CI 成功は実行された検査の成功を示す。列挙していない経路や実モデルでの協調を証明するものではない。
