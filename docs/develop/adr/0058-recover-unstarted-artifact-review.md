# 58. レビュー成果を保持した継続と実行上限の変更

Date: 2026-11-01

## Status

Accepted。停止レビューの復旧は Issue #213・#238・#240・#242 の変更を統合した現行契約とする。
旧版ごとの操作順は併記しない。利用時の入力は
[CLI リファレンス](../../userguide/cli-reference.md#停止済み初回指示の整合回復)を正本とする。

[ADR-0075](0075-blacklist-authorization-and-recovery.md) に従い、停止 code の許可一覧は
未開始取消、検証 receipt の再利用、正式停止済み Reviewer の分類に使わない。
停止 receipt、session と利用量、入力同一性、所有権、期限と承認の検査は維持する。

## Context

レビュー対象は成功した Coder の出力に限らず、人のコード・文書・テストや失敗途中の作業も含む。
予算や期限を理由に停止しても、候補・入力検証・保存解析・累計消費を捨てて最初から調査し直す必要はない。
一方、停止後の再契約だけでは、稼働実行を維持したまま期限不足へ対処できない。
操作者が停止前に時間と費用を一度に確認できる判断要求と、正式な実行上限の変更を設ける。

## Decision

### 停止前の判断と同じ実行の延長

runner はモデル応答待ちと利用量受信時に `resource_approval_required` を保存・出力する。
工程計画または保守的な時間見込みに、検証・レビュー・修正・保存と人間の回答待ちを含める。
native 利用額の残量が上限の20%以下の場合も判断を求める。推定は完了保証ではない。
外側の操作者は観測可能な累計消費と残工程から、費用と時間の不足を一つの承認判断へまとめる。
イベントは承認でも取消でもなく、回答待ちを理由に有効な実行を止めない。

`workflow extend --file JSON --preview` は、現在の契約・指示・session・runner と、
変更前後の固定期限・native 上限・理由・承認元を hash に束縛する。
利用量と出力の増加だけでは承認を失効させず、別の指示や実行への流用は拒否する。
正確な承認後に同じ `expectedHash` で適用し、`ReviewFlow.extensions` と
`RESOURCE_EXTENDED` 監査へ原子的に追記する。runner の所有権を取得しない。

元の期限・予算・工程計画・指示は保持する。有効期限と native 上限だけを追記判断から求め、
同じ worker context と session を旧期限の後も維持する。消費・回数・合格入力検証をリセットしない。
時間だけの承認は native 上限を変えない。承認の記録と上限の適用は別であり、
適用監査・保存した有効期限と同じ実行の対応を確認してから延長済みと報告する。
現在の工程境界を過ぎた後の承認、減額、権限変更、役割や修正回数の追加はこの操作で扱わない。
独立した呼出し元の期限・取消、提供元停止、account 残量下限は上書きしない。

外側の requests・tokens・credits は `workflow budget --file` の明示した観測単位と範囲で扱う。
任意の `time` は現在の有効期限と残工程・修正・レビュー・保存・回答待ちの秒数を受け取り、
費用不足と時間不足を同じ報告で示す。単位換算、自動承認、provider 枠の拡張、
新しい外側累計上限の自動登録は行わない。必要な追加承認は同じ判断で根拠とともに保存する。

### 回答受信の中断と結果確定を分離する

runner の process 終了は、native session の回答が存在しないことを意味しない。
`workflow recover` の `finalizeSavedResult` は、保存した session の終了済み回答を
通常の `finishResult` へ渡す。特定の障害文言や手入力した回答を成功根拠にしない。
実行中の回答受信も保存観測も、同じ受入れ検証・runtime 終了・管理 worktree 解放・正式結果保存を使う。
live イベントは表示より先に receipt を保存し、出力先の故障による証拠喪失を減らす。

旧 process の停止根拠と同一 runner、session、作業場所、保存済み実行観測を要求する。
native main-agent の最終回答と turn 終了が元の提出期限内で、正常 shutdown がある場合だけ扱う。
観測用の接続は pending work・tool・hook・transcript 修復を無効にする。
元期限を過ぎてからの観測には、別途許可した短い host 検証期限を使う。
これはモデル作業の再開や承認上限の変更ではなく、消費・修正回数も維持する。
所有権は正式結果保存後にのみ回収する。
host 検証済み結果と runtime 終了を append-only receipt として保存し、
検証・終了・worktree 解放・正式結果保存の間で中断しても同じ段階から確定する。
解放済み path を再作成せず、Git 登録の不在と元 repository の immutable target を再照合する。
native の per-call usage は保存履歴に含まれない場合があるため、既存 receipt は保持し、
正常 shutdown のモデル別・同単位の集計との差分だけを加算する。単位変換や消費のリセットはしない。
検証済み段階では、その時点までの利用量を変更不能な prefix として照合する。
shutdown 集計の追記で検証結果を無効にせず、runtime 終了済み段階で最終利用量を固定する。
検証開始だけが保存され、検証済み結果がない場合は、結果不明の host コマンドを再実行しない。

`SUCCEEDED` は担当作業の完了を表す。Reviewer の `REQUEST_CHANGES`・`BLOCKED` では、
製品の承認を妨げる指摘も結果として保持する。Coder の成功や Reviewer の `APPROVE` では
blocker を許さず、品質判定・修正承認とは分けて検査する。

### 正式停止後の一括復旧

入力検証 `PASSED` の最初の成果物 Reviewer は `workflow recover-review` で扱う。
Controller が既存の取消・後続提案・確認・工程計画・作成を隔離メモリ内で検査し、
一つの外側 hash 承認と実 store の transaction へまとめる。下位 hash の個別承認は求めない。
preview と失敗時に旧状態・実 index・worktree・保持 ref を変更しない。

未開始指示には応答・開始・session・結果・runtime receipt がないことを要求する。
準備済みの管理 worktree は登録・同じ repository・detached baseline・index・対象ファイルと
追加／ignored ファイル不在を検査して保全する。
実行済みでは `continueStoppedExecution:true` と、指示・session・結果・利用量に一致する
正式な Controller 停止 receipt を必須にする。未開始と偽って扱わない。
稼働実行、runner 所有権、未解決 feedback、候補や証拠の不一致は拒否する。

通常は現在の有効期限を維持する。期限切れ後や新しい窓が必要な場合は
`deadlineRecontract:{previousDeadline,deadline,reason}` を同じ要求に含める。
保存された元期限の完全一致、旧期限より後の新期限、要求との一致、適用時にも未来であることを要求する。
変更は新しい Reviewer-only 後続にだけ適用し、旧 Flow・指示・終端 session は変更しない。

合格入力検証は再利用し、保存出力の資格情報を除いた末尾最大8192文字を実際の後続指示へ渡す。
Coder と入力検証は再実行しない。Reviewer の結果受理検証は実施する。
元 transcript・usage・worktree を保持し、未保存の思考の復元を約束しない。
入力目的 `diagnostic` の受入れ完了は製品品質の合格ではない。

## Compatibility and rollback

新しい追記監査は既存 workspace document と budget audit に保存する。
legacy Ledger の固定 wire と SQL schema は変えない。
読取り時にも extension と監査の完全一致を要求し、削除・変更・監査のない延長を拒否する。
新記録を保存した store を旧 binary で操作しない。
rollback は元成果を保全し、適用前の整合バックアップを別配置で使う。
product mode の配布物・policy・skill の更新は実行停止中に行い、稼働中の binary を差し替えない。

## Validation

`TestLiveReviewerExtensionKeepsSessionReceiptAndUsageAcrossOriginalDeadline` は実際の runner と
試験 runtime で旧期限を越え、同じ session・累計利用量・合格入力検証からレビュー完了へ進む。
入力検証1回と結果受理検証1回を区別し、承認中の利用量増加を許容する。
変更済み要求、二重適用、旧期限不一致、遅れた承認、独立 caller 期限の迂回、
追記判断の削除・書換えを拒否する。時間・費用不足の一括見積りと判断要求の出力も対象とする。

未開始／実行済み、元期限内／期限切れの停止後復旧は既存回帰試験を維持する。
実製品環境での導入と受入れは、試験 runtime・配布物の検証とは区別する。
