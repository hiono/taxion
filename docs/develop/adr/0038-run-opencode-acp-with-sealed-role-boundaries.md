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

## Decision

`runtime.provider: opencode` を opt-in とし、承認した OpenCode 1.19.0 実行ファイルと
操作者作成の server 設定を絶対パス・内容 hash で固定する。server 設定は初期対応では
`model` だけを受け付ける。Taxion は ACP のモデル変更操作を送らず、接続後に観測した
model が操作者の設定と一致することを確認する。認証・契約・quota の管理は OpenCode に任せる。
認証情報の注入は未対応であり、今回の実受入れは匿名利用できる Big Pickle に限定する。

Linux の user・mount・PID namespace と chroot を使い、capability を落とした process に
承認済み file の複製だけを渡す。host HOME、Git metadata、credential、個人設定と shell は
公開しない。Reviewer の workspace と agent 設定は OS の read-only mount で保護する。
Coder の変更は path・file 種別・host 側の変更競合を照合してから export する。
export 失敗時は成功にせず、隔離側の成果を保持してその場所をエラーに含める。

native tool の未承認操作は ACP permission request を必ず cancelled にし、
client-side filesystem・terminal を実装しない。tool 定義を残す ask は実行許可ではない。
skills は承認した hash の directory だけを複製する。shell、実行型 plugin、MCP、
RTK extension は未対応とし、付随検査は別途承認した host validation で行う。

明示 deadline と repair 上限を使う。Copilot の AI-credit ceiling と account guidance を
無視せず、OpenCode に指定した場合は拒否する。ACP の prompt usage は最新 assistant
message の値であり、全 tool iteration や Flow の合計とは扱わない。累積 cost と
context 使用量も別 event として保存し、未取得値からゼロ・無料・無制限を推測しない。

停止は session/cancel の通知だけで完了にせず、所有 process の終了・reap と部分成果の
保存を確認する。別 process や別 runtime の session ID への再接続は未対応とする。
旧 Dispatch、予算、usage、receipt、Git 証拠は保持し、後続 Task の設定と成果を再承認する。
Copilot 用の仕様送信許可を OpenCode に流用せず、`opencode` 用の exact hash 承認を要求する。

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
