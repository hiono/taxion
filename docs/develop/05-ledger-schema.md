# 05 Ledger のデータ形式

この文書は既存 Ledger の wire を定義する。
版付き Task Graph は別ファイルとして導入し、ここにあるキーを変更しない。
現在の操作は [Task Graph](10-task-graph.md)、
分離と移行の決定は
[ADR-0022](adr/0022-sqlite-atomic-workspace-state.md) を参照する。

以下の JSON 形式は、Go の core・CLI・API（`internal/core`、`internal/server`）と TypeScript の表示処理（`web/`）の間で固定した契約である。M2（#9〜#11）の終了時、2026-10-10 に固定した。

## キーの変更条件

キーを追加・削除・改名する場合は、次の全項目が必要。

1. この文書を置換・修正する ADR。
2. この文書の更新。
3. `internal/core/wire_test.go` の更新。CI でキー集合の完全一致を確認する。
4. `web/src/types.ts` の同時更新。

データ形式は #7 まで小文字 tag への変更などが続いた。固定は、M3・M4 の作業と ADR-0012 の再検討の前提である。

## Ledger object の最上位

全キーを必ず含める。値は配列、未初期化 slice は `null`。挿入順にも意味がある。

| キー           | 要素 |
| ------------- | ------- |
| `tasks`       | Task |
| `dispatches`  | Dispatch |
| `evidence`    | Evidence |
| `checkpoints` | Checkpoint |

## Task

| キー         | 型   |
| ----------- | ------ |
| `id`        | string |
| `title`     | string |
| `repo`      | string |
| `createdAt` | string (RFC3339Nano, UTC) |

## Dispatch

| キー         | 型   |
| ----------- | ------ |
| `id`        | string |
| `taskId`    | string |
| `repo`      | string |
| `active`    | boolean |
| `createdAt` | string (RFC3339Nano, UTC) |

## Evidence

| キー         | 型   |
| ----------- | ------ |
| `sha`       | string（呼出し元が指定する Coder の出力 SHA。検証の制約は後述） |
| `taskId`    | string |
| `createdAt` | string (RFC3339Nano, UTC) |

## Checkpoint

| キー    | 型    |
| ------ | ------- |
| `name` | string  |
| `done` | boolean |

## 命名規則

キーは、小文字で始まる camelCase（`taskId`、`createdAt`）とする。新規キーに別の命名規則を使わない。

## HTTP API

`taxion serve` が提供し、M4 から `web/` 経由で読み書きする。

読取りの操作と応答は次のとおり。

- `GET /api/ledger` は、上記 Ledger object 全体を返す。
- `GET /api/tasks[?repo=]` は Task 配列を返す。
- `GET /api/tasks/plan?repo=REPOSITORY` は、契約、作業指示、依存状態、Memory、Dispatch の証拠から再構成した後続計画を返す。SQLite Task Graph のみ。repo の明示がなければ 400、Graph が使えないか未知の repository なら 409、記録が読めなければ 500。
- `GET /api/dispatches[?repo=]` は Dispatch 配列を返す。
- `GET /api/dispatches/{id}/instruction` は、変更不可の Dispatch instruction と `instructionHash` を返す。SQLite workspace のみ。未知の Dispatch は 404。
- `GET /api/evidence[?task=]` は Evidence 配列を返す。
- `/api/ledger` と `/api/evidence` は GET のみで、他の method は 405。`/api/tasks` と `/api/dispatches` は、後述の POST も受け付ける。未知の path は 404、読めない Ledger は 500 とし、処理を許可しない。

書込み（v1）は M4 で追加した。当初の範囲は認証なしの localhost のみだった。現在の公開 mode と token の条件は[リモート接続](../userguide/06-remote-access.md)に記載している。

- `POST /api/tasks` の `{title, repo}` は 201 と Task を返す。title・repo の欠落は 400（REP-001）。
- `POST /api/dispatches` の `{task, repo}` は 201 と Dispatch を返す。Task Graph がある場合、任意の `{relationship, relatedDispatchId, relationshipReason}` で後続 Dispatch を識別する。初回 Dispatch は relationship を使わない。未知の task は 404、他の Dispatch が active なら 409（DSP-001）、承認済み Memory 述語の不一致は 409（MEM-001）、Memory policy の読取り不能・形式不正は 500。task・repo の欠落は 400。述語の意味は [Memory](../userguide/08-memory-model.md)を参照する。Ledger のキーは変更しない。
- `POST /api/reviews` の `{task, repo, sha}` は 201 と Evidence を返す。空の sha は 400（SHA-001）、task・repo の欠落も 400。
- request body は CLI と同じ検査を使い、Web は POST request を変更せずに渡す。書込みのための Ledger キー追加はなく、固定した形式を保つ。

`review` は、指定された SHA を証拠として記録する。Reviewer の実行や、SHA が Git 内に存在することの確認はしない。現在の検査は空の SHA を拒否する。呼出し元が意図した出力 revision を正確に指定する必要がある。
