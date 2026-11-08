# Planner・直接質問の照合と所有権の検証

Issue #282、[ADR-0074](adr/0074-reconcile-interrupted-request-planner-attempts.md) の検証記録。
v0.8.2 の公開 baseline は `6a9f91255e3dd566fefb62032395a88f5d16cff1`。
初回 Coder と承認済み repair は正式に FAILED となり、両 worktree の未コミット成果、native 回答、停止結果と観測記録を保全した。
別 worktree に観測済み部分成果を回収し、外側 operator が修正した。これは成功した Coder 結果ではなく、独立品質レビュー前の supplied artifact である。

## 回帰の確認

`cmd/taxion/request_recovery_test.go` は公開 `request recover` の preview・apply を実行し、legacy native 入力との照合、stale hash の拒否、checkpoint・Graph・元期限・既知利用量と未知状態の保持、取消、同一判断の冪等性を確認する。
実際の exclusive lease を取得した後だけ stale owner を回収する。同じ store の新しい公開 `request` が、別の明示的な計画・承認を経て Flow start と未開始 Dispatch の発行まで到達することを確認する。runner は model を実行せず、これは元の運用環境の復旧の証拠ではない。

送信前の attempt、session、send-intent の順序、解決済みの先行 attempt と最新 attempt の区別、未解決の先行 attempt の拒否を確認する。
同じ attempt の利用量補正は差分だけを加算し、少ない観測で既知の floor を下げず、未知の累積利用量を既知へ置き換えない。
不正な保存回答は継続用に受理せず、停止・不確実性承認を伴う取消で元回答の hash を保持する。
legacy 照合では native Planner context の Task ID と元 human text を照合し、別の場所に引用した Task ID や request digest だけでは受理しない。

Controller・Copilot の回帰は main input/message/turn ID と sequence を持つ終了、後着 usage、後続 tool・child・error の拒否、routine shutdown がない停止済み main turn の限定観測を確認する。
advice の credential redaction と出典の redaction 履歴を保持し、実行提案内の credential-like 値を拒否する。
回答 receipt の保存は formal completion ではなく、quota・cleanup が失敗した場合に成功と判定しない。
未開始 Dispatch の行政取消では Graph の CANCELLED と Ledger の inactive を同じ transaction で保存し、架空の実行 receipt を作らず投影を一致させる。

## 検証状況

公開復旧・利用量補正・privacy/provenance の対象回帰は通過した。
全体検証の前回実行では CLI の旧 receipt assertion と出典の redaction 履歴に5件の失敗があった。修正後の `go build ./...`、`go vet ./...`、`go test ./...` は通過した。
旧全件 race 実行は同じ修正前のCLI assertion で失敗し、Controller 全件は15分のsuite上限で終了した。最後の実行中 test は15秒で、stack は JSON decoding を実行中だった。個別 test の deadlock と断定しない。
Copilot と OpenACP の全件 race、およびCLI・Controllerの `Request|Recovered|Question|Isolated|Planner|Investigation|Search` 対象 race は通過した。
追加の公開入口回帰と、旧suite終了時の `TestResumeArtifactReviewerDoesNotReplayInputValidation` は単独の race 実行で通過した。未開始 Dispatch の投影回帰も race で通過した。
公開入口回帰を追加した最終 snapshot の `go test ./...` は再度通過した。Controller 全件 race の完走を示す結果ではない。

2026-11-07 の初回独立品質レビューは `c5778d7` を対象とし、検索が対応する `assistant.turn_end` なしの待機通知で成功する点と、管理スキルが直接質問の公開復旧を未対応と案内する点を指摘した。正式評価は `UNMET`、判定は `REQUEST_CHANGES` であり、製品品質は未承認である。
検索の待機通知による成功を拒否し、対応する主回答の終了と後続イベント検査だけを受理の入口とした。待機だけ・終了前の回答・終了後の回答を拒否する回帰と、直接質問の復旧案内の整合検査を追加した。これらと既存の質問・計画・検索の利用枠検査は、CLI・Controller の対象 race 実行で通過した。
初回Reviewerの原文読取りは失敗した。送ったハッシュには `sha256:` 接頭辞がなく、原文を読めたとは記録しない。後続の独立再レビューでは、正式な原文索引のID・完全なハッシュと実際の読取りを確認する。
この補正記録を保存した時点では、補正対象の独立再レビュー・公開文面レビュー・配布検証は未実施である。後続の正式結果は Issue #282・関連PRと同一対象の Controller receipt で照合する。独立設計評価、初回の指摘受理、対象回帰の成功は製品品質承認を代替しない。

その後の `96b82cc` 再レビューでは、原文の完全なハッシュによる読取りに成功し、実行・照合・利用量・回帰の3条件は通過した。ただし保存回答の判定は `REQUEST_CHANGES` であり、CLIリファレンスと管理スキル末尾に残った直接質問の未対応案内を指摘した。前述の一箇所だけの案内補正では不十分だったため、旧版の依頼と直接質問の対応条件も含めて両方の案内を同期し、管理スキル全体とCLIリファレンスの矛盾を検出する回帰へ拡張した。新しい補正対象の品質承認、公開文面レビューと配布完了は、この保存回答からは導けない。

`2505ef9` の追加品質レビューも `REQUEST_CHANGES` となり、直接質問の終了前に子エージェントの利用量を受け入れる点と、管理スキルの取消禁止と正式な停止確認付き取消の矛盾を指摘した。質問の個別イベント分岐に分散していた主エージェント確認を全イベント共通の入口へ移し、利用量を含む子エージェント活動を受理前に拒否する回帰を追加した。通常の取消・再送の禁止と、証拠に束縛した復旧取消の条件も区別した。過去の指摘受理は、この新しい補正対象の品質承認ではない。

`64b6ac3` の品質レビューは、保存履歴が回答終了後の出力差分を見逃す点と、検索が順序番号のないイベントを受け入れる点を指摘した。原文と追加承認は読取り済みで、`REQUEST_CHANGES` は正式受理され、元Flowは停止・所有権解放済みである。対象の実行前・結果受理時の全体Go検証が通過しても、この品質判定は変わらない。

保存履歴では正規化した全イベントに終了後の活動検査を適用し、出力差分も主回答の実行中に限定した。検索では全イベントに非ゼロかつ単調増加する順序番号を要求する。通常終了と明示停止の両方で開始前・終了後の差分を拒否し、実行中の差分は保持する回帰と、検索の入力・開始・回答・利用量・終了それぞれの順序番号欠落を拒否する回帰を追加した。`go test -race ./internal/runtime/copilot ./internal/runtime/controller -run "TestSavedTurn|TestSearch|TestIsolated|TestQuestion|TestPlanner"` は通過した。補正後の独立再レビュー、全件race、保護されたCIと配布は、この記録時点では未完了である。

`2a95820` の再レビューは `REQUEST_CHANGES` として正式受理され、元Flowは停止・所有権解放済みである。共通の終了後検査が一時的な空キューで戻る点、検索の未対応イベントの見逃し、OpenACPのメタデータとの不整合、旧履歴の関連付けで整合検査エラーを無視する点が指摘された。

共通の終了後検査は、対応する回答終了の後に、提供者の待機通知かstreamの確定した終了を待つ。遅れて届く子エージェント・実行イベントを拒否する回帰を追加し、待機通知なしで閉じるstreamの既存互換性も維持した。検索ではホストが許可した `taxion_search` の開始と成功した完了だけを対応付け、他のツール・未対応イベント・未完了の取得を拒否する。OpenACPの接続・能力メタデータは回答と区別して扱い、利用量未取得はゼロとして受理しない。

旧履歴の整合検査では、正常な履歴に現行の試行記録がない場合だけを区別し、その他のエラーをpreviewとトランザクション内の再照合で保持する。破損hash・不完全な連番はstore自体が拒否するため、公開復旧の回帰にはstoreを通る逆順の時刻を使い、native履歴の観測前に拒否することを確認した。追加検証で見つかったfixtureの不備と閉じたstreamの互換性を補正した後、CLIとControllerの対象race、およびOpenACP全件raceは通過した。補正後の独立品質判定、全件race、保護されたCIと正式配布は未完了であり、元運用への適用も行っていない。

`061da3b` のレビューでは `APPROVE` が正式受理されたが、主要ソースの読取りに失敗していたため品質承認として採用せず、公開を保留した。未使用の再レビュー枠を同一候補の完全レビューへ変更する承認を得て、元の判定を書き換えずに再確認した。その結果は `REQUEST_CHANGES` として正式受理され、Flowは停止・所有権解放済みである。

指摘は、保存履歴で正規化済みの権限・圧縮イベントが照合前に欠落する点と、検索の共通処理が取得なしでも成功する点である。公開検索callerは後段で取得なしを拒否していたが、共通処理の成功判定と保存する観測の整合は別に修正する必要があった。

質問の許可イベントをSDK非依存の共通規則にし、live質問、保存した質問の正規化入口、公開復旧の受理検査に適用した。保存した質問ではreceipt用の投影より先に全ての正規化済みイベントを検査する。一方、通常のReviewer復旧は質問とは異なるAPIのまま扱い、既存のreceiptの順序番号に対応する投影を変更しない。権限・圧縮・提供者エラーのある保存質問を通常終了と明示停止の両方で拒否し、通常のReviewer履歴は従来どおり照合できる回帰を追加した。公開previewでも、提供者から返る権限・圧縮・未対応イベントを拒否する。

検索の共通終了判定は、許可した `taxion_search` の開始と成功した完了を必須とした。取得なしを拒否する回帰を追加し、利用枠を検査する正常系fixtureにも実際の取得イベントを含めた。Copilot・Controller・CLIの対象raceは通過し、公開復旧の追加検査後のCLI対象raceも通過した。補正後の独立品質判定、公開文面、全件race、保護されたCIと正式配布は、この記録時点では未完了である。

`1eb9afc` の独立レビューは `REQUEST_CHANGES` として正式受理されている。
一部の呼出しの cache-read、cache-write または nano-AIU が不明な場合、
既知の小計を下回る native 集計まで矛盾なしと判断する不具合が残っていた。
共有の利用量比較で、完全な呼出し別内訳がなくても既知の小計を下回る集計を拒否する。
不明な内訳をゼロと推定せず、小計以上の集計を不足だけで矛盾とみなさない。
3次元の回帰は修正前に全て失敗し、修正後は通過した。
公開復旧の対象試験、CLI 全通常・全 race、build、vet、diff check も通過した。
この補正後の候補に対する独立品質承認、保護された公開検査、正式配布と
下流の実運用再開は未完了であり、過去のレビュー結果を合格へ書き換えない。

別の停止済み Flow に未開始の `ISSUED` Dispatch が残ると、対象の取消を相互に
遮断する不具合を確認した。同じ workspace に2個の停止 Flow を作る回帰は、
修正前の公開 reconciliation で `another Dispatch is active` と失敗した。
共有判定では、停止・hold・所有権なしの対応 Flow、未開始状態、実行証拠と
runtime receipt の不在を照合し、Graph と ledger の拒否条件を揃えた。
停止 code や役割の許可一覧は追加せず、他の Flow・Dispatch は変更しない。
対象の取消後に他の保存指示が不変であることと、他方の稼働・session・receipt による
拒否を含む通常・race の対象試験、build、vet、gofmt、diff check は通過した。
この追記時点で新候補の全体検証、独立品質・ADR適合判定、保護されたCI、
正式配布と下流の実運用再開は未完了である。

`c8be87b` の独立レビューは `REQUEST_CHANGES` として正式受理された。
SDK正規化の非転送結果が、既知の非実行メタデータと未対応イベントを区別しておらず、
live配送と保存質問の検査の前に未分類の活動が消失していた。
拒否側に個別の例外を足さず、共通正規化で未分類の観測を中立なイベントとして残す。
既知イベントの変換失敗も黙って捨てず、入力不正として伝える。
未対応のモデル呼出し通知は、子呼出しや編集要求を含み得るため非実行メタデータと
みなさない。SDKが既知の型として復号しても、対応する受入れ契約がなければ拒否する。
質問・検索の既存受入れ条件を緩めず、通常Reviewerの索引付きreceipt投影は維持する。
SDK観測からlive配送と保存回答読取りへ至る回帰は、修正前に失敗した。
未知型・未対応の既知型・終了前後・明示停止の拒否、正常メタデータと通常receiptの
互換性、実際のSDK読取りプロトコルでモデルを送信しないことを検査する。
Copilot全件raceと、Controller・CLIの質問・保存回答・公開復旧の対象race、
build、vet、gofmt、diff checkは通過した。未知活動のController拒否、
不正利用量の変換失敗と子メタデータの観測保持も対象に含めた。
元の運用事象の最初のreceipt停滞原因を、この独立レビューの指摘と同一と断定しない。

`c8be87b` のController全件raceは30分のsuite上限で終了した。
最後の実行中testは8秒であり、個別のdeadlockが原因と断定せず、完走とも記録しない。
新候補の独立品質・ADR判定、保護されたCI、正式配布と実運用再開は、
この追記時点では未完了である。

`27e7057` の独立レビューは `REQUEST_CHANGES`、ADR-0074は `FAIL` として
正式受理された。入力検証と結果受理時のbuild・vet・全通常試験・diff checkは通過したが、
terminal receiptがない復旧で、保存された個別利用量を生成処理が捨て、
native集計を期待値へコピーする不具合が残っていた。
比較関数単体の回帰では、この呼出し元による自己比較を検出できなかった。

共有の終了観測生成では、保存された `EventUsage` があればその個別値を保持し、
不正な値の復号を拒否する。個別記録がない旧履歴では既存のnative集計入力を維持する。
preview、apply時の再照合、旧履歴関連付けは同じ生成処理を使い、
既存のreceipt・停止・承認・利用量を変更しない。
公開 `request recover` の回帰をterminal receiptの有無で実行し、
native集計が既知利用量を下回る場合の拒否と、不完全な集計での既知利用量保持を
previewからapplyまで検査した。元実装を使うGo source overlayでは
`cancellation accepted a known conflict in native usage totals` と失敗し、
補正後はCLI全件race、build、vet、gofmt、diff checkが通過した。
この補正後の独立品質・ADR判定、保護されたCI、正式配布と実運用再開は未完了である。

`8498a98` の追加Reviewerは `SUSPENDED / BLOCKED` として保存された。
実際に提示されたworktree名から `-flow` を省いたパスを要求し、直接読取りが拒否された。
正しいパスは実行環境の情報に含まれており、読取り成功や全体品質確認とは記録しない。
対象の入力検証は通過したが、ADR-0074はFAIL、ADR-0075・0076はBLOCKEDである。

供給diffから指摘された利用量の不完全性は、実コードのモデル不要回帰でも再現した。
比較処理が「既知小計以上で矛盾なし」を「完全一致」と扱っていた。
前の3次元回帰は矛盾の拒否だけを確認し、通常継続の一致判定を確認していなかった。
共有比較で、native集計がある次元の個別内訳が不完全なら、一致を返さない。
既知小計未満の集計は引き続き矛盾として拒否し、小計以上の不完全な集計は
停止・不確実性承認付き取消に限って保持する。個別・集計とも任意値が未提供の
既存互換性は変更せず、未知値をゼロと推定しない。
公開previewの通常継続は修正前に誤受理され、補正後は同じ利用量受入れ条件で拒否された。
receipt有無双方の取消previewからapplyで、既知費用とunknown状態を保持する。
3次元の一致・矛盾の区別と既存完全一致・旧履歴の互換性を含むCLI全件raceは
152.394秒で通過し、build、vet、gofmt、diff checkも通過した。
この新しい補正後の独立全体品質・ADR判定、保護されたCI、正式配布と
下流の実運用再開は未完了である。過去の停止結果を合格へ書き換えない。

外部連携用の5環境変数を `0` にして、許可のない GitHub・GitLab・Copilot integration を実行しない。
必要な最終確認は `go build ./...`、`go vet ./...`、`go test ./...`、
変更した runtime package と CLI の race test、`gofmt`、生成 completion と `git diff --check`。

## 実運用との区別

native 保存履歴の復旧回帰は合成 fixture と SDK reader の検証である。
元の運用store・native session へアクセスせず、元実行の再送、製品実行、別 store による迂回、旧 FAILED 記録の変更、期限や累積予算のリセットを行っていない。
最初の receipt 受理停滞の個別原因と、元 native metadata が復旧条件に適合するかは未確認である。
公開版の正式な復旧操作も、対象 operation の停止確認と exact preview の人間承認を別途要する。取消後の通常 gate は免除しない。

## 互換性と延期項目

version-2 request の限定 legacy association をサポートする。
version-1 checkpoint、binding のない旧直接質問、非 Copilot provider の保存履歴照合は対象外とする。
新しい receipt を削除して旧版へ戻さず、更新前の整合した private backup を保持する。
汎用 scheduler、無条件の履歴復元、自動再送、未知利用量の推定補完は実装しない。
リリースノートは [ADR-0037](adr/0037-publish-release-notes-on-github-only.md) に従い GitHub Release に保存する。
