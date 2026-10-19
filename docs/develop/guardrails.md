# ガードレール

| ID | 必要な不変条件 | 確認する箇所 |
|----|-----------|-------------|
| PRIV-001 | 公開成果物に個人名、email、user ID、home path、内部 hostname、組織固有のリポジトリ名を含めない。 | 文書レビュー、CI の hygiene job |
| ROLE-001 | Coder と Reviewer は、独立して隔離した実行 context を使う。 | `runtime.Identity`、Controller 管理の独立 Reviewer worktree、Git の証拠 |
| SHA-001 | Reviewer の実行を、保持した Coder の出力 commit に一致させる。明示承認した snapshot mode では、保持した tree と変更していない baseline に一致させる。 | metadata は `core.BindEvidence`、実行は `gitproof.Verify`・snapshot/ref の検証・Controller の引継ぎ |
| CHK-001 | checkpoint の順序が未完了なら、準備完了と報告しない。 | `core.CheckReady` |
| CHK-002 | checkpoint は宣言順に完了させる。順序違い、未知の checkpoint、全完了後の進行は拒否する。 | `core.Sequence.Advance` |
| LED-001 | Ledger の tasks・dispatches・evidence・checkpoints を保存・読込みで保持する。Server 段階へ延期した SQLite backend でも同じ形式を保つ。 | `core.Ledger.Save/LoadLedger` |
| DSP-001 | active な Dispatch を通知なく置き換えない。 | `core.GuardActiveDispatch` |
| MEM-001 | Dispatch 前に、選択 workspace の承認済み構造化 Memory 検査をすべて満たす。policy の読取り不能や形式不正は失敗とする。 | `memory.CheckDispatch`、CLI・HTTP・ACP の dispatch。Web は HTTP の判定を使う |
| SDK-001 | 管理処理の core に SDK 固有の型を含めない。 | `internal/core` は標準ライブラリだけを import。CI の `go list` で確認 |
| REP-001 | Task 作成、Dispatch、証拠の対応付けには `--repo` の明示が必要。cwd から対象を推測しない。 | `core.CheckRepo`、CLI の `task create`・`dispatch`・`review` |
| REP-002 | `endpoints` の6項目は単位で宣言し、https、hostname、port、path の契約を満たす。設定の採用元だけの違いは不整合としない。 | `config.validateEndpoints`、`taxion config check` の `repository.identityMode` |
| REP-003 | Git remote を scheme、host、port、path に分離できる。曖昧な `scp` 形式、percent encoding、`.`・`..`、backslash を拒否する。 | `gitproof.ParseRemote` |
| REP-004 | remote から取り出した repository identity を `repository.path` と照合する。path は大文字小文字を区別する。 | `gitproof.VerifyEndpointsRemote` |
| REP-005 | clone prefix、host、port を承認済み `endpoints` と照合する。標準外の port は `ssh://` URL だけが表現できる。 | `gitproof.VerifyEndpointsRemote` |
| REP-006 | 同じ API 応答の `id`・`path_with_namespace`・`ssh_url_to_repo`・`http_url_to_repo`・`web_url` を同時に照合する。未認証と到達不能も失敗とする。 | `forge.checkGitLabTargetEndpoints`、`forge.verifyProjectEndpoints` |
| REP-007 | v2 snapshot は保存値を正本とし、実効設定の drift と remote の相違を区別して fail closed する。自動変換と再 binding をしない。 | `portable.VerifySnapshot`、`portable.checkSnapshotOrigin` |
| SPEC-001 | 仕様の出典・版・用途別承認を同じ snapshot として保持し、送信・公開時に許可とローカル原本を照合する。 | `core.CheckSpecificationUse`、controller の実行・質問・公開、役割間の snapshot 検査 |
| MEM-002 | 完全な移行は両 scope の本文・出典・版・承認・失効・履歴・設定を保持し、確認した hash のファイルを新しいローカル保存先に設置する。 | `memory.Snapshot`、`InspectSnapshot`、`RestoreSnapshot` |
| FILE-001 | Task 承認済み fileAccess と Dispatch 開始時点の追跡対象を permission・Git 変更検査に使い、全レベルで worktree 外と `.git` を拒否する。Reviewer は read-only とする。 | `FileAccessPolicy`、SDK permission、`gitproof.CheckChangedPaths` と fileaccess 回帰 |
| STOP-001 | 保存 session の停止専用接続は新規 session・指示送信・通常再開を拒否し、pending work と全 permission を無効にする。取消・保存の失敗を停止成功として扱わない。 | `copilot.NewForStop`、`ResumeForStop`、Controller.Stop、停止専用 SDK protocol 回帰 |
| LED-002 | 同じ Dispatch の suspension と cancellation の結果 receipt を追記保持し、最新結果・履歴の順序・内容 hash・利用量を照合する。 | `Controller.transition`、`validateResultReceipt`、suspension→cancellation 回帰 |
| BUD-001 | 停止済み Flow の上限変更で再開せず、別 worktree・新しい Task と予算の承認で継続する。既知の Reviewer 設定承認漏れは CLI の有料 turn 前に検出する。 | successor Git proof、intake confirmation、`CheckReviewFlowConfiguration` と継続・設定回帰 |

この表は必要な制約を定義しています。現在の `review` 操作は証拠の対応付けだけです。空でない SHA を要求しますが、その形式、Git 内の存在、Reviewer の実行は検証しません。Controller の Runtime review flow は、別途独立した Reviewer worktree を作成し、commit・tree の一致を確認します。metadata だけを扱う `review` コマンドは、その実行の証拠にはなりません。FILE-001、SHA-001、STOP-001、LED-002、BUD-001 の判断と制約は ADR-0026〜0028 に記録しています。REP-002〜REP-007 の判断と制約は ADR-0042 に記録しています。

## 一般化した失敗の知見

- リポジトリの混同：実装リポジトリと検証対象を、明示した識別情報で区別する。Taxion CLI は `--task`・`--sha` を明示させ、cwd だけから対象を推測しない。将来の Server では `repo identity gate` の作業として扱う。
- 通知のない置換：error なしの Dispatch 上書きで証拠を失った。DSP-001 に対応する。
- 曖昧なレビュー対象：固定した SHA ではなく「最新」をレビューした。SHA-001 に対応する。
