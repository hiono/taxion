# ガードレール

> この文書には Task / Dispatch / ReviewFlow 時代の制約も履歴として残る。これらは Job-native 実行の権限根拠ではない。Job-native 経路には JOB-001〜JOB-004 を適用し、旧経路の置換完了時に旧専用項目を削除する。設計上の優先順位は ADR-0081、ADR-0080、ADR-0077 とする。

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
| REP-001 | 旧 Task / Dispatch CLI の対象指定には `--repo` の明示が必要。Job-native 経路では JobApproval 済み Job Contract の repository identity を使用し、cwd や Task から権限を推測しない。 | 旧 CLI: `core.CheckRepo`。Job: `JobApprovalProposal`、Controller の Job boundary 検査 |
| REP-002 | `endpoints` の6項目は単位で宣言し、https、hostname、port、path の契約を満たす。設定の採用元だけの違いは不整合としない。 | `config.validateEndpoints`、`taxion config check` の `repository.identityMode` |
| REP-003 | Git remote を scheme、host、port、path に分離できる。曖昧な `scp` 形式、percent encoding、`.`・`..`、backslash を拒否する。 | `gitproof.ParseRemote` |
| REP-004 | remote から取り出した repository identity を `repository.path` と照合する。path は大文字小文字を区別する。 | `gitproof.VerifyEndpointsRemote` |
| REP-005 | clone prefix、host、port を承認済み `endpoints` と照合する。標準外の port は `ssh://` URL だけが表現できる。 | `gitproof.VerifyEndpointsRemote` |
| REP-006 | 同じ API 応答の `id`・`path_with_namespace`・`ssh_url_to_repo`・`http_url_to_repo`・`web_url` を同時に照合する。未認証と到達不能も失敗とする。 | `forge.checkGitLabTargetEndpoints`、`forge.verifyProjectEndpoints` |
| REP-007 | v2 snapshot は保存値を正本とし、実効設定の drift と remote の相違を区別して fail closed する。自動変換と再 binding をしない。 | `portable.VerifySnapshot`、`portable.checkSnapshotOrigin` |
| SPEC-001 | 仕様の出典・版・用途別承認を同じ snapshot として保持し、送信・公開時に許可とローカル原本を照合する。 | `core.CheckSpecificationUse`、controller の実行・質問・公開、役割間の snapshot 検査 |
| MEM-002 | 完全な移行は両 scope の本文・出典・版・承認・失効・履歴・設定を保持し、確認した hash のファイルを新しいローカル保存先に設置する。 | `memory.Snapshot`、`InspectSnapshot`、`RestoreSnapshot` |
| FILE-001 | 旧 Task / Dispatch 経路に限り、承認済み fileAccess と開始時点の追跡対象を使用する。Job-native 経路では Job Contract の repository/file-access 境界を全 Role に適用し、worktree 外・`.git`・禁止副作用を拒否する。Reviewer を Role 名だけで read-only にしない。 | 旧経路: `FileAccessPolicy`、SDK permission。Job: `JobEffectObservation`、Controller evaluator、Copilot / OpenACP adapter 回帰 |
| STOP-001 | 保存 session の停止専用接続は新規 session・指示送信・通常再開を拒否し、pending work と全 permission を無効にする。取消・保存の失敗を停止成功として扱わない。 | `copilot.NewForStop`、`ResumeForStop`、Controller.Stop、停止専用 SDK protocol 回帰 |
| LED-002 | 同じ Dispatch の suspension と cancellation の結果 receipt を追記保持し、最新結果・履歴の順序・内容 hash・利用量を照合する。 | `Controller.transition`、`validateResultReceipt`、suspension→cancellation 回帰 |
| BUD-001 | 停止済み Flow の上限変更で再開せず、別 worktree・新しい Task と予算の承認で継続する。既知の Reviewer 設定承認漏れは CLI の有料 turn 前に検出する。 | successor Git proof、intake confirmation、`CheckReviewFlowConfiguration` と継続・設定回帰 |
| APP-001 | 旧 Task 実行経路に限り、bundle-required Task は現在の契約と設定に一致する ApprovalBundle を要求する。Job-native 権限の根拠は JobApproval の immutable revision とし、Task ApprovalBundle から継承しない。 | 旧経路: `TaskGraph.CheckBundleAtEntry`。Job: `JobProposal`、`CurrentJobApproval` |
| JOB-001 | Job の実行権限は現在の Job revision に一致する JobApproval だけを根拠とする。Task、会話、RoleExecution、旧 ApprovalBundle から権限を導出しない。 | `JobProposal`、`CurrentJobApproval`、Start / Resume / Send の Controller 検査 |
| JOB-002 | 通常の可逆操作は Job の resource/worktree 境界内で許可する。禁止副作用は拒否し、高リスク・不可逆・外部副作用は具体的な観測と対象を固定して人間承認を要求する。 | `JobContract.EvaluateJobEffect`、`JobEffectApprovalRequest`、`JobEffectApprovalDecision` |
| JOB-003 | Start / Resume / Send / provider tool preflight は同一の Job revision、Runtime identity、resource boundary、budget、deadline、effect evaluator を再検証する。 | `JobRoleController`、Copilot / OpenACP `JobEffectAuthorizer` |
| JOB-004 | Reviewer の独立性は Role ACL の read-only 制約ではなく、独立した実行環境、対象成果物 snapshot、evidence provenance で保証する。 | Reviewer worktree / snapshot binding、JobEvidence、独立レビュー回帰 |

この表は必要な制約を定義しています。現在の `review` 操作は証拠の対応付けだけです。空でない SHA を要求しますが、その形式、Git 内の存在、Reviewer の実行は検証しません。Controller の Runtime review flow は、別途独立した Reviewer worktree を作成し、commit・tree の一致を確認します。metadata だけを扱う `review` コマンドは、その実行の証拠にはなりません。旧 FILE-001、SHA-001、STOP-001、LED-002、BUD-001 の判断と制約は ADR-0026〜0028 に記録しています。旧 REP-002〜REP-007 の判断と制約は ADR-0042、旧 APP-001 は ADR-0043 に記録しています。Job-native の認可・副作用境界は ADR-0080、Job中心の協調モデルは ADR-0077、全体再設計原則は ADR-0081 を優先します。

## 公開文章の保護名検査

非公開の製品名や組織固有名は、文書、ADR、Issue、PR、コメント、コミットメッセージ、配布説明文に記載しません。例や検証記録には架空の名称を使います。保護名の一覧は公開せず、GitHub Actions の secret `PUBLIC_TEXT_PRIVATE_IDENTIFIER_SHA256` に登録したダイジェストで照合します。

リポジトリの文書と ADR は、既存の `public-communication` ジョブで検査します。`public-identifiers` ワークフローは、PR の作成・編集・更新時にタイトル、本文、全コミットメッセージを検査します。Issue、コメント、レビュー本文、配布説明文は投稿・編集後に検査します。main への push でも、新しく到達可能になったコミットのメッセージを検査します。検出した名称と本文はログに出しません。

このワークフローは、既定ブランチの検査コードだけを実行します。PR 側のコードを実行せず、外部から投稿された文章をデータとして読みます。保護名の設定がない場合、API が失敗した場合、PR のコミット一覧が不完全な場合は検査に失敗します。GitHub API が返すコミット数の上限を超えるため、251件以上のコミットを含む PR も拒否します。

検査の成功は「公開箇所すべてから名称を除去した」という保証ではありません。照合範囲は `CheckIdentifiers` が認識する英数字・アンダースコア・ハイフンのトークンと登録済みのダイジェストに限られます。未登録名、画像、添付ファイル、過去の投稿、既存タグ、Git の過去履歴、GitHub のキャッシュは対象外です。Issue やコメントは公開前に拒否できず、下書き Release ではイベントが配信されない場合があります。PR のマージを止めるには、ブランチ保護で `protected-identifiers` を必須にします。履歴の書換えとキャッシュの削除は、別の監査・承認・確認が必要です。

## 一般化した失敗の知見

- リポジトリの混同：実装リポジトリと検証対象を、明示した識別情報で区別する。Taxion CLI は `--task`・`--sha` を明示させ、cwd だけから対象を推測しない。将来の Server では `repo identity gate` の作業として扱う。
- 通知のない置換：error なしの Dispatch 上書きで証拠を失った。DSP-001 に対応する。
- 曖昧なレビュー対象：固定した SHA ではなく「最新」をレビューした。SHA-001 に対応する。
