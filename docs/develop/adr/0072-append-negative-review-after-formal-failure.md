# ADR-0072: FAILED Reviewer の保存済み指摘を失敗履歴とは別に追記する

## Context

Reviewer のモデル回答が保存されても、通信切断により Controller が正式な `FAILED` を保存する場合がある。
最終回答・turn 終了・routine shutdown があることと、Controller が品質検証を完了したことは別である。
既存の `recover` は `RUNNING` かつ正式結果なしの確定、
`recover-review` は停止 receipt 付き `CANCELLED` の成果物レビュー後続、
`accept-saved-review` は特定の不具合で停止した正式成功済み `APPROVE` の受入れを扱う。
これらの対象条件を広げるだけでは、不変な失敗履歴と品質検証の契約を維持できない。

## Decision

`workflow record-failed-review` を追加し、元の実行に束縛された `REQUEST_CHANGES` だけを
`Records.FailedReviewSupplements` へ追記する。これは正式な指摘記録であり、
成功した `DispatchResult` への置換でも、品質結果の正式受理でもない。
回答中の `status:SUCCEEDED` はモデルの申告として保持し、Dispatch 自体は `FAILED` のままとする。

| 元 Dispatch と保存回答 | 必須条件 | 操作と効果 |
|---|---|---|
| `RUNNING`、正式結果なし | 元 runner の停止、期限内 native 完了、元実行との照合、host 検証権限 | 既存 `recover`。通常の結果確定へ合流する |
| `CANCELLED`、成果物 Reviewer | 元契約に一致する停止 receipt、検証済み候補、後続の承認 | 既存 `recover-review`。新たな Reviewer 実行を伴う |
| `FAILED`、`REQUEST_CHANGES`、shutdown が失敗記録以前 | 同じ session・設定・指示・実行観測、期限内の回答と turn 終了、停止確認、準備済み tree の一致 | 指摘だけを追記する |
| 同じ負の回答、shutdown だけが失敗記録より後 | 回答と turn 終了は失敗記録以前。正常 shutdown は turn 終了以後、保存済み提出期限前、観測時刻以前で、後続実行なし | 接続終了を回答完了と区別して指摘だけを追記する |
| 回答または turn 終了が失敗記録より後 | 正常 shutdown があっても対象外 | 拒否する |
| `FAILED`、`APPROVE` または `BLOCKED` | 完了した native 履歴があっても対象外 | 拒否する。今回の負の指摘記録から一般化しない |
| `FAILED`、原文・実行 anchor・期限・候補のいずれかが不明または不一致 | 回答ファイルや申告で不足を補わない | 拒否し、元の履歴を保存する |
| 同じ Dispatch の追記済み回答 | 二重処理しない | 既存記録を確認する |
| 保存回答の観測が timeout/cancel、または処理・出力に失敗 | operation phase と追記境界を明示し、秘密を含む native/SQL/Git error は表示しない | `NOT_WRITTEN` は追記前または rollback を証明できた場合のみ、commit の成否が不明なら `UNKNOWN`、成功後の出力失敗も `COMMITTED` |

### 時刻・状態を組み合わせる判断

上の状態表だけでは時系列の網羅を示さない。
`A` は回答保存、`E` は対応する turn 終了、`F` は元の FAILED `verified-result.at`、
`S` は routine shutdown、`D` は保存済み提出期限、`O` は今回の観測時刻とする。
`F` を実行中の failed event や Dispatch の `updatedAt` で置き換えない。
共通条件は同一 session・実行 anchor・候補・停止証拠と `REQUEST_CHANGES` であり、
次の「時刻条件を満たす」は全適格性・品質の合格ではない。

| ID | 時系列または証拠の組合せ | 判断 |
| --- | --- | --- |
| T1 | `A ≤ E ≤ S ≤ F ≤ O`、`A/E/S < D` | 時刻条件を満たす。後続の適格性照合へ進む |
| T2 | `A ≤ E ≤ F < S ≤ O`、`A/E/S < D` | 時刻条件を満たす。正常な接続終了だけを失敗後に許す |
| T3 | `A > F` または `E > F` | 拒否。失敗後のモデル提出は接続終了と区別する |
| T4 | `A ≥ D`、`E ≥ D`、`S ≥ D` のいずれか | 拒否。期限と同時刻も期限内に含めない |
| T5 | `A` が Dispatch 作成前、`E < A`、`S < E` | 拒否。順序矛盾を許可しない |
| T6 | `E > O`、`S > O`、`F > O` | 拒否。未来の証拠を使わない |
| T7 | 回答・turn 終了・routine shutdown の欠落、非正常終了、別 session、後続実行 | 拒否。main agent 以外の後続 tool・turn 活動も含め、時刻の推測や回答ファイルで補わない |
| T8 | `O ≥ D` だが `A/E/S < D` で他条件も一致 | 観測の遅さだけでは拒否しない。新しい実行は許可しない |
| T9 | Flow の元期限内だが、保存済み予約を引いた `D` を超える | 拒否。元期限だけで期限内と断定しない |
| T10 | 保存済み延長が存在する | 延長後期限と現在の保存済み修正回数から `D` を求め、T1〜T9を同じ規則で評価する |
| T11 | 複数の時刻条件が不成立 | 失敗した比較をすべて返す。後続の適格性は `NOT_EVALUATED` |
| T12 | preview 後の native 履歴・workspace 変化、二重追記、旧版で新条件の記録を読む | 適用・再処理・旧版読取りをそれぞれ拒否し、元履歴を保持する |
| T13 | operation observation timeout/cancel、store append が未開始、証明済み rollback、または commit 結果不明 | static phase/cause と `NOT_WRITTEN` / `UNKNOWN` / `COMMITTED` を返す。`UNKNOWN` は記録を調べ、同じ hash だけで配送・保存済みと判断しない |

時刻の同時成立・不成立は共通 `FailedReviewTiming` で Controller と保存時に評価する。
T1〜T6・T8・T11 の比較は `TestFailedReviewTimingSeparatesAnswerFromShutdown`、
T2 と、T3〜T6 の代表的な拒否診断、preview／追記／再読取り・不変性は
`TestFailedReviewSupplementPreservesFailureAndNeverReplaysValidation` で確認する。
T7 の native 履歴は `TestSavedTurn*` と `TestStopOnlySDKProtocolNeverCreatesOrSends`、
回答前の子 agent 活動と、回答後・shutdown 前後の活動を区別する
`TestSavedTurnRejectsLaterSubagentActivity`、
T12 の変更拒否・履歴保持は同じ Controller 試験で確認する。
T9・T10 の予約・延長は既存の工程計画試験と期限計算のコード照合が対象であり、
指摘追記操作との全組合せ、原本・実サービス試験は未実施である。
T12 の旧版拒否は旧版の保存検証条件からの互換性判断であり、旧版での全操作を再試験していない。

preview は実行を再開せず元 Copilot session を観測する。pending work、tools、hooks、
transcript 修復を有効にしない。保存済みの実行 anchor と native 履歴を照合し、
曖昧な回答、別 session、後続実行、正常終了の欠落を拒否する。
Caller が作った回答 JSON の入力は提供しない。

apply は同じ要求、workspace 全体、元 Dispatch、native 履歴、保存回答を含む preview hash を要求する。
回答、turn 終了、routine shutdown の順序と各時刻を保持し、
回答と turn 終了は保存済み提出期限前かつ元の FAILED `verified-result` receipt の時刻以前とする。
shutdown はその失敗記録より後でもよいが、turn 終了以後、同じ提出期限前かつ観測時刻以前とする。
後続の main-agent 実行・tool 実行や曖昧な native 履歴は引き続き拒否する。
保存形式の読取り・更新でも元の FAILED receipt の内容と時刻を照合する。
処理中の workspace 変更は transaction 内で再確認して拒否する。
観測用のtransactionでは、同じcontextを`QueryRowContext`・`QueryContext`・`ExecContext`へ渡す。
文書と文字列プールの補助処理も同じ接続で実行し、途中でbackground contextへ戻さない。
標準APIにcontext引数のない`Commit`・`Rollback`と、メモリ上の検証・JSON処理の強制中断は保証しない。
取消時も、確定・取り消しを証明できた範囲だけで保存結果を分類する。
追記記録の削除・入れ子の書換えと、元 Dispatch の指示・結果・利用量の変更は拒否する。
現在の Task・Flow・owner・operation・既存 receipt は変更しない。
別の稼働実行があれば拒否し、同じ旧 Flow の claim は停止確認と完全一致した場合だけ保持したまま観測する。

host 検証は開始済みでも未実施でも再実行しない。
回答中の検証申告は `NOT_REPLAYED_NOT_ACCEPTED` と区別し、検証 receipt に変換しない。
shutdown 集計は追加証拠として保持するが、旧利用量をリセット・加算・確定せず、
`accountingReconciled:false` とする。後続の実行には累計消費の別途照合が必要である。
修正、再レビュー、品質合格、公開、完了をこの操作から許可しない。

`.env.ci.example` の index 拒否は変更しない。秘密検査の前に全 prospective index の
`.env` と `.env.*` を拒否する既存方針は、変更していない例示ファイルにも適用される。
差分だけへの縮小、scanner の迂回、例示名の一括許可は今回の対応に含めない。

## Alternatives considered

`FAILED` を `RUNNING` に戻す方法は、正式結果と利用量の履歴を変えるため採用しない。
通常 finalizer への再投入は host 検証の再実行を伴うため、今回の非実行契約には使わない。
新 Reviewer への再送は、既存指摘の原本を処理する目的とは異なるため採用しない。
回答を品質合格として扱う方法は、`REQUEST_CHANGES` の意味とも独立検証とも一致しない。

## Consequences

v0.7.24 は shutdown まで失敗記録以前であることを要求していた。
匿名化された報告では、回答・turn 終了は失敗前、routine shutdown は通信失敗後だった。
この時系列を合成再現し、モデル提出の完了と接続終了を別の証拠として扱う判断へ変更した。
失敗基準は元の `verified-result.at` であり、実行中の failed event や Dispatch の
`updatedAt` ではない。個別実行の全原本や適格性を確認済みとは扱わない。

提出期限は `ExecutionDeadline()`、工程計画がある場合はそこから未使用修正枠の
`repairSeconds + validationSeconds + reviewSeconds` を引いた値を使う。
保存済み延長があれば末尾の期限を採用し、元の Flow 期限だけで期限内と断定しない。
時刻拒否は非秘密 JSON に採用期限・出所・予約秒数・各時刻・全失敗比較を返す。
後続の実行 anchor・回答・tree 照合は `NOT_EVALUATED` とし、診断を適格性の証明にしない。

保存された指摘を参照しながら、失敗理由と未確認の品質・利用量を区別できる。
旧 Flow を再開したり Task を自動で `REQUEST_CHANGES` へ遷移させたりはしない。
準備済み Reviewer worktree が解放・移動・変更された場合、commit mode、復元 workspace、
他 provider の native 履歴は対象外である。対象外を旧状態の手編集で解消してはならない。

## Security and operational impact

申請者・承認出所・停止証拠は既存と同様の協調的な記録であり、認証 credential ではない。
credential に見える回答や申請情報は出力・追記を拒否し、原本を秘匿して保持する。
実行権限は追加しない。既存の停止・品質・公開 gate と予算上限を維持する。
運用 store 操作、製品検証の再実行、運用機への導入は、この変更の開発検証とは別の承認範囲である。

## Compatibility

DB の table schema は変えず、任意の Records 配列を追加する。
追記前の既存 workspace はそのまま読める。追記後は新 field を理解する版を使用する。
旧版へ戻すために追記記録を削除する移行は提供しない。必要なら操作前の保全済み複製を使う。
失敗後の shutdown を持つ追記記録は v0.7.24 の読取り条件に適合しない。
その記録を保存した DB を旧版へ渡さず、更新前の整合バックアップを別配置で使う。
既存の `recover`、`recover-review`、`accept-saved-review` の状態条件は維持する。

追記の Controller observation は既定30秒、正の値で最大5分とし、CLI の
`--observation-timeout` で指定する。呼出し元のより早い deadline/cancellation が優先される。
context は Controller lock の取得前に開始し、native read、prepared Git verifier、
初回 Store read と append transaction へ伝播する。cancellable SQL/native/Git 処理を含む
Controller observation の上限であり、CLI の file/store read、DB open/migration、
Copilot runtime setup、operation lease、cleanup/output、および Controller/Store の
in-process mutex 待ちは対象外である。
したがってコマンド全体の厳密な wall-clock 上限とは主張しない。
失敗 JSON は versioned static phase/cause/message と persistence code だけを出し、
内部 error identity は Unwrap で保持する。supplement の canonical hash と保存 schema、
既存 `expectedHash` は変えない。

## Status

2026-11-06 の実装判断。合成 repository、合成回答、ローカル SDK protocol peer の検証と
個別事象の原本確認を区別する。運用原本未提供の事象を確認済みとは扱わない。
変更範囲と検証記録は [Issue #268](https://github.com/hiono/taxion/issues/268) を参照する。
