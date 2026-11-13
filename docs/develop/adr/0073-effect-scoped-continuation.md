# 73. 承認範囲内の継続判断を操作の副作用で分ける

Date: 2026-11-06

## Status

Accepted for the policy and implementation below.
実装前レビューを受け、実装も独立レビューの対象とする。
配布・運用機への導入・実運用での受入れは別の判断であり、本 ADR の採用から推論しない。

## Context

Taxion の目的は製品開発を進めることであり、Taxion 自身の更新を繰り返すことではない。
v0.7.25 までの修正を同じ原因とは扱わないが、個別の復旧操作と前提条件を追加するだけでは、
保存済み成果を使って目的を達成できない問題が残る。
条件の不足だけでなく、条件過多と重複した判断を調べる必要がある。

具体例として `workflow run` と `workflow resume` は同じ保存 Dispatch の起動を行うのに、
後者だけが Coder、未 acknowledge、bundle-required、packet lifecycle の利用を必須にしていた。
packet lifecycle は通常の実行では任意である。
Reviewer 発行時にも budget audit と承認を保存しているが、resume はそれらを利用しなかった。
この差を新しい例外フラグで埋めず、実際に行う操作の安全条件へ戻す。

## Decision

### 原則と責務

承認された対象・権限・資源・受入条件の範囲内では、agent が達成手段を選ぶ。
Controller は想定した手順との一致ではなく、禁止された副作用を防止する。
「全条件を削除する」「AI が成功と判断すれば合格にする」という変更ではない。

| 責務 | 既存の配置 | 判断すること |
| --- | --- | --- |
| 契約・権限 | core の ApprovalBundle、configuration | 対象、変更範囲、権限、公開先、費用・期限 |
| 証拠 | runtime receipt、gitproof、store | 観測事実、同一候補、利用量、実行・配送の既知／不明 |
| 継続判断 | Controller の純粋な判定 | 今行う操作が何を変えるか、具体的な禁止理由 |
| スケジューリング | 既存 Flow runner と外側の coding agent | 保存成果の処理を優先し、依存関係と残資源内で次操作を選ぶ |
| 実行・保存 | runtime adapter と store transaction | 選択した操作と append-only な結果保存 |
| 利用者への提示 | CLI と managed Skill | 同じ判定の操作、理由、後続処理。独自の gate は追加しない |

Task は目的と契約、Flow は達成計画、Dispatch は個別の実行を表す。
一つの Dispatch の通信失敗で他の成果や元の承認を無効にしない。
新しい汎用 scheduler、plugin、DAG executor、独自の policy DSL は作らない。

### 操作ごとの禁止事項

| 操作の種類 | 禁止する副作用 | 他の操作へ持ち込まない条件 |
| --- | --- | --- |
| 読取り・診断 | 権限外の読取り、秘密の出力、読取りを装った実行 | 実行の残予算不足だけで保存証拠の確認を拒否しない |
| 証拠の追記 | 原本改変、帰属の捏造、未検証事実の合格認定。timeout/cancel 後の不明な commit を成功・rollback と推測しない | 接続終了や transport failure を回答内容と同一視しない。`UNKNOWN` は記録確認を要求し、同じ canonical approval hash だけでは解決しない |
| 未開始の実行 | 契約外の対象・権限、二重送信、競合、資源超過 | role 名や任意 packet 機能の未使用だけで拒否しない |
| 結果受理・品質判定 | 別候補の証拠流用、検証・独立レビューの捏造 | transport status だけで完成済み証拠を捨てない |
| 公開・外部変更 | 未承認公開、秘密漏洩、配送不明の再送 | 内部工程の許可を公開権限とみなさない |

未解決事項は保持し、影響する操作で解決する。関係のない操作まで全面停止しない。
独立していることを確認できない場合に、独立していると推測して並列実行しない。
現在の operation lease をこの変更だけで並列 scheduler に置き換えない。

### 未開始 Dispatch の共通判定

`run` と `resume` は同じ未開始判定を使う。
結果や execution-start がある実行を、新規起動として再送しない。
acknowledge 済みというだけでは失敗にしないが、既存の handoff release と実行未開始の証拠を保持する。
hold が最初の send より前に入った場合には session が既に作成・保存されている。
その既存 session へ再接続する通常 runner の動作を維持し、新しい session を作らない。
session の存在だけを実行済みとみなさず、保存利用量も引き継いで残予算を確認する。
明示的な hold と未処理 draft は無視しない。

resume は Coder 専用の承認経路ではない。
発行済み role の正確な承認と configuration、最新 issuance audit、元の limits、
有効期限、instruction、対象 worktree の証拠を読む。
packet lifecycle は opt-in のままにし、有効な場合だけ既存の canonical packet 検証を行う。
packet の Reviewer 限定委譲は修正権限の根拠にしない。一方、元の Flow 契約が既に
許可する bounded repair を、packet 機能を有効にしたという理由だけで拒否しない。
修正の認可は元の ApprovalBundle、同一契約の検証、明示された設定対象と元の修正上限から導出する。
IssueDispatch が READY から ACTIVE にした現在の role Task は、ローカルな比較値だけを
READY に投影して発行時の承認と照合する。保存 state や承認は書き換えない。

Reviewer の準備前には source と保存 target の同一性を確認し、準備済みなら管理 worktree の同一性を確認する。
判定だけでは worktree を作らない。実際の準備と起動は通常 runner が行う。
artifact input validation の未開始／完了／実行結果不明は区別し、判定を通すために command を再実行しない。

### スケジューリングと完了条件

保存回答の処理、完成済み検証の再利用、次の承認済み工程の順に検討する。
通常工程は既存 runner、想定外の技術的問題の対処は外側 agent が担当する。
新しい権限・予算・期限・受入条件が必要な場合だけ、人間の実質判断へ戻す。
全経路を一つの巨大な状態遷移 whitelist として列挙し直さない。

新規の工程計画では `stageBudget.version:2` を選ぶ。時間・native 使用量の見積もりは
助言であり、未発生の修正まで予約して実行を早期停止させない。agent は承認済みの
全体期限・実使用量・session/Dispatch/修正回数の範囲で配分する。
提出、Coder 検証、成果物入力検証には有効な全体期限を適用し、呼出し元期限が早ければそちらを守る。
見積もり不足・超過だけでは再承認を要求しない。実上限への到達、
正の native 上限に対する使用量不明、品質不足は別に判定する。
provider-stop での残高不明を、この変更で停止条件へ追加しない。

保存済み FAILED Reviewer の負の回答を追記する observation は元の提出期限と別の
Controller 内上限を持つ。既定30秒、明示値は正で最大5分とし、呼出し元の早い
deadline/cancellation を優先する。これは操作全体の厳密な wall-clock cap ではない。
CLI の file/store read、Store open/migration、Copilot runtime setup、operation lease、cleanup/output と Controller/Store の
in-process mutex wait は予算外として文書化し、SQL/native/Git 処理には context を渡す。
この継続判断は負の証拠の append に限り、Flow・FAILED・usage・元の承認や hash shape、
再実行 eligibility を変更しない。

version 2 の計画 hash は対象・設定・認可・期限・実使用量上限・回数・役割別ソフト上限を
固定する。助言にすぎない時間/native 見積もりと実績の抽出根拠は hash から除く。
開始時には見積もりも含む計画全体を保存し、発行監査と照合して以後の書換えを拒否する。
version 1 の hash・予約・提出期限は変更しない。

runtime Start を試みる前の準備失敗は、その操作を失敗として返し、同じ RUNNING Flow と
発行済み role を保持して runner claim を解放する。自動 retry はしない。
実際の取消・期限・利用量違反を準備エラーで隠さず、Start 試行後の成否不明は従来どおり停止する。
保存した RUNNING は process の生存証明ではない。

負の指摘の追記は、品質受理や実行再開と分ける。FAILED の元結果・receipt・session・時刻・
candidate・設定・利用量の照合を残す一方、無関係な実行の停止を要求しない。
同意 hash は元 Flow・親/role Task・要求・照合結果に限定する。
適用直前の全状態比較は競合防止として残すが、無関係な変更を新権限の承認にはしない。
保存済みの品質受理も、証拠条件ではなく特定の過去エラーメッセージだけで拒否しない。

安全性だけでなく、承認範囲内の作業が完遂できることを検証する。
独立レビューは「実装がこの表に一致するか」だけでなく、
「この条件を残す理由があるか」「条件を外してよい反例がないか」を確認する。

### 原文に遡れる引継ぎ

要約は担当が資料を探す入口とし、判断の根拠には原文を使う。
これは今回の全面改定の必須条件であり、後続の改善候補へ延期しない。
毎回のプロンプトへ全文を詰め込むことと、必要時に全文へ到達できることを区別する。

依頼原文・明示承認・設計理由・未解決事項・前工程の証拠について、
出典、版、内容 hash を固定した読取り専用の資料と索引を渡す。
担当は要約に疑問や不足があれば原文を読み、自分の推測で欠落を補わない。
参照 hash や管理領域の path を示すだけでは、担当からの到達保証として認めない。
索引、実際の読取り手段、対象へのアクセス権を一つの引継ぎ契約として扱う。

Reviewer は Coder の説明を唯一の根拠にせず、依頼原文・承認条件と実際の成果を
独立して照合する。同じ原文を参照しても、Coder の判断や未検証の成功主張は継承しない。
原文へのアクセスは、実行・変更・公開の権限を増やさず、
controller DB、他 Task、資格情報、非公開の内部推論への包括的なアクセスを与えない。
未提供・参照不能・版不一致の資料はその状態を明示し、読んだことや原文の完全性を捏造しない。

原文は既存の append-only な Task Decision に保存し、既存の提案・承認 hash に含める。
intake が保存済みの依頼原文、承認記録、選択済みの資料と、明示的に提供された設計理由・
未解決事項を再利用する。前工程の結果は保存済み参照と実物を照合する。
仕様の抜粋を利用する許可を、元ファイル全文の利用許可へ拡張しない。

Copilot には固定した資料だけをページ単位で返す読取り専用ツールを渡す。
OpenACP には同じ資料を既存の読取り専用 `/config` 内へ複製し、その資料だけの読取りを許可する。
どちらも製品ファイルの読取り権限や shell 権限を追加しない。
原文アクセスの導入版と、既存の結果・証拠の最低対応版を分け、
旧 Dispatch の設定 hash・prompt・tool surface を維持する。
資料を読んだ回数は新たな品質 gate にせず、Reviewer の独立照合を既存の品質責務に含める。

## 実装計画と範囲

### 確認した条件と処置

以下は今回のソース追跡で確認した主な条件・重複であり、全製品の条件数ではない。

| 既存の条件・重複 | 処置 | 残す判定と根拠 |
| --- | --- | --- |
| resume は Coder のみ | 削除 | 発行済み role の承認と対象証拠を使う |
| 親 Task は常に ACTIVE | 適用先を変更 | 実際に起動する role Task が ACTIVE。親は Coder 完了後 BLOCKED になり得る |
| acknowledge 済みは一律拒否 | 適用範囲を縮小 | release 済み未開始なら、通常 runner と同じ扱い |
| session があれば一律拒否 | 適用範囲を縮小 | 最初の send より前の保存 binding は再接続。実行開始済みは再送しない |
| resume には packet lifecycle が必須 | 削除 | 有効なときだけ packet の正当性を確認 |
| packet の field を resume 専用に再検証 | 共通検証へ統合 | `checkLifecyclePacket` を使い二重実装をなくす |
| resume のみ verified-legacy を拒否 | 削除 | 保存 schema の分類を通常実行と一致させる |
| 最後の budget event が発行でなければ拒否 | 適用範囲を縮小 | 最新発行の binding と、現在の利用量をそれぞれ確認 |
| session 数の形を通常 Coder Flow に限定 | 適用範囲を縮小 | 保存された artifact の zero-Coder / one-Reviewer 上限も照合 |
| packet 使用時は元契約からの修正認可を導出しない | 削除 | packet ではなく元の修正権限・設定・上限から認可 |
| 読取り専用の input assessment に全 workspace 停止を要求 | 適用先を変更 | 観測保存・intake 適用で停止条件を確認。読取りは実行許可にしない |
| 準備だけの失敗で Flow を正式停止 | 適用範囲を縮小 | Start 未試行なら同じ認可で技術問題を直せる。実際の取消・上限到達は停止 |
| 保存品質の受理に過去の固定エラー文字列を要求 | 削除 | STOPPED/SAFETY_STOP と既存の成功結果・独立レビュー・hold・Git・packet 等の証拠を照合 |
| 負の指摘追記に無関係な実行の停止を要求 | 削除 | 元実行の停止・正式 FAILED・元 session と負の回答を照合 |
| 負の指摘追記の同意 hash に全 workspace を含める | 適用先を変更 | 対象の証拠を固定。全状態比較は適用中の競合防止に残す |
| 未発生工程の予測時間・利用量で実行を止める | 新規 version 2 では削除 | 実期限・実使用量・回数・品質を守る。version 1 は保持 |
| 予測や抽出根拠の変化で計画の再承認を要求 | version 2 の hash から除外 | 外枠・権限・対象の変更だけは引き続き別承認 |

独立レビューで、準備済み Reviewer の判定に使う既存 Git helper が clean filter を
起動し得ることも指摘された。これは新しい手順制限ではなく、read-only 操作が
実際に外部コマンドを実行しないための共通 helper 修正として扱う。

### 原文参照保証の実装状態

従来の Dispatch は目的・承認条件・選択した Memory と仕様・前工程の構造化結果を渡す。
compact 表現は機械的な射影であり、モデルによる自由な要約ではない。
ただし、依頼の会話原文すべてを保存・参照可能にする保証はない。
元の Task 化で省いた背景を、担当が原文に戻って補えるとは限らない。
原本の保存と担当からの到達可能性を区別する必要がある。

上記の Decision に従い、原文参照保証を同じ全面改定の実装対象に含める。
継続判断・工程配分の変更に加え、instruction version 6 に原文アクセスを実装する。
`contextSources` は既存 Task Decision に保存し、依頼原文・承認・Memory/仕様・
前工程の正確な結果と検証出力を `ContextDocument` に固定する。
Copilot は `taxion_read_context`、OpenACP は読取り専用の資料ファイルを使う。
version 5 の結果・追記証拠の受理は別の最低対応版で維持する。

`request` の依頼原文、人の追記、画面へ表示する Planner の計画・理由・質問は、
private workspace の append-only `context-source` record に保存する。
これは従来の digest のみを残す方針からの明示的な変更である。
既知の credential pattern は置換し、元入力の digest と秘匿化の有無を版に記録する。
checkpoint と planning receipt は最小のままにし、実行提案へ資料をコピーして承認 hash に含める。
private reasoning、native session 全文、外側の会話の自動収集は含めない。
原文が保存済みなら、その原文から未開始 Planner を継続できる。
旧記録の欠落した本文を digest から復元したことにはしない。

実装レビューでは、Copilot の custom-tool permission、OpenACP の workspace 相対
permission と長い一行の読取り、`request` の原文欠落、quota 観測への実行文脈依存が指摘された。
reader の登録だけでは到達可能性を証明できないため、実 permission handler と長行の末尾を検証する。
Git のない隔離環境で native project worktree が `/` になる点も照合し、
process の `/workspace` を基準と誤認しない。quota は専用の model 起動不能 observer を使い、
実行用 tool asset・tracked-file snapshot の準備と照合を数値観測の前提にしない。
OpenACP は短い JSONL chunk の `text` を連結して元の UTF-8 本文を復元する。
quota だけの観測は、実行用の原文・仕様準備を行わず、元の設定・account・期限の照合を維持する。

原文アクセスの実装前設計は独立レビューで承認済みである。
実装は原文の末尾までの読取り、Unicode の保持、別版の拒否、
実際の Coder→Reviewer→修正→再レビューへの引継ぎ、
旧設定 hash の一致、OpenACP の OS 読取り専用境界を合成試験で確認する。
実モデルが原文から妥当な指摘を作る受入れとは区別する。
原文参照を含む実装は独立コードレビューで承認された。
全体の build・vet・通常回帰、変更箇所の race、OS 読取り専用境界を確認した。
OpenCode 1.19.3 の実 permission 評価でも、資料・製品・skill と拒否対象の42ケースを照合した。
これらは実モデルの判断品質や運用受入れの代わりではない。
正式公開は別の配布検証と Release・Issue・Milestone の対応を確認して完了する。

### 判断単位ごとの監査結果

| 対象 | 現在の判定場所 | 状態 |
| --- | --- | --- |
| 保存した未開始 role の実行 | run、resume gate、role admission、gitproof | 今回の実装・回帰試験の対象 |
| 入力の読取りと観測の適用 | input observation、intake | 純粋評価から全 workspace 停止条件を除外。保存・契約適用の条件は維持 |
| FAILED の保存回答と通常結果の受理 | failed review、recovery、result finalizer、store | 負の証拠・品質受理・実行再開を分離。incident 文言と無関係な実行条件を除外 |
| 依頼・契約・configuration の変更 | operation、approval、configuration | 新権限の照合は維持。元 issuance と追加対象承認を再利用し、packet の有無を別認可にしない |
| 利用量・期限・工程予約 | budget、stage budget、artifact、extension | version 2 で予測と実上限を分離。既存 version 1 と明示延長の認可は維持 |
| 公開と送信不明の照合 | publication の hash/approval/delivery/reconcile | 独立した読取り監査で不要な拒否は確認されず。対象・内容・権限・送信不明の保護を維持 |
| managed Git、tool/service permission | managedGit、configuration、tools policy | 独立した読取り監査で不要な拒否は確認されず。bound Coder・正確な設定・実 tool/service 権限を維持 |

### 実施順序

1. 共通の未開始判定、通常の role 承認、任意 packet 検証を使って、run/resume の不要な差を除く。
   Coder、Reviewer、repair/re-review、artifact Reviewer の保存 issuance を対象にする。
   読取り専用の `assess-input` には全 workspace の実行停止を要求しない。
   実際の観測保存・intake 確認の入口では既存の quiescence 検査を維持する。
2. 読取り、証拠追記、結果受理、公開の各入口について、条件の守る対象と適用操作を棚卸しする。
   各条件を維持・統合・削除・適用範囲縮小に分類し、根拠と回帰事例を残す。
3. その結果から追加実装を選ぶ。設計名だけの共通 policy layer や、使われない assessor は作らない。

上表は主要な判断単位の監査であり、全 adapter 内部・全 CLI 入口・全状態の形式検証ではない。
未実装の artifact packet lifecycle、復元先 artifact 実行を黙って許可しない。
保存品質の受理は既存の review-only snapshot 証拠構造に限り、FAILED を成功へ変換するものではない。
全製品の reachable path 数は未確定であり、Task 13状態、Dispatch 9状態、Flow 3状態の
直積や ADR の表の行数を、その数として報告しない。
報告された FAILED REQUEST_CHANGES は ADR-0072 の保存回答処理の事例であり、
未開始 Dispatch の resume がその事例を解決したとは説明しない。

## 公開単位と追跡

今回の公開単位は `0.8.0` とし、新しい到達目標 `M17` を
[Milestone 38](https://github.com/hiono/taxion/milestone/38) に対応付ける。
これは今回の到達目標への新規採番であり、未採番の過去の計画を復元したという意味ではない。
既存の採番・割当・完了状態を一括変更しない。

[実装 #274](https://github.com/hiono/taxion/issues/274)、
[正式配布 #275](https://github.com/hiono/taxion/issues/275)、
[元の復旧依頼 #272](https://github.com/hiono/taxion/issues/272) の順に依存を記録する。
正式配布はソースの承認だけで完了とせず、配布処理全体と公開後の配布物の照合、
更新・切戻し手順と依頼への回答の引渡しまでを含める。
運用環境への導入や運用台帳の変更を、この公開から自動的に許可しない。
公開参照は対象の存在と結果の完了を分離する。静的な関連リンクの存在確認は、
その後のマージや完了で無効にせず、完了を主張する場合は引き続き実際の状態と時刻を照合する。
Milestone の参照も GitHub の正式なメタデータで照合する。

既公開の作業で追跡先が未設定だったものは、整理用の新規目標を作らず、
文書同期・試験版の判断監査 `#260`・`#264` を既存の `0.7.21` の公開単位、
復旧手順・保存指摘・その配布 `#266`・`#268`・`#270` を既存の `M13` の復旧目標へ対応付ける。
対応する PR も同じ追跡先とし、当時の公開版や完了判断は書き換えない。

## 互換性・移行・rollback

保存済みの契約、Dispatch、FAILED、利用量、期限、receipt を書き換えない。
新しい schema migration や operational store 操作を要求しない。
旧版で正常に利用できた起動は、新しい禁止理由が実証されない限り維持する。
旧 issuance に必要な証拠が欠ける場合は、その不足を返し、推測値で補わない。
旧 binary に戻しても、新しい版が追加した履歴を削除しない。
既存 version 1 計画を version 2 へ書き換えない。方式を変える場合は旧記録を保持し、
新しい計画の外枠を明示承認する。新計画を理解しない旧 binary でその store を更新しない。
instruction version 6 と `context-source` record を理解しない旧 binary も、
新しい記録を含む store の更新には使わない。
rollback は現在の成果を保全し、更新前バックアップを別配置で使用する。
改定前に得た未適用の preview hash は再観測する。保存済みの追記証拠は保持する。
配布、導入、既存公開 tag の変更はこの設計から推論しない。

## 検証

- packet lifecycle を使わない承認済み Coder を、再承認なしで継続できる。
- 同じ契約の Reviewer と repair/re-review の発行済み承認・予算を使える。
- artifact Reviewer は input validation を再実行せず継続できる。
- 明示的な hold、開始済みや結果のある対象を新規送信しない。
- release 済みの未開始 session へは同じ binding で再接続し、利用量をリセットしない。
- 不正 packet、configuration/契約/対象の drift、期限・予算違反を拒否する。
- preview は DB、index、worktree、承認、利用量を変更しない。
- 共通判定を通った保存対象が実際の runner で次工程へ進み、独立レビューまで完了する。
- 準備失敗後、同じ承認・Dispatch から進める。live 取消・期限切れを隠さない。
- version 2 の予測超過・未見積もり・予測 hash 変化で止めず、実期限・実使用量不明・実上限で止める。
- 成果物入力検証が予測時間を超えても全体期限内で完了でき、実期限では停止する。
- 無関係な実行・計画変更は負の指摘追記の同意を変えず、元の回答・履歴・対象変更は拒否する。

実際の検証結果、独立レビュー指摘、未対応範囲は PR に記録する。

候補ソースの全体通常回帰は2564件成功・29件省略、package は21成功・2省略である。
build・vet と変更箇所の race も成功した。原文の権限評価42ケースと、
両役割の Linux namespace 読取り専用検査は実モデルを呼ばずに行った。
公開する版の CI・配布物の照合結果は、対応する PR と Release に別途残す。
