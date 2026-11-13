# 認証付き操作を信頼された MCP へ委ねる

認証付きの外部操作には、Task の `taxion.tools.v1` 承認へ保存した固定 MCP を使えます。
対応するのは Copilot の操作専用役割です。通常の coding role に秘密を追加する機能ではありません。
モデルへ秘密を貼り付けず、信頼された host の専用環境変数に置きます。
実 credential を試験や公開ログに使わないでください。

## 承認する内容

既存の役割別 policy の `mcp` binding に、次の3項目を一組で追加します。

```json
{
  "name": "mcp_send_approved_message",
  "command": {"path": "/trusted/mail-service", "hash": "sha256:<approved executable hash>"},
  "args": [],
  "tool": "send_message",
  "arguments": {"recipient": "approved@example.invalid", "body": "Approved message"},
  "credentialRef": "env:TAXION_BROKER_MAIL",
  "operationId": "approved-message-001",
  "expiresAt": "2026-12-01T10:00:00+09:00"
}
```

これは binding の形を示す例であり、実行可能な承認ではありません。
実際の実行ファイルと入力は `taxion tools hash --path PATH` で固定し、
Task・役割・利用者・出典、専用 `evidenceDirectory` を含む承認を正式な Task 手順で保存します。
メールの宛先と本文、支払いの相手と金額、削除の対象など、
副作用を決める内容はすべて `arguments`、固定引数、承認済み入力に含めて確認します。
引数の変更は拒否され、別の承認が必要となります。参照名自体は秘密ではありません。

`credentialRef` は `env:TAXION_BROKER_` に大文字から始まる英数字・underscore の名前を続けます。
host がその変数を用意し、呼出し直前に存在・形式を検査します。
値を `args`・`arguments`・Task・skill・設定ファイルへ転記しないでください。
同じ役割に shell・file tool・skill・plugin・RTK・他の管理操作・通常 MCP を混在させられません。
現在の Task と session の承認照合がない接続も拒否します。
このため、Controller の実行用 callback を持たない独立の adapter 構築や
現在の `doctor --dispatch` で、この役割を接続可能とは判定しません。
保存した承認の内容は既存の Task Graph 表示で確認してください。

## 実行結果と停止

MCP には承認した request と専用 credential だけを渡し、その他の host 環境変数を継承しません。
専用 credential は Copilot 子プロセスの環境からも除外します。
応答本文・stdout・stderr は返さず保存もしません。成功時は固定の完了通知だけを返します。
内容を必要とする検索・読取り用 MCP は、従来の認証なし経路とは別に設計してください。

`operationId` は同じ証拠 directory 内で一回限りです。
起動前に試行記録を永続化し、プロセス起動失敗も含めて同じ ID の再呼出しを拒否します。
期限切れ・取消・不明な結果を自動再送しません。
外部操作が既に成立している可能性があるため、記録を消す、directory を替える、
ID だけを替える操作を復旧手順にしないでください。
外部状態を確認し、必要なら新しい内容とリスクを明示して再承認します。
directory の同期を提供しない filesystem では副作用を始めず拒否します。

## 保護範囲と未対応

固定 MCP は信頼する host code です。認証の用途と通信先、設定の読取り先、
応答だけでなく外部へ送る情報も、人間が実装と入力を確認して承認します。
信頼していない MCP に credential を渡す用途には使えません。
同一 UID の悪意ある process や host の侵害から秘密を隔離する OS sandbox ではありません。
Copilot 自身の runtime 認証・HOME の完全隔離も保証しません。
OpenCode の MCP、組織アプリの credential 仲介、OS 隔離はこの実装の対応範囲外です。

合成試験は、対応する境界で秘密が継承・返却・保存されないことと固定操作の拒否を確認します。
実サービスの認可や、すべての prompt injection への耐性を実証したものではありません。
Issue #229 の未対応構成は完了と扱わず、[ADR-0070](../develop/adr/0070-mediate-exact-credentialed-operations.md)に残しています。
