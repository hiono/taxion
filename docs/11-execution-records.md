# 実行の復旧と承認済み外部記録

Taxion は、実行結果と送信予定の外部記録を Task Graph と同じ SQLite transaction で保存する。
controller が結果・Git・承認を照合し、GitHub adapter が承認された公開操作を実行する。
CLI はローカルの信頼された管理者が操作する。`actor` と承認の出典は管理者が指定する参照であり、
参加者の本人認証は M15 の実装・受入れで扱う。

## 復旧時の照合

| 保存された状態 | 再起動後の処理 |
| --- | --- |
| 発行済み、実行開始前 | 現在の設定承認・主体・権限・worktree を確認して開始する。保存済み Session があれば同じ Session を使う |
| Coder・Reviewer の実行中 | 同じ役割・設定の Session に接続して観測する。欠落・失効・不明な実行はエラーとして保持し、停止確認と後続契約の承認で処理する |
| Coder の結果取得後 | 結果と利用量の receipt、現在の origin・branch・HEAD・親 SHA・変更範囲・検証記録を照合して独立レビューへ進む。snapshot mode は baseline commit SHA、保持 ref、output tree SHA と path を照合する |
| Reviewer の結果取得後 | 解放済み worktree の不存在と Git registry、元 repository の対象 commit・親 SHA・origin、直前 Coder と同じ出力 SHA を照合する。snapshot mode は Coder と同じ output tree SHA も照合する |
| Human 判断待ち・保留中 | 対象、保留、補足、指示案の版、質問・調査の出典、明示反映を復元する。続行は保存済みの保留解除・承認条件に従う |
| 完了 checkpoint | receipt と最新の Coder・Reviewer の Git 証拠を再確認する。改変は明示的なエラーになる |

snapshot mode では `outputSha` は baseline commit SHA のまま、`outputTreeSha` は Controller が Git tree から算出する成果物識別子である。両者は別の値として Task・Dispatch・Reviewer の証拠に保存する。Controller は tree object を `refs/taxion/snapshots/` に保持し、再起動後も ref と実 worktree の tree・変更 path を照合する。ref の保持はローカル Git metadata の変更であり、branch、commit、index、remote は変更しない。

`workflow recover` は、観測した runner ID と旧 process の停止確認、判断者、理由、証拠を要求する。
全 Dispatch の終了と証拠照合が済むと、旧 runner の所有権を解除して復旧記録を保存する。
実行中の Dispatch は先に `runtime stop` または確認できた結果で処理する。
失敗した照合では所有権と元の結果を保持する。

実行開始は指示送信前に永続化する。開始済み Dispatch の再送はエラーになる。
開始記録だけが残った場合も、Session の観測と停止確認で処理する。
SQLite の破損、非互換の JSON、未知の公開状態は読み込みエラーとして扱う。

## イベントと結果の receipt

controller は output・usage・turn 終了・失敗・cancel のイベントについて、
Dispatch、Session、stream ID、sequence、内容の hash、観測時刻を保存する。
usage とその receipt は一緒に保存する。同じ stream・sequence の同一イベントは再利用し、
異なる内容の再送と cancel 後のイベント・結果を拒否する。

SDK adapter の sequence は接続した adapter 内の番号である。
provider の永続 event ID として扱える範囲が限られるため、保存済み stream から
別 stream へ再接続したイベントの採用は明示的な照合エラーにする。
接続後の観測と、結果・利用量の確定は分けて扱う。

結果の receipt は、保存した結果とその時点の利用量をまとめて照合する。
`SUSPENDED` の後に安全停止して `CANCELLED` を保存する場合は、同じ Dispatch の
結果 receipt に連続した sequence を付けて追記し、元の suspension receipt を保持する。
照合時は最新結果との一致に加え、履歴の順序、過去の suspension の Dispatch・Session
識別子、内容 hash、保存済み利用量との一致を検査する。
旧版の成功結果も読み出せるが、receipt を持たない checkpoint の自動採用は
証拠不足として停止する。履歴を確認し、停止・後続契約の明示承認で作業を進める。
hash は保存内容の取り違え・個別改変の検出に使う。保存先全体の書換えに対する
真正性は、管理者のアクセス制御と保管運用で確保する。

## 外部記録の公開手順

実装済みの操作は、GitHub の Issue 作成、Issue・PR のコメント、Issue の完了 close と、
GitLab の Issue 作成・コメント・完了 close、merge request のコメントである。
GitLab merge request の作成・承認・close は未対応である。
追加の操作・host・添付形式は、能力エラーとして返し、対応する adapter の実装後に利用する。

公開要求には、Task ID、provider・host・repository、外部種別・番号、操作、本文、
作成時の件名、API account、credential 参照、判断者、承認の出典を指定する。
repository と作業記録先が異なる場合は `taskRepository` にコード側の repository を指定する。
これら全体の hash を Human が確認し、`expectedHash` として承認要求に戻す。

```sh
taxion records proposal --file requested-publication.json > reviewed-publication.json
# Human が reviewed-publication.json の接続先・主体・内容・hash を確認する
taxion records approve --file reviewed-publication.json --scanner /trusted/betterleaks
taxion records send --id accepted-report --scanner /trusted/betterleaks
taxion records show
```

入力は `{"update": {...}, "expectedHash": "..."}` という JSON object である。
proposal の段階では `expectedHash` を省略できる。`status`・送信 claim・時刻などは controller が設定する。
GitHub は `gh:active`、GitLab は `glab:active`、またはいずれも `env:NAME` を使う。
各 API process には選択した provider credential だけを渡し、Copilot credential は runtime が扱う。

Issue 作成は、`PENDING:<登録Task ID>` と登録 Task の完了依存を持つ DRAFT Task に対して行う。
送信を確認すると、新しい Issue 番号を Task に保存する。
登録 Task の証拠確認・完了と、対象 Task の READY への承認は既存の Task 遷移で行う。
対象 Task の実行許可は、登録 Task の完了と READY への承認で確定する。
既存 Issue の再利用は、その番号を登録した Task に対して行う。

## 送信状態と再起動

| 状態 | 意味と次の操作 |
| --- | --- |
| `PENDING` | 承認済みの送信待ち。本文の再検査と現在の権限・Task 条件の確認後に送信する |
| `CLAIMED` | 一つの sender が所有する。別 process は同じ記録を送信できない |
| `UNKNOWN` | 書込みを試みたが応答を確認できない。旧 sender の停止確認と送信先の照合を行う |
| `SENT` | 外部 ID と確認した結果を保存済み。同じ要求は保存済み結果を返す |

送信前の検査・権限確認の失敗は `PENDING`、書込み後の応答喪失は `UNKNOWN` に保存する。
送信先では本文に付けた `<!-- taxion:<hash> -->` を検索し、
本文・作成時の件名・作者・外部種別・ID を確認する。重複や一致しない marker は照合エラーになる。
close は、対象 Issue の `closed`・`completed` という状態を照合する操作である。

```sh
taxion records reconcile --id accepted-report --claim <保存されたclaim> --sender-stopped
```

照合できると `SENT` を保存し、Issue 作成なら Task への関連づけも復元する。
marker が見つからない場合は Human が provider 側の処理・遅延・操作履歴を確認する。
新しい承認は、その確認結果を出典にして別 ID で行う。
外部 API の書込みと SQLite の保存には時間差があるため、
不明な結果を保存して照合する方式で重複送信を防ぐ。

## 失敗分析と close

`records finding --file finding.json` は、失敗・レビュー指摘の結果 hash、
原因、是正措置、lesson 候補、検証の参照、Human の決定と出典を保存する。
検証の参照には、その結果に保存された evidence、または `dispatch:<ID>:result` を使う。
是正後の検証は、同じ Task／ReviewFlow の終了済み結果を
`dispatch:<ID>:result:<結果JSONのSHA256>` で指定し、保存済み receipt と照合する。
新しい分析 ID で是正の確認結果を追加し、最初の失敗判断も保持する。
記録が指す結果の成否は元の結果から読む。是正措置の記載と、是正後の成功証拠は別々に確認する。
同じ ID・同じ内容の判断は再利用し、異なる内容の上書きはエラーになる。
lesson の強制ルール化は Memory の明示的な承認手順で行う。

Issue の close は、Task・ReviewFlow の完了、acceptance と検証結果、
Reviewer の対象 SHA、最新 Git、receipt、出典付き例外承認、依存状態、
先行する外部記録の `SENT` を照合してから行う。
claim 後にも、外部書込みの直前に現在の Task 条件を確認する。
書込みとの時間差で契約が変わった場合は、保存失敗を表面化し、claim と送信先を照合する。

## 公開前の秘密検査

Betterleaks 1.9.0 の固定既定ルールで、承認時と送信時の本文・件名をローカル検査する。
scanner の失敗・欠落・不正な結果は公開を停止し、エラーには秘密値を含めない。
添付と追加の API 操作は、対応する公開形式の検査を実装した adapter で扱う。

push 前は、確認済みの remote base と公開する head を full SHA で固定して全範囲を検査する。
各 commit の tree と message を private index で読み、
最新ファイルで削除済みの秘密も検出する。worktree と本来の index は保持する。

```sh
taxion git check-range --base <full-remote-base-SHA> --head <full-head-SHA> --scanner /trusted/betterleaks
```

公開者は検査した head を指定して push する。新規 branch では、公開先が既に持つ基点を確認する。
scanner の検出範囲、submodule 内部、外部から取得する内容はそれぞれ確認が必要である。
provider 側の受信時保護と CI の検出・merge gate は
[開発基盤の対応評価](12-forge-capabilities.md) に整理する。

## 再現できる障害ケース

無料の回帰テストでは、SQLite の閉鎖・再接続、実 Git repository・worktree の照合、
重複 process、結果・利用量の改変、イベント再送、cancel 後の結果、
Issue 作成後の応答喪失と再関連づけを確認する。
復旧→レビュー指摘の保存→修正 SHA のレビュー→公開応答喪失→照合→close も検証する。
GitHub の応答障害は fixture、CLI 引数・credential 分離・pagination は gh 脚本で再現する。
有料 SDK 受入れは、通常 CI と分離した明示承認・予算上限付きの検証として行う。

GitHub 実受入れでは、Human が接続先・主体・本文 hash を承認し、
新しい CLI から [#56 のコメント](https://github.com/hiono/taxion/issues/56#issuecomment-6115563384) を公開した。
`SENT` と外部 ID を保存した後、別 process から同じ ID を再送して同じ記録を取得し、
GitHub 側の marker・作者・コメント件数が一件であることを照合した。
この受入れの追加 SDK 推論は 0 件である。
