# 03 — Runtime Contract (SDK-neutral)

## Interface

```go
type Runtime interface {
    Start(ctx context.Context, taskID string) (sessionID string, err error)
    Send(ctx context.Context, sessionID, prompt string) error
    Resume(ctx context.Context, sessionID string) error
    Stop(ctx context.Context, sessionID string) error
    Events(ctx context.Context, sessionID string) (<-chan Event, error)
}
```

## Rules

- Core (`internal/core`) uses standard-library types, never SDK structs.
  Adapters implement the Runtime interface outside core.
- Each `Runtime` implementation MUST isolate Coder vs Reviewer sessions (ROLE-001).
- Events are neutral `{Type, Payload}` — no SDK event types leak into core.
- Adapters return errors for unknown or stopped sessions, out-of-order steps,
  and empty task IDs. They advance state and report readiness only for valid requests.
- `runtime.Identity` exposes the role, worktree, and configuration hash needed
  to bind a runtime session to one authorized Dispatch.
- Selected Dispatches require `runtime.ConfigurationIdentity`: the saved tuple,
  effective runtime authentication and repository API access checks. The controller
  calls both checks before start/resume/send and rechecks approval afterward.
  The GitHub.com adapter selects `gh:active` or `env:NAME`, confirms the expected
  repository account and exact target, and requires read permission (plus write
  permission for Coder). Unknown access and unsupported providers stop execution.
  Successful checks are appended to `configuration.accessChecks` with the operation,
  runtime hash, observation interval, role permission and unverified contract.
  Recording rechecks approval transactionally and does not claim session creation.
  The first observation fixes the checked runtime identity even before a session
  exists. Earlier checks are not reused after restart; absent legacy observations
  imply no confirmation. These checks are not AI contract verification or Git push
  authentication. After session creation/attachment, and before each model send,
  selected Dispatches also call the adapter's SDK-neutral `CheckServiceAccess`.
  The Copilot adapter queries `session.quota.get`, requires successfully acquired
  account snapshots with explicit service access and capacity, and rejects missing,
  denied, exhausted or unknown observations without consulting numeric balances.
  A prior turn's admission observation is not an account snapshot. Approval is
  rechecked after this query; a rejected newly created session is stopped without
  binding it. These observations are not added to the preceding access receipts
  and do not identify the payer or guarantee future admission. Safe stop bypasses
  all three external checks. Configuration-less legacy controllers are unchanged.
  The same sourced Human approval must cover the selected account and contract
  reference in the current Task and issued instruction. Its reason records the
  approved use; checking that record does not verify the legal truth of its text.
- Saved-record side questions use a separate, tool-free Orchestrator runtime.
  They require sourced Task configuration approval, fresh authentication,
  repository read access, provider service checks and explicit time/token limits.
  Answers cite existing fields in the supplied observation and separate recorded
  facts, assumptions and unknowns. The observation is never a live-state proof.
  No Dispatch is bound, no workspace history or approval is changed, and returned
  session/usage information belongs to this question, not the main flow.
  Unknown usage and invalid citations stop the query. Safe cleanup uses its own
  bounded context after cancellation; errors are not suppressed.
  Prior result decisions are checked against their actual saved Dispatch and
  compacted across Task/history, instructions and flow contracts. Exact results,
  Memory and source hashes remain; `projection` explains that original instruction
  hashes do not hash the projected instruction. Durable histories are unchanged.
  A four-role question-input regression requires less than half the original
  JSON input size without an additional model call.
- `AskInvestigation` uses those same checks and ceilings, with explicitly selected
  committed UTF-8 files added before the model-only send. `ReadCommittedFiles`
  permits only fixed-SHA Git object reads, canonical scoped paths, 1-16 files
  and a total source ceiling up to 64 KiB. It rejects missing/binary/oversize
  content without returning partial evidence and ignores ambient Git routing
  variables. No working files, shell commands, hooks, textconv or agent tools
  are used. The separate Git observation identifies this investigation, never
  previous reasoning or successful validation.
- `RecordQuestionDraft` shares the Planner adoption transaction and existing
  supplement approval/publication path. Only selected confirmed indices and an
  explicit Human instruction are applied; normal chat is not silently copied.
  Saved context, Memory, role, repair ceiling and current result targets are
  checked. Investigation SHA, file bytes and hash are reconstructed before
  adoption. Its bounded source snapshot, selected claims, uncertainty and
  proposal hash remain durable. Native session IDs remain supplied provenance.
  A proposal is neither authenticated provider output nor semantic correctness.
- `RequestFlowInterrupt` atomically records targeted feedback/hold and, for
  running execution, `CANCEL_REQUESTED`. It never records stop confirmation or
  Task cancellation. The owning runner performs safe stop through existing
  monitoring/cleanup; failure leaves the request unconfirmed and cannot
  authorize continuation. Issued execution is held, not fictitiously stopped.
  Exact replay is checked before applying a new request to current state.
  Partial work and issued instructions are preserved.
- Intake persists original request text in a sourced, unapproved DRAFT Task.
  Optional planning configuration approval permits isolated bounded questions
  only. Exact-hash Human confirmation records separate execution authorization
  and configuration approval before READY; incomplete scope/acceptance or
  validation commands cannot pass. Confirmation does not issue work or choose
  flow ceilings. A revised issued contract is a successor Task, requiring the
  held predecessor target, no runner and no active Dispatch. Old flow limits,
  approvals, instructions and results remain unchanged; explicit reapproval
  stops/supersedes the predecessor and prepares separately bounded work.
- Selected non-execution Planner actions are sourced unresolved feedback, not
  repairs. They retain the held boundary until explicit disposition, separate
  investigation or successor reapproval. Exact replay cannot apply old advice
  to another role. No extra model call is inserted in automatic progression.
- Isolated CLI questions constrain citations to actual supplied record paths in
  the native response schema. Composite records are valid sources; nested
  histories need not be copied into that catalog. Adoption still independently
  verifies the full current context, hashes and source content. Formatting a
  JSON configuration cannot make unchanged evidence stale, but changing its
  values while retaining an old observation hash is rejected.
- `--controller-validation-submission` on runtime or workflow execution separates
  model submission from controller-run validation: the role returns its genuine
  result with no invented checks, and the controller validates before saving
  success. Missing output, failed checks and unsupported Reviewer acceptance
  still block progress. Repeat the flag on session attachment; default prompt
  text remains unchanged so existing session configuration hashes are preserved.
- `StepReviewFlow` uses the same runtime and durable runner claim as automatic
  execution, but executes at most one role and does not issue its successor after
  completing the turn. Further progression requires another explicit request.
  Watched conversations hold the boundary before reading input; EOF never grants
  continuation. Observed continuation compares the displayed target, draft,
  contract and hold version transactionally. Supplement approval and publication
  remain separate; existing automatic runner behavior is unchanged.
  `--interactive-execution` keeps watched input available during its selected
  runner, serializes concurrent output, rejects a second owned execution and
  reports failures. EOF/quit cancels and awaits only that console's owned runner;
  without the opt-in flag, synchronous watched behavior is retained.
## Saved result and checkpoint recovery

`workflow recover` releases the exact saved runner claim after process-stop
confirmation and reconciliation of terminal Dispatches. When the latest result
is a successful Coder submission, recovery also checks the saved result contract
and current Git facts: repository origin, branch, HEAD, baseline ancestry,
changed paths, clean worktree, validation provenance and commit parents.
The fresh observation must match the saved Git evidence, with observation time
excluded from comparison. The original result and its timestamps stay intact.

Ordinary Coder-to-Reviewer handoff applies the same check. A mismatch leaves the
recovery claim intact and returns an explicit error; a handoff mismatch records
a STOPPED flow with its reason. A matching result proceeds to independent review
of the exact saved output SHA. These checks read Git and saved validation records;
validation command execution belongs to the original controller submission.
Released Reviewer results also require the managed worktree's absence and
registry removal, the source repository's origin and immutable commit parents,
and the same output SHA as the preceding Coder. Completed checkpoints apply
these checks again. Successful results require a controller receipt matching
the saved result and usage; legacy checkpoints without one are explicit
evidence-reconciliation blockers.

Execution authorization is persisted before sending instructions. A RUNNING
Dispatch is available for observation and stop; new instructions require a new
authorized Dispatch. Runtime output, usage and terminal events are persisted
with a stream ID and sequence. Identical sequence replay within one stream is
idempotent; conflicting replay and post-cancel results are errors. Adoption
across reconnect streams requires explicit reconciliation because normalized
SDK sequence numbers belong to the current adapter instance.

The full recovery, approved publication and failure-record procedures are in
[execution records](11-execution-records.md). Provider capabilities and
selected follow-up work are in [forge capabilities](12-forge-capabilities.md).

## Adapters

- `internal/runtime/local`: in-process reference adapter for state transitions.
  Runs a `core.Sequence` per session (`CompleteStep` enforces CHK-002 order),
  records accepted instructions as neutral `input` events, and emits
  `checkpoint`/`done` events.
- `internal/runtime/proc`: starts a configured external command, streams stdout
  and stderr as `output` events, writes instructions to stdin, and reports
  `done` or `failed` at process exit. `Resume` validates the existing session;
  process launch belongs to `Start`. Checkpoint sequencing remains separate.
- `internal/runtime/copilot`: wraps the official Go Copilot SDK. Configure one
  adapter per role and worktree; sessions use Auto and an explicit tool
  allowlist. Infinite-session compaction is enabled. The CLI appends the saved
  Dispatch snapshot to the role-specific system prompt so the authorization
  contract remains in system context across turns and compaction. Resume
  re-supplies the same system prompt, worktree and tools. The SDK permission
  handler grants Coder file writes inside the worktree and one-time use of
  allowlisted SDK tools; Reviewer receives read access inside its worktree.
  The handler rejects shell requests and file paths outside the assigned
  worktree. It governs SDK permission requests; host and external tools use
  their own security controls.
- `internal/runtime/controller`: checks the Dispatch role, assigned worktree,
  active status, and configuration hash before creating, resuming, sending,
  stopping, or recording usage for its bound session. Session IDs, hashes,
  model and token/cache usage are stored with the Dispatch in SQLite.
  Managed staging and commits additionally require an active Task, the invoking session
  for tool calls, and a running review flow within its saved deadline and
  observed known-usage ceiling when the Dispatch belongs to a flow.

## Skills・plugins・MCP・hooks と出力圧縮

固定した Go SDK `v1.0.19` の `SessionConfig` と `ResumeSessionConfig` は、
`SkillDirectories`、`PluginDirectories`、`MCPServers`、`Hooks` を持つ。
`EnableSkills` は skills の読込み、`EnableFileHooks` は `.github/hooks/` の読込みを制御する。
SDK の `Hooks` callback と file hooks は別の設定である。
明示した `PluginDirectories` は `EnableConfigDiscovery: false` の場合にも読み込まれる。
MCP の resident resume では、既に実行中の server を `DisabledMCPServers` だけで停止する機能は提供されない。

Taxion は、Task の出典付き decision に保存した `taxion.tools.v1` の承認から、
役割別の skills・skill-only plugins・固定 MCP リクエスト・RTK を接続する。
実行ファイル、入力、directory の内容 hash、引数、tool 名、承認済み JSON、
private evidence directory を session の設定 hash に含め、create/resume で同じ条件を使う。
config discovery・file hooks の自動読込みは無効のまま、明示した directory と SDK hooks を接続する。

skills と plugins は canonical な絶対パスを指定する。
plugin は root の `plugin.json` と `skills/` からなる skill-only 形式を使う。
`taxion tools hash --path /absolute/path` の hash は、相対名・mode・size・内容を含む
asset の識別値であり、仕様入力の `documentHash` は原本 bytes の SHA-256 である。
承認済み asset の変更は再承認を必要とする。

承認済み role policy に `skills` または `plugins` があれば、Taxion は
SDK の許可 tool に `skill` を追加する。名前を prompt に書くだけでは接続しない。
asset のない policy や未承認の session で `skill` を明示指定すると、実行前に拒否する。
skills のない MCP / RTK のみの policy では `EnableSkills` を有効にしない。
Coder と Reviewer は、承認済み skill の通常の custom-tool permission を扱えるが、
managed approval を要求する操作の拒否、Reviewer read-only、worktree 外の拒否は維持する。
skill に書かれた追加ファイルの読み取りやスクリプト実行も、この境界を越える許可にはならない。

`workflow run|step` は、Coder と将来の Reviewer の role policy を発行済み
decision と現在の Task で照合し、asset の存在と hash、明示した `skill` tool の承認を
session 作成前に検査する。実行直前と各 tool hook でも照合を続けるため、
事前検査後の asset 改変を許可したことにはならない。

MCP は host が exact arguments を照合してから、承認された stdio server を一回起動する。
server の実行ファイルは信頼された host code として承認し、ambient credential を子プロセスへ渡さない。
SDK の role permission は引き続き通常の read/write と管理 commit を判断する。
独立 Reviewer は、controller が発行した元 Coder との対応と同じ decision を照合して、
元 Task の Reviewer 向け tools 承認を引き継ぐ。停止用の resume は extensions を追加せず接続する。

SDK hooks は原本の tool result と failure を先に private JSON へ保存し、
opaque filename と hash の receipt を返す。directory は `0700`、原本ファイルは `0600` とする。
保存失敗と圧縮失敗は runtime の失敗イベントにも通知し、controller が停止処理へ進めるようにする。
RTK は isolated HOME で `pipe --filter` を実行し、成功した 4096 bytes 以上の plain text を圧縮する。
JSON/XML と failure は原形を保持し、圧縮結果には warning/error/fail 行を保持する。
SDK built-in result の `textResultForLlm` と custom result の `textResultForLLM` を扱い、
status・exit code・telemetry をそのまま残す。
RTK 自身の stdout・stderr・exit status も原本として保存する。

承認形式は `kind`、`taskId`、`actor`、役割名を key にした `roles` を持つ JSON である。
各 role policy は `skills`、`plugins`、`mcp`、`rtk`、`compressionFilter`、
`evidenceDirectory`、`allowPublication` を保持する。
MCP binding は `name`、`command`、`args`、`inputs`、`tool`、`arguments` を固定する。
この JSON を Task decision の `text` に保存し、decision の `source` に Human の判断の出典を記録する。
通常の Task 承認・発行手順は [Task Graph reference](10-task-graph.md) に従う。
tools を使った Task の公開には `allowPublication: true` の明示承認を必要とする。

`--compact-context` は保存済みの結果・exit code・証拠を保持して重複した指示を省く。
元の完全な instruction は SQLite に保持する。
native plugin discovery、実 stdio MCP、実 RTK の圧縮率と原本保持、
SDK の create/resume 設定と権限を無料回帰で確認する。
実 SDK による仕様検索から独立レビューまでの受入れ状況は
[仕様入力](13-specification-input.md) と #60 に記録する。
通常テストは独自の合成 skill と一時 directory を使い、特定の外部 skill、
個人の home、インストール済み Copilot、認証、ネットワーク、有料モデルを要求しない。
native plugin discovery は `TAXION_TEST_LOCAL_PLUGIN_DISCOVERY=1` による
無料の明示 opt-in とし、指定時に CLI がなければ失敗する。

SDK 設定・permission・hook の合成回帰は、実モデルが skill を選び、正常に
ロードし、文書の品質工程を完了した証拠とは区別する。実利用は Taxion 配下の
session の tool request と完了結果、private tool evidence、成果物の SHA、
検査結果と独立レビューを対応付けて確認する。
親の開発支援 session の skill 起動や discovery の成功を代替証拠にしない。
Release 0.1.0 の文書 dogfood における実 skill 利用は Issue #78 の未確認条件である。
そこで選択する外部 skill は製品の固定依存ではなく、操作者が利用許可と内容を承認する。

## Dispatch の実行と context

残予算を指示へ付加する設計は [ADR-0029](adr/0029-separate-budget-guidance-from-provider-quota.md)
に採択したが、現在の adapter はこの補足指示や account quota の取得を実装していない。
実装時は Controller が保存済み Flow usage と adapter の非推論 read-only 観測を分離し、
`off` / `flow` / `account` の解決結果に応じて既存の `Send` に補足する。
動的観測は送信ごとの receipt に保存し、system prompt の変更や別の有料 turn を必要としない。
単位・account・resource・対象期間・取得元・観測時刻と
`available` / `unsupported` / `unavailable` / `stale` を保持する。
quota の未知を成功やゼロに変えず、Copilot の実験的 API 型を core に漏らさない。
stop-only 接続は quota 照会や新しい指示を発行しない。

`taxion runtime start|send|resume|events|stop` is the Copilot CLI execution
path. The ACP server accepts only a fresh, CLI-issued Dispatch ID in
`session/new`; it does not create Tasks or Dispatches. ACP and CLI use the same
controller for session binding, tool authorization, event recording, result
verification and safe stop. A fresh ACP session sends one prompt as additional
context to the saved Dispatch contract; reconnecting to a running Dispatch
observes the existing turn without sending another prompt. Runtime output
deltas are streamed as ACP session updates. `taxion acp` requires a per-session
AI-credit ceiling and timeout, plus a Coder system prompt; a Reviewer prompt is
required for Reviewer Dispatches. The SDK commit and staging tools remain
explicit opt-ins through `--managed-commit` and `--managed-stage`. ACP sessions
can reconnect to a running Dispatch by ID; use the CLI runtime commands to
inspect or reconcile a saved session when ACP cannot resume it.

Task の `outputMode` は `commit`（省略時の既定）または `worktree-tree-snapshot` を選ぶ。
snapshot mode では Coder の `outputSha` は開始時の commit SHA のままにし、
Controller が算出した `outputTreeSha` と変更 path で成果物を識別する。
Controller は tree object をローカルの `refs/taxion/snapshots/` に保持する。
これは branch、commit、実 index、remote を変更しないが、Git ref を追加する。
Reviewer は baseline commit から同じ tree を別 worktree に再現し、開始時と終了時に照合する。
Git tree に含まれない ignored file はレビュー対象に含まれない。

`--compact-context` on `runtime` and `workflow run|step` renders exact prior
results once per sourced decision instead of embedding their full prior
instructions recursively. It compares each `result:` decision with the actual
saved Dispatch result and rejects missing or altered sources. Current contract,
Human decisions and Memory stay intact; full immutable instructions remain in
the Graph. Result and instruction hashes identify omitted source records.
The default rendering is unchanged. Compact rendering changes the system
prompt/configuration hash and must be repeated for execution attachment,
not cancellation-only `runtime stop`; do not switch an existing execution
binding to the new rendering.

`workflow planning-context` exposes saved Memory, acceptance, blockers,
dependencies, concerns, related result/SHA targets and existing flow limits
without full nested histories. It is a read-only projection with observation
times and a content hash, not an atomic live-state snapshot, model judgment or
execution authorization. Workspace and Memory are still read separately;
execution must recheck its existing controller gates. No provider call occurs.

`conversation plan` / `AskPlanner` reuse the isolated-question transport and
its sourced configuration approval, authentication, repository/service checks,
input/output/token ceilings, approval rechecks and independent cleanup.
They send compact Planner context to a separate tool-free Orchestrator session.
The native schema adds an advisory recommendation with explicit confirmed,
assumed and unconfirmed evidence. Actual JSON-pointer sources are required;
an `APPROVE` or execution verdict is not an allowed recommendation.
The returned original observation remains attached when records change during
reasoning. Recommendations are not applied automatically, and no model call is
added to ordinary workflow progression.

`RecordPlannerDraft` / `workflow planner-draft` and watched `/adopt` select
unchanged planning advice into an unapproved `SUPPLEMENT` draft. They compare
the reconstructed context, result target, Memory, graph, flow contract and held
boundary before publication. Missing/forged context fields cannot reuse a valid
observation hash. The transaction rechecks the graph and Memory; independent
Memory storage still does not provide a cross-database atomic snapshot.
Only result-compatible `REVIEW` or `REPAIR` can enter the existing
contract-preserving draft path. Observation/source metadata and uncertainty
remain with the draft. Session IDs are reported provenance, not verified
provider receipts. Core exact-ID replay checks preserve prior versions.
Human draft approval and explicit handoff release remain separate operations;
controller checks still govern dispatch and execution. `REPLAN`, `INVESTIGATE`,
`HOLD`, `HUMAN_REQUIRED` and `REPORT` are not silently turned into a repair
instruction. Changed-contract approval and unattended decision-driven
progression remain unfinished.

`runtime` and `workflow run|step` optionally accept
`--strict-result-identities`: the native response schema pins the issued
Dispatch ID, Task ID, role, repository and expected execution-start SHA.
Repair uses its immutable target SHA rather than the initial baseline.
Output SHA and verdict remain reported facts subject to independent controller
verification. The strict schema also limits `nextTransition` to the existing
Task status vocabulary, without selecting a transition or changing saved state.
The option changes the configuration hash and must be repeated
on execution attachment, not cancellation-only `runtime stop`. The default schema remains unchanged for
older sessions; rejected responses are not repaired into successful results.
Agents without shell permission can submit `validations: []`; the controller
runs the authorized commands after a successful implementation submission and
stores actual exit codes, timestamps and Git provenance. They must not fabricate
an earlier command execution or treat pending controller validation as evidence
that tests passed.

After completed controller execution and Git verification, the controller adds
the `controller-validation` evidence marker if absent. Agents need not claim
that stage before it occurs. It is not generated if controller validation returns
an error, is cancelled or is unexecuted; no unrelated source references or
verdicts are invented. The marker means commands were observed, not that every
test passed: a valid REQUEST_CHANGES review may retain an actual nonzero test
exit code.

Reviewer `acceptance[].evidence` and `findings[].evidence` must reference
strings also present verbatim in the result's top-level `evidence` array.
Dangling references are rejected even when the proposed verdict or finding is
otherwise plausible; source analysis must not be mislabeled as an executed test.

`runtime auth` is an explicit preflight, not an execution action. Fixed
configuration and current/saved Dispatch approvals are checked before the
Copilot adapter calls `auth.getStatus`. `CheckAuthentication` verifies an
authenticated account matching the requested account and an explicit
`github.com` host. It creates no SDK session, does not cache the result, and
does not alter the adapter's configuration hash. Provider status messages
and raw RPC errors are withheld from its returned errors; cancellation
remains identifiable.

This probe does not resolve credential references, select the repository actor
or establish contract eligibility. Execution-time checks use the same
authentication method separately; local protocol fixtures do not demonstrate
signed-in acceptance.

`config bind --dispatch` records a complete approved configuration selection,
decision reference, field origins and recording time in the Dispatch. The
controller uses one workspace transaction to check current Task and issued
instruction approvals before saving it. The core stores SDK-neutral JSON and
validates its identity, origins and membership in the saved approval; the
configuration layer validates the complete tuple schema.

Once selected, Dispatch configuration checks and the explicit authentication
probe require that tuple. Repeating identical values keeps the initial origins
and time. Binding different values or adding a first selection to an existing
session requires a newly authorized Dispatch. Missing selections on legacy
records remain readable. The selection record alone is not evidence of
effective authentication or contract eligibility.

The Copilot adapter's optional selected tuple is validated, canonicalized,
owned and included in its configuration hash. `runtime.ConfigurationIdentity`
exposes only neutral JSON and an account-authentication capability. Unselected
legacy hashes remain unchanged. The controller rejects a selected Dispatch
with a missing or different runtime tuple; selected start/resume/send check
current Task and saved instruction approval and the effective account before
SDK work. Approval is rechecked after authentication, in the new session
binding transaction and before sending an instruction. Managed Git checks the
tuple and approval inside its own publication transaction.

The runtime CLI resolves fixed configuration for start/resume/send/events/commit.
Fresh Dispatches save their selection automatically; legacy sessions without a
selection require a new authorized Dispatch. Shared runtime/workflow factories
construct the adapter from the saved selection. Every workflow Task must be
explicitly named in its issued configuration approval. The optional
`additionalTaskIds` on `taxion.execution-config.v1` lists exact additional
Task IDs; it is not a pattern or implicit delegation to all children.
For flow ID `flow` and `maxRepairs: 1`, reviews are `flow-review-0` and
`flow-review-1`; repair Coders retain the original Task ID. An omitted list
preserves single-Task approval. Empty, whitespace-bearing, pattern, duplicate
and primary IDs are rejected.

Generated review Tasks inherit the sourced approval record unchanged.
Configuration/core checks accept only the primary or explicitly listed Task
with a complete allowed tuple. Unlisted reviews fail closed; the flow's
independent authorization and ceilings remain required. The list must be
recorded before issuance and cannot widen an existing Dispatch. Local checks
exercise selected Coder→review→repair→re-review through controller and actual
Git, not a signed-in SDK model.

CLI safe stop uses a cancellation-only adapter bound to the saved session ID,
role, worktree and configuration hash, without reconstructing changed prompts,
tools or schemas. It uses the saved selected credential reference rather than
current fixed settings, and does not require an authentication probe. Start,
Send and ordinary Resume are rejected. Stop attachment disables pending-work
continuation, resume events, transcript repair and discovery, exposes no tools,
and denies every permission request. It sends abort before detaching even
without local busy-state observations. Older execution contracts may stop
without gaining permission to execute under a new contract. Attachment,
cancellation and persistence failures remain explicit; a model instruction is
never sent by this path. Unconfigured local/proc usage retains existing
behavior. Credential resolution, repository actor selection, contract
eligibility and signed-in acceptance remain separate requirements.

`runtime commit` is a host operation that scans a locked index snapshot and
message before publishing its exact tree. It does not stage files or complete
the Task. `runtime start --managed-commit` and `workflow run --managed-commit` explicitly
enable `taxion_commit` for Coder sessions. The neutral `runtime.ManagedCommit`
binding carries a Dispatch ID, scanner reference and host callback; SDK tool
types remain inside the Copilot adapter.

The callback receives invocation cancellation, session ID and message only.
The registered tool cannot choose its repository, scope or scanner. It rejects
unknown fields, missing messages, unknown/idle/stopped sessions and attributed
sub-agent invocations. Host interruption and flow deadlines also cancel Git
work. Permission checks remain enabled. Controller errors are failed SDK tool
calls, including scanner failures and explicit partial-publication errors.
Successful output contains the SHA and reminds the agent that review and
acceptance are still required.

Creation and resume register the same tool. Dispatch, absolute scanner path
and operation version are part of the configuration hash when enabled.
Without the option, the previous tool surface and configuration hash are
preserved. Existing sessions cannot gain or lose the tool on resume without a
binding mismatch. Shared runtime/workflow construction uses the same callback
and does not register commit tools for Reviewers.

Commit uses already-staged content. The additional `--managed-stage` option
requires `--managed-commit` and registers `taxion_stage` on creation and resume.
Its separate operation version preserves commit-only session identities.
The neutral binding's staging callback receives session ID and an explicit
paths array; repository, scope and scanner still belong to the host.

Staging shares the controller's authorization transaction, invocation checks
and flow ceilings. It selects canonical relative files or tracked deletions,
not recursive directories, force adds, Git metadata or parent symlinks outside
the worktree. Literal path selection prevents wildcard/pathspec expansion.
Only a private index is changed before full-tree scope, path and content checks.
The checked copy is flushed and atomically published as the real index, while
HEAD and working files remain unchanged. Failed checks do not publish it.
Commit scans again. Git clean filters apply and local unreachable blobs can
remain after rejected staging; trusted repository configuration and hostile
host-process races are not sandboxed by these operations.

Local checks invoke registered SDK stage and commit handlers through controller
and real Git, but do not demonstrate a signed-in model's tool invocation. Live
stage→commit→independent-review acceptance remains separately tracked in #54/#72.

## Event subscription lifetime

Adapters replay events recorded by the current adapter instance before
following live ones. Copilot emits normalized message, turn, failure,
cancellation, usage, context-window, compaction, and permission-decision
events; usage is persisted with the bound Dispatch. Context and compaction
events report SDK-provided token limits and compaction outcomes when available.
A finished turn leaves the session stream available for the same role's next
instruction. Stop closes the stream.
Cancelling the subscription context closes only that subscription; consumers
must still observe their context rather than waiting only for session closure.
After a process restart, the SDK resumes Copilot's conversation by session ID.
The normalized event stream starts with events observed by the current adapter instance.

The SDK-neutral boundary is justified in
[ADR-0003](adr/0003-keep-governance-core-sdk-neutral-behind-runtime-interface.md).
Planned adapter evaluation belongs in [the roadmap](04-roadmap.md).
