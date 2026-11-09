# Planner・直接質問の照合と所有権の検証

この記録の「実業務の対象未特定」をOSS開発の停止理由とした判断は誤りです。
[2026-11-09の方針訂正](#oss開発と実業務利用の方針訂正2026-11-09)を適用してください。
当時の候補・試験・予算・権限の記録と、現在のOSS開発方針は区別します。

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

`e818850` の追加レビューは、保存回答では `REQUEST_CHANGES` だったが、
acceptance の参照が結果全体の evidence に含まれておらず、Controller は正式に
`FAILED` とした。保存回答のADR合格記述を、正式な品質承認として扱わない。

SDK `v1.0.22` の原文と保存履歴を確認すると、`session.usage_checkpoint` は
cache の管理情報だけでなく、先行する利用量receiptを包含する累計会計snapshotだった。
最初の不整合は、SDK正規化がこの観測を未検査で捨てる段階にある。
個別利用量と累計snapshotを同じ呼出しとして加算すると、同じ費用を二重計上する。

累計snapshotをSDK非依存の別イベントで保持し、質問・検索・回答終了後の検査へ渡す。
通常受理では、それまでの個別利用量と累計を照合し、scopeとwatermarkも確認する。
保存した質問の累計は個別receiptへ変換せず、通常Reviewerの既存receipt索引も変えない。
公開復旧のpreviewとapplyは同じ照合を使い、不完全な観測では通常継続を認めない。
明示停止と不確実性承認付き取消では、帰属不明の既知費用も捨てず、
watermarkで累計の外にあると確認できた後続receiptの費用だけを加える。
未提供の値をゼロや存在しないモデル識別子で補わず、旧aggregate-only入力は維持する。

追加回帰は実SDK型から保存質問への読取りと通常Reviewerの索引互換性、
live・終了後双方の質問受理、scope不一致・不正値の拒否、公開取消での既知累計費用保持を
対象とする。修正前のGo overlayではSDK観測の消失と、receipt有無双方の
公開取消での既知費用消失を再現した。
全通常試験、build、vet、SDK・Controllerの対象raceとCLI全件raceは通過した。
global費用のpartial状態を追加保持した後も全通常試験と対象raceを通過し、
値がないpartial観測を消さない最後の条件補正は対象raceで検証した。
同一候補の実行前・結果受理時の必須検証は別途維持する。
この追記時点では、
新候補の独立全体品質・ADR判定、保護されたCI、正式配布と実運用再開は未完了である。

`0e061f9` の第五Reviewerは原文と実候補の読取りに成功したが、
判定は `REQUEST_CHANGES`、Controller結果は `FAILED`、ADR-0074は `FAIL` だった。
入力段階のbuild・vet・全通常試験・diff checkは、同一treeで全て通過した。
Reviewer結果受理段階の検証は未実行であり、入力検証をその代用としない。

未解決の指摘は、SDKの累計費用がゼロの場合の観測消失、
`ProviderModelMetrics` の観測消失と、待機通知後の一時的な空キューによる早期受理である。
正規化で正の値だけを保持する条件とprovider/model集計を扱わない実装、
終了後検査の非blocking分岐はソースで確認した。
指摘されたゼロ集計の取消経路と遅延イベントの実運用再現は未確認である。
SDKは費用の完全性も別に定義するため、ゼロを無料と推定したり、
提供者やモデルが不明な集計に架空の識別子を補ったりして修正しない。

同一のpinned scanner `betterleaks 1.9.0` による未公開commit全範囲の検査は、
scannerだけのCPU affinityを限定した別試行でもcommit treeの期限で終了した。
検出なしや全範囲PASSとして扱わず、ルール・期限・公開条件を緩和しない。
第五審査の保存結果・native履歴・管理worktree・候補の未追跡ファイルを保全した。
承認された追加審査枠は使い切っており、過去の合格を新候補へ転用しない。
この時点で修正完了・公開資格は未達であり、PR、CI、merge、配布と実運用再開は未実施である。

外部連携用の5環境変数を `0` にして、許可のない GitHub・GitLab・Copilot integration を実行しない。
必要な最終確認は `go build ./...`、`go vet ./...`、`go test ./...`、
変更した runtime package と CLI の race test、`gofmt`、生成 completion と `git diff --check`。

## 第五審査残件のSDK契約修正（2026-11-09）

調査開始時の候補は `fix/native-event-contract-083`、
HEADは `e00cae25b58952f164b3a53794061a86f24f4c14`、worktreeはcleanだった。
`e00cae2` の直接の親はコード修正 `0e061f9475a34b96b067605701c5295dcf6ba3f5` である。
依存SDKは `github.com/github/copilot-sdk/go v1.0.22` のまま変更していない。
公開mainの基準 `6a9f91255e3dd566fefb62032395a88f5d16cff1` から元候補までは75ファイル、
5,482行追加・226行削除であり、今回の差分とは別である。
第五審査の `REQUEST_CHANGES`／Controller `FAILED`／ADR-0074 `FAIL` と保存証跡は変更しない。

### 追証結果と最初の不整合

以下の「確認済み」は候補とSDKのソース契約についての判定であり、元の本番障害の個別原因を確定したという意味ではない。
行番号は今回の修正候補に対応する。

| 問題 | 元候補の実態と最初の不整合 | 修正した契約 | 確度・参照 |
| --- | --- | --- | --- |
| ゼロ累計 | SDKの非pointer `TotalNanoAiu` はwire欠落と明示0をデコード後に区別できない。正数だけを設定する正規化と、空checkpointを完全扱いする受入れ判定が併存していた | pointerで存在が観測できた費用と `complete` を保持する。根拠のない0は未知のまま、明示的な完全ゼロだけを受理する | 確認済み。`copilot/usage_checkpoint.go:17 normalizeUsageCheckpoint`、`runtime/usage_checkpoint.go:21 DecodeUsageCheckpoint` |
| 個別Provider | `AssistantUsageData` に存在するID・kind・labelを中立JSONへ渡していなかった | `DispatchUsage` のoptional Provider・表示名・modelProviderを保持する。旧記録のProviderは推測しない | 確認済み。`copilot/copilot.go:1229 normalizeEvent` |
| checkpoint／shutdown集計 | Provider/Model集計を正規化・保存回答へ渡さず、shutdownのnested snapshotも復旧へ届かなかった | 集計をreceiptとは別の `UsageCheckpoint` に保持し、保存回答、質問・検索結果、terminal receipt、公開復旧へ引き継ぐ | 確認済み。`copilot/usage_checkpoint.go:91 normalizeUsageShutdown`、`copilot/saved_turn.go:56 savedSessionTurn` |
| 二重計上 | SDKのProvider/Model集計は先行receiptを包含する。今回、既存実装による実際の二重課金は確認していない | global・model・Provider/Modelは同じ費用の別集計として最大値を採用し、watermark外のreceiptだけ加算する。完全なscope付きsnapshotはreceiptが保存されていなくても照合できる | SDK包含契約は確認済み。既存の二重課金は未確認。`runtime/usage_checkpoint.go:251 KnownNanoAIU`、`core/usage.go:19 CompareProviderModelUsage` |
| 早期受理 | `drainCompletedTurn` はidle後の一時的な空queueを成功とし、境界前のchannel closeも成功にしていた | 回答の `assistant.turn_end` とproviderの通常idleを分ける。空queueと境界前のcloseを成功証拠にしない | 確認済み。`controller/completed_turn.go:13 drainCompletedTurn` |
| Stop時の欠落 | Abort RPC成功後にstopped設定・watcher終了を行い、後から来るcallbackを履歴へ記録しない経路があった | main idleを履歴・watcher queueへ記録してからStopを解除する。待機取消ではsessionを保全する。Disconnect失敗でもbindingを捨てない | コード経路は確認済み。元のnative履歴で欠落した個別イベントは未確認。`copilot/copilot.go:1202 onEvent`、`:1404 emit`、`:1447 stop` |
| Autopilot idle | SDKの `Mode` を参照せず、継続境界をターン完了へ正規化していた | Autopilot idleは継続、aborted idleは取消として区別する。Abort通知やモデルエラーだけではidle待機を解除しない | 確認済み。`copilot/copilot.go:1229 normalizeEvent` |

SDK側の根拠は `rpc/zsession_events.go:853 SessionUsageCheckpointData`、
`:2236 SessionIdleData`、`:2925 SessionShutdownData`、
`rpc/zrpc.go:25370 AiCreditsStatus` と `session.go:1556 dispatchEvent`、`processEvents`、`Abort` である。
SDKはcallbackをFIFOで同期実行するが、idleの受信だけで費用の完全性やsession切断まで保証するものではない。
`EventsAfter` の保証はTaxionがすでに記録した履歴の再送に限られ、未到着callbackの取得には使わない。

### 修正範囲と既存修正の評価

`0e061f9` のcheckpoint／receipt分離、known-cost floor、watermark・scope検査を維持した。
不足していたのは、SDKからその契約へ渡す情報と、回答生成・idle・停止を同一視していた境界条件である。
ゼロ値の問題とProvider情報の問題は異なる変換欠落であり、非同期完了は別のライフサイクル不整合である。
同じ単一原因とは断定しない。

今回の追加は既存DTOのoptional項目と既存の正規化・保存・照合・停止処理に限定した。
新しいイベントファミリー、SDK依存変更、汎用usage基盤、固定sleep、審査回数変更は導入していない。
ADR-0075/0076、古い承認・receipt・native履歴、運用storeは変更していない。

### 回帰対象と残る制約

SDK wireの費用欠落／明示0、完全ゼロ、partial／正数、Providerあり／不明、
同一Modelの複数Provider、nested／top-level集計、shutdown読取り、
scope不一致・重複Provider・既知receiptより小さい集計・費用欠落を検証する。
質問のlive／drainからStoreに保存したterminal receiptまでProvider/Model集計を確認し、
保存checkpointの順序、receiptなしのscope付き復旧、partialを完全へ昇格しないことを確認する。

非同期試験はchannel、callback、sequenceで同期する。
通常idle・Autopilot idle・aborted idle・child idle・Abort通知・失敗通知を区別し、
Abort応答後にidleを保留してStopを取消した場合のsession保全、
その後のusageとidleの履歴・cursor・再送をSDK RPC fixtureで確認する。
境界前のchannel close、取消、期限、後続実行の拒否も維持する。
再接続だけでbusyと推測しない。観測したmain turn startで完了待機を登録し、childの開始では登録しない。

これらはモデルを起動しないローカル契約試験である。
本番のイベント発行順、元の運用storeのpreview/apply、独立Reviewerによる新候補の合格、
公開CI、公開・配布・TKB3復旧は未確認・未実施であり、この修正の成功証拠へ混ぜない。
旧データを新しいProviderや完全費用へ補完しないため、不完全な既存履歴は引き続き保留または承認済み取消を要する。

SDK `session.go:2077 Disconnect` はRPCに `context.Background()` を使い、
RPC失敗時にもlocal event processingをretireする。
そのため、呼出し元の期限だけでDisconnectの待機時間を制限できるとは保証しない。
失敗後の同じbindingは明示cleanup retryのために保持するが、live sessionとして再送しない。
idle後にSDKへ到着する未知の会計イベントを、StopやEventsAfterで全回収できたとも主張しない。
費用の完全性は保存checkpointのstatus、scope、watermarkと個別receiptの整合で判定する。

### 最終ローカル検証と利用量

| 検証 | 結果・範囲 |
| --- | --- |
| gofmt、go build ./...、go vet ./...、go test ./...、git diff --check | 最終コードでPASS。通常試験2,776件、23package |
| 対象race | core／runtime／copilot／CLIの904件とController関連177件がPASS。最後のmain turn start修正後はcopilot全310件もPASS。件数は重複を含むので合算しない |
| 公開request recover preview／exact-hash apply回帰 | receiptなしのscope付き完全snapshotを含む10ケースPASS。合成fixtureであり本番storeの成功証明ではない |
| go test -race ./... | Controller packageが600秒suite timeout。全raceは未完了でありPASSではない。timeoutだけでdata raceやdeadlockとは断定しない |
| 全indexed内容のsecret scan | Betterleaks v1.9.0で5.91 MBのsnapshotをscanし、leakなし。別indexからexportし、repositoryの実indexは変更しなかった |
| 未公開commit全範囲のsecret scan | main基準6a9f912..元候補e00cae2をcheck-rangeで実行。fb671283929932cc0e45e60fbdfd1f0fcab1d00cでcontext deadline exceeded。未完了でありPASSではない |

Scannerは公式v1.9.0 linux_x64 archiveを公開checksumと照合した。
SHA-256は `f8b185a39ffcece2a1ca82bf3a4e7435cd81963ffd16b7a9128daf75f35f6de7`。
全indexed内容のscanとcommit履歴全範囲のscanは別の検証であり、前者で後者のtimeoutを補わない。

外側operatorの正規照会は `taxion runtime quota --account hrykono --approve-account-read --cli /home/onohr/.local/bin/copilot`。
`hiono` はGitHub操作用でありquota照会先ではない。
`2026-11-08T23:22:48.106Z`（08:22 JST）の `sdk.account.getQuota` は
19,600/50,000 requests使用、account残30,400を返した。
人間提供の起点19,400との差200 requestsは同一accountの累計差分であり、厳密な作業別課金ではない。
承認済み作業上限20,200、reserve 50を維持すると、この観測時の作業枠は550 requestsとなる。
固定期限08:46 JST、審査枠は変更していない。
Session AIC usは別単位でありrequestsへ換算していない。
照会結果の `executionAuthorized:false` は追加の実行・審査承認ではない。

第六審査、push、公開CI、公開・配布、共有環境への適用、TKB3復旧は実施していない。
Issue #282への申し送りは修正commitとこの記録を参照し、第五審査のFAILを維持する。

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

## 再開監査の変更前分析（2026-11-09）

### 基準と履歴

今回の基準は未公開の `3aa73017d6561ce38dbf208e9cb46c6194f90ba6`、
branch `fix/native-event-contract-083` のcleanなworktreeである。
公開mainは `6a9f91255e3dd566fefb62032395a88f5d16cff1`。
候補branchはremoteに存在せず、GitHub上で候補を審査したとは扱わない。
`0e061f9`、`e00cae2`、第五審査のFAILを保持する。

変更前のbuild、vet、通常試験2,776件／23packageを再実行し、PASSを確認した。
Controller全raceは783件PASS、8件SKIP後、suite全体の600秒timeoutでINCOMPLETEとなった。
最後の `TestRecoveryPreservesSuccessfulResultForHandoff` 自体の経過は1秒で、
stackはSQLite初期化中だった。timeoutだけからこの試験のdeadlockやdata raceとは判定しない。
完全JSONログはoperator証跡 `083-baseline-controller-race.jsonl` に保持する。

過去の修正は別々の境界を対象としていた。
`c5778d7` はrequest復旧、`2a95820` はmain turn終了とSearch順序、
`3b76f6a` は保存advice受理、`c76b097` はpartial subtotal矛盾、
`0e061f9` と `3aa7301` はSDK checkpoint／Provider／idleを対象とする。
PR #269の失敗Reviewer回答追記も、PR #280の保存review apply診断も、
Plannerの正式受理やusage完全性、所有権解放を代替しない。

### 不整合と影響範囲

| 不整合 | 発生点と伝播先 |
| --- | --- |
| checkpoint観測が会計へ届かない | Dispatch `StreamEvents` → `recordEvent` → Graph → task／stage／Flow予算。保存turnもreceipt保存だけではGraph会計に反映されない |
| watermarkより後のreceiptをsnapshot内として比較する | 共通 `DecodeUsageCheckpoint` → Question／Search／drain／保存回答復旧。`KnownNanoAIU` の包含規則と不一致 |
| 観測と受理を混同する | Question／Search／drainがpartial checkpointを即拒否し、Questionは完全checkpointだけの回答も拒否する |
| Planner計上がcheckpointを落とす | `QuestionResult` → `recordPlannerUsage` → Operation累計／復旧時の差分計上 |
| 子agentを親の完了に使う | Dispatch `StreamEvents` のoutput／terminal consumer。SDK SendAndWaitやQuestionとは異なる |
| 新usage metadataのcopyが不完全 | Graph cloneのProvider、費用、status、追加token等のalias → 保存前後の独立性 |
| identityによる重複排除が不統一 | Dispatch receiptにはあるがQuestion／Search／drain／保存回答の配列にはない |

これらは実装経路で確認した欠陥であり、本番で二重課金や誤受理が実際に起きたという観測ではない。
Issue #282のPlannerStarted残存とは、回答受理・累計会計・cleanupの順序で接続する。
所有権guardを削除して解決しない。

### 修正設計と不変条件

既存 `UsageCheckpoint` のJSON契約を維持し、個別API callとは別の観測として保持する。
Graphに保持する必要がある場合も同じDTOを共有し、新しい正規形式や架空receiptを作らない。
費用集計ではsnapshotの包含範囲と範囲外receiptを分け、最大の既知floorを使う。
不完全な観測は保存し、最終受理や予算判定とは区別する。

変更後も次の条件を維持する。

- 未取得、明示ゼロ、partial、非該当を同一視せず、欠落を推測で補わない。
- watermarkで覆われたcallを二重計上せず、後続callの既知費用を失わない。
- session／Provider／Model／Accounting identityの矛盾を、別の完全snapshotで迂回しない。
- 同identity・同metricsは冪等、同identity・異metricsは拒否する。
- checkpointのない旧データのJSONとPlanner usage hashを維持する。
- main完了と子agent完了、Abort応答とidle、保存回答と正式受理を区別する。
- liveとsavedのscope・会計条件を一致させ、元deadline・承認・所有権制約を緩めない。
- Graph clone後のusage metadataを変更しても原本が変わらない。
- empty queueやSDK Disconnectを、未到着イベントを含む完全回収の証明にしない。

共通DTO／包含判定、全consumer接続、Planner受理・会計の順で変更単位を分ける。
各単位で変更前の再現試験と直接・関連経路の回帰を比較する。
snapshotのみ、receiptとの重複、watermark外tail、partial tail、複数Provider、
子agent終了後のmain継続、保存後の再読込、clone独立性を重点対象とする。
正当な旧操作を新guardで拒否した場合も回帰として扱う。

### 実利用と権限の未解決事項

PATHのbinaryはrevision `4334fed`、表示 `v0.7.21-rc.13`、対応skill `1.3.19`。
installed skill `1.3.28` とは `ready --check-skill` がincompatibleを返した。
この組合せで管理操作や実モデル実行を進めず、共有installを変更しない。
当時の報告は、実業務worktreeと承認済みTask／Flowの未特定を停止理由として扱った。
これはOSS開発と実業務利用の混同による誤りであり、下記の方針訂正で撤回する。
実業務開発の再開は未実施だが、機密workspaceの提示をOSS側の修正・検証の前提にはしない。

09:59 JSTの正規account照会は19,700／50,000 requests使用。
承認上限20,200とreserve 50を維持した作業残枠は450 requestsである。
outer期限は人間が追加承認した12:00 JSTとし、旧Flow期限は変更しない。
第六審査、外部実行依頼、push、公開・配布、TKB3復旧は禁止のままである。

### 検証中の設計見直し

最初の全対象package試験では、既存Evaluationの欠落・重複usageケースと、
receiptなしsnapshotの復旧ケースが不合格になった。この候補は不合格のまま原因を再分析した。
共通のFlow fixtureへcheckpointを常時追加したため、従来「個別receipt以外の証拠なし」を前提とした試験の入力を変えていた。
既存fixtureの入力と期待値を維持し、checkpoint付きFlowは明示した別ケースへ分離する。
重複・scope不正のusageを集計不能としても、観測そのものを「未取得」へ変えない。

復旧側では、native aggregateを個別receipt配列へ入れる旧fallbackと、
新しいscope付きcheckpointを同時に使うことが不整合だった。
checkpointがある場合はaggregateを個別callへ変換せず、元のcheckpointとして集計する。
checkpointのない旧fallbackと旧hashは維持する。

外部予算契約では、完全な回答・token照合と、creditsの完全性を別々に判定する。
有効なpartial checkpointだけを理由に、liveで受理できる回答をsaved経路で拒否しない。
この場合もcreditsはunknownのまま保持する。
正のnative費用上限、scope矛盾、既知subtotal矛盾、停止・期限・所有権の各guardは緩めない。
公開CLIのpreview／exact-hash applyと既存の拒否試験で、この区別を確認する。

さらに、aggregateから架空deltaを作らない変更でも、旧reconciliationの
「保存済み消費を下回る最終aggregateは拒否する」という条件を維持する必要がある。
checkpointと最終aggregateの既知subtotalを比較し、双方がcompleteなら総量の一致も要求する。
より小さい費用・tokenと、complete checkpointより大きい費用を拒否する回帰ケースを先に追加し、
新しいcheckpoint分岐が従来の拒否条件を迂回することを再現してから修正する。

### 再接続した契約と検証証跡

既存checkpoint DTOとdecoderをcoreへ移し、runtimeの公開型・関数はalias／wrapperとして維持した。
Graph、Dispatchのlive／saved受信、task／stage／Flow予算、Evaluation、
Plannerのlive／recovery累計で同じ包含判定を使う。
Graph cloneはProvider、Accounting identity、追加token、費用、status、snapshotのnested値を独立にcopyする。
review-finalizationとverified-resultはcheckpointもbindする。
checkpointなしの旧Planner usage hashと旧result hashは変更しない。

| 確認 | 今回の結果 |
| --- | --- |
| gofmt、build、vet、diff check | PASS |
| 通常試験、公開CLI合成fixture有効 | 最終treeは2,802件PASS、32件SKIP、FAILなし。変更前は2,776件PASS。件数にはsubtestを含む |
| 全repository race、公開CLI合成fixture有効 | PASS。2,802件PASS、32件SKIP、FAILなし。22package PASS、1packageは試験なし。Controllerは1,657.607秒で完了し、既存CIの30分timeout内 |
| public-text repository policy | UNVERIFIED。必須の `TAXION_PUBLIC_PRIVATE_IDENTIFIER_SHA256` が環境にないためfail-closed |
| indexed secret scan | 最終indexのtree IDと結果を同一commit本文・operator証跡へ記録する。未実施・失敗・timeoutはPASSとしない |
| 未公開commit全範囲のsecret scan | INCOMPLETE。既存20commitを10件ずつの祖先範囲へ分けて同時実行した追試も、各595秒と終了猶予5秒以内でtimeout。範囲全体や後続commitをPASSとしない |
| 実モデル、本番store、公開CI、第六審査 | UNVERIFIED／未実施 |

operator証跡の最終通常試験は `083-consumer-final-normal-v2-20261109.jsonl`、
最終raceは `083-consumer-final-race-v2-20261109.jsonl`、
途中の失敗は `083-consumer-intermediate-failures-20261109.txt` に保持する。
未コミット候補全体は `083-consumer-prevalidation-20261109.tar.gz` とpatchにも保全した。
turn-start fixture修正後の候補は `083-consumer-final-v2-20261109.tar.gz` にも保全した。
最終確認後の差分で新たな失敗が確認された場合、これらの結果だけで合格としない。

公開CLI fixtureを有効にした最初の全repository raceは、2,800件PASS、32件SKIP、
`TestFailedReviewPublicCLISynthetic` 1件FAILだった。data raceの報告はなかったが、suiteはFAILである。
同じ試験は非raceでも失敗し、保存した基準 `3aa7301` のarchive上でも同じnative-read拒否を再現した。
基準の追証は `083-baseline-public-cli-race-20261109.log` に保持する。
fixtureがtool完了と回答の間のmain turn startを欠いており、既存readerが正しく拒否していた。
本番readerのguardや期待値は緩めず、fixtureに対応するturn startを追加する。
turn startなしを拒否するreaderの負例も追加し、通常・race双方で再確認する。
この既存不合格を変更による回帰や合格と混同せず、最終treeの結果を別に記録する。
fixture修正後はbuild・vetと通常・raceの全repository試験を再実行し、上表の最終結果を得た。
本番readerの変更はない。既存の負例も維持し、確認した試験では変更による新しい失敗は残っていない。
実モデル等の未確認範囲を含む回帰ゼロや、第五審査の合格を意味しない。

range scanの境界は公開mainから `f4cba6a9026d06607a7bcc72d2bba4704357a459`、
同commitから `3aa73017d6561ce38dbf208e9cb46c6194f90ba6` までとした。
両方ともexit 124であり、`083-range-a-20261109.log`／`083-range-b-20261109.log` と実行結果を保持する。
scannerは公式release checksumを照合したBetterleaks 1.9.0をtask-local配置し、
共有install、remote、既存commit、operation policyは変更していない。

### 利用経路ごとの制限

以下は実モデル・実業務環境での利用判定の記録であり、
OSS側の読み取り、修正、隔離fixture検証を一律に禁止する判定ではない。

| 利用経路 | 実利用判定と根拠 |
| --- | --- |
| 調査・読み取り | CONDITIONAL GO。正規quota照会、Git／SDKの調査、隔離storeのfixtureに限定する |
| Planner | NO-GO。live／savedの会計・正式受理の合成回帰は確認したが、実モデルでの正式受理・終了・保存・復旧は未確認 |
| Coder | NO-GO。合成Flowの開始・保存・復旧を製品実装の成功とは扱わない |
| Reviewer | NO-GO。第五審査FAILを維持する。失敗回答の追記は品質受理・実行許可・会計確定ではない |
| cancellation／resume | NO-GO。実セッションでの中断・復旧と未到着イベントの回収は未確認 |
| usage／quota | CONDITIONAL GO。requestsの正規account readのみ。unknown creditsの確定・ゼロ補完・換算はしない |
| store ownership | NO-GO。本番storeの確認・復旧は未実施。隔離fixtureの所有権・別workflow試験で代替しない |

SDKのAbort RPC応答はidle到達ではなく、空queueもcallback終了の証明ではない。
Disconnectの待機とidle後の未到着eventの完全回収は保証しない。
Providerがnilの観測は、根拠なく非該当や特定Providerへ変換しない。
観測値の既知floorと完全な費用確定を区別する。

### CLIの追証と開発再開の申し送り

共有installを変更せず、今回のsourceからtask-local binaryをbuildした。
候補worktree直下ではoperation policyがなく、entrypointが `stateChanged:false` で拒否した。
policyを作成・緩和せず、既存の管理対象workspaceからread-onlyの `ready --check-skill` を行うと、
候補binaryとinstalled skill `1.3.28` は `compatible:true` だった。
PATHの旧binaryとskillの不適合が解消したわけではなく、候補の公開・配布・採用も行っていない。

新しいGraph checkpointはoptionalで、旧storeの読み込みとcheckpointなしのhashは維持する。
新fieldを含むstoreを旧binaryへ渡せるとは保証しない。
rollbackのためにcheckpointを削除せず、互換性を確認したbinaryと整合したbackupを使う。

機密workspace、実業務のTask／Issue、受け入れ条件の提示はOSS版Taxion開発の前提条件ではない。
OSS側でTaxionを安定させ、安定版を実業務環境へ導入し、非機密の知見をOSS側へ戻す。
実業務開発の再開は未実施であり、Issue #282の修正をその成功とは扱わない。
第六審査・外部実行依頼・公開・配布・TKB3復旧は追加承認なしに開始しない。
11:38 JSTの正規照会は20,100／50,000 requests使用で、
承認上限20,200・reserve 50を維持した観測時残枠は50 requestsだった。
この残枠を新しい審査の承認として使わない。outer期限12:00 JSTと元Flow期限を維持する。

## OSS開発と実業務利用の方針訂正（2026-11-09）

停止理由の訂正は、保存済みcommit `84406f11d2703d88ae61b811dc61d1956dc37085` を起点とする。
同commitと先行する `0e061f9`、`e00cae2`、`3aa7301` は改変せず、
訂正前の記録をGit履歴で参照できるように保持する。

誤認は、当初のOSS開発と実業務利用の分離を判断へ反映せず、
実業務の開始条件をOSS側の安定化の前提へ転用したことによる。
従来のADR-0002は実装・履歴の移植を禁じていたが、
開発Agentの入口には「機密workspaceの提示をOSS開発の前提にしない」という指示がなかった。
合意を適用しなかった判断を、対象情報の不足や利用者側の問題として扱わない。

| 訂正前の記録（`84406f1`） | 訂正 |
| --- | --- |
| 「実利用と権限の未解決事項」で実業務worktree／Taskの未特定を停止理由にした | 機密workspaceの提示はOSS開発の前提ではない |
| Plannerの利用判定に「対象Taskは未確認」を含めた | 実モデルの受入れは未確認。実業務Taskの未提示でOSS修正を禁止しない |
| 「CLIの追証と開発再開の申し送り」で本来の製品workspace／Task未特定を前提にした | OSS側でTaxionを安定化し、実業務導入と非機密フィードバックを分ける |

方針の正本は [公開情報と出典の方針](privacy-provenance.md#oss-development-boundary) に統合し、
ADR-0002で適用範囲を確認する。[開発Agentの指示](../../AGENTS.md) から参照して判断に適用する。
実業務環境内の承認済み機密コード処理、Runtime・DTO・store・既存の拒否条件と承認境界は変更しない。

旧枠の停止時点は20,150 requests使用、承認上限20,200、reserve 50で作業残枠0だった。
12:00 JSTの期限も経過していた。停止理由は利用可能な承認済み予算の消尽と期限経過であり、
機密workspaceや実業務Taskの未提示ではない。
追加承認後の13:33 JSTの正規照会は20,200／50,000 requests使用で、
上限20,700・reserve 50を除いた観測時残枠は450 requests、
新しいouter期限は `2026-11-09T16:25:42.772+09:00` である。
元Flowの期限や審査回数は変更しない。

技術的な未解決事項と、予算による停止・再開は別に記録する。
SDK Disconnectの待機とidle後の未到着eventの完全回収は未保証、
全未公開履歴のsecret scanはINCOMPLETE、
保護識別子の網羅確認、実モデル、本番store、公開CI、実業務導入はUNVERIFIEDのままとする。
保存済み通常・race試験のPASSを、これらの確認済みや第五審査合格へ読み替えない。
第六審査・外部実行依頼・push・公開・配布・導入・復旧は、
この訂正や追加予算だけでは承認されない。

本訂正の確認範囲は文書と開発Agentの参照である。
変更前の `84406f1` の文書試験PASSを保存済み証跡として保持し、
変更後は次の既存試験を実行してPASSを確認した。

```bash
go test ./cmd/taxion -run '^Test(Documentation|UserFacingTerminology|UserGuide|DeveloperGuide)' -count=1
git diff --check
```

文書リンク試験の対象に `AGENTS.md` を加え、Agentの入口から正本とADRへのリンク・anchorも検査する。
gofmtと変更差分を確認し、本番Goコード、DTO、schema、store、Runtimeの変更はない。
変更前の全repository通常・race試験を再実行したとは扱わず、
実モデルによる指示遵守や実業務への導入を確認したとも扱わない。
最終indexのsecret scanは同じtreeに結び付けてcommit本文とoperator証跡へ記録し、
全未公開履歴や保護識別子の網羅検査の代わりにはしない。

再開時のread-only棚卸しは57 worktree、うち18件dirty、未確認0件だった。
未コミット成果がある他worktreeは変更しない。訂正は元branchの追加commitとして保存し、
既存履歴、共有install、operation policy、元Task／Flow／Dispatchを変更しない。
