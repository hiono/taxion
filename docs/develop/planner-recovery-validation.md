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

外側operatorのquota照会では、正しい利用者アカウントとCopilot CLIの実行ファイルを明示する。
GitHub操作用アカウントをquota照会先として使用しない。
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

## Stop中の観測と回答受理の接続（2026-11-09）

最初の不整合は、providerのidle後からStop完了までにTaxion履歴へ登録したusageを、
質問・検索の返却結果とcleanup receiptに反映していなかったことにある。
回答本体はStop前のterminal receiptに保存済みでも、復旧時にその古いusageだけを
native集計値と照合すると、正当な回答を不整合として拒絶していた。
これは [ADR-0074](adr/0074-reconcile-interrupted-request-planner-attempts.md) と
#282の送信済みPlanner照合・所有権解放に関わる保存／受理の接続不備である。
Permission、Approval、Dispatchの拒否条件を緩めて解決する問題ではない。

既存のEventCursorでStop成功後の履歴を読み、共通のDispatchUsage／UsageCheckpointへ
正規化する。質問の回答は従来どおりStop前に保存し、後続の有効な会計観測を
append-only cleanup receiptのoptional `usage`／`usageCheckpoints`へ保存する。
後続activity、曖昧なsequence、子agent、資格情報らしい内容、矛盾する会計情報を
正常な完了証拠として受理しない。cleanup保存失敗や観測エラーでも回答を削除せず、
advisory operationを完了扱いにしない。物理的なStop成功と受理拒絶は別に保存する。

CLI復旧のpreviewとapplyは、保存terminalとcleanupの会計を同じ方法で統合する。
terminal receiptがない場合はnative履歴から復元するため、そこに含まれる
Stop中のusageをcleanupから再加算しない。
`postStopError`のある試行を、通常の回答受理や次の試行への自動移行で解決済みにしない。
既存の停止確認付きuncertainty cancellationだけを別の判断として扱う。
受理後のPlannerStarted解除は、次段階の実行承認を与えない。

回帰は、回答がStop前に永続化されること、Stop中のusage／checkpointが保存されること、
terminal receiptの有無にかかわらず公開CLIのpreview→applyで回答を受理できること、
累積集計との整合、receipt重複、期限切れ、後続activityの拒絶を確認する。
受理後もApprovedはfalseでDispatchは増加しないことを検査する。
既存のApproval、HOLD、Runtime Stop、所有権、Recovery／Resumeの試験は維持する。

| 検証 | 状態 |
| --- | --- |
| 質問・検索・公開CLI復旧の対象試験 | PASS |
| gofmt、build、vet | PASS |
| 全repository通常試験 | 2,811 PASS／33 SKIP／0 FAIL、22 packages |
| controller／cmd/taxionのrace試験 | 1,533 PASS／11 SKIP／0 FAIL、2 packages |
| skippedだった公開CLIの合成復旧回帰 | 候補binaryを指定して通常・raceとも1 PASS／0 SKIP／0 FAIL |
| 最終indexのsecret scan | 同一treeの判定をcommit本文とoperator証跡に記録する（全履歴検査とは別） |

公開CLI回帰のSKIPは候補binaryの環境変数未指定によるものだった。
補完後の未実施は通常32件、変更packageのrace 10件であり、これらをPASSへ読み替えない。
JSONL証跡はoperator側の `083-stopped-tail-{normal,race}-20261109.jsonl` と
`083-stopped-tail-public-cli-{normal,race}-20261109.jsonl` に保存した。
検証コマンドは次のとおり（公開CLIの合成fixtureはモデルを実行しない）。

```bash
go build ./...
go vet ./...
go test -json -timeout 30m ./...
go test -race -json -timeout 30m ./internal/runtime/controller ./cmd/taxion
TAXION_TEST_FAILED_REVIEW_CLI=build go test ./internal/runtime/controller -run '^TestFailedReviewPublicCLISynthetic$' -count=1
TAXION_TEST_FAILED_REVIEW_CLI=build go test -race ./internal/runtime/controller -run '^TestFailedReviewPublicCLISynthetic$' -count=1
```

この修正で保証するのは、既にTaxion履歴へ登録されたStop中の観測の保持である。
SDK v1.0.22のDisconnectはcallerのStop contextを受け取らず、
未到着callbackの全回収も保証しない。このSDK境界、実モデル、本番store、
公開CI、全未公開履歴のsecret scan、保護識別子の網羅確認は未解決・未検証のままとする。
旧cleanup形式は読み取れるが、新しい会計投影のある未解決試行には更新後の復旧readerを使い、
古いbinaryへのrollbackのためにreceiptを削除しない。
第五審査FAILを変更せず、第六審査、push、公開・配布、TKB3復旧は実施しない。

## Clarification契約の最初の実装単位（2026-11-09）

開始時は `fix/native-event-contract-083` の `9afdf08`、clean、未pushだった。
親は `d1ccc36` で、方針訂正を再実装していない。
commit本文の通常・race・公開CLI補完の4 JSONLは、実ファイルのSHA-256と集計が一致した。
他worktreeのdirty成果18件はread-onlyで確認し、変更していない。
最新の #90／#283／#284／#282／#228／#196／#189／#220 の本文・コメントを取得した。

| 対象 | 実装と既存証拠 | 残る受入れ条件 | 開始時判定 |
| --- | --- | --- | --- |
| #282 | `9afdf08` のStop後観測、cleanup、preview/apply、二重計上拒否の回帰 | 独立した品質判定、正式配布などを含むIssue全体 | UNKNOWN（該当の既存回帰はPASS） |
| #228 | 既存RequestRecord、snapshot、operation、Recovery | 質問契約の新セッション復元、移送先での実受入れ | UNKNOWN |
| #283 | AskQuestion、AskPlanner、HumanDecisionRequest、Feedbackは存在 | 型付きルーティングと実際の質問・回答接続 | FAIL |
| #284 | 既存の承認、HOLD、所有権、Recovery検査は存在 | 新しい質問契約の再検証と続行E2E | FAIL |

今回の単位は、明示的なController／CLI入力による宛先判定、原文再利用の選択、
可逆な仮定の範囲検査、質問の重複抑止、回答の出典・受信時刻の保存までである。
全状態を `OBSERVED` とし、`ACCEPTED`、`AUTHORIZED`、`DISPATCHED` を生成しない。
宛先のrole名とactor/sourceは監査metadataであり、本人認証や実行権限の代用ではない。

| 既存機構 | 再利用・責務境界 |
| --- | --- |
| RequestRecord / Workspace transaction | `planning` kindと既存sequence／SHA-256を使用。新table・新kind・別保存領域を作らない。判定と追記を同一transactionで行えるよう既存appendをWorkspaceからも呼ぶ |
| ContextDocument / DispatchContextDocuments | 既に選択対象に結び付いた原文だけを提示。digestだけのreferenceは回答原文にしない |
| ApprovalBundleの7次元 | 仕様・対象・証拠の照合hashに再利用。観測snapshotのhashとは区別し、進捗時刻だけで同じ仕様質問を再発行しない。どちらのhashも承認ではない |
| AskPlanner / AskQuestion | Planner宛だけ、既存tool-free sessionと設定・期限検査に接続。Coder／Reviewerへ読み書き可能な新sessionを作らない |
| #282のterminal / cleanup / operation | 回答本体は従来のterminal receiptだけに保存し、質問側はID・sequence・hash参照を保存する。参照時に原本を読み直す。usage、checkpoint、所有権、Stopの処理を複製しない |
| #228 snapshot / store reopen | 既存planning receiptから質問・観測回答を復元する。新しいsnapshot engineを作らない |
| HumanDecisionRequest / #196 | 情報としての人間の回答を正式なdecisionや承認へ変換しない。scope／予算／期限の変更は従来の明示承認が必要 |
| Feedback / HOLD / instruction draft / interrupt | 今回の観測APIから変更しない。STOPという宛先判定とRuntime停止完了を混同しない。一括Steering APIの実装済み扱いもしない |

Planner回答は通常のterminal receiptと質問への参照を同じtransactionで追記する。
同時に人間の回答が到着して参照接続を拒否しても、正常に観測したPlannerのterminalを
rollbackしない。Stop失敗時も回答は観測として保持し、既存operationをBLOCKEDにする。
参照だけを根拠にcleanup成功、受理、次工程の権限を推定しない。

合成試験は、保存原文の返却と質問非発行、scope内の仮定、ロール別宛先、
Reviewerの自己回答拒否、同時再送の単一追記、同一回答の冪等性、異なる回答の上書き拒否、
進捗と仕様hashの分離、既存Planner呼出し、canonical回答の参照・再open、
Stop失敗、同時回答による接続失敗時の原本保持を対象とする。
公開CLIの試験は、開発用の一時store／provenance policyで実binaryを起動し、
observe→request→人間の回答→historyを通して、Graph・承認・Dispatch・HOLD・所有権が
変わらないことを確認する。CLI help／completionの追加でprovenance検査を迂回しないことも検査する。
いずれも実モデルを呼ばない。

### 未達条件と次の実装

#283全体は未完了である。現状の分類は呼出側の明示入力を検査するもので、
自然言語から既存資料で解決済みかを自動判断した証拠ではない。
実行中Roleからの自発的質問取り込み、Coder宛の実Role回答への接続、
矛盾する回答を追加観測として保持して解決する処理、受理済み情報の利用側への反映が残る。
質問の保存や宛先表示だけで、安全な待機・続行が完成したとは扱わない。
次の単位は、既存のRole呼出し・tool許可・ownershipを維持した質問取り込みと宛先への接続である。

#284は未着手とする。回答後の仕様・対象・証拠・権限・予算・期限・owner再検証、
依存作業の停止と独立作業の続行、正式な受理・承認・Dispatch、二重実行防止、
同一成果物の検証・独立Reviewer到達のE2Eを、この単位のPASSで代用しない。
#90／#189／#220の品質基準やADR-0062のH5全体の達成とも区別する。
ADR-0062のIssue参照原文は履歴 `8945b4c` に存在し、現在の候補にはファイルがない。
この差を未確認の採択済み実装として補完せず、今回その文書を復元していない。

### この単位の検証結果

`go build ./...`、`go vet ./...`、変更Goファイルのgofmt、`git diff --check` はPASS。
新しいfixtureで既存の拒否条件を弱めず、Plannerの出典付きconfirmedを使用した。

| 実行 | 実際の結果 |
| --- | --- |
| `TAXION_TEST_FAILED_REVIEW_CLI=build go test -json -timeout 30m ./...` | 初回2,820 PASS／2 FAIL／32 SKIP。FAILは新しい文書の人間の呼称とhelpのコマンド数だった |
| `TAXION_TEST_FAILED_REVIEW_CLI=build go test -json -count=1 -timeout 15m ./cmd/taxion` | 呼称を既存規範に合わせ、実際に追加したcommandを索引試験へ反映後、398 PASS／0 FAIL／2 SKIP |
| `go test -json ./internal/runtime/controller -run 'TestClarificationReplayPreservesTheAnswerContract$' -count=1` | 最後に追加した回答先・形式・期限の再送回帰、1 PASS |
| `TAXION_TEST_FAILED_REVIEW_CLI=build go test -race -json -timeout 30m ./internal/runtime/controller ./internal/store ./cmd/taxion` | 初回1,544 PASS／2 FAIL／10 SKIPに加え、controller packageが30分でtimeout。単一コマンドの完走とは扱わない |
| `TAXION_TEST_FAILED_REVIEW_CLI=build go test -race -json -count=1 -timeout 15m ./cmd/taxion` | 同じhelp・文書の修正後、398 PASS／0 FAIL／2 SKIP |
| 同じ再送回帰の `go test -race -json ... -count=1` | 1 PASS |
| timeoutしたcontrollerの未完了29 top-level testだけを `-race -json -timeout 30m -count=1 -run SELECTOR` で再実行 | subtest込み59 PASS／0 FAIL。初回の完了済み結果と照合し、現行のcontroller top-level 331件すべてにPASSまたはSKIPの終端がある。data raceの報告なし |

同じpackage／test名の修正後結果を採用し、再実行を二重計上しない集計は、
通常2,823 PASS／0残存FAIL／32 SKIP、race1,606 PASS／0残存FAIL／10 SKIPとなる。
初回の2 FAILとrace package timeoutを削除せず、上表の再実行と区別して保持する。
分割raceの完了は、単一の全package raceコマンドが時間内に完走した証拠ではない。

SKIPは、明示的に有効化していない実モデル・実サービス・実store／resource比較、
実scannerを使う追加hook／managed stage評価、document skill環境、
既存の未対象recontract組合せなどである。
最終indexだけのscanner実行は、SKIPしたhook／全履歴試験を代替しない。
実binary CLIと既存のfailed-review CLI回帰は合成環境で実行した。

JSONLはsession artifactに保存し、照合用のSHA-256を残す。

| 証跡名 | SHA-256 |
| --- | --- |
| `clarification-20261109-normal.jsonl` | `4ab0e4fd9f5dd42367b1b00966b69fddda2c8611807ff94f5e9856ae71d0eb79` |
| `clarification-20261109-cli-normal-fixed.jsonl` | `98ffc5fa80a95dd814f5d53bfd9373a4fbee6c4adb7b1e411e0c8f51bec86728` |
| `clarification-20261109-replay-normal.jsonl` | `b5463d85b205ff310b3c54c0eb9dcc1c778abed7b867602774f0fb830cd55e16` |
| `clarification-20261109-race.jsonl` | `c768ead8fc58909cf978c6c9775b7a94b15bf27af3bb7fc4e6971602a67cd9f2` |
| `clarification-20261109-cli-race-fixed.jsonl` | `dde7c19428d7c68320efcc3d07ccd785b8fbe1d80cb0569b5089823733b4a1a4` |
| `clarification-20261109-replay-race.jsonl` | `ea0a160fcf8ca7dcbdd7757e0a5c99e231a92d1123ee7890b9937e0c17efa347` |
| `clarification-20261109-controller-race-remainder.jsonl` | `b321de10a3f02124641c8d7a86d6f1d628a5e669824cbc1677d50fb612af7774` |

最終indexではtask-local Betterleaks 1.9.0を使用する。
scanner archiveは `f8b185a39ffcece2a1ca82bf3a4e7435cd81963ffd16b7a9128daf75f35f6de7`、
binaryは `0ace40b41652344608f89967fe93c6caf4faae9a4df20423632c7efb02a4d4e1` と照合した。
実行コマンドは `go run ./cmd/taxion git check-index --secrets --scanner EXACT_TASK_LOCAL_BINARY`。
index更新後に再実行し、結果をcommit本文へ記録する。

実モデル比較、公開CI、本番store、機器間の実移送、全履歴secret scan、
SDK未到着イベントの完全回収、Disconnect期限は未検証・未保証のままである。
第五審査FAIL、第六審査・push・公開・配布・TKB3復旧の未承認状態を維持する。

## 実行中Roleの質問取込み（2026-11-09、#283の限定単位）

開始時は `fix/native-event-contract-083` の `321c607`、clean、未pushだった。
親の `9afdf08` とその後の17ファイルの成果を保持した。
他worktreeのdirty成果18件はread-onlyで確認し、変更していない。
#283／#282／#228／#196／#189／#220／#90の最新本文・コメントを取得し、
本文は前回取得分と一致した。Issueの完了条件を引継ぎ報告で置き換えていない。
19:14 JSTの正規照会は20,850 requests、19:22 JSTは20,900 requestsだった。
承認上限21,100、reserve 50、利用可能上限21,050、期限20:48:12 JSTを維持した。

### 接続点と責務境界

不足していたのは、既存の質問保存APIに実行中Roleのhost toolを接続する処理だった。
新しい質問engineや保存領域を作らず、Copilotのmanaged tool登録を再利用した。
経路は `request --clarification-intake` → 保存request引数 →
`workflow run --clarification-intake` → `newDispatchWorker` →
SDKの `taxion_clarify` handler → `Controller.ObserveRoleClarification` →
共通の `requestClarification` → 既存planning `RequestRecord` である。
requestのcheckpoint再開でも引数を復元する。既定では無効であり、
OpenCodeとcredential brokerのRoleでは明示オプトインを拒否する。

factoryは既存operationのID・owner、Flow・runner、Dispatch、仕様のscope hashを固定する。
handlerはroot sessionの識別、実行中turn、停止・cancel状態、64 KiB上限を確認する。
Controllerは同一Workspace transactionで、保存済みDispatchのRUNNING状態、session、
runtime Role・config・worktree、operationのEXECUTION／RUNNING状態とowner、
Flow所属・runner・期限、Task・仕様・証拠の同一性を照合して追記する。
Task／Dispatch／Roleを入力で明示した場合も、不一致は拒否する。
質問期限はoperation期限を超えない。

toolの有無と固定対象は既存runtime config hashへ含める。
再開時に更新されるownerとrunnerはconfig hashに含めず、各呼出しで保存状態と照合する。
これらをhashへ含めると、正当に再取得したleaseだけで既存sessionのconfig照合が失敗するためである。
旧ownerのcallbackは拒否し、保存済み質問のIDを新ownerの質問へ付け替えることも拒否する。
originは既存receipt内のsession・operation・owner・runnerの出典情報であり、
別の承認・ownership管理ではない。

質問本文はcanonical receipt内だけに保存し、toolはID・状態・宛先・RequestRecord参照を返す。
重複判定には質問契約とoriginのhashに加え、対象・originと正規化した原文の明示比較を使用する。
同一実行の並行再送も単一追記とし、同じIDの本文変更を拒否する。
入力の観測は `OBSERVED` までであり、回答受理・承認・Dispatch・owner取得を発生させない。
HOLDによるhandoff停止と実行中Roleの観測を区別し、このtoolからHOLDやRuntime Stopを変更しない。

### 受入れ条件と再現経路

| 今回の条件 | 検証する実経路・判定 |
| --- | --- |
| host toolからControllerへ到達 | `TestClarificationHostToolPersistsOnlyBoundRunningRoleObservation` がSDKの実登録handlerから実Controller・一時storeまで通す |
| Role／session／Dispatch／Task／ownerの照合 | unknown session、nested agent、owner／operation／runner／対象／Role／設定／scopeの不一致を拒否 |
| 停止済み・失効済み実行の拒否 | stopped／idle／cancel済みcontext、CancelRequested、operation phase変更、期限切れを拒否。Stop要求を停止完了と扱わない |
| canonical保存と本文非複製 | RequestRecordだけが1件増え、原文とoriginを保持。toolは参照を返す。store再open後も同じ履歴 |
| 重複・混在の拒否 | 逐次2回と並行8回の再送で1件だけ追記。同じIDの本文変更、owner更新後の旧callback・質問ID付け替えを拒否 |
| 観測と権限の分離 | OBSERVEDのまま、回答なし。Graph・Ledger・HOLD・operation・ownerは不変。managed approval要求とReviewerのwrite拒否も維持 |
| 実利用入口と再開時の選択 | `TestRequestClarificationOptInReachesOwnedRoleFactoryAndResume` がrequest引数、checkpoint再開、実factoryのbinding構成を確認。Role実行自体は合成runtime |
| 既存Planner・保存・停止契約 | Clarification／Question／Planner／Operation／Feedback／HOLD／Stop／Recovery／Resumeの対象回帰とadapter・storeの既存suiteを実行 |

SDK handler試験は実モデルを呼ばず、SDKのsessionを合成する。
request試験のfake Roleが検証・独立レビューへ到達しても、
今回のtoolを使った実モデル行動や#284の回答後再検証E2Eの証拠にはしない。
初期fixtureのFlow承認・Ledger projection不足、Stop要求時のacknowledgement引数誤りは、
既存保存契約に合わせてfixtureを修正した。製品の拒否条件は緩和していない。

### この接続の検証結果

`go build ./...`、`go vet ./...`、変更Goファイルのgofmt、
`go generate ./cmd/taxion`、`git diff --check` はPASS。
新しい本体差分に対して実行した通常・raceの結果は次のとおりである。
前節の `321c607` の結果や初期fixture失敗を成功数に合算していない。

| 実行 | PASS / FAIL / SKIP |
| --- | --- |
| `go test -json -count=1 -timeout 10m ./internal/runtime/copilot ./internal/store` | 392 / 0 / 11 |
| `go test -json -count=1 -timeout 10m ./internal/runtime/controller -run "$SELECTOR"` | 166 / 0 / 0 |
| `TAXION_TEST_FAILED_REVIEW_CLI=build go test -json -count=1 -timeout 10m ./cmd/taxion` | 399 / 0 / 2 |
| 上記adapter／storeに `-race` を付加 | 392 / 0 / 11 |
| 上記controllerに `-race` を付加 | 166 / 0 / 0 |
| 上記CLIに `-race` を付加 | 399 / 0 / 2 |

`SELECTOR` は次の実行可能な正規表現である。

```bash
SELECTOR='Test(Clarification|ReviewerClarification|Question|Planner|Operation|RecoveryRequiresStoppedExecutionAndExactOwner|TargetedInterrupt|FlowRunnerObservesInterrupt|InterruptOfUnstarted|HandoffCleanup|Feedback|ResumeContractGate|StoppedTurn|ReviewProgress)'
```

各行はsubtest込みの件数で、通常・raceはそれぞれ957 PASS／0 FAIL／13 SKIPである。
raceの報告とpackage timeoutはない。SKIPは、実Copilot session・書込権限・compaction・
継続session usage・account quota・native履歴・実サービス・plugin探索の11件と、
実Betterleaks full hookおよび実OpenCode document flowの2件である。
全packageの通常・raceの再実行は行わず、今回の変更と関係する上記範囲に限定した。
初期の対象試験では、fixtureのFlow／Ledger不備とStop要求引数の不備でFAILし、
保存契約に合わせて修正後、登録handlerとrequest再開の両試験がPASSした。

JSONLはsession artifactに保存し、照合用のSHA-256を残す。

| 証跡名 | SHA-256 |
| --- | --- |
| `intake-20261109-adapters-store-normal.jsonl` | `f0108a4c452878757a31145873f1b027a0f89e59d8d9a813483d40761bac803a` |
| `intake-20261109-controller-normal.jsonl` | `a1c04d5c00b93b91716c4d74216b39541b37d7303207ba9772962ce7af7d6ff5` |
| `intake-20261109-cli-normal.jsonl` | `75fc7d04fa4efb0016fc1de22b9555f72830bb2eef930bc58c9b83980e631d2c` |
| `intake-20261109-adapters-store-race.jsonl` | `ebc298832638c49a782bdc81cfd1d77e89b4b5981e4a1525fffc87734ab765ec` |
| `intake-20261109-controller-race.jsonl` | `46603e42c8a86f0c0ac24d061c441019949851ade1277192a4e16a29abb14d9f` |
| `intake-20261109-cli-race.jsonl` | `4fbd26068b263303b5f1d478b5cbd3cd1b20e614ee565750a1f374a2ab6e4a6b` |

19:35 JSTの正規照会は21,000 requestsで、reserveを除いた残り50 requestsを
最終記録・index検査・ローカル保存に使用し、追加の実装単位を開始しない。
最終index検査は、前節と同じhashに固定したtask-local Betterleaks 1.9.0で
`go run ./cmd/taxion git check-index --secrets --scanner EXACT_TASK_LOCAL_BINARY` を実行する。
実行結果とlogのhashはcommit本文に記録する。全履歴scanやSKIPしたhookとは区別する。

### 未達条件と次の最小作業

#283全体は未完了であり、Coderへの実配信と矛盾回答の保存・解決は今回の対象外である。
次の最小作業は、保存済みCoder宛質問を既存Coder実行へ配信する接続点と、
回答のcanonical receipt・独立Reviewerとの境界を確認することである。
#284は未着手で、回答後のACCEPTED／AUTHORIZED／DISPATCHED、正式な再開、
依存作業の自動制御や汎用Recoveryを実装していない。
#282／#228の全条件、#90／#189／#220およびADR-0062のH5全体の達成とも区別する。

実モデル、公開CI、本番store、OpenCode intake、機器間の実移送、全履歴secret scan、
SDK未到着イベントの完全回収、Disconnect期限は未検証・未保証のままである。
旧binaryはorigin付きreceiptの読取りに失敗し得るため、rollback前に原本を保持する。
第五審査FAILを維持し、第六審査・push・PR・公開・配布・TKB3復旧は実施しない。

## Coder宛質問の配信接続調査（2026-11-09、BUDGET_STOP）

開始時は `fix/native-event-contract-083` の
`b5c19f91ce60e53529f3a6a4568204b8cc2c808f`、worktree cleanだった。
他worktreeはread-onlyで確認し、変更していない。
#283／#90はOPENで、本文は前回取得分と一致し、前回の記録コメントを確認した。
19:41 JSTの正規照会は21,000 requestsだったが、
19:42:49 JSTの実装可否判断時に21,050 requestsとなった。
承認上限21,100、reserve 50を差し引いた作業可能残はゼロである。
期限20:48:12 JSTを変更せず、配信実装・追加試験を開始しない。

確認できた生成元は `controller/clarification.go:requestClarification` である。
Reviewerのimplementation／evidence質問は `Recipient: CODER` として保存される。
`Request.DispatchID` と `Context.Dispatch` は質問したReviewerの実行を指し、
`Origin` のsession・ownerも発行元の情報である。配信先Coderのbindingではない。
既存 `ClarificationHistory` とcanonical RequestRecordを再利用できるが、
質問保存の重複抑止は配信の冪等性や到達確認を保証しない。

候補となる接続先は `Controller.SendWithBudgetGuidance` と
`runtime.Identity.Send`／`copilot.Adapter.Send` である。
前者はRUNNINGのDispatchに対して実際のSendを行わず `markRunning` を返す。
後者はbusy sessionに `ErrSessionBusy` を返す。
したがって既存Sendの成功をそのまま「実行中Coderへ配信成功」と記録できない。
SDKのSend戻り値もadapterでは捨てており、質問IDに結び付いた永続的な配信確認はない。

実行中workerを保持しているのは `controller/run.go:runReviewFlow` の
Start→Send→StreamEvents→Closeの区間である。
この区間が既存実行へ接続する候補であり、新しいRoleやsessionを起動する必要はない。
ただし通常の逐次FlowではCoderをCloseしてからReviewerを実行する。
`core/reviewflow.go:StoppedCoderReviewer` もReviewerの `RelatedID` が
成功済みCoderを指す条件を明示している。
同じFlowにCoderが存在するだけでは、生きた配信先が存在する証拠にならない。
ReviewerとCoderのTaskも同じIDとは限らず、単純なTask一致や最新Coder検索では解決しない。

次の最小単位は、質問のcanonical参照とReviewerの `RelatedID` から
既存CoderのTask／Dispatch／session／owner／実行状態を解決し、
一意に照合できない場合や終了済みの場合は配信せず保留する対象解決処理である。
実送信へ進む前に、busy中入力、配信要求と確認の区別、
並行再送、送信途中停止、結果不明時の非再送を既存receiptで表現できるか確認する。
既存のSend拒否条件を削除したり、終了済みCoderを勝手に再起動したりしない。

今回は製品コードを変更せず、試験も実行していない。
実SDK handlerからCoderへ届くこと、実モデルの挙動、配信成功・失敗・再開は未検証である。
開始時のIssue JSONはsession artifactに保存した。
#283は `e0f5f35f09216a4bb89b6720cb91b69c733e57f467fe9c735cd738c97045936d`、
#90は `b9e899d2e51425262e3b2abf5bf4ca4feeaa88e7927591cf52faf6349514ae3a`。
HEADの `controller/controller.go` のSHA-256は
`059b13eca121d93cec0fb8e42b0c8342284fe08775bc4263ec5291390cf55e99`、
`copilot/copilot.go` は
`4cde1ab0dd288961ff4722816783517c8bb9896409b911006483da8626ffff89`。
新しいIssue完了条件は達成扱いにせず、#284未着手・第五審査FAILを維持する。

## RelatedIDによるCoder解決と配信保留（2026-11-09）

開始時は `fix/native-event-contract-083` の
`b5c19f91ce60e53529f3a6a4568204b8cc2c808f` で、前節の未コミット文書差分を保持した。
既存の調査と取得済み #283／#90 の本文・コメントを再利用し、他worktreeは変更していない。
承認上限は21,500 requests、reserve 50、利用可能上限21,450、期限23:48:12 JSTへ延長された。
19:54 JSTの正規照会は21,050、20:06 JSTは21,100 requestsだった。

### 実装した接続と既存責務

`requestClarification` のCoder宛観測から、同じWorkspace transaction内で宛先を解決する。
これにより既存host tool／Controller／CLIの質問保存から保留処理へ接続する。
以前から保存されている質問には
`conversation clarification resolve-coder --id QUESTION_ID` を追加した。
この入口にも従来のprovenance検査を適用し、helpだけを例外とする。

宛先はReviewer Dispatchの `RelatedID` から一意に選ぶ。
質問元Task／Dispatch／session／configと保存snapshot、Reviewerを含む唯一のFlow、
CoderのTask／Dispatch／Role／repository／instruction、既存の `CreatedFrom` を照合する。
通常FlowではCoderはFlowのTaskに対応する。承認済みのreview-first continuationでは、
既存 `SourceDispatchID` と `Contract.Supersedes` で元CoderのTaskを照合する。
同じrepositoryの最新Coderや別sessionを推測で選ばない。

初期試験でReviewer TaskのParentID一致を仮定したところ、正当な既存Flowを誤拒絶した。
`issueFlowReviewer` はParentIDを空にし、`CreatedFrom` を使うため、
既存のRelatedID／Flow所属／CreatedFrom契約に合わせて修正した。
拒否条件を一括削除したり、新しい親子関係を生成したりしていない。

終了済み・停止要求済みCoderは識別できても、現在のReviewer ownerを付与せず保留する。
仮に対象が実行中なら、session・configと仕様scopeに加え、既存の実行operation、
そのowner、Flow runner、期限、質問元originを照合する。
ownerは質問metadataのコピーではなく、既存実行leaseから解決する。
入力経路のRUNNING／busy拒否を迂回せず、最後まで照合できても `no-safe-input-path` として保留する。
通常FlowがReviewerへ進む前にCoderを終了・Closeする既存順序も変えていない。

### canonical保存と再送

配信保留は `ClarificationDelivery` のversion 1 metadataとして、
既存planning RequestRecordの `clarification-delivery:QUESTION_ID` に1件だけ追記する。
内容は元質問のID・sequence・hash、宛先snapshot、`HELD`、理由、時刻であり、
質問・回答本文や元質問snapshotを複製しない。新table・scheduler・brokerを追加していない。
元質問の参照を照合し、並行再送もWorkspace transactionで単一追記にする。

再確認でsession／owner／対象・証拠が変われば、元の宛先を保持したまま保留を返す。
元snapshotを更新したり、新しい宛先へ再送したりしない。
`ClarificationHistory` はcanonical参照を検証して保留metadataを表示する。
履歴は保存時snapshotであり、再確認時の一時的な理由変更は既存receiptを書き換えない。
送信を開始していないため、配信成功・開始済み・結果不明を捏造しない。
将来実送信へ進む際のprovider確認と不明結果の照合・冪等性は未実装である。

質問自体は `OBSERVED` のままである。配信の `HELD` はTaskのHOLDとは別であり、
受理・承認・Dispatch、Role起動、Runtime Stop、owner取得を発生させない。
回答の自動受理、矛盾解決、#284の正式再検証・Recoveryは追加していない。

### 検証する受入れ条件

| 条件 | 実経路・確認範囲 |
| --- | --- |
| 正しいRelatedID解決と終了済み保留 | 既存Flowのfake Coderを実Controllerで完了させ、Reviewerの保存質問から元CoderのID／sessionを解決する |
| 不明・不一致・失効 | RelatedID、候補重複、Role、Task、Dispatch、session、scope、owner、期限のmetadata probeで保留。live probeは未保存の仮想状態であり、正当な並行Flowを実行した証拠ではない |
| 再送・並行処理 | 同じ質問の再保存と8並行の解決で元質問＋保留の2 receiptだけ。別sessionへの付け替えと異なるcanonical参照を拒否する |
| 旧質問と再open | 配信metadataのない既存snapshotを通常のstoreへ読み込み、初回解決後の再openで同じ参照を保持する |
| 実利用入口 | requestの既存Coder→Reviewer経路から質問保存とCLI handlerのresolve-coderを通し、元Coderを保留する。モデルの代わりにfake Roleを使用 |
| SDKの入力拒否維持 | 実Copilot adapterのSendに合成busy／stopped sessionを渡して、SDK送信前の既存拒否を確認する |
| 権限と実行不変 | Graph・Ledger・HOLD・operation・ownerは不変。既存Clarification／Planner／Stop／ownership／Recovery回帰を実行する |

実Coderへの配信、providerの到達確認、実モデル、本番store、公開CIは未検証である。
#283は実配信・矛盾回答解決を残し、#284未着手・第五審査FAILを維持する。
push／PR／Merge／Release、第六審査、TKB3復旧は実施しない。

### この単位の検証結果

`go build ./...`、`go vet ./...`、gofmt、
`go generate ./cmd/taxion`、`git diff --check` はPASS。
新しい製品差分に対し、次のコマンドを実行した。

| 実行 | PASS / FAIL / SKIP |
| --- | --- |
| `go test -json -count=1 -timeout 10m ./internal/runtime/copilot ./internal/store` | 393 / 0 / 11 |
| `go test -json -count=1 -timeout 10m ./internal/runtime/controller -run "$SELECTOR"` | 189 / 0 / 0 |
| `TAXION_TEST_FAILED_REVIEW_CLI=build go test -json -count=1 -timeout 10m ./cmd/taxion` | 399 / 0 / 2 |
| 上記adapter／storeに `-race` を付加 | 393 / 0 / 11 |
| 上記controllerに `-race` を付加 | 189 / 0 / 0 |
| 上記CLIに `-race` を付加 | 399 / 0 / 2 |

```bash
SELECTOR='Test(CoderClarification|Clarification|ReviewerClarification|Question|Planner|Operation|RecoveryRequiresStoppedExecutionAndExactOwner|TargetedInterrupt|FlowRunnerObservesInterrupt|InterruptOfUnstarted|HandoffCleanup|Feedback|ResumeContractGate|StoppedTurn|ReviewProgress|LegacyBudgetStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun|BudgetStoppedCompletedCoderContinuesAtReviewerWithoutRerun)'
```

各行はsubtest込みで、通常・raceはそれぞれ981 PASS／0 FAIL／13 SKIPである。
raceの報告・package timeoutはない。SKIPは、実Copilot session、実サービス、
quota／native履歴、plugin探索の11件と、実scanner full hook・実OpenCode document flowの2件である。
前の実装単位の成功数や初期失敗を合算していない。
全packageの通常・race再実行、実SDKへの通知RPC、実モデルの到達確認は行っていない。

初期の対象試験では、ParentIDの誤った前提で正当な宛先を誤拒絶したため、
既存のCreatedFrom契約を使用するよう本体を修正した。
その後のlive metadata probeでは、関連元の確定結果を消すと先に証拠検査で拒否されるため、
確定証拠を保持した仮想状態と明示的なstale scopeを分けて検査した。
試験追加時の関数配置による構文エラーと、helpの行数をsubcommand数と取り違えた
索引試験の失敗も修正し、対象試験の再実行後に上表の回帰を完走した。

JSONLはsession artifactに保存し、照合用のSHA-256を残す。

| 証跡名 | SHA-256 |
| --- | --- |
| `coder-resolution-20261109-adapters-store-normal.jsonl` | `2b0a5ecc2401bded4e24cd146823e8d6e9938affe642ce3931652806e79d8f15` |
| `coder-resolution-20261109-controller-normal.jsonl` | `179ed56c4c9867f3f3c7afc2af12d0f4c508f33a783cafb03be0bcf675a0b239` |
| `coder-resolution-20261109-cli-normal.jsonl` | `053a42bc96dbcc5fca88b9c0ab33ae3a8bb3c9b4dc51692a881baf8ea5d8dbe8` |
| `coder-resolution-20261109-adapters-store-race.jsonl` | `ddb38c89abce4cdf78d63866da03a351f2ecd860a029611caa7040c207fa8904` |
| `coder-resolution-20261109-controller-race.jsonl` | `e6f01d166848b1ce663f0b8bdd4cfdbc3d276dfefed687211e44de5ab8801302` |
| `coder-resolution-20261109-cli-race.jsonl` | `c0ab841e445757dbddc3ed6bb795659a80308ecbcecbe1dda2c5714df148cad6` |

最終indexは、前節と同じarchive／binary hashに固定したtask-local Betterleaks 1.9.0で
`go run ./cmd/taxion git check-index --secrets --scanner EXACT_TASK_LOCAL_BINARY` を実行する。
結果とlogのhashはcommit本文に記録し、全履歴scanやSKIPしたhookとは区別する。

## 正式な後続Coderへの明示的引継ぎと到達ACK（2026-11-09）

開始時は`fix/native-event-contract-083`の`14cfe40`、cleanであった。
20:37 JSTの正規照会は使用21,200 requests、承認上限21,500、reserve50、
作業可能残250、期限23:48:12 JST。21:14 JSTの実装後観測も使用21,200であった。
口座全体の残量を、この作業の追加承認とみなしていない。

### 承認された対象変更

`internal/core/reviewflow.go`はReviewerの前のCoderに`SUCCEEDED`と確定結果を要求する。
終了済みCoderへの送信禁止と、元Coderへの実配信は同時に成立しない。
固定SDK 1.0.22には`MessageOptions.Mode`のenqueue／immediateがあるが、
終了済みDispatchを再開せずにこの条件を解消するAPIではない。
この衝突について確認し、元の宛先を保持したまま、
正式発行された後続Coderへの明示的引継ぎに対象を変更する承認を得た。
並行Role実行、終了済みDispatchの再開、暗黙の宛先変更は承認対象に含めない。

### 接続と保存

`conversation clarification handoff --id --dispatch --reason`は、
同一Flow内で対象Reviewerの直後に正式発行された修正Coderだけを選択する。
Reviewerの確定結果、元CoderとのRelatedID、修正CoderのAMENDS関係を照合する。
対象は通常の経路で既にRUNNINGになっている必要があり、このコマンドでは起動しない。
Task／Dispatch／Role／session／設定hash／仕様scope／operation owner／runner／期限を
保存snapshotと比較し、各入力と到達イベントでも現在のbindingを再検証する。

既存の`ManagedClarification`にprotocol 2のinbox callbackを接続した。
Coder用の登録済みSDK host tool `taxion_clarification_inbox`が、
安全なtool入力境界で`receive`を呼んだときだけ質問を返すpull方式である。
`Send`のbusy拒否を変更せず、新しいturn、session、Dispatch、schedulerを作らない。
旧opt-in sessionの設定hashを維持したまま新しいtoolを後付けすることもない。

元の`clarification-delivery`と質問・回答は変更しない。
新しい記録は既存planning RequestRecord内の`clarification-handoff:<id>`に追記する。
元質問と元宛先のcanonical ID／sequence／hash、明示した理由、新しい宛先snapshotだけを持ち、
質問本文は元のreceiptから取得する。引継ぎ先は一つで、同じ引継ぎは追記せず、
別の宛先・理由への置換は拒否する。

| 配信段階 | 根拠 |
| --- | --- |
| `HELD` | 明示的な引継ぎを保存。本文の投入前 |
| `REQUESTED` | Coderのreceive callに対して、本文を返す前に同一transactionでcall IDを保存 |
| `DELIVERED` | 既存SDKイベント射影・Controllerの`recordEvent`を通った、同じroot session／callの成功tool完了。モデル向け本文を含む応答全体のhashも一致 |
| `ACKNOWLEDGED` | Coderが別のinbox呼出しで受信時のrequest receipt hashを明示し、現在のbindingとDELIVEREDを照合 |

`DELIVERED`はモデル向けtool結果が対象sessionのイベントに記録されたことを表す。
モデルが内容を理解したことは証明しない。`ACKNOWLEDGED`もCoder側の明示的な受信確認に限る。
hashだけで同一性を保証せず、保存済みQuestion／Task／Dispatch／session／ownerと
SDKのcall・成功イベントを合わせて検証する。応答が圧縮・改変・欠落した場合は昇格しない。
ACKのreceipt hashは認証用bearer tokenではない。
質問は`OBSERVED`のままで、回答受理・承認・実行許可・Task HOLDには進めない。

本文を返す試行は一回に限定する。並行receiveは一件だけが本文を得る。
結果不明での再呼出し・store再openはmetadataを返し、本文を再送しない。
成功イベントやACKの再送は配信段階を増やさず、イベント前のACKは拒否する。
停止・期限切れ・owner失効後の遅延イベントでは昇格しない。
不明結果の自動回復・新宛先への再配信は未実装であり、#284には接続していない。

### 検証範囲

正常系は、既存Flow fixtureで元Coder完了、ReviewerのREQUEST_CHANGES、
正式な修正Coder発行、通常ControllerのStart／Sendまで進めた実storeとfake Runtimeで検査した。
receiveの並行要求、API成功だけで昇格しないこと、別call・失敗・子Agent・改変本文のイベント拒否、
正しい完了イベント、明示ACK・重複ACK、各binding不一致、終了済み対象、
保存・再open後の不明結果、失効ownerの遅延イベントを検査した。
Graph／Ledger／HOLD／承認／operation所有権に変更がなく、元の宛先と質問状態が保たれることも検査した。

SDK側は実際のcreate／resume登録から取り出したhandlerと既存のnativeイベント射影を、
合成managed session・ToolInvocation・ToolExecutionCompleteDataで検査した。
SDK callbackの参照型とcall IDの保持、停止・idle・子Agent・無効session・cancelを確認した。
これは実provider接続やSDK wire RPCの統合試験ではない。
実モデルへの配信、モデルの自発的なinbox利用、実サービスのイベントpayloadとACKは未検証である。

初期の試験追加では関数の配置ミスによる構文エラーを修正した。
最初の通常回帰では、metadata内のcanonical`question`参照を本文と誤認する文字列assertionが失敗した。
top-level本文fieldをJSONで判定するよう試験だけを修正した。
既知の失敗を避けるため途中のraceを停止し、成功済みadapter／storeの結果を再利用して
影響するController／CLIを再実行した。中断したraceを成功数に含めない。

最終結果は通常・raceともに985 PASS／0 FAIL／13 SKIPであった。
SKIPの内訳は節1.12と同じ実サービス系11件、固定scanner full hookと実OpenCode document flowの2件である。
build／vet／gofmt／completion再生成／diff checkもPASS。
21:29:43 JSTの保存前観測は使用21,300、作業可能残150、reserve50を維持した。

```bash
SELECTOR='Test(CoderClarification|Clarification|ReviewerClarification|Question|Planner|Operation|RecoveryRequiresStoppedExecutionAndExactOwner|TargetedInterrupt|FlowRunnerObservesInterrupt|InterruptOfUnstarted|HandoffCleanup|Feedback|ResumeContractGate|StoppedTurn|ReviewProgress|LegacyBudgetStoppedSnapshotCoderContinuesAtReviewerWithoutCommitOrRerun|BudgetStoppedCompletedCoderContinuesAtReviewerWithoutRerun)'
go build ./...
go vet ./...
go generate ./cmd/taxion
go test -json -count=1 -timeout 10m ./internal/runtime/copilot ./internal/store
go test -json -count=1 -timeout 10m ./internal/runtime/controller -run "$SELECTOR"
TAXION_TEST_FAILED_REVIEW_CLI=build go test -json -count=1 -timeout 10m ./cmd/taxion
go test -race -json -count=1 -timeout 10m ./internal/runtime/copilot ./internal/store
go test -race -json -count=1 -timeout 10m ./internal/runtime/controller -run "$SELECTOR"
TAXION_TEST_FAILED_REVIEW_CLI=build go test -race -json -count=1 -timeout 10m ./cmd/taxion
```

| 対象 | 通常 PASS / FAIL / SKIP | race PASS / FAIL / SKIP |
| --- | --- | --- |
| Copilot／store | 394 / 0 / 11 | 394 / 0 / 11 |
| Controllerの上記selector | 192 / 0 / 0 | 192 / 0 / 0 |
| CLI package全体 | 399 / 0 / 2 | 399 / 0 / 2 |

JSONLはsession artifactに保存した。証跡hashは次のとおり。

| 証跡名 | SHA-256 |
| --- | --- |
| `clarification-ack-20261109-adapters-store-normal.jsonl` | `e7fd5f00821f7c72f58fe31b2ebf68cdc190e7f2b8e4414504bb77f93077d3f7` |
| `clarification-ack-20261109-controller-normal.jsonl` | `20cafa2039933d752b16a0a14f9666b70529c6fb39c0a97e630ca04280b85976` |
| `clarification-ack-20261109-cli-normal.jsonl` | `3a96174d2edb7f0c9cfc96aa04a2cd5b20c2ec13edea9a1e6bb84f755cb423a9` |
| `clarification-ack-20261109-adapters-store-race.jsonl` | `b88b83ff3d4e9ba202d53b57448774ab1d5b21fe39ddfe29b05dd001cbacb007` |
| `clarification-ack-20261109-controller-race.jsonl` | `093ff53900d7aa90cf388c6a7004c7bb0bd6df48d5a83083b2ea556eb4d6a3cc` |
| `clarification-ack-20261109-cli-race.jsonl` | `b0a7f0ea6abeb8caa0ba5236b84c6449f3266b02f101813e74fd26ea76ad54d3` |

最終indexは固定したtask-local Betterleaks 1.9.0で
`go run ./cmd/taxion git check-index --secrets --scanner EXACT_TASK_LOCAL_BINARY`を実行し、
結果とscan logのhashをcommit本文に記録する。SKIPしたfull hookや全履歴scanとは別の検査である。

### 残課題と境界

#283全体は未完了であり、矛盾回答の解決、実providerでの到達確認、不明結果の安全な再処理が残る。
元Coderへ直接届ける機能ではなく、承認された明示的な後続Coder引継ぎだけを実装した。
#284は未着手、第五審査FAILを維持する。
Push／PR／Merge／Release、第六審査、TKB3復旧、実モデルへの追加照会は行わない。

## 配信・ACK契約の独立監査と安全停止（2026-11-09）

### FACT：監査対象と実行条件

対象は`de0ef5634b907463d31019355a7f5d930d312886`、親は`14cfe40`である。
開始時のbranchは`fix/native-event-contract-083`、worktreeはcleanで、
親からの後続履歴は対象commit一件だった。既存commitは変更しない。
別コンテキストのread-onlyレビューで`git show --stat`と実差分、
既存SDK 1.0.22のAPI・イベント型・callback実装、登録と入力経路、保存・復元を確認した。
実provider・実モデルへの照会は行っていない。

22:02:07 JSTの正規照会では使用21,300 requests、承認上限21,500、
reserve50、作業可能残150、期限23:48:12 JSTだった。
口座全体の残量は追加の実行承認に含めない。

### FACT：root帰属の未確定条件

`internal/runtime/copilot/clarification.go`のinbox guardは、
空の`ToolInvocation.AgentID`を許容し、同じsessionがbusyであることを検査する。
しかし固定SDKの`types.go:2064–2066`には、空値はrootだけでなく、
runtimeが子Agentの帰属を付けない場合にも使用されると明記されている。
したがって、この空値検査だけからroot限定を結論できない。
既存の合成試験は明示的な`AgentID: "child"`を拒否するが、
帰属が提供されないケースを独立に証明していない。

これはSDK契約上の曖昧さの確認であり、実環境での侵入再現ではない。
AvailableTools、permission、Role policyがすべての実行経路で子Agent生成を
禁止することまでは本監査で確定していない。
限定した追加確認でも、`internal/runtime/copilot/tools.go:104–105`のpolicy hookが
`task`／`run_subagent`／`agent`を拒否することは確認したが、
`ToolPolicy == nil`を含む全inbox登録経路への適用は未確定だった。
この前提を確認せず、到達可能な権限違反を断定することも、
逆にroot限定の安全性を確認済みと扱うこともできない。

### FACT：SDK応答と不明結果の復元

SDKの`session.go:1925–1965`は、空でない`TextResultForLLM`を
`HandlePendingToolCall`へ渡す。handlerの成功は完了イベントを合成しない。
`rpc/zsession_events.go:3280–3292`にtool完了の型とcall識別があり、
`5722–5723`の`Result.Content`はモデル向け文字列だが、
token効率のため切り詰められる可能性がある。
このため、同一hashになることはSDKだけでは保証されない。
現実装が改変・省略を成功扱いしない方向は維持する。
合成試験では、返した文字列をそのまま完了イベントへ設定している。
wire往復や実サービスのイベント生成・本文保存は検証していない。

生イベント側の`recordEvent`は到達projectorを呼ぶが、
`internal/runtime/controller/receipts.go`の`adoptSavedTurn`は、
復元したイベントを保存しても`recordClarificationArrival`を呼ばない。
一致済みイベントのreplayもprojection前に終了する。
handoff再構築はRequestRecordの段階を読むだけなので、
切断中の完了を後で取得しても`REQUESTED`から進まず、ACKは拒否される。
再送しない安全側の動作はあるが、確定できる不明結果の復元は未完成である。

さらに既存`RuntimeReceipt`はAgentIDを保持していない。
保存済みreceiptだけからrootを推定して再投影すると、帰属不明のイベントまで
昇格させる可能性がある。単にprojectorを追加呼出しするだけの修正は採用しない。

### DECISION：追加実装の停止

root帰属の安全条件が未確定のため、配信・ACKの実行を追加せず、
不明結果の再処理と矛盾回答の新規実装も停止した。
変更は監査記録とCLI制約の訂正に限定する。
inboxをコード上で無効化・修正したわけではなく、重大な違反を再現済みという判定でもない。
前節の985 PASSは限定的な合成試験の証拠であり、上記の不足を解消する根拠として流用しない。

#283はOPENで、今回の監査だけで受入れ条件のcheckboxは変更しない。
矛盾回答の保存・解決待ち・仕様所有者への委任は、本区間では未監査・未実装である。
#284は未着手、第五審査FAILを維持する。
Push／PR／Merge／Release、第六審査、TKB3復旧は行わない。

### PROPOSAL：次の最小作業

最初に、固定SDK／実行設定で子Agent生成が禁止されることを全経路で示すか、
SDKから確実なroot帰属を取得できる方法を、モデルを使わない最小のwire試験で確認する。
どちらも確認できなければinboxをfail-closedにする。
その後、検証済みnative履歴から帰属を保持して到達証拠を復元し、
同一owner／session／scope／期限の検査と同じtransactionで一度だけ投影する。
これが成立してから矛盾回答の保存・解決へ進む。新しい権限・予算を承認した提案ではない。

### 検証記録

製品コードを変更していないことを`git diff --name-only -- '*.go'`で確認した。
`go build ./...`、`go vet ./...`、`git diff --check`はPASS。
gofmt対象の変更とcompletionの変更はないため、再生成・整形は行っていない。
次の二つのコマンドはそれぞれ完走し、各5 PASS／0 FAIL／0 SKIPだった。

```bash
go test -json -count=1 -timeout 3m ./internal/runtime/controller ./internal/runtime/copilot ./cmd/taxion -run 'TestClarification(Successor|InboxSDK)|TestUserFacingTerminology'
go test -race -json -count=1 -timeout 3m ./internal/runtime/controller ./internal/runtime/copilot ./cmd/taxion -run 'TestClarification(Successor|InboxSDK)|TestUserFacingTerminology'
```

この結果は既存の合成・fake Runtime試験と用語検査に限る。
新たな帰属不明callのwire再現、検証済み履歴からの配信段階復元、
矛盾回答の保存・解決の試験は未実施であり、上の成功数に含めない。
実SDK wire RPC／実provider／実モデルは未検証。
監査結果と異なる意味で、既存試験の成功を安全性の証明に使わない。

| session artifact | SHA-256 |
| --- | --- |
| `clarification-audit-20261109-normal.jsonl` | `9495ef4a1373ad39a771ad785e8a5edaa454d8bafe2207c16879c44f2ef2c062` |
| `clarification-audit-20261109-race.jsonl` | `18b6ac06b35956fdda87fea6aac81823fc32aa6676033538fb0fea28a866bc61` |

22:08:26 JSTの保存前観測は使用21,300 requests、作業可能残150、reserve50で、
承認済み期限は23:48:12 JSTのままである。
最終indexは固定したtask-local Betterleaks 1.9.0で
`go run ./cmd/taxion git check-index --secrets --scanner EXACT_TASK_LOCAL_BINARY`を実行し、
結果とscan logのhashをcommit本文に記録する。

## SDK inboxの無効化と既存回答の保全

DECISION: SDK 1.0.22でroot呼出しを確証できないため、
`taxion_clarification_inbox`をSDK経路で無効化した。
空のAgentIDをrootと解釈する方式へ戻さず、登録・許可・直接呼出しを拒否する。
質問取込みの`taxion_clarify`、明示的handoff、canonical質問・元宛先・回答履歴は保持する。
配信機能の完成ではなく、根拠の不足した入口を閉じる修正である。

FACT: 開始時は`fix/native-event-contract-083`、HEAD
`74c2317f8a07aed788ec3af3bbfd029fbcde9e54`でworktreeはcleanだった。
SDKの`ToolInvocation.AgentID`は`session.go`の`executeToolAndRespond`で渡された値をそのまま設定する。
`types.go`には、runtimeが帰属を提供しない子Agent呼出しでも空値となる契約がある。
`IncludeSubAgentStreamingEvents`はイベント転送の設定であり、子Agent起動の拒否を証明しない。
`ToolPolicy`がnilのnative構成ではpolicy hookを設置しないため、
hookだけを根拠としたinbox許可も採用しない。実環境での子Agent到達は再現していない。

変更はSDK adapterと関連テストに限定した。
`internal/runtime/copilot/clarification.go`ではinboxを登録対象から外し、
残るhandlerもController callbackを呼ばずにエラーを返す。
`internal/runtime/copilot/copilot.go`では自動allowlist・SystemPromptの利用誘導・自動許可を除去し、
`AvailableTools`への明示追加を設定エラーとした。
`clarification_test.go`ではToolPolicyのnil／non-nil、新規／再開session、
空／子Agent ID、receive／ackとその再実行を検証し、callbackが一度も呼ばれないことを確認した。
質問取込みtoolの登録は両構成で維持される。

配信完了証拠の復元は追加していない。既存RuntimeReceiptにはAgentIDがなく、
保存turnのイベントを無条件に投影すると帰属不明を到達確認へ昇格させるためである。
既存のREQUESTEDは未確認のまま、HELDは保留のままとし、本文の自動再送や推測による昇格を追加しない。
SDK型の完了イベントを直接渡す合成試験では、call IDとモデル向けcontent hashの保持だけを確認した。
root保証、実SDK wire RPC、実provider、実モデルでの配信確認を意味しない。

回答処理は変更していない。既存`AnswerClarification`は異なるRole／Actor／Source／Textを
既存回答と照合し、不一致なら追記前に拒否する。同一回答のretryでは追記しない。
既存試験で、矛盾回答を拒否した後のDB再openでも元の回答・出典・時刻が残り、
OBSERVEDを越える承認・Dispatch・HOLD・所有権変更を起こさないことを確認した。
矛盾回答自体の保存と判断者による解決は未実装である。

以下は作業worktreeでrtk経由により実行したコマンドである。
通常とraceは別々に実行し、それぞれ63 PASS／0 FAIL／0 SKIPだった
（Copilot 22、Controller 37、CLI 4）。全suiteの完走としては扱わない。

```bash
rtk proxy go test -json -count=1 -timeout 3m ./internal/runtime/copilot ./internal/runtime/controller ./cmd/taxion -run "Clarification|TargetedInterrupt|FlowRunnerObservesInterrupt|StoppedTurn|Provenance|UserFacingTerminology"
rtk proxy go test -race -json -count=1 -timeout 3m ./internal/runtime/copilot ./internal/runtime/controller ./cmd/taxion -run "Clarification|TargetedInterrupt|FlowRunnerObservesInterrupt|StoppedTurn|Provenance|UserFacingTerminology"
rtk proxy go build ./...
rtk proxy go vet ./...
rtk proxy gofmt -w internal/runtime/copilot/clarification.go internal/runtime/copilot/clarification_test.go internal/runtime/copilot/copilot.go
rtk git diff --check
```

build／vet／gofmt／diff checkもPASS。通常・raceのJSONはsession artifactへ保存した。

| session artifact | SHA-256 |
| --- | --- |
| `clarification-safety-20261109-normal.jsonl` | `bb1d1ad315bea3fb0cbed655c337ed7f638742100c4bc29d16aebdf23c87dcb9` |
| `clarification-safety-20261109-race.jsonl` | `263c93f466165b3931df4a5ffef90b4fa82feeef711dbf7da198a3050f3dce12` |

PROPOSAL: 再有効化には、実SDK wireでroot帰属を証明できる情報または子Agent起動拒否を
全登録経路で成立させる条件を確定し、その証拠を保存receiptまで維持する必要がある。
その後に、unknown outcomeの安全な復元と矛盾回答の保存・解決を実装する。
#283はOPEN、第五審査FAILを維持し、#284には着手しない。
Push／PR／Merge／Release、第六審査、TKB3復旧は実施しない。

22:42:42 JSTの正規観測は使用21,350、承認上限21,500、reserve50、作業可能残100 requestsだった。
今回の終了期限は23:00 JSTへ短縮されており、以前の23:48:12 JSTを使用しない。
固定したtask-local Betterleaks 1.9.0による最終index検査結果とscan log hashをcommit本文へ記録する。

## Clarificationライフサイクルの統合評価と改善順序

今回の起点は`1404845bf183cc2b88bbf81f2732b031ad1d4568`である。
追加400 requestsにより承認上限は21,900、reserveは50を維持する。
直近の正規観測は23:09:30 JSTの21,450 requestsで、作業可能残は400だった。
期限の追加2時間は従来の23:00を基準とし、11月10日01:00 JSTまでとする。
調査・計画60、実装160、検証・レビュー120、最終整理60を目安とし、reserveは配分しない。

### 起点の到達点と責務

FACT: 実装・試験とIssue #283／#284の受入れ条件を照合した。
既存の成功試験は対応するコード経路の根拠として再利用し、実モデルの行動の根拠にはしない。

| 到達点 | 実装と確認できる範囲 | 不足・責務 |
| --- | --- | --- |
| 保存情報の再利用・可逆的な仮定 | `RequestClarification`のREUSE／ASSUMEとscope検査 | 原文が意味的に十分か、仮定が妥当かは呼出側の判断。実モデル未評価 |
| 適切な質問先と原文保存 | Role別route、Task／Dispatch／scope、canonical RequestRecord | 質問後に依存作業を停止する統合は#284 |
| Plannerへの質問 | `AskPlannerClarification`→既存`AskPlanner`、canonical終端receipt参照 | 隔離Orchestratorと既存の設定・予算・ownership契約が必要 |
| Coderへの引継ぎ | RelatedIDの解決、明示的な正式後続Coder指定、HELD記録 | SDK inboxはrootを確証できず無効。配信完了・ACKの実環境証明なし |
| 回答の記録 | CLIの明示回答、回答者・出典・host時刻、OBSERVED維持 | 異なる回答は拒否するだけで、その回答本文が残らない |
| 回答後の再検証 | scopeを観測する基礎関数と既存InputDecisionがある | Clarification専用の読取り専用評価がない。正式な受理・Recovery接続は#284 |
| 不明配信と再起動 | REQUESTED／HELDの履歴を保持し本文を再送しない | 帰属を失わない完了証拠の復元は未実装 |

既存機構との対応は次のとおりである。`AskQuestion`／`AskPlanner`は
`internal/runtime/controller/question.go`に実在し、toolなしの隔離質問として再利用できる。
`HumanDecisionRequest`は既存Taskの選択・判断metadataであり、回答を認可へ変換するAPIではない。
SteeringはADR-0024の設計上の参照であり、この起点のGo実装には対応名のAPIを確認できない。
存在を推測して配信先にしない。#220が参照するADR-0062も、この起点のtrackedファイルには存在しない。
その文書があるものとして判断せず、現物のあるADR-0024、0066、0073と既存Issueを根拠にする。

### 実施計画と完了判定

DECISION: SDK root問題に独立した、回答証拠と次の判断の可視性を先に改善する。
安全な代替として、公開CLIで質問を読み、明示的な出典付き回答を観測し、
既存の正式な計画・承認・実行gateへ戻す経路を使う。
これはCoder到達ACKの代替証明ではなく、人間が回答を仲介する別の経路である。
SDK inboxの再有効化、停止済みCoderの再開、暗黙のDispatch発行は行わない。

| 順序 | 独立した作業単位と受入れ条件 | 依存・実装境界 |
| --- | --- | --- |
| 1 | 矛盾する回答もcanonical履歴へ保存し、元回答を上書きしない。同じ回答の逐次・並行retryは追記しない | 既存RequestRecordとOBSERVEDを再利用。意味的な解決や承認は行わない |
| 2 | 読取り専用の回答評価を既存InputDecisionで返す。矛盾、期限、対象・scope・session・ownerの変化を阻止条件として表示する | 1の証拠を使用。保存済み予算検査を再利用し、実残量・認可・再開を証明しない |
| 2と同時 | 保存済み回答からの明示選択を観測判断として記録する。候補receiptと評価hashを固定し、異なる質問・古い観測・無言の選択変更を拒否する | 既存TaskDecisionの出典・理由を再利用。回答の受理・実行許可ではなく、SDKにも依存しない |
| 3 | 公開CLIの質問→回答→評価→矛盾保存→再評価と、再起動・並行処理を検証する | 1・2の共通schema確定後に接続。通常・race、build／vet／固定scannerと証跡を保存 |
| 次工程 | 実SDKでroot帰属または子Agent起動拒否を証明し、帰属付き完了証拠の保存・復元を接続する | 未確認API・空AgentIDによる昇格は禁止。wire／provider／modelの検証を分離する |
| 後続統合 | 観測した回答選択を正式gate、Stop／HOLD／Recovery、独立レビューへ接続する | #283の回答契約と#284の正式統合を区別。既存#282／#228／#196を再実装しない |

通常・raceの試験は独立して実行できる。共通schemaを変更する実装同士は並行しない。
実際の再開は既存gateに委ね、InputDecisionの`accept`をACCEPTED／AUTHORIZED／DISPATCHEDと同一視しない。
回答の論理的妥当性、任意の回答からの自動再開、実モデルの自発的質問行動は今回の完了条件に含めない。
これらを含む全到達点に未達がある間、#283／#284はOPEN、第五審査はFAILを維持する。

### 実装結果と確認根拠

FACT: 異なる回答を`clarification.conflict`として元と同じcanonical RequestRecordへ保存する。
元回答は上書きせず、回答者・出典・host時刻・本文を保持したあと、未解決を示すエラーを返す。
回答者・出典・本文の違いを保守的に検出するもので、論理矛盾を意味解析したという主張ではない。
同じ質問の同一回答は並行retryでも追記しない。別質問の同じ回答を誤って抑止しない。

保存した回答の選択には`resolve-answer --file`を追加した。
既存TaskDecisionの出典・理由と、`assess`の評価hash、選択する回答のID・sequence・hashを記録する。
同じsnapshotをトランザクション内で再評価し、古い観測、別質問、hash不一致、宛先外Roleを拒否する。
選択結果は観測判断として追記し、元の回答と宛先を変更しない。
同じ判断の逐次・並行retryと再起動後のretryは追記せず、別の判断への上書きも拒否する。
既知の回答の再入力では解決を取り消さず、新しい回答証拠が追加された場合だけ未解決へ戻す。
保存時の選択先・直前receipt・判断IDの整合も読取り時に検証する。

DECISION: 回答者や選択者のRole・actorはCLIで申告した出典metadataとして扱う。
認証や仕様所有者の権限を証明するものではないため、選択を正式な受理・承認へ変換しない。
`OBSERVED`と、ADR-0066の入力適合を示す`accept`を維持する。
権限変更、期限切れ、対象・scope・session・ownerの変化、Stop/HOLD、不明配信は
回答の保存や選択では解除せず、既存の正式gateへ戻す。

`assess --id`は一つの保存snapshotから現在のTask／Dispatch／scope／evidence、
session／設定／Role／状態、operation owner／runner／期限、FlowのStop/HOLD、
保存済み予算、未回答・未解決・人間承認・REQUESTEDの配信不明を読取り専用で評価する。
`savedBudget`は保存済み情報に限り、実provider残量の証明ではない。
評価はRequestRecord、Graph、承認、Dispatch、operationを更新せず、Runtimeも操作しない。

変更箇所はControllerの既存Clarification投影と、新しいassessment／resolutionの処理、
対応する試験、CLI・help・completion生成・offline helpの許可一覧である。
既存ADR-0024／0066／0073の観測と認可の区別を再利用し、
新しい質問engine、scheduler、永続化基盤、Roleは追加していない。

### 検証範囲と再現コマンド

FACT: 以下は実際に実行したコマンドである。通常・raceは別々のコマンドで完走した。
全packageの全試験を一括実行した結果ではない。

```bash
gofmt -w internal/runtime/controller/clarification.go \
  internal/runtime/controller/clarification_assessment.go \
  internal/runtime/controller/clarification_assessment_test.go \
  internal/runtime/controller/clarification_owned_assessment_test.go \
  internal/runtime/controller/clarification_resolution.go \
  internal/runtime/controller/clarification_resolution_test.go \
  cmd/taxion/clarification.go cmd/taxion/clarification_test.go
go generate ./cmd/taxion
go test ./internal/runtime/controller ./cmd/taxion -run Clarification -count=1 -timeout 3m
go build ./...
go vet ./...
git diff --check
go test -json ./internal/runtime/copilot ./internal/runtime/controller ./cmd/taxion \
  -run 'Clarification|Question|Provenance|Completion|TargetedInterrupt|FlowRunnerObservesInterrupt|StoppedTurn' \
  -count=1 -timeout 5m
go test -race -json ./internal/runtime/copilot ./internal/runtime/controller ./cmd/taxion \
  -run 'Clarification|Question|Provenance|Completion|TargetedInterrupt|FlowRunnerObservesInterrupt|StoppedTurn' \
  -count=1 -timeout 8m
```

通常・raceともに187 PASS／0 FAIL／0 SKIPで、package別ではCopilot27、Controller146、CLI14だった。
件数は親とsubtestを含む完了testイベント数である。build、vet、completion生成、diff検査もPASS。
新しい試験fixtureではGraphだけでなくLedgerと履歴の整合が必要だったため、そのfixtureを修正した。
また、completion生成で新しいhelpを参照できるよう既存のoffline help一覧を更新した。
製品のvalidationやprovenance policyを緩めたわけではない。

公開CLIの別process試験は、質問→未回答評価→回答→適合評価→矛盾保存→保留評価→
明示選択→再評価→履歴取得を実行し、Graph・承認・Dispatch・HOLD・operationの不変を確認した。
Controller試験では実intake・実storeとfake Runtimeを使用し、owner変更・失効、
HOLD・Stop、配信不明が回答後も解除されないことを確認した。
並行選択、再起動、古いhash、別質問、宛先外Role、選択変更、追加証拠による再保留も検証した。
raceのCLI試験で起動する子binary自体は通常の`go build`であり、Go試験側のrace検査と区別する。

実SDK wire RPC、実provider、実モデルの呼出しは実施していない。
内部の受信処理を直接呼ぶ合成試験は、SDK inboxの登録や実際のCoder到達を証明しない。
SDK 1.0.22のroot帰属と切断後の完了証拠復元は今回変更せず、inbox無効化を維持した。

| 保存した証跡 | SHA-256 |
| --- | --- |
| `clarification-lifecycle-20261109-final-normal.jsonl` | `099d443ab7122b9fd561d6b232eb013f7fea79ed22eeb0ce586cd2e8228d894c` |
| `clarification-lifecycle-20261109-final-race.jsonl` | `f8da212c15903ffc0617f1d6b3180f3c5e3e39891e634ef1da50b7765c538e18` |

最終整理前の正規観測は23:47:17 JSTの21,600 requestsだった。
承認上限21,900との差300からreserve50を除き、作業可能残は250である。
account entitlementの残量を追加の作業権限には含めず、01:00 JSTの期限も変更していない。

### 未達条件と次の判断

PROPOSAL: 次の独立した作業単位は、観測した回答・選択receiptを既存の正式な入力確定と
Recoveryのpreviewへ結び付け、現時点の権限・対象・期限を検証することである。
SDK root問題に依存する自動配信は別に扱い、確実な帰属と保存・復元を証明できるまで有効化しない。
SDK調査と正式gateへの入力設計は独立して調査できるが、共通の状態更新へ同時着手しない。

回答の意味的妥当性、選択者の権限、実モデルの質問行動、Coderの到達・ACK、
正式な回答受理と自動Recovery、実環境E2E、後続の独立品質レビューは未確認である。
#283／#284はOPEN、第五審査FAILを維持し、今回の入力適合や回答選択だけでは完成扱いにしない。

## 相互情報補完のIssue精査と質問元への返信参照（2026-11-10）

FACT: 起点の変更とworktree状態を確認した。
作業開始前に承認済みの利用枠とreserveを確認し、残量を支出許可として扱わず、
承認上限を超える作業を行わない方針を維持した。
今回の時間上限は2人日であり、以前の01:00までの作業単位とは分ける。
予算は調査30、実装50、検証45、記録・保全25 requestsを目安とし、reserveは配分しない。

### Issue群は必要条件を持つが、固定Flowだけでは目標を満たさない

FACT: GitHubのOPEN Issue全31件、直近のCLOSED Issue30件の索引を取得し、
#90、#283、#284、#224、#64、#196、#220の本文を確認した。
実装は`clarification_intake.go`、`question.go`、`operation.go`、
`context.go`、`clarification_assessment.go`、SDKの`clarification.go`と関連試験を照合した。
ADR-0040の品質制約とADR-0066の観測・認可分離を継承する。
全Issueの全コメントや全ADRを通読したという意味ではない。

| 所有するIssue | 目標への適合と不足 |
| --- | --- |
| #90 | 質問・連携・費用・品質を横断管理する。往復通信と判断更新の受入れ証拠が必要 |
| #283 | 既存情報の再利用、相手の選定、回答・矛盾・選択を所有。質問元sessionへ回答を返す経路が欠けていた |
| #284 | 回答後の正式受理・認可・Recoveryを所有。返信を読む機能の前提にしない |
| #282／#228 | 回答保存・ownershipと中断後の復元を再利用。新しい保存基盤は不要 |
| #224／#64 | 独立した相談実行とPlanner起動の効果を扱う。固定Flow以外の相談と実行中operationの境界は未統合 |
| #189／#196 | 工程・予算・承認内適応を管理する。動的な相談の代替にはならない |
| #223／#225／#227 | 必要な入力、反復抑止、帰属を支える。相互理解や実モデルでの成立自体の証明ではない |

INFERENCE: Issue群の方向性は部分適合であり、番号の統合や新Issueは不要と判断した。
#283に双方向の応答参照と判断更新の受入れ条件を補い、#90でG1～G8を追跡する。
Reviewerを後工程だけに固定しない相談は残課題として明記する。

FACT: `ObserveRoleClarification`は質問を保存して参照だけを返し、
回答本文や矛盾は公開CLI／store側に残っていた。
また`askQuestion`は`checkOperation`へ空のFlow IDを渡すため、
RUNNING Flowがあると既存の排他制御が相談開始を拒否する。
`WithOperation`の伝播だけではこのFlow検査を通らない。
この拒否を迂回してPlanner sessionを追加する変更は行わなかった。

### 既存toolで自分の質問への返信を読めるようにする

DECISION: 新しい会話エンジンや受信inboxを作らず、`taxion_clarify`のprotocol 3に
`action: "read"`と質問IDだけを指定する観測を追加した。
既存のsession／設定／worktree、operation owner／runner／期限、scope検査を質問登録と共有し、
さらに元質問のTask／Dispatch／Role／originを照合する。
宛先Roleという理由だけで他者の質問を取得させず、SDK inboxも有効化しない。
空AgentIDからrootを証明できるとする判断は追加していない。

FACT: `assessClarification`を再利用し、canonical回答候補、出典・時刻・receipt、
矛盾、明示選択、保存時点の適合判定を返す。
Graph、RequestRecord、承認、HOLD、ownerを書き換えない。
質問本文の再送や、返信本文の人間による再転記は不要になる。
ただし、回答者の起動・回答生成・保存が自動化されたわけではない。
取得後にモデルが判断を更新したことも未実証である。
同じ実行の次の安全なtool呼出しで読み、反復pollは指示しない。
応答は64 KiBに制限し、超過時に部分本文を返さない。
旧protocolには追加機能を後付けせず、設定hashの違いで新設定からの再利用を拒否する。

### G1～G8は実モデルの成立まで含めて判定する

次の判定は相互情報補完の全体に対するもので、限定経路の試験合格とは区別する。

| Gate | 判定 | 今回確認した動作と未達条件 |
| --- | --- | --- |
| G1 相互情報交換 | FAIL | SDK登録handler→Controller→実storeで、質問元がPlanner名義の保存返信と矛盾・選択を取得。回答元の自動実行と3 Roleすべての双方向連携は未接続 |
| G2 相互理解更新 | BLOCKED | 回答・根拠をモデル向け応答へ返せる。実モデルが自身の計画・判断を更新した証拠はない |
| G3 動的協調 | FAIL | 結果提出を待たず同じ実行中に返信参照できるが、RUNNING Flowから任意の相手へ相談するownership経路は不足 |
| G4 合意形成 | FAIL | 矛盾保持、明示選択、新証拠による再保留を実storeで確認。Role間の反論・判断理由の往復と合意形成は未実装 |
| G5 協調制御 | FAIL | 同一質問・並行retry・返信読取りで二重登録しないことを確認。実モデルの不要議論や反復pollを抑制した証拠はない |
| G6 人間との協調 | FAIL | 明示回答・選択と権限の分離、owner変更時の読取り拒否を確認。実モデル協調中の人間による方向修正E2Eは未確認 |
| G7 継続性 | FAIL | store再open後に同じorigin／選択／回答receiptを参照できる。新session・失効ownerへの継続は拒否し、正式Recoveryの統合は未達 |
| G8 実環境での成立 | BLOCKED | 実SDK wire RPC／provider／modelの協調実行は行っていない |

合成sessionと実storeの試験は、保存された返信をモデル境界へ返す限定往復の証拠である。
実際のPlannerが回答したこと、root限定、到達ACK、意味的な妥当性、G1全体の合格は証明しない。
全体のPASSはまだなく、#283／#284のOPENと第五審査FAILを維持する。

### 次は実行中の相談元・回答元を同じ承認枠へ結び付ける

DECISION: 今回は返信の参照までに限定し、正式gateやSDK配信証明を主目的にしなかった。
既存の工程順序を変えただけではなく、動作中の質問元sessionが自ら保存返信を取得する経路を追加した。
一方、応答生成はまだ外部からの明示操作に依存し、人間の仲介を全面的には解消していない。

次の最優先は#283／#282／#224の境界で、既存の隔離質問実行をRUNNING operationへ
安全に関連付け、質問→回答生成→質問元の参照を一つの承認済み相談として検証することである。
session・親子帰属・累計予算・期限・取消・不明結果を保持し、
Reviewerは独立した成果評価とは別の助言コンテキストを使う。
その後、同じ問いについて根拠付きの反論・提案を往復させ、判断更新を記録する。
通常の実装相談と、正式な受理・認可・Recoveryを要する変更は分ける。
#284は後者の統合を担当し、今回の返信読取りを正式受理とみなさない。

ownershipの設計と、固定した小課題による実モデルの受入れ手順は独立して準備できる。
共通の状態更新は並行実装しない。新たなモデル実行は残予算だけでなく
対象・構成・課金・独立性の承認条件が揃ってから行う。

### 検証コマンドと確認範囲

以下の通常・raceはそれぞれ別のコマンドで完走した。全packageの全試験ではない。

```bash
gofmt -w internal/runtime/controller/clarification_intake.go \
  internal/runtime/copilot/clarification.go \
  internal/runtime/copilot/clarification_test.go cmd/taxion/runtime_worker.go
go test ./internal/runtime/copilot ./internal/runtime/controller -run Clarification -count=1
go test ./internal/runtime/copilot -run TestClarificationHostToolPersistsOnlyBoundRunningRoleObservation -count=1
go test -json ./internal/runtime/copilot ./internal/runtime/controller ./cmd/taxion \
  -run 'Clarification|Question|Provenance|Completion|DispatchWorker' -count=1 -timeout 5m
go test -race -json ./internal/runtime/copilot ./internal/runtime/controller ./cmd/taxion \
  -run 'Clarification|Question|Provenance|Completion|DispatchWorker' -count=1 -timeout 8m
go build ./...
go vet ./...
go generate ./cmd/taxion
git diff --check
```

FACT: 最初のClarification対象試験は、既存fixtureのnative予算上限による`blocked`を
新しい返信参照試験が`accept`と期待してFAILした。
製品の予算検査は変更せず、回答取得後も予算blockが残ることを検証する形に修正した。
再実行はPASSし、最終の通常・raceは各184 PASS／0 FAIL／0 SKIPだった。
内訳はCopilot27、Controller141、CLI16で、親とsubtestの完了イベントを含む。
build、vet、gofmt、completion生成、diff検査もPASS。completionの差分はない。

SDK 1.0.22の登録済みtool handlerを合成呼出しし、実SQLite storeで
未回答→返信→反復・並行読取り→矛盾→選択→再open→新証拠の追加を実行した。
別origin、旧protocol、owner変更、不正な入力、応答上限超過も拒否した。
実SDK wire RPC／provider／modelの試験はSKIPであり、未実施を通常試験のSKIP件数へ含めていない。
回答者の`PLANNER`はfixtureの申告metadataで、実Plannerの本人認証ではない。
既存の受入れ・HOLD・Stop・設定・質問保存の回帰を同時に実行した。
独立した品質Reviewerによる同一候補の評価は未実施である。

| 証跡 | SHA-256 |
| --- | --- |
| `mutual-info-20261110-normal.jsonl` | `ba0e2caba0c46cb61e58465aaf4c12ad363a0ad3c52e9e7424a02a03a04d8f45` |
| `mutual-info-20261110-race.jsonl` | `505f7aecc303df1d79e8e05618a643726782503d483d755ba152800ad86b1d81` |

最終indexの機密検査には、既存の固定Betterleaks 1.9.0 binaryを使用する。
コマンドは`go run ./cmd/taxion git check-index --secrets --scanner "$SCANNER"`であり、
`SCANNER`は既にchecksum照合済みのtask-local binaryの絶対pathを指す。
結果は`mutual-info-20261110-index-scan.log`へ保存する。
日本語lintは直接Python実行で辞書依存が不足したが、
`uv run --offline`で宣言済みの依存を使うと実行できた。
新しい節の検出はFACTラベル、技術用語、既存節との文頭反復、
工程変更との区別を説明する対比だったため、事実区分と意味の明確さを優先して残した。

08:18:41 JSTの整理前観測は使用21,800 requestsだった。
開始時からのaccount差分は100、承認上限まで100、reserveを除く作業可能残は50である。
この差分は他のaccount利用も含み得る保守的な観測で、厳密なsession別消費ではない。
追加実装には使わず、残りを記録・検査・commitに限定する。
