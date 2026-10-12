# 13 — 取得済み仕様の入力と利用許可

仕様入力は、必要な本文と出典・該当箇所・版・取得日時・用途をまとめた引用スナップショットである。
取得した資料を人が確認し、Task の `specifications` に登録する。
Coder と Reviewer は同じスナップショットと acceptance を受け取る。
検索 connector の採択と接続は #59・#60 で進める。

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
Task の登録・改訂は [Task Graph reference](10-task-graph.md) の状態と承認手順に従う。

## 版の保持と不足の検出

Dispatch は承認と本文を含む仕様を複写し、instruction hash に含める。
レビュー進行は、Coder・Reviewer・修正 Coder 間で同じスナップショットを要求する。
Task の仕様を変更した場合は、更新した契約の Human 承認を求める。

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
公開操作自体の本文・接続先・主体の承認は [実行記録](11-execution-records.md) に従う。

## ローカル自動検索

Human は、公開・合成資料をローカルで自動検索し、承認した合成本文を
Coder・Reviewer に渡す方式を採択した。判断は [ADR-0025](adr/0025-approved-local-specification-search-and-tools.md) に記録する。
M365 の接続は、組織の認証・license・資料利用許可を確認する
[#77](https://github.com/hiono/taxion/issues/77) として BLOCKED を保持する。

検索 scope は、PDF・DOCX・PPTX の exact asset、題名、出典、版、`public` または `synthetic` の
閲覧範囲、private evidence directory を持つ。検索は大文字小文字を区別しない literal AND 方式で、
該当 paragraph・slide・page を引用スナップショットに変換する。
ローカル管理者の承認記録が検索権限を定義し、`copilot` と `publication` が用途別の送信許可を定義する。

DOCX/PPTX は Go の ZIP/XML reader、PDF は承認した Python と `pdfminer` を使う。
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

`taxion specification scope-hash --scope scope.json` の hash を Human が確認し、
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
Task の configuration・認証・repository access と、positive な `--max-tokens`、
`--timeout`、`--max-ai-credits` を必要とする。
host は exact query・root session・資料の Copilot 送信許可を確認し、取得した引用を保存する。
session ID と観測 usage は private evidence に残す。
token 上限は usage event を受けた時点の停止条件、credit 上限は SDK の soft cap として扱う。
請求額の hard cap として使う場合は、provider 側の制御を別に確認する。

## 実 SDK の受入れ状況

公開・合成資料による無料回帰では、実 PDF parser・stdio MCP・native plugin discovery・RTK、
検索から Coder と独立 Reviewer への同じ引用の受け渡しを確認した。
有料受入れは env opt-in に分離し、通常 CI は無料回帰を実行する。

実 SDK では、Orchestrator が検索を呼び、別 session の Coder が同じ引用を使って成功結果を保存した。
独立 Reviewer は承認した追加 session で起動したが、観測 token 上限に達して CANCELLED となった。
使用した session は 3 件、観測 input/output tokens の合計は 34,471 である。
Reviewer の成功と APPROVE の受理は未達であり、#60 と M14 を open に保持する。
追加の有料実行は Human の再承認を必要とする。
