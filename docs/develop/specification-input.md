# 取得済み仕様の入力と利用許可

仕様入力は、必要な本文と出典・該当箇所・版・取得日時・用途をまとめた引用スナップショットである。
取得した資料を人が確認し、Task の `specifications` に登録する。
Coder と Reviewer は同じスナップショットと acceptance を受け取る。
ローカル仕様検索 connector は M14 (#58–#61) で採択・実装し、受入れを完了した。

## 入力と承認

各スナップショットは次の情報を保持する。

| フィールド | 内容 |
| --- | --- |
| `id`・`title` | Task 内で一意の資料 ID と題名 |
| `source`・`location` | 出典の参照と節・ページ・スライドなどの該当箇所 |
| `version`・`retrievedAt` | 確認した版と取得日時 |
| `body` | 作業に必要な引用本文。上限は 1 MiB |
| `usage` | 許可を求める資料の用途 |
| `localPath`・`documentHash` | 任意のローカル原本の絶対パスと `sha256:` 付きの全文 hash。両方を指定する |
| `ingestion` | ローカル取り込みの承認 |
| `copilot` | GitHub Copilot に資料を送信する承認 |
| `publication` | 資料を使った Task の外部記録を公開する承認 |

承認ごとに `actor`、`source`、`hash` を指定する。
`hash` は承認フィールドを除いたスナップショット全体に対応する。
次の JSON を `spec.json` に保存し、`taxion specification hash --file spec.json` で確認する。

```json
{
  "id": "service-v1",
  "title": "合成資料のサービス契約",
  "source": "https://example.org/service",
  "location": "第2節",
  "version": "v1",
  "retrievedAt": "2026-01-01T00:00:00Z",
  "body": "受付成功時の応答は 202 とする。",
  "usage": "既存 acceptance に沿った実装と独立レビュー"
}
```

確認した hash と判断の出典を、用途別の承認に記録する。
ローカル取り込みには `ingestion`、役割への送信には `copilot` を用意する。
外部公開を許可する場合は `publication` も記録する。

```json
{
  "actor": "human",
  "source": "human:reviewed-copilot-transfer",
  "hash": "sha256:<確認したhash>"
}
```

資料の閲覧許可と、それぞれの利用許可は個別に確認する。
承認は信頼されたローカル管理者の判断記録として扱い、
本人認証は M15 の受入れ範囲に置く。
Task の登録・改訂は [Task Graph reference](task-graph.md) の状態と承認手順に従う。

## 版の保持と不足の検出

Dispatch は承認と本文を含む仕様を複写し、instruction hash に含める。
レビュー進行は、Coder・Reviewer・修正 Coder 間で同じスナップショットを要求する。
Task の仕様を変更した場合は、更新した契約の利用者の承認を求める。

ローカル原本を指定した資料は、指示の生成、実行、質問への送信、外部公開で全文 hash を照合する。
原本は 256 MiB までの通常ファイルを扱う。
原本の変更・削除や利用許可の不足を検出すると、処理は理由付きで停止する。
保存済みの Task・Dispatch は、原本が参照できない状態でも復旧のために読み出せる。
同じ版の資料に後から用途別の承認を追加した場合は、
その hash に一致する過去の引用にも現在の承認を適用する。

URL を出典とする手動引用は、登録者が取得した本文・版・取得日時を保持する。
URL の再取得と最新版の判定は、採択する connector が担当する。
PDF・DOCX・PPTX も取得済み本文と該当箇所を登録する形式で扱い、
ローカル原本を指定すればファイル全体の変更を検出する。
本文の抽出結果と原本の一致は登録者が確認する。

## 指示と引用、公開の扱い

指示では `QUOTED SPECIFICATIONS` に資料を JSON として引用し、
Task の authorization・scope・acceptance を別のフィールドに保持する。
資料に含まれる命令文は、出典を持つ評価対象として扱う。
実行権限は引き続き controller と役割別の tool 許可で検査する。
この分離がモデルの解釈を完全に保証するものではないため、Reviewer は結果と証拠を確認する。

外部記録は、その Task の現在・過去の仕様と引用元 Dispatch の利用許可も確認する。
非公開仕様を利用した Task の公開には、引用元の `publication` 承認を要求する。
秘密検査に合格した短い報告にも、この条件を適用する。
公開提案の承認と実際の送信で再確認し、送信待ちに原本が失われた場合も停止する。
公開操作自体の本文・接続先・主体の承認は [実行記録](../userguide/execution-records.md) に従う。

## ローカル自動検索

利用者は、承認したローカル資料から本文を検索し、出典付き引用を
Coder・Reviewer に渡す方式を採択した。公開・合成資料の判断は
[ADR-0025](adr/0025-approved-local-specification-search-and-tools.md)、
利用者が明示した会話要求の扱いは
[ADR-0035](adr/0035-use-selected-user-chat-messages-as-requirement-sources.md) に記録する。
M365 の接続は、組織の認証・license・資料利用許可を確認する
[#77](https://github.com/hiono/taxion/issues/77) の条件付き後続拡張として扱う。
組織が利用可能な環境と許可範囲を提示した場合に接続方式を評価する。

検索 scope は、Markdown・PDF・DOCX・PPTX、または M365 Copilot 会話 export `.json.md` の exact asset、
題名、出典、版、閲覧範囲、private evidence directory を持つ。会話 export は `private` に分類し、
scope に含めた `messageIds` のうち、時刻付きで user が書いた本文だけを検索する。
assistant の返答、未選択メッセージ、export header は結果に含めない。
対象 message ID は scope hash に含み、原本は hash で固定する。
検索は大文字小文字を区別しない literal AND 方式で、
該当 paragraph・slide・page・選択済み user message を引用スナップショットに変換する。
ローカル管理者の承認記録が検索権限を定義し、`copilot`、`opencode`、`publication` が用途別の送信許可を定義する。
会話の取り込み、各 runtime への送信、外部公開は別々の用途であり、
前の承認から後の用途を推定しない。

DOCX/PPTX は Go の ZIP/XML reader、PDF は承認した Python と `pdfminer` を使う。
通常の `.md` は UTF-8 の段落単位で検索し、原本の行番号を保持する。段落は最大 1 MiB とし、
外部の Markdown parser や shell は起動しない。`.json.md` を通常 Markdown として読むことはなく、
選択した user message だけを検索する既存の制約を維持する。
scope には 1–128 原本、原本は各 32 MiB、query は 512 bytes、結果は 64 引用までを指定できる。
Office XML は part ごとに 1 MiB、合計 8 MiB を上限とし、PDF は最大 4096 pages・1 MiB 本文、
512 MiB memory・10 秒 CPU・15 秒実行時間で読む。
画像だけの PDF は、承認済み OCR を追加する後続作業として扱う。
PDF parser の stdout・stderr・exit status は private evidence に保持する。

### Scope と承認

次の JSON を `scope.json` に保存する。各原本の asset hash は
`taxion tools hash --path /absolute/path/service.docx` で取得する。
PDF を含む scope は、同じ asset 形式の `pdfPython` と `pdfLibrary` も指定する。
`pdfLibrary.path` は承認した `pdfminer` package directory を指す。

```json
{
  "documents": [{
    "asset": {"path": "/absolute/path/service.docx", "hash": "sha256:<asset hash>"},
    "title": "合成資料のサービス契約",
    "source": "synthetic:service",
    "version": "v1",
    "access": "synthetic"
  }],
  "evidenceDirectory": "/absolute/private/search-evidence"
}
```

M365 Copilot の会話 export を使う場合は、原本を `private` として扱い、
検索対象の user-authored message ID だけを `messageIds` に指定する。
`source` は会話の識別情報を含まない参照名で足りる。
`.json.md` 原本の hash は全文を固定するが、検索結果には選択した user 本文だけが入る。
message ID を変えた場合は別の scope hash になる。

```json
{
  "documents": [{
    "asset": {"path": "/absolute/private/conversation.json.md", "hash": "sha256:<asset hash>"},
    "title": "Day 1 の利用者要求",
    "source": "private:conversation/day-1",
    "version": "export:2026-10-09T11:54:58.153Z",
    "access": "private",
    "messageIds": ["<selected-user-message-id>"]
  }],
  "evidenceDirectory": "/absolute/private/search-evidence"
}
```

`taxion specification scope-hash --scope scope.json` の hash を利用者が確認し、
同じ JSON に `approval` を追加する。

```json
{
  "actor": "human",
  "source": "human:approved-synthetic-search",
  "hash": "sha256:<scope hash>",
  "copilot": true,
  "publication": false
}
```

この承認から、各引用の exact hash に結び付いた用途別承認を作成する。
OpenCode に送信する場合は `opencode: true` を指定する。`copilot: true` だけでは
OpenCode に送信できず、逆も同様とする。設定未確定の Dispatch 発行時には少なくとも一つの
runtime 用許可を要求し、実際の送信直前には選択した provider の許可を再照合する。
公開許可は、資料を使った Task の外部記録を許可するときに選択する。
検索前後に原本を照合し、版変更・参照不能・権限不足は error、
一致する結果がない場合は空配列を返す。取得失敗時は部分的な結果を Task に保存しない。

### CLI と Orchestrator

```bash
taxion specification search --scope scope.json --query "受付 202"
taxion conversation search --task <id> --scope scope.json --question "受付 202"
taxion specification serve --scope scope.json
```

`specification search` は metadata を表示する。
`conversation search` は取得した引用を、編集可能な Task へ手動コピーなしで添付する。
Console に表示するのは ID・hash・該当箇所・版であり、本文は private なローカル状態に保持する。
`serve` は read-only な stdio MCP の `taxion_search` を公開する。

`conversation search --sdk` は、Orchestrator が host-owned `taxion_search` を呼ぶ明示的な有料操作である。
Task の configuration・認証・repository access と、`--timeout`、
`--max-ai-credits` を必要とする。
host は exact query・root session・資料の Copilot 送信許可を確認し、取得した引用を保存する。
session ID と観測 usage は private evidence に残す。
token 利用量は各 call で報告するが、停止条件にはしない。
account 残高・割合は同じ単位の allowance を確認できない限り unavailable とする。
credit ceiling は SDK の session soft ceiling であり、請求額の hard cap ではない。

## 実 SDK の受入れ状況

公開・合成資料による無料回帰では、実 PDF parser・stdio MCP・native plugin discovery・RTK、
検索から Coder と独立 Reviewer への同じ引用の受け渡しを確認した。
有料受入れは env opt-in に分離し、通常 CI は無料回帰を実行する。

実 SDK では、Orchestrator が検索を呼び、別 session の Coder が同じ引用を使って成功結果を保存した。
その後の再承認済み Reviewer は、保存済みの Coder SHA・同じ引用・解放済み worktree を照合して
`APPROVE` を返し、#60 の独立 Reviewer 受入れを完了した。追加の有料実行は新しい利用者の承認を必要とする。
