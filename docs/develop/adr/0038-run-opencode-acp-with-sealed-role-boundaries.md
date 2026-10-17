# 38. OpenCode ACP を隔離した role 別 process で実行する

Date: 2026-10-16

## Status

Accepted

## Context

Issue #80 は OpenCode を ACP サーバーとして使い、承認済みの後続作業を実行する。
Taxion が提供する ACP サーバーとは接続の向きが逆になる。OpenCode SDK や
provider API を追加せず、既存の Task、Dispatch、設定承認、検証、独立レビューを使う。

OpenCode の設定には agent 別 permission があるが、ACP は解決済みの実行設定全体を
返さない。環境変数で設定を渡すだけでは、個人設定・managed config・project config の
追加や上書きを排除できない。Reviewer の read-only と承認 path を、prompt や
設定の自己申告だけで保証する方法は採れない。

無料モデルの公開案内と、特定の接続条件での実行可否も区別する。実際に OpenCode
1.19.0 の Big Pickle は、native tool を一律 deny にした設定で無料枠エラーを返した。
必要な native tool 定義を ask として残し、ACP client が未承認要求を cancelled に
する設定では、同じ実行ファイルとモデルによる最小 prompt が成功した。
この観測を「Zen は OpenCode からしか使えない」という一般仕様にはしない。
設定以外のサーバー側条件もあり得るため、この比較だけで原因は確定しない。

## Decision

初期実装では `runtime.provider: opencode` を opt-in とし、承認した OpenCode 1.19.0 実行ファイルと
操作者作成の server 設定を絶対パス・内容 hash で固定する。server 設定は初期対応では
`model` だけを受け付ける。Taxion は ACP のモデル変更操作を送らず、接続後に観測した
model が操作者の設定と一致することを確認する。認証・契約・quota の管理は OpenCode に任せる。
認証情報の注入は未対応であり、今回の実受入れは匿名利用できる Big Pickle に限定する。

Linux の user・mount・PID namespace と chroot を使い、capability を落とした process に
承認済み file の複製だけを渡す。host HOME、Git metadata、credential、個人設定と shell は
公開しない。Reviewer の workspace と agent 設定は OS の read-only mount で保護する。
Coder の変更は path・file 種別・host 側の変更競合を照合してから export する。
export 失敗時は成功にせず、隔離側の成果を保持してその場所をエラーに含める。

shell は native 設定でも `deny` とする。file tool のない助言 session では read 系の
定義を `ask` として残すが、ACP permission request は必ず cancelled にする。
`ask` は実行許可ではなく、client-side filesystem・terminal も実装しない。
skills は承認した hash の directory だけを複製する。コピー中に読んだ元の metadata と
内容を既存の asset hash で照合し、元の file・directory の権限を保持する。
directory の割当サイズは新規作成時に異なり得るため、コピー先に同じ asset hash は要求しない。
承認した skill の補助資料は `/config/opencode/skills/<skill-name>` にある。
file 読取り tool を許可した role に限り、その skill の subtree を native の
`external_directory` 規則で許可する。既定の外部 directory 拒否を残し、
未承認の skill、host path、tool のない role に読取りを広げない。
skill の実ロードだけで補助資料の読取り成功を推測せず、実 tool 結果で確認する。
skill を含む設定 directory は OS の read-only mount のままにする。
OpenCode の内部検索に必要な native `rg` は Linux 環境の前提とし、隔離内へ複製して
read-only mount にする。モデル接続前に起動を確認し、自動取得に依存しない。
shell、実行型 plugin、MCP、
RTK extension は未対応とし、付随検査は別途承認した host validation で行う。

明示 deadline と repair 上限を使う。Copilot の AI-credit ceiling と account guidance を
無視せず、OpenCode に指定した場合は拒否する。ACP の prompt usage は最新 assistant
message の値であり、全 tool iteration や Flow の合計とは扱わない。累積 cost と
context 使用量も別 event として保存し、未取得値からゼロ・無料・無制限を推測しない。

role prompt、保存 Dispatch の契約、隔離内の path 案内、最終結果の schema は、
生成する native agent の `prompt` に設定する。OpenCode 1.19.0 の
[ACP prompt 処理](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/acp/service.ts)は
本文を user message の parts として渡し、
[モデル入力の生成](https://github.com/anomalyco/opencode/blob/v1.19.0/packages/opencode/src/session/llm/request.ts)は
agent の `prompt` を system message に配置する。従来の user message への前置だけで
system 指示を設定したとは扱わない。`session/prompt` には今回の実行指示と予算補足だけを送り、
role 指示を二重送信しない。この設定も runtime hash に含め、旧 session へ遡及適用しない。
system 指示への配置は、モデルが実操作や正しい JSON 提出を必ず行う保証にはしない。

OpenCode 1.19.0 が送る assistant の `messageId` ごとに本文を区別し、最終メッセージだけを
正式な結果として検証する。途中の説明も delta と message 開始 event に残す。
tool 結果の `content` 配列は assistant 本文と別に保存し、空の最終応答は明示的に拒否する。
最終メッセージは JSON 全体、または末尾の単一 `json` fence だけを受け付ける。
後者の前にある説明は delta に残し、複数の block・末尾の説明・不正 JSON は拒否する。
JSON 内の必須項目、識別子、Git と実行証拠の検証は共通 controller で維持する。

停止は session/cancel の通知だけで完了にせず、所有 process の終了・reap と部分成果の
保存を確認する。期限切れで所有 process が先に終了した場合も、同じ adapter の
停止専用照合から正式な取消記録を完了する。実行用 resume は終了後も拒否する。
別 process や別 runtime の session ID への再接続は未対応とする。
旧 Dispatch、予算、usage、receipt、Git 証拠は保持し、後続 Task の設定と成果を再承認する。
共通の Coder 結果再利用では、旧 `BUDGET_LIMIT` だけでなく `SAFETY_STOP` も扱う。
実期限切れは後者で記録されるため、分類の違いだけを理由に成功済み Coder を再実行しない。
所有 runner の解放・handoff 保留・正式な成功結果・同一成果物を要求し、
Reviewer の停止後なら、その Coder に結び付いた正式 `CANCELLED` と session の停止証拠も照合する。
旧停止記録を書き換えず、後続 Task・設定・期限の新しい承認から独立 Reviewer だけを開始する。
元 Task が既に `SUPERSEDED` でも、停止・成果・出典・exact hash の照合を省略せず、
別の後続 Task を新たに承認できる。元 Task を再遷移させたり、旧承認を再利用したりしない。
この Reviewer の tools/skills は、旧 Coder の承認を転用せず、後続 Task の新しい承認から解決する。
通常の Coder→Reviewer だけでなく、保存済み Coder を参照する Flow の最初の Reviewer も、
Flow、元の成功 Coder、独立レビュー関係、Task の出典と保存済み decision が一致する場合だけ許可する。
Copilot 用の仕様送信許可を OpenCode に流用せず、`opencode` 用の exact hash 承認を要求する。

### 2026-10-17 追補：実行ファイルの更新と接続互換性

この追補は、初期実装の「OpenCode 1.19.0 との完全一致」を接続条件とする判断を置き換える。
設定内容の承認、隔離、権限、期限、結果検証は変更しない。
実行ファイルの内容固定は、通常利用と再現実験を分けて任意にする。

根拠は [ACP v1 の初期化仕様](https://agentclientprotocol.com/protocol/v1/initialization)と、
Taxion の実接続・合成 peer の検査である。仕様では `protocolVersion` は非互換変更で増える
整数であり、client と agent は合意した版の仕様に従う。agent 自身の製品版とは別の識別子で、
新しい任意 capability の追加だけでは非互換変更としない。agent の名前・版は仕様上 SHOULD
であり、Taxion も欠落だけで接続を拒否せず、報告があれば記録する。製品版だけでは必要な応答の有無も、
隔離した設定の有効性も判断できないため、完全一致を互換性判定に使わない。

実際に、共有 CLI が 1.19.0 から 1.19.3 に変わった後、旧条件ではモデル送信前に拒否された。
条件変更後は、内容 hash を照合した 1.19.3 で ACP v1、承認済み model、
隔離した `taxion` agent の一致を確認し、匿名の Big Pickle から `{"ok":true}` を受理した。
この最小応答検査では既存対応の末尾 `json` fence を指定した。別の 1.19.0 の試行では
JSON 以外の文章が混ざって拒否されており、接続互換性とモデルの出力品質は分けて判定する。
出力解析を緩めたり、無料枠やモデルの可用性を保証したりする根拠にはしない。

新しい接続条件は ACP v1、非空の session ID、承認した model と
`taxion` agent の一致である。接続結果を `acp_connection` event に記録する。
通常利用では実行ファイルの `acpCommandHash` を省略でき、設定した絶対パスの
CLI 更新だけでは再承認を求めない。起動時に読み取ったファイルを隔離内へ複製し、
その内容 hash と pin の有無を自動記録する。更新で起動済みの process は差し替わらない。
`acpCommandHash` の指定があれば従来どおり一致を要求し、更新による不一致は拒否する。
明示 pin の解除や設定内容の変更は別の設定変更として承認を要する。
保存済み承認・session・結果は書き換えない。
adapter の構成識別子も更新し、旧接続条件と新接続条件を同じ runtime hash と扱わない。

この区別は、接続に必要な互換性の自動確認と、利用者に版・hash の固定を要求することが
別の運用だからである。前者は Taxion が担当し、後者は再現性が必要な開発・評価環境で選択する。
既存の一律 pin 必須方式は、通常の CLI 更新でも手作業と作業承認を繰り返させるため撤回する。
自動記録した hash は観測値であり、そのファイルの安全性や特定版の品質の保証ではない。
実行ファイルの選択は利用者が管理する環境を信頼し、設定したパス、モデル、作業権限の境界は保つ。

版番号だけを理由に将来の v2 を拒否しない一方、v2 全体を対応済みとは宣言しない。
合成 peer では ACP v1 を保つ 1.19.0・1.19.3・2.0.0 の接続・結果受理と、
製品版の不明・欠落の許容と、ACP v2、異なる model・agent の拒否を確認する。
通常の CLI 更新で設定 hash が変わらず新しい複製を使うこと、
既存の複製を変えないこと、明示 pin の不一致と設定内容の改変の拒否も回帰に含める。
実際の v2 が native 設定、tool、通知形式を変更する場合は、その差分に対応した
adapter の変更と実受入れが別途必要になる。未知の必須操作に権限を与えたり、
別モデルや provider へ自動で切り替えたりはしない。

Copilot SDK も製品版の永久固定を互換性方針にはしない。`go.mod` / `go.sum` の固定は
ビルドを再現するためのもので、更新禁止ではない。
SDK の版、CLI の製品版、両者の通信プロトコルの版は別であり、この通信は ACP ではない。
採用 SDK の `verifyProtocolVersion` が初期接続で対応範囲を確認する。
Taxion が SDK を更新・検証し、利用者に SDK の依存更新や CLI との組合せ管理を要求しない。
[Go SDK v1.0.19 から v1.0.22 の差分](https://github.com/github/copilot-sdk/compare/go/v1.0.19...go/v1.0.22)では、
create/resume 設定項目、生成 RPC・event 型、終了時の JSON-RPC 応答処理などが変更されている。
今回 v1.0.22 へ更新し、Taxion が使う認証、session、権限、停止、usage の回帰を確認する。
モデル名の増加だけから SDK 更新が必須と判断せず、実際に必要な API・event と CLI の
互換性を確認する。依存の更新を、全モデルの判断品質や実課金の検証と取り違えない。
更新時の確認手順は [CONTRIBUTING](../../../CONTRIBUTING.md#実行ハーネスと-sdk-の更新)に置く。

## Consequences

role の file 境界と設定の固定を、OpenCode 内の permission 解決だけに依存せず確認できる。
一方、Linux の namespace とシステム command が必要になり、非対応環境は起動前に拒否する。
host network はモデル通信に使うためネットワーク隔離は行わず、通信先の制限や課金資格を
保証したとは説明しない。

通常テストは fake peer と一時 workspace で動き、実 OpenCode と外部 skills は opt-in に分ける。
最小 prompt の成功は、実文書の skill 利用・検証・独立レビューや Issue 完了の代替にはしない。
公開文書を同じ作業の仕様 source に使うため、ローカル検索に UTF-8 Markdown の段落と
行番号を追加し、会話 export の選択 user message 限定は維持する。

## Alternatives

OpenCode の permission 設定だけに依存する方法は、解決済み設定全体を ACP から
検証できないため採らない。権限を一括 allow にする方法も採らない。
Zen の API 直接接続は公式に可能な別設計だが、agent loop・tools・認証等の責務が変わるため
ACP-only の Issue #80 には追加しない。クライアント識別の偽装や別モデルへの自動切替も行わない。
