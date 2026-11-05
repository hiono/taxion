# Ledger と workspace のデータ形式

現在の保存単位は SQLite の workspace である。`store.Workspace` は基本記録の
`Ledger`、実行契約と工程の `Graph`、実行・復旧・公開証拠の `Records` を同じ transaction で扱う。
Task Graph が別ファイルに保存される構成ではない。
全体の責務は[アーキテクチャ](architecture.md)、状態遷移は [Task Graph](task-graph.md)を参照する。

## 現在の保存構造

`internal/store/sqlite.go` が接続と transaction、`documents.go` が記録単位の保存を担当する。
`workspace_documents` は area・field・position ごとに内容と hash を持ち、
配列は要素単位、scalar と空値は position `-1` で保存する。
`workspace_strings` は長い共通文字列を共有し、保存内容は圧縮する。
形式情報は `workspace_format`、旧一括 JSON からの移行は store が扱う。

`LoadWorkspace` は一貫した状態を読み、`UpdateWorkspace` は変更前後の整合と
追記履歴を検証して保存する。SQL を直接編集して承認や停止条件を変更する操作は提供しない。
`Open` は作成・移行を行い、`OpenReadOnly` は既存 DB を作成・移行せず読む。
Memory は別の DB、成果物と保持 ref は Git に存在する。
一ファイル移送は [利用者ガイド](../userguide/user-guide.md#別マシンへの一ファイル移送)に従う。

Reviewer の結果確定は `Records.Events` の `review-finalization` receipt に、
検証済みと runtime 終了済みの二段階を追記する。結果・指示・設定・session を束縛し、
利用量は検証済み段階で保存時点の prefix、終了済み段階で全記録を照合する。
`saved-turn-observed` は復旧要求、native 履歴 hash、回答・turn 完了時刻と shutdown 集計を保存する。
利用量の不足分だけを Graph と budget audit に加算し、既存の receipt と消費を保持する。
これらは実行観測であり、正式結果の `verified-result` の代用にはならない。

`Records.FailedReviewSupplements` は、正式失敗した snapshot Reviewer の native 回答を
負の指摘として追記する任意の配列である。`dispatchHash` は元 Dispatch 全体を固定し、
`receiptHash` はその Dispatch の元 runtime receipt 列を固定する。
`historyHash` は native 履歴、`previewHash` は承認した要求と観測対象を束縛する。
回答・元期限内完了時刻・shutdown 時刻と集計・申請者・承認出所・停止証拠・記録時点を保存する。
回答から shutdown までの順序、元の正式失敗以前であることと FAILED receipt の内容は、
読取り・更新時にも照合する。
回答のモデル申告 `status` を正式 Dispatch 状態や品質結果として使わない。
追記の削除・変更と元 Dispatch の変更を拒否し、既存 receipt・Graph・利用量は書き換えない。
操作と対象条件は [ADR-0072](adr/0072-append-negative-review-after-formal-failure.md) を参照する。
新 field を知らない旧版へ渡すための記録削除は提供しない。

以下の Ledger JSON は、CLI・HTTP API と Web 表示の互換契約である。
Graph や Records の新しい field を、ここに定義した legacy Ledger のキー追加とは混同しない。

### 入力観測の追記記録

`Records.InputObservations` は取得した現在の入力を保存する任意の配列で、
既存の正式結果を変更しない。記録は `version:1` と内容の `hash`、
読取り用の `contract` と `configuration`、出所の作業・工程・指示・結果の参照、
`target`、旧方針への `priorPolicy`、取得者・理由・証拠・取得時点を持つ。
内容のハッシュでは、自分自身の `hash` と `observedAt` を除外する。
取得時点の変更も更新時の不変検査で拒否する。

更新前に正規化した内容を保存し、削除・置換・入れ子配列の書換えを拒否する。
読取り・更新では内容、元の正式結果と入力検証記録、対象識別を検査する。
取得範囲を示す読取り用契約は実行の承認ではなく、
旧契約の承認識別子や判断を引き継がない。
未記録の作者、検証、原因、利用量は未知として保持する。

後続の `input-observation:TASK_ID` 判断は、参照ハッシュに加えて
具体的な阻害理由、保存済み検証出力、未確認事項を含む。
確認時に保存記録から再生成した内容と照合し、担当への実際の指示にも残す。
観測時と契約確定時の判定は既存の制御処理で行い、判定専用の有料モデルは使わない。
Git の保持参照は `observation:HASH` を元に導出し、一ファイル移送へ対象の木と
参照を含める。復元時は既存のパス対応表を使い、元記録を書き換えない。
新しい記録を知らない旧版は安全に読めないため、形式を削って戻す移行は提供しない。

## キーの変更条件

この文書の固定 wire は legacy Ledger の Task／Dispatch を対象とする。
レビュー継続はこの wire にキーを加えず、workspace の既存追記監査
`Records.DispatchReconciliations` の任意の `kind` と、Task decision の内容を使う。
`kind` 省略は従来の未開始取消、`executed-artifact-review-continuation-v1` は
正式停止 receipt のある実行済み成果物 Reviewer の継続判断を表す。
後者は旧 Flow／指示／session／結果／利用量を変更せず、
元指示と一致する Controller 停止 receipt を検査する。未知の種別は拒否する。
同じ監査の任意の `deadlineRecontract` は期限切れ後の後続契約を表す。
`previousDeadline` は保存元の期限、`deadline` は承認した後続期限、
`reason` は変更理由である。元期限の完全一致と監査時点でも未来の新期限を検証する。
実行済み継続で監査時刻が旧期限以後なら、この明示記録を必須にする。
既存の監査を追記後に変更せず、legacy Ledger の固定 wire と DB schema は維持する。
`reviewInput` の内容は `kind:taxion.review-input.v1` の decision として保持する。
稼働中の延長は `ReviewFlow.extensions` と `Records.Budgets` の
`RESOURCE_EXTENDED` に同じ追記判断を保存する。
各判断は basis/hash、旧・新 deadline と native 上限、actor/source/reason、承認適用時刻を保持する。
元期限・元予算・工程計画は変えず、有効値だけを最後の判断から求める。
新記録の読取り時も全 prefix を照合し、削除・変更・監査のない延長を拒否する。
根拠と制約は [ADR-0058](adr/0058-recover-unstarted-artifact-review.md)、
実行契約は [Runtime 契約](runtime-contract.md#保存結果と-checkpoint-の復旧)を参照する。
新しい監査を旧 binary で操作せず、戻す場合は適用前の整合 backup を別配置で使う。

キーを追加・削除・改名する場合は、次の全項目が必要。

1. この文書を置換・修正する ADR。
2. この文書の更新。
3. `internal/core/wire_test.go` の更新。CI でキー集合の完全一致を確認する。
4. `web/src/types.ts` の同時更新。

保存構造の判断は [ADR-0022](adr/0022-sqlite-atomic-workspace-state.md)と
[ADR-0033](adr/0033-reference-results-and-persist-workspace-records-incrementally.md)を参照する。

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

書込み（v1）は M4 で追加した。当初の範囲は認証なしの localhost のみだった。現在の公開 mode と token の条件は[リモート接続](../userguide/remote-access.md)に記載している。

- `POST /api/tasks` の `{title, repo}` は 201 と Task を返す。title・repo の欠落は 400（REP-001）。
- `POST /api/dispatches` の `{task, repo}` は 201 と Dispatch を返す。Task Graph がある場合、任意の `{relationship, relatedDispatchId, relationshipReason}` で後続 Dispatch を識別する。初回 Dispatch は relationship を使わない。未知の task は 404、他の Dispatch が active なら 409（DSP-001）、承認済み Memory 述語の不一致は 409（MEM-001）、Memory policy の読取り不能・形式不正は 500。task・repo の欠落は 400。述語の意味は [Memory](../userguide/memory-model.md)を参照する。Ledger のキーは変更しない。
- `POST /api/reviews` の `{task, repo, sha}` は 201 と Evidence を返す。空の sha は 400（SHA-001）、task・repo の欠落も 400。
- request body は CLI と同じ検査を使い、Web は POST request を変更せずに渡す。書込みのための Ledger キー追加はなく、固定した形式を保つ。

`review` は、指定された SHA を証拠として記録する。Reviewer の実行や、SHA が Git 内に存在することの確認はしない。現在の検査は空の SHA を拒否する。呼出し元が意図した出力 revision を正確に指定する必要がある。
