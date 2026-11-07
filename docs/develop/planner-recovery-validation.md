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
