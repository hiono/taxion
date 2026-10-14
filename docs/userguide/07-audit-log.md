# 07 監査ログ

`serve` API は、変更を試みた操作と access 拒否を追記専用の監査ログに記録する。M5-2 で導入し、M8-D で `who`、M8-E で `reason` と 429、M8-G で `prev` の hash chain を追加した。既定の保存先は Ledger と同じ directory の `.taxion/audit.jsonl`。`serve --audit PATH` で変更し、`serve --audit ""` で無効にする。

## 記録する内容

一行ごとに一つの JSON object を記録する。

```json
{"ts":"2026-10-10T...Z","method":"POST","path":"/api/tasks","repo":"demo","who":"operator-1","status":201,"authed":true}
```

| field | 内容 |
| --- | --- |
| `ts` | 応答の送信日時。UTC、RFC3339Nano |
| `method` | HTTP method |
| `path` | request path のみ。query string は含めない |
| `repo` | POST body に明示された repo の識別情報。指定がある場合だけ記録 |
| `who` | `X-Operator` で自己申告した操作者のラベル。指定がある場合だけ記録（M8-D） |
| `status` | 応答 code。検査による拒否（400・404・409）と lockout（429）も含む |
| `authed` | 有効な token を提示した request かどうか |
| `reason` | 拒否の理由。429 だけに `"lockout"` を記録（M8-E） |
| `prev` | 直前の行の bytes 全体に対する16進表記の SHA-256。最初の行では省略（M8-G） |

変更を試みた全操作（POST・PUT・PATCH・DELETE、結果を問わない）、全認証拒否（401・403、method を問わない）、全 rate-limit lockout（429、method を問わない）を記録する。成功した読取りは記録しない。

## 認証失敗による lockout

M8-E の policy は固定で、変更 flag はない。同一 IP から1分以内に5回 login に失敗すると、その IP を5分間 lockout する。期間中は正しい token を使っても 429 を返し、ログに `"reason":"lockout"` を記録する。認証が一回成功すると、その IP の counter をリセットし、入力間違いを累積させない。

識別に使うのは接続の `RemoteAddr` host であり、`X-Forwarded-For` は信頼しない。

## 記録しない内容

token、`Authorization` header、request body（Task title、SHA）、query string は記録しない。使った credential を記録せず、操作と結果を記録する。

`who` は自己申告であり、本人確認の証拠ではない。共有 token を持つ呼出し元は、任意の `X-Operator` 値を送信できる。ラベルで呼出し元を区別するが、その identity を認証しない。操作者別の本人確認は、延期した IdP 作業に属する（ADR-0016）。

## 保存先の障害

`serve` は起動時に `CheckAudit` で保存先を確認し、書込み不能なら起動を拒否する。稼働中の書込み失敗は server の標準errorに報告する。行が欠けた場合、操作者は監査記録の欠落として扱う必要がある。

## hash chain の照合

M8-G の各行は、`prev` に直前の行の bytes 全体の SHA-256 を16進表記で保存する。最初の行は `prev` を省略する。server 再起動時は既存の最終行から chain を続け、別の先頭行を作らない。

```sh
taxion audit verify [--audit PATH]   # default: next to the ledger
```

終了 code 0 は `chain OK` を表示する。終了 code 1 は最初の不整合行を示す。その行以降を信頼できないものとして扱い、追記を続ける前に調査する。

保存 hash が一致しなくなった場合、対象行の変更、挿入、削除、順序変更を検出する。ただし、ログ自体の真正性は確認しない。ファイル全体を置き換えて hash を再計算すると、内部的に整合した別の chain を作れる。末尾の切詰めも検出しない。末尾を削った chain は、短い有効な chain のままになる。これらの変更も検出する必要がある場合は、独立して信頼できる複製・hash を保ち、Taxion の外で行数を監視する。

`prev` のない旧行は、先頭に連続する場合だけ有効な legacy 行として扱う。M8-G 前のログも更新後に照合でき、最初の chain 行は最後の legacy 行に連結する。chain 行の後に legacy 行がある場合は欠落と判定し、照合を失敗させる。ローテーションは新しい先頭行から始めるため、過去の部分を照合できるよう旧ファイルを保存する。

## 保存とローテーション

- ログは追記専用。Taxion 自体はローテーション、切詰め、削除を行わない。監査履歴の削除は、ログで保持する証拠を失わせる。
- 作成時の file mode は `0600`。Ledger とともに backup する。
- ローテーション時は `serve` を停止し、ログを保存してから、空または新しい audit path で再起動する。server は開いた file handle と最後の hash を保持するため、稼働中の移動・切詰めではその状態をリセットできない。ローテーションは Taxion の外で記録し、旧ログを保存する。
- Ledger のある世代を保持する間は、対応する監査行も保持する。
