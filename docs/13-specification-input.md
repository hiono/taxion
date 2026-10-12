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

## 検索接続の状態

Human は、仕様入力と Memory 移行を先行し、検索 connector は BLOCKED として保持する方式を採択した。
M365 API の接続許可は見送られた。
再開時は検索方式・認証・組織許可・送信範囲を採択し、許可された実環境で受入れを行う。
M14 の検索完了条件は、会話から検索・出典確認・役割への自動受け渡しまでの実行である。

OfficeCLI の [公式 README](https://github.com/iOfficeAI/OfficeCLI) では、
DOCX・PPTX・XLSX の構造付き読取りと MCP 接続を確認した。
MCP は編集・削除も提供するため、採択する接続では役割に合わせた読取り tool を選別する必要がある。
PDF 読取りの reader と、OfficeCLI の実行環境の検証は接続方式の採択後に実施する。
