# 58. 入力検証に合格した停止レビューを後続契約へ引き継ぐ

Date: 2026-10-28

## Status

Accepted。Issue #213。

`v0.7.21-rc.11` の承認集約、実行後の継続、入力目的と予算見積りは
[Issue #240](https://github.com/hiono/taxion/issues/240) の同一変更で追跡する。

## Context

持込み成果物の入力検証に合格した後、レビュー用 worktree の準備に失敗すると、
Flow は終端停止し、モデル未起動の Reviewer 指示だけが残る。
従来の未開始指示の整合回復は Coder 専用であり、完了した検証を保持して
独立レビューへ進む正式操作がなかった。事前点検でレビュー用の場所を作った場合も該当する。

## Decision

`workflow reconcile-stopped` の対象に、最初の持込み成果物 Reviewer を追加する。
以下は RC8 時点の条件であり、準備済み worktree への RC10 の拡張は後述する。
保存状態が `STOPPED / SAFETY_STOP`、引継ぎ保留、runner なし、
入力検証 `PASSED`、指示 `ISSUED` であることを要求する。
応答・開始・session・結果・利用量・runtime receipt・管理済みレビュー worktree
がある指示には適用しない。他の稼働実行も拒否する。
実行停止の確認証拠、配送不明の受容、現在の preview hash は従来どおり必要である。
Controller が承認済み判断と観測から入力を準備し、内部手続きだけを理由に同意を増やさない。

取消は旧指示と停止状態を監査へ保持し、Reviewer Task だけを停止する。
親 Task はレビュー待ちのまま、新しい正式な後続契約へ移す。
後続の intake と confirm に同じ `reconciliationHash`、confirm に
取消後の正確な feedback target と `replacesFlow` を渡す。
候補、元 worktree、branch、目的、範囲、権限、受入れ条件、検証コマンド、
必要証拠、仕様、利用量方針を変更しない。

後続の plan と start は元の `artifactSource` と
`reuseArtifactValidation:true` を明示する。
対象と実効設定を再照合し、既存の合格 receipt を内容・実施時刻・元の実施場所ごと継承する。
`validationSourceFlowId` と `INPUT_VALIDATION_REUSED` 監査が元の実施監査と
整合回復記録を結び付ける。架空の検証開始・完了や Coder 成功は生成しない。
旧 Flow は再開せず、旧停止理由・合格 receipt・結果 hash を変更しない。

新しい `reviewRoot` は Controller が管理する。利用者や事前点検は
実際のレビュー worktree を先に作らない。既存の場所を自動削除・流用しない。
合格した入力検証 stage は再実行しないが、Reviewer 結果受理時の既存の実検証は維持する。
新しい契約・工程計画の承認、実行前検査、独立判断、期限・課金上限は省略しない。

## Compatibility and rollback

### 復旧判断の一括化（v0.7.21-rc.11）

既存の個別操作では、停止確認の後に取消、後続契約、工程計画の各 hash を生成する。
これを人間の別々の判断と扱うと、同じ目的・候補・予算の承認質問を繰り返してしまう。
`workflow recover-review` は同じ既存の検査を組み合わせ、
停止証拠と配送不明の受容、同一候補、新しい識別子と場所、
絶対期限、工程配分と費用上限を一つの preview と外側 hash へまとめる。

保存状態全体と入力を承認対象に結び付け、隔離メモリ内で既存操作を検証する。
適用時は保存状態と Git 入力を再照合し、一つの保存 transaction で確定する。
失敗時は取消や新しい契約だけを残さない。元の retained snapshot を再利用し、
preview や失敗した検査で永続 Git ref を追加しない。
一括承認の入力と hash を後続 Flow の authorization に保存し、
下位の個別監査も保持する。内部監査の時刻などによる下位 hash の違いを
追加の人間の判断とはしない。未承認の権限拡張には用いない。

モデル起動と承認済み検証コマンドの再実行はこの操作では行わない。
適用後は通常の入力 receipt 再照合、起動前検査、独立レビュー、
結果受理検証へ進む。一括承認内の内部処理を再度承認させない。
既に承認済みの後続 Flow は本操作へ戻さない。
絶対期限を固定し、遅延適用で時間枠を延長しない。
失敗原因の自動推定・証拠収集、任意の承認の合成、包括委任は対象外である。
legacy Ledger の固定 wire と DB schema は変えず、
既存の追記監査へ任意の `kind` を追加する。省略は従来の未開始取消、
`executed-artifact-review-continuation-v1` は実行後の正式継続である。
未知の種別は拒否し、新しい監査を旧版で操作する保証はしない。
rollback は適用前の整合した backup を別配置で使う。

### 実行後に取消された成果物 Reviewer の継続

未開始専用の条件を単に外して、実行済みの指示を未配送と分類してはならない。
同じ `workflow recover-review` の `continueStoppedExecution:true` は、
実行済み・正式停止済み・`CANCELLED` の最初の成果物 Reviewer を別の条件で検証する。
元の session・結果・利用量と一致する Controller の停止 receipt を必須とし、
合格済み入力、候補、準備済み worktree、稼働実行と所有権の不在を再照合する。
外部実行停止の確認と未知の利用量の受容を維持し、未開始向け reconcile は変更しない。
一括承認に結び付いた既存の追記専用監査へ、実行後の継続であることを保存する。
旧指示の状態や実行履歴は変更しない。

通常の後続の絶対期限は元 Flow を超えない。明示的な期限再契約は次節に従う。
合格入力 receipt を再利用し、保存済み出力の末尾最大8192文字を
墨消し・省略表示付きで後続の正式提案と実際の Reviewer 指示に含める。
完全な保存出力は元の不変監査に保持する。未保存の出力・思考は未知のままとする。
終端 session の再起動機構や汎用的な実行履歴移植は作らない。
同じ候補に対する残りのレビューを後続指示として実行し、
Coder と入力検証の再実行を追加しない。結果受理検証は維持する。
累計予算は外側の承認記録と先行 Flow の観測から引き継ぎ、
新しい Flow の単体上限を消費履歴のリセットと解釈しない。

### 期限切れ後も同じ成果を使う再契約（v0.7.21-rc.12）

[Issue #242](https://github.com/hiono/taxion/issues/242) の同一修正として追跡する。

RC11 は未来の要求期限と元期限以下を同時に要求し、
元期限が経過した場合の正式な再契約を実装していなかった。
期限の自動延長を禁止することと、人間の新期限判断まで禁止することを分離する。
新しい管理操作は作らず、同じ `workflow recover-review` の任意の
`deadlineRecontract` に保存元の `previousDeadline`、
request と同じ未来の `deadline`、変更理由 `reason` を指定する。
元期限の完全一致と、新期限が元期限より後で適用時にも未来であることを要求する。

旧期限・新期限・理由・同一候補・receipt・停止証拠・予算・工程計画を
一つの外側 hash へ束縛し、この実質的な変更だけを一括承認する。
未指定時の元期限内条件は維持する。未開始と正式停止済みの実行済み
成果物 Reviewer の双方に対応し、実行済みの停止 receipt 検証は省略しない。
新期限の承認は旧 Flow の再開ではなく、同じ入力を使う後続契約への権限である。
旧期限・指示・session・利用量・停止と worktree は変更しない。
入力検証と Coder を再実行せず、保存解析を引き継ぎ、結果受理検証は実施する。
予算を追加せず、累計消費と未知の利用量も維持する。

`Records.DispatchReconciliations` に任意の同名 object を初回追記時から保存する。
実行済み監査の旧期限前という制約は、有効な明示再契約がある場合だけ新期限へ置き換える。
既存監査の変更や、期限を通すための DB 編集はしない。
新しい再契約監査を旧 binary で扱わず、戻す場合は適用前の整合 backup を別配置で使う。
期限待ちで候補や合格 receipt を破棄することも、保存済み解析を再調査することも解決策としない。

### 入力目的と予算継続性を復旧契約へ含める

Reviewer を成功した Coder の後段に限定しない。
人のコード・文書・テスト、失敗・中断途中の成果、繰返し失敗の診断について、
`reviewInput` を既存の Task decision に保持する。
`taxion.review-input.v1` は目的、出典、質問、既知の失敗、検証状況、読める証拠を固定し、
通常の提案・承認・指示の hash に含む。未知版や不正・重複入力は拒否する。
復旧でこれらを変更・破棄して入力検証 receipt を再利用することは拒否する。
私的管理領域への権限を追加せず、必要な抜粋と許可済み snapshot を渡す。
実際の指示と省略表示にも目的別説明を含める。
診断の受入れ条件は調査としての質問・証拠であり、製品テスト成功ではない。
診断の完了を `NOT_ASSESSED_DIAGNOSTIC` として明示し、製品公開の品質根拠へ昇格させない。
製品の失敗・欠陥・未実施検査は診断報告に残す。

予算停止の後に全調査・全工程を再実行することを避ける。
ready、管理スキル、外側の運用方針に早期の不足見積りと一括確認を明記する。
`workflow budget --file` は一つの単位・明示範囲の累計消費と、
残工程・修正・独立レビュー・保存の追加見込みから不足分を算出する。
追加承認待ちでは新たな課金を行わず、承認を実行済み処理の再試行許可と解釈しない。
既存 Flow の次操作を案内し、先行 Flow の予算観測を併記する。
この見積りは残量取得、単位換算、自動課金、累計上限の登録を行わない。
実際の承認上限は既存の外側の記録と実行検査で維持する。
安全・独立レビューを削るのでなく、重複調査・背景再送・再承認を削る。

### 準備済み worktree への拡張（v0.7.21-rc.10）

追跡: Issue #238。

`prepareReview` は管理済み worktree を作成してから `Start` へ進む。
`session-starting` の記録後、repository identity query で失敗すると、
worktree 作成済み・指示 `ISSUED`・session 未開始のまま停止する。
RC9 までの worktree 不在条件では、この正常な起動順序から生じる停止を復旧できない。

保存状態の適格性と、外部 worktree の検証を分離する。
Controller は preview と適用の両方で元候補を照合し、準備済みの場合は
候補との共通 Git repository、正確な場所での登録、detached baseline HEAD、
baseline index、承認済み target tree のファイル、保持 snapshot ref を確認する。
実 index とファイルは変更せず、既存の private index による照合を再利用する。
追加ファイル・ignored ファイル・staged 変更・場所や内容の不一致は拒否し、
自動修復や削除で通過させない。

preview は `preservedReviewWorktree` を返す。取消監査の元 Dispatch は
その場所と `ReviewWorktreeSource` を保持し、適用後も旧 worktree を残す。
後続は従来どおり新しい `reviewRoot` で同一候補をレビューする。
応答・開始・session・結果・利用量・runtime receipt、他の稼働実行の拒否、
実行停止の証拠、配送不明の受容、正確な hash の承認は緩和しない。
`session-starting` はモデル開始の証明ではなく、記録欠如は未配送・無課金の証明でもない。

旧 worktree は取消後の成果物として保存するが、後続 Reviewer の入力とはしない。
元候補と合格 receipt を引き継ぎ、入力検証の再実行と Coder 起動を追加しない。
結果受理検証は維持する。期限切れ Flow は再開せず、
新しい契約・工程計画・期限・予算に必要な承認を得る。
新しい監査を旧版で操作せず、戻す場合は更新前の整合した backup を別配置で使う。

新しい `validationSourceFlowId` と監査種別を持つ DB は旧版で操作しない。
適用前の整合した backup を別配置で使う。既存の Coder 整合回復と、
継承を指定しない入力検証の動作は維持する。汎用的な停止解除ではない。

## Validation

### RC12 の期限再契約

期限切れの停止済み fixture に対し、未開始と正式停止済み実行済みの双方で、
未来の期限を提案・一括承認・適用し、同じ候補の独立レビュー完了まで確認した。
旧期限・指示・session・利用量と保存解析を変更しない。
入力検証は元の1回を再利用し、Reviewer 結果受理検証の1回を維持する。
旧期限の不一致、変更理由の欠如、新期限と要求の不一致、
期限判断変更後の旧 hash 流用、正式停止 receipt の欠如・変造を拒否した。
期限切れの実行済み監査から再契約 object を外すと照合を拒否する。
旧期限内の継続と明示再契約なしの期限延長拒否も維持する。

`mise run check` の format、vet、全 Go 試験、build と Web 試験12件、
配布・保全の Python 試験25件が成功した。
Controller と CLI の対象 race 試験が成功した。store には今回の selector に合う試験がなく、
直接の race 合格とは扱わない。独立した正当性レビューと正確な配布文レビューが承認された。
CLI・管理スキル1.3.18・実行契約・監査形式・更新制約を同じ PR に含める。
実モデルや製品環境での適用は別の受入れであり、この記録を適用成功とは扱わない。

### RC11 の検証と引継ぎ

`mise run check` の format、`go vet ./...`、`go test ./...`、
`go build ./...` と Web 試験12件が成功した。
配布・worktree 保全の Python 試験25件と公開文全114ファイルの静的検査も成功した。
Controller と CLI で対象の race 試験を実行した。他の package に一致する試験が
なかった選択実行を、その package の race 合格とは扱わない。

実際の試験用 runtime の開始・送信・出力保存・正式停止から、
`CANCELLED`／`STOPPED` の同一候補を後続指示へ引き継ぎ、独立レビュー完了まで確認した。
入力検証の実行回数は継承後も1回、結果受理検証後は2回である。
元指示・session・結果・利用量と保存解析を維持し、実際の指示へ解析を渡す。
未開始・実行済みの両方で元期限の延長を拒否する。
停止 receipt の欠如・変造、不一致、承認 basis の変更と部分保存も拒否した。

独立レビューで見つかった preview の永続 Git ref 追加、CLI の `--file` 未登録、
未開始経路の期限延長を修正し、回帰試験と独立再レビューが承認された。
診断入力からの実際の指示・完了・製品公開拒否、累計消費を保持した早期不足見積りを確認した。
配布文は正確な本文 hash に対する独立レビュー済みである。
同じ Issue と PR にコード・ADR・操作・開発者契約・更新と rollback・制約を含める。
実モデルや製品環境での適用は別の受入れであり、この検証から成功を推定しない。

### 既存経路の検証記録

実際の場所衝突による停止を再現し、未起動指示の取消、正式な後続確認、
合格 receipt の継承、Coder なしの同一対象独立レビューと完了までを回帰試験で確認する。
検証 stage の実行回数、元の停止と場所の保存、対象 drift、設定・検証契約の変更、
開始・応答・session のある指示の拒否を確認する。

`mise run check` と、Controller・CLI・core・store の対象 race 試験に合格した。
独立した正当性レビューと、公開 Issue・PR・配布文の読者視点レビューも承認済みである。
実際の製品環境での適用は未実施であり、この記録を製品の品質合格とは扱わない。

RC10 の回帰試験は `prepareReview` の成功後に repository identity 確認を失敗させる。
runtime の `Start` が呼ばれず、旧 worktree と合格 receipt が保持されること、
preview 後の変更も適用時に拒否すること、取消後に正式な後続契約を確認して
Coder なし・入力検証再実行なしで独立レビューを完了できることを確認した。
結果受理検証の実行回数も維持する。変更・追加・ignored・staged ファイル、
branch の変更、元候補の不一致、場所の消失、登録の不一致を拒否する。

`go vet ./...`、`go test ./...`、`go build ./...`、
`go test -race ./internal/core ./internal/gitproof ./internal/store ./internal/runtime/controller ./cmd/taxion -run 'TestStopped|Test.*Artifact|Test.*Reconciliation|TestReadySkill' -count=1`
が成功した。新しい経路は Controller の試験で実行され、
Git 操作は一時 repository、runtime は試験用であり、実モデルを起動していない。
既存の Web 試験12件、Python 試験25件、公開文の全114ファイル検査も成功した。
準備済み worktree の独立した正当性レビューに blocking 指摘はなく、
Issue・PR・配布文は正確な本文 hash に対する独立した読者視点レビューで承認された。
