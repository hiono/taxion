# Taxion

**Taxion** — Evidence-governed control plane for coding agents.

> Order / Formation / Taxis → Taxion

Task Graph + Dispatch + Evidence + Independent Review form the core value proposition.
Taxion is developed **clean-room**: no code, history, or internal identifiers ported
from any private predecessor; only generalized requirements, guardrails,
procedures, and failure lessons are carried over as sanitized docs.

## What it is

採択した製品要件は [Taxion の要件と到達条件](docs/09-orchestration-requirements.md)
を参照する。階層化した Memory と Go Copilot SDK の役割別 Session による
自動実装・独立レビューは、現在の部品から実装する次の到達条件である。
以下は現在の構成を示す。到達条件の進捗は Roadmap と対応 Issue の完了条件で確認する。

```
Taxion Core (SDK-neutral)
 ├─ Server (Task Graph / Dispatch Ledger / Evidence Ledger / Policy / Checkpoint)
 ├─ CLI (human interface: init / task / dispatch / runtime / review)
 ├─ Web UI (task/dispatch/review forms + HTTP API proxy)
 ├─ ACP Server (stdio agent with dispatch/review methods)
 └─ Runtime adapters (local, process-backed, and Go Copilot SDK)
```

## Non-goals (Foundation → Local)

- The governance core is not coupled to a Copilot SDK; SDK types stay outside `internal/core`.
- No Git history / Issues / code ported from private predecessors.
- No personal data, internal hostnames, or org-specific repo names in public artifacts (PRIV-001).

## Quickstart

```bash
mise install
go build ./...
go test ./...
go run ./cmd/taxion --help
```

Serve the API with a token (required beyond local use; see `docs/06-remote-access.md`):

```bash
go run ./cmd/taxion gen-token                          # once: store it outside the repo
TAXION_TOKEN=<tok> go run ./cmd/taxion serve --addr 127.0.0.1:3938
```

## Docs

- [ADR-0020](docs/adr/0020-layered-memory-and-role-sessions-for-orchestration.md) —
  採択した到達条件、階層化した Memory、Go Copilot SDK の役割別 Session。
  現在の実装状況とは区別する。
- [ADR-0021](docs/adr/0021-durable-task-graph-and-dispatch-change-control.md) —
  Task Graph の情報、依存と完了条件、Dispatch の変更管理。
- [ADR-0024](docs/adr/0024-human-handoff-control-and-side-questions.md) —
  Human の受け渡し保留・途中中断、版付き指示案、本流を変更しない状況・根拠の質問。
  採択した設計と現在の実装を区別する。
- `CONTRIBUTING.md` — development checks, document ownership, and comment rules
- `docs/00-charter.md` — founding scope and project constraints
- `docs/01-guardrails.md` — behavioral invariants with IDs
- `docs/02-privacy-provenance.md` — sanitization policy
- `docs/03-runtime-contract.md` — SDK-neutral Runtime interface
- `docs/04-roadmap.md` — task breakdown
- `docs/05-ledger-schema.md` — frozen ledger wire format + HTTP surface
- `docs/06-remote-access.md` — SSH-tunnel-first remote access (SSH + docker),
  loopback-by-default policy. **Start here before exposing any port.**
- `docs/07-audit-log.md` — append-only audit log (mutations + denials) and
  retention policy.
- `docs/08-memory-model.md` — current Memory storage, scopes, promotion, search, and export.
- [docs/09-orchestration-requirements.md](docs/09-orchestration-requirements.md) —
  製品要件、段階別の完了条件、現在の実装との差分。
- [docs/10-task-graph.md](docs/10-task-graph.md) —
  版付き Task Graph の登録・遷移、scope out の保持、dispatch 検査と制約。
- `docs/adr/` — design decisions and rationale; Issues record implementation results.

## CLI

```bash
taxion config check --sources
taxion git check-index
taxion git check-index --secrets --scanner /absolute/path/to/betterleaks
taxion git hook status
taxion init
taxion task create --title "…" --repo <repo>
taxion task plan --repo <repo>
taxion workflow start --file <authorized-flow.json>
taxion workflow advance --id <flow-id>
taxion workflow run --id <flow-id> --coder-prompt-file <path> --reviewer-prompt-file <path>
taxion workflow show --id <flow-id>
taxion workflow planning-context --id <flow-id>
taxion workflow planner-draft --file <selected-planning-proposal.json>
taxion workflow question-draft --file <selected-question-findings.json>
taxion conversation plan --task <id> --question "Repair or revisit the plan?" --max-tokens 8000 --timeout 2m --max-ai-credits 30
taxion conversation investigate --task <id> --path src/app.go --question "What did the saved implementation miss?" --max-bytes 8192 --max-tokens 8000 --timeout 2m --max-ai-credits 30
taxion workflow recover --file <recovery-confirmation.json>
taxion workflow handoff --file <hold-or-release.json>
taxion workflow interrupt --file <targeted-interruption.json>
taxion workflow feedback-target --id <flow-id>
taxion workflow feedback --file <targeted-feedback.json>
taxion workflow feedback-resolve --file <keep-contract-decision.json>
taxion workflow draft --file <instruction-proposal.json>
taxion workflow draft-discard --file <discard-proposal.json>
taxion workflow draft-approve --file <approve-supplement.json>
taxion dispatch --task <id> --repo <repo>
taxion runtime start --dispatch <id> --system-prompt-file <path>
taxion runtime send --dispatch <id> --system-prompt-file <path> --prompt-file <path>
taxion runtime resume --dispatch <id> --system-prompt-file <path>
taxion runtime events --dispatch <id> --system-prompt-file <path>
taxion runtime stop --dispatch <id> --system-prompt-file <path>
taxion runtime commit --dispatch <id> --system-prompt-file <path> --message-file <path> --scanner <path>
taxion review --task <id> --sha <coder-sha> --repo <repo>
```

`workflow planning-context` reconstructs bounded Planner input from saved Task,
Memory, concerns, unmet dependencies, flow limits and related Coder/Reviewer
results. Targets identify each instruction, result and SHA; the observation
hash identifies the returned content. `recordedNextCheck` is the existing
controller's check, not an AI judgment or execution approval. The command calls
no model and does not change records; it supplies evidence for the separate
planning operation.

`conversation plan` uses that compact input in an isolated, tool-free
Orchestrator session to recommend `REVIEW`, `REPAIR`, `REPLAN`, `INVESTIGATE`,
`HOLD`, `HUMAN_REQUIRED` or `REPORT`. It evaluates saved Memory, past findings,
dependencies and limits rather than treating `REQUEST_CHANGES` as an automatic
repair instruction. The response includes sourced confirmed claims, assumptions,
unknowns, observation hash and reported usage. Source validation establishes
that cited fields exist, not that the reasoning is semantically correct.
This is an explicit paid operation requiring positive token, time and per-session
credit ceilings; there is no automatic planner call after every role.
`conversation watch` accepts `/plan TEXT` with its configured question ceilings.
Advice does not release the hold, alter a verdict or issue a Dispatch.
Existing automatic workflow behavior is unchanged.

In a held running flow, `/adopt` selects the last successful `/plan` result
as an **unapproved** versioned supplement. Only `REVIEW` after a saved successful
Coder or `REPAIR` after a saved independent `REQUEST_CHANGES` within the repair
ceiling can enter this path. `/approve` separately confirms that exact draft
within the unchanged contract; `/step` separately releases and executes it.
Neither planning nor adoption infers those choices. Changed observations,
Memory, targets, hold IDs and draft versions are rejected; a failed `/plan`
clears the console's cached proposal. Other recommendations require a separate
investigation, replanning or Human decision, not a coerced repair.

The same operation is available as `workflow planner-draft --file PATH`.
Its request contains `flowId`, `id`, `expectedDraftId`, `expectedChangeId`,
`target` from `workflow feedback-target`, `actor`, `source`, `reason`, and
`proposal` containing the exact JSON result from `conversation plan`.
The output stores observation times/hash, source result/SHA targets, Memory
versions, reasoning and uncertainty in the draft, without recursively embedding
full histories. Repeating identical IDs/content returns the original version;
reusing an ID with different content is rejected. Imported session IDs and
actor/source fields are supplied provenance, not authenticated provider/Human
identity. Semantic validity and contract preservation still require the explicit
Human supplement approval. Scope, acceptance, identities or budget revisions,
unattended Orchestrator-selected execution and new-request intake remain
incomplete.

Side-question observations also project prior result decisions in Task history,
Dispatch instructions and flow contracts: exact results and their source hashes
remain, but recursively repeated instructions are omitted. `projection` describes
this when present; instruction hashes still identify the original saved records,
which remain available through `task graph`. This uses no additional model call.

`conversation investigate` performs a separate, explicitly bounded investigation.
It checks the sourced configuration approval and fresh authentication/repository
access, then reads only explicitly selected UTF-8 committed files within the
saved scope and outside forbidden paths. The source is the last saved successful
Coder SHA (or the Task baseline), **not mutable HEAD or working files**.
`--path` is repeatable (1-16 files); `--max-bytes` is a required total source
ceiling up to 65536. Overflow, missing files and binary content are errors, not
truncated evidence. No shell commands, filters, external diff tools or model
tools are permitted. The isolated question retains its usual explicit time,
token and per-session credit ceilings and service admission check. New Git
evidence has its own SHA, observation interval and content hash; it never implies
the earlier role considered the file, ran tests or checked live execution.

The watched equivalent is `/investigate PATH QUESTION`, using
`--investigation-max-bytes` and the configured question ceilings.
`/apply REVIEWER 0,2 HUMAN-INSTRUCTION` explicitly selects zero-based confirmed
finding indices from the last successful question or investigation. Use `CODER`
only after a saved independent `REQUEST_CHANGES` within the repair ceiling.
The operation proposes an unapproved supplement, retaining selected claims,
their citations, uncertainty, source targets, Memory versions and bounded
investigation evidence. Unselected chat is not copied into the next instruction.
`/approve` and `/step` remain separate. Changed records, forged source bytes/hash,
invalid indices and incompatible roles are rejected. Failed questions clear the
cached question answer; no ordinary question is automatically applied.
`workflow question-draft --file PATH` accepts the same fields as `planner-draft`,
plus `nextRole`, `selected` (integer indices) and `humanInstruction`, with
`proposal` containing the exact `conversation ask|investigate` result. Imported
responses and session identifiers are supplied provenance, not proof of model
origin. Approval must still assess the selected reasoning and unchanged contract.

`workflow interrupt` takes `flowId`, `id`, `expectedChangeId`, the displayed
`target`, `actor`, `source` and `reason`. It atomically saves a targeted interruption
request and handoff hold. A running Dispatch becomes `CANCEL_REQUESTED`, without
inventing a stopped result or cancelling the Task. The owning runner observes
that state and performs runtime safe stop; only successful stop records
`runtime-stop:` evidence and a cancelled Dispatch. Failed stop stays unconfirmed
and blocks continuation. Issued/unstarted execution is held without fictitious
stop evidence. Exact input replay cannot interrupt another role; ended or stale
targets are rejected. Partial files and Git history are never rolled back.
If no runner owns the active session, attach its exact saved configuration with
`runtime stop`, then reconcile ownership before any newly authorized execution.

Watched `/interrupt` (or `いま止める`) is distinct from `/hold` and Task cancellation.
`conversation watch --interactive-execution` opts into accepting Human input
while its selected `/step` or `/auto` runs. It rejects a second owned run, reports
progress and execution failures, and keeps targeted hold/interruption operations
available. EOF or `/quit` safely interrupts **only this console's owned execution**
and waits for cleanup; another process's execution is not cancelled. Without the
flag, watched execution remains synchronous. Noninteractive workflow defaults
and the immutable flow ceilings are unchanged. Interruption stops the flow;
it is not permission to resume or to change the contract.

`runtime` and `workflow run|step` accept `--compact-context` to omit recursively
nested prior instructions from the role prompt. Exact prior results, findings,
validation evidence and source hashes remain; Human decisions, Memory and the
current contract remain intact. Every projected result decision is checked
against its actual saved Dispatch, not summarized by a model or silently
truncated. The full history remains available through `task graph`.
The option changes the system prompt and session configuration hash: use it on
fresh sessions and repeat it on attachment and safe stop. Existing sessions
retain their original options. This reduces repeated input, not a guarantee
about provider credits; there is no extra model call for context generation.

`config check` validates fixed Global/Project YAML settings and supported
environment overrides against the
versioned configuration schema. All fields below are required in the resolved
configuration. `version` is an
integer; the remaining values are nonempty strings without whitespace.
Unknown fields, duplicate keys, implicit non-string values, aliases, and
multiple documents are rejected.

The Global file is `taxion/config.yaml` under Go's `os.UserConfigDir()`:
`$XDG_CONFIG_HOME` or `~/.config` on Linux, `~/Library/Application Support`
on macOS, and `%AppData%` on Windows. The project file is
`.taxion/config.yaml` in the current working directory. Parent directories are
not searched. Missing default files are skipped; unreadable or invalid files
are errors. If no files are found, the command reports an error.

`--file`, `--user-file`, and `--project-file` have been removed. Move live
settings to the fixed Global or Project path and use supported `TAXION_*`
variables for environment-specific overrides. Removed options report an error
with migration guidance; they do not read the supplied path.
Every loaded settings file inside a Git worktree must be both untracked
(`git ls-files` returns no entry) and covered by a Git ignore rule. Tracked
settings and unignored files are rejected before reading their contents, even
when a later layer would override them. Symlink paths and their resolved
targets are both checked. Files outside worktrees do not require Git ignore
rules. Shared examples should be separate from the live settings files.

In a Git worktree, the entire cwd `.taxion/` directory requires an ignore rule,
even when only Global settings are loaded or the directory is absent.
Add `/.taxion/` to `.gitignore`, or use `.git/info/exclude` for a local-only
rule. A file-only `/.taxion/config.yaml` rule is insufficient. Tracked
descendants and effective ignore exceptions are rejected before loading
settings. The directory and configuration symlinks are checked at their
selected paths and resolved targets; a directory symlink needs an ignore rule
matching the link itself, such as `/.taxion`.

The cwd `.env`, when present, must also be untracked and ignored; its symlink
target is checked too. An indexed `.env` is rejected even if its working-tree
copy is missing. These checks run independently of whether environment
overrides are set. Taxion does not read dotenv files or infer where environment
values originated.

`.taxion/` holds local settings, workspace state, and cache. Shared
specifications, decisions, and reviewed Memory exports belong outside that
directory. Existing local Memory remains in its workspace store; shared Memory
storage and migration are still tracked in #69.
For already tracked local files, review the indexed paths before removing
them from the index and adding ignore rules. Ignore rules do not remove
tracking or existing history. Review exposed credentials and rotate them where
needed. Taxion reports failures and leaves Git state unchanged.

Precedence is supported environment values > Project > Global.
Every file requires `version: 1`; other fields can be omitted in a layer and
are inherited from lower layers. Explicit empty or null values are errors.
Each layer is checked before merging, so a higher-priority value cannot hide
an invalid lower-priority value. `--sources` prints each field's winning layer
without printing its value. An environment override is reported as
`environment`. At least one fixed YAML file is required;
environment values supplement the file settings.

| Environment variable | Configuration field |
| --- | --- |
| `TAXION_PROJECT_ID` | `project.id` |
| `TAXION_REPOSITORY_PROVIDER` | `repository.provider` |
| `TAXION_REPOSITORY_HOST` | `repository.host` |
| `TAXION_REPOSITORY_PATH` | `repository.path` |
| `TAXION_REPOSITORY_ACCOUNT` | `repository.account` |
| `TAXION_REPOSITORY_CREDENTIAL_REF` | `repository.credentialRef` |
| `TAXION_RUNTIME_PROVIDER` | `runtime.provider` |
| `TAXION_RUNTIME_ACCOUNT` | `runtime.account` |
| `TAXION_RUNTIME_CREDENTIAL_REF` | `runtime.credentialRef` |
| `TAXION_RUNTIME_CONTRACT_REF` | `runtime.contractRef` |
| `TAXION_EXECUTION_REQUESTER` | `execution.requester` |
| `TAXION_EXECUTION_WORKER` | `execution.worker` |
| `TAXION_EXECUTION_APPROVAL_REF` | `execution.approvalRef` |

Set environment values are strings, including numeric-looking identifiers.
An explicitly empty or invalid value is an error. Unset variables preserve
the lower layer. Only the listed variables
are read for configuration overrides; `TAXION_TOKEN`, `GH_TOKEN`,
`GITHUB_TOKEN`, and `COPILOT_GITHUB_TOKEN` retain their separate credential
uses and are not copied into this configuration.

mise is an optional way to inject environment values. A shared `mise.toml`
may reference a local `.env`:

```toml
[env]
_.file = ".env"
```

Keep the real `.env` untracked and covered by an ignore rule such as `/.env`.
The shared mise file should contain the loading reference and safe defaults,
not credential values. mise, the shell, or CI reads/injects the environment;
Taxion records only `environment` as its source. Arbitrary dotenv paths and
secrets copied into other files are not covered by this path policy.
These configuration checks are not commit or publication protection.

`git check-index` checks the complete tree in Git's actual index without
reading working-file contents or changing Git state. It rejects any path with
a `.taxion` component and any basename equal to `.env` or starting with
`.env.`, including nested paths, forced additions, indexed symlinks, and
unchanged tracked local files. Staged removal of these paths is allowed.
For shared, value-free examples, use names such as
`docs/environment.example` or `docs/taxion-config.example.yaml` outside
`.taxion/`; `.env.example` is intentionally covered by the prohibited pattern.

The command preserves Git's repository and index selection, including
`GIT_INDEX_FILE` used for partial commits and linked worktrees. Running from a
subdirectory still checks the whole index. Errors report category counts, not
file names or values; an unreadable or unmerged index fails the check.
Run it immediately before a manual commit, or invoke it from a pre-commit
hook. This command does not install a hook; managed controller commits invoke
the same checks directly as described below.
Only paths are checked: secrets copied into source files, a shared example,
or `mise.toml` require content scanning. Add `--secrets` for the local content
check described below.

`git check-index --secrets` requires a trusted
[Betterleaks 1.9.0](https://github.com/betterleaks/betterleaks/releases/tag/v1.9.0)
binary (`betterleaks` on PATH, or selected with `--scanner`). Verify the release
archive against its published checksums before installation. Taxion rejects
missing, incompatible, failed, or interrupted scanners and malformed reports.
No scanner package is added to Taxion's Go dependencies.

The scan reads blobs from the selected index, including unchanged
tracked content and symlink text, rather than working files or a staged diff.
Git replacement objects cannot substitute different content. Gitlinks contain
submodule commit IDs; files inside submodules require their own scan.
The local scanner receives blob content on stdin with the indexed path as
local rule metadata. It runs in
a private temporary directory with explicit version-bundled default rules,
no project config/ignore/baseline, no inherited credentials or scanner
configuration variables, and credential API validation disabled.
Inline allow comments are ignored. Reports and diagnostics are not forwarded;
errors expose only a finding count or a failed-check reason.

This check uses the index entries captured when the scan starts. Rerun after
changing the index; the command does not lock it across a later manual commit.
The two-minute overall timeout stops the check rather than accepting partial
coverage. Detection remains limited to the pinned rules and their filters;
low-entropy values, unsupported formats, encoded content, and false positives
need review. Bundled path filters exclude Go manifests/checksums, common lockfiles,
some vendor trees, images, and other binary formats. This avoids treating
dependency hashes as credentials, but secrets copied into an excluded file
remain outside this check's content coverage. Safe template acceptance and a
generated, unissued GitHub-token-shaped fixture were checked locally, without
API validation. Findings are not automatically allowlisted or treated as
proof that a credential is active.

Install the pre-commit hook explicitly from a persistent Taxion
binary:

```bash
taxion git hook install --scanner /absolute/path/to/betterleaks
taxion git hook status
taxion git hook uninstall
```

`install` without options finds Betterleaks on PATH and requires version
`1.9.0`; it does not fall back to path-only protection when the scanner is
missing. The full hook runs `git check-index --secrets` using its recorded
scanner. To explicitly accept only prohibited-path checks, use
`taxion git hook install --paths-only` instead. Combining `--paths-only` with
`--scanner` is an error.

The installer uses Git's effective hooks directory, including `core.hooksPath`
and linked worktrees; a shared hooks location affects every repository or
worktree using it. It creates a hook only when none exists and leaves custom,
edited, nonexecutable, and symlink hooks unchanged with an explicit error.
For an existing hook, integrate
`taxion git check-index --secrets --scanner /absolute/path/to/betterleaks` manually while
preserving its existing checks. Reinstallation of the identical hook is
idempotent; uninstall removes only the exact hook for the running binary.
An existing path-only hook is not upgraded automatically. To change modes or
scanner paths, explicitly uninstall the old managed hook and install the
desired one. Custom or edited hooks require manual review.

The hook records the Taxion and scanner absolute paths, including shell quoting.
Use the same installation path for status and uninstall. A moved or edited
installation requires manual review rather than automatic replacement.
Do not install using `go run`: its temporary executable will disappear.
Status recognizes the installed mode, checks that the hook and Taxion binary
are executable, and verifies the recorded scanner's version for full hooks.
Missing or incompatible scanners stop commits and make status fail.
Uninstall of an exact hook still works when its scanner has disappeared.
A missing Taxion binary also stops the commit. Installation currently
requires Unix executable permissions.

`--paths-only` explicitly accepts limited coverage and does not invoke the
content scanner. Full hooks retain the documented scanner rule and path-filter
limits. Both modes use Git's actual index, including partial commit indexes.
Local hooks can be skipped with `--no-verify`, replaced, or
disabled and are not carried into new clones. Managed controller commits
enforce their own checks independently of installed hooks.

`runtime commit` is a host operation for a running Coder Dispatch bound to the
same role, runtime configuration hash, and worktree. Use the same system prompt
and runtime options as the saved session. It does not start or resume an SDK
session, stage files, or mark the Task or Dispatch complete. The message file
must use the Dispatch's Conventional/Contextual policy.

The controller verifies the repository, branch, baseline, execution-start
ancestry, authored commit messages, and authorized/forbidden paths. It holds
the real index lock, ignores ambient Git redirection, copies the staged index
to a private snapshot, and scans all its blobs plus the proposed commit message.
The committed tree is that snapshot, not working-file content. Split indexes
and linked worktrees are supported. Concurrent branch movement makes the
compare-and-swap publication fail without replacing the external update.
Unmerged indexes, active merge/rebase operations, empty changes, invalid scope,
and unavailable or failed scanners stop the operation.

Publication uses `commit-tree` and `update-ref`, not Git's commit porcelain,
so project hooks are not executed. `commit.gpgsign=true` requests signing with
the configured Git signer; signing failures do not fall back to unsigned commits.
The real index and working files are left unchanged by Taxion. A failed branch
update can leave a scanned, unreachable commit object. A failed or interrupted
update reports the candidate SHA so the branch can be inspected before retrying.
Git publication and the
workspace database are not one transaction: a database failure after publication
reports the published SHA explicitly.

This is not a sandbox against other processes changing Git files directly.
It does not inspect earlier commits for secrets or enforce publication checks;
those remain in #56. Scanner rule/filter limits still apply. Independent
review and acceptance are still required after a successful managed commit.

Enable the Coder's `taxion_commit` SDK tool explicitly when creating a new
session:

```bash
taxion runtime start --dispatch <id> --system-prompt-file <path> \
  --managed-commit --scanner /absolute/path/to/betterleaks
```

Use the same `--managed-commit`, scanner, role prompt, and other runtime
options on `send`, `resume`, `events`, `stop`, and host `commit` for that session.
The tool binding includes the Dispatch ID, resolved absolute scanner path,
and managed-operation version in the configuration hash. A session started
without the tool cannot silently acquire it on resume. `--tools taxion_commit`
is not a substitute for the host binding.
Keep the scanner's absolute path: a missing executable at that saved path
does not prevent attaching to stop the session, but every commit revalidates
its availability and version and fails if it is unavailable.

Managed stage and commit tools are preloaded rather than hidden behind tool
search. Auto may select a model whose write tool is `apply_patch` rather than
`create`/`edit`; initial tool metadata does not guarantee the post-selection
surface. For scoped Coder edits, explicitly allow
`--tools view,create,edit,apply_patch` (or the corresponding `--coder-tools`).
Permissions still check every write against the assigned worktree; stage and
commit separately enforce the Dispatch scope. Native final-response JSON
formatting and the controller's strict result verification remain enabled.

`workflow run --managed-commit --scanner PATH` supplies the same tool only to
Coder sessions; Reviewer sessions receive none. The tool accepts just a
complete `message` string, not a repository, branch, scanner, or Dispatch
selected by the agent. The SDK registers it on creation and resume without
skipping permission checks. Its handler checks the active root session and
the controller verifies that invoking session against the saved Dispatch.
Host cancellation and saved review-flow time/known-usage ceilings apply.
Scanner and authorization failures remain failed tool calls.

The commit tool uses **already-staged content only**. To let a new Coder session
stage explicitly selected files, also pass `--managed-stage`:

```bash
taxion runtime start --dispatch <id> --system-prompt-file <path> \
  --managed-commit --managed-stage --scanner /absolute/path/to/betterleaks
```

`workflow run` accepts the same pair of options. `--managed-stage` requires
`--managed-commit`; repeat both when attaching to that session. Staging changes
the operation version and tool surface in the configuration hash, so an
existing commit-only session cannot acquire staging on resume.

`taxion_stage` accepts only `{"paths":["src/file.go","src/deleted.go"]}`:
unique, canonical repository-relative file paths or tracked deletions.
It does not expand wildcards or pathspec syntax, recurse through directories,
force-add ignored files, or select Git metadata. Existing prohibited local
files cannot be added; their tracked deletions can be staged when authorized.
Parent symlinks outside the
worktree and special files are rejected. Symlink files are staged as link text,
not the target's contents. Deleted files are supported even when their parent
directories no longer exist.

The controller uses the same bound session, active Task and flow limits as
commit. It copies and locks the index, stages only those files in the private
copy, and checks the resulting full tree's scope, prohibited paths and contents.
This preserves unselected staged files. Successful staging flushes and
atomically replaces the real index with the checked copy; it creates no commit
and leaves working files and HEAD unchanged. Failed checks leave the real index
unchanged. Branch/HEAD or direct index changes detected before publication stop
the operation. Commit rechecks the snapshot rather than trusting a prior stage.

Git attributes and configured clean filters apply normally; filtered blobs,
not merely working contents, are scanned. Git repository configuration and
filter executables remain trusted host inputs, not a sandboxed tool surface.
Staging can write blobs to Git's local object database before a rejected scan;
it does not erase those unreachable objects or claim to prevent all local
storage of secret values. Git-file races by hostile host processes and the
scanner's documented detection/filter limits remain outside the guarantees.

Registered SDK handlers were checked through controller and actual Git for
stage→commit locally. Arbitrary shell commands and sub-agent staging/commit
calls are not enabled. Live SDK stage→commit→independent review and negative
stage refusals are recorded in #72. Conversational and repair/re-review
acceptance remains in #53/#54.

```yaml
version: 1
project:
  id: project-a
repository:
  provider: github
  host: github.com
  path: owner/application
  account: developer-a
  credentialRef: gh:active
runtime:
  provider: copilot
  account: developer-b
  credentialRef: copilot:logged-in
  contractRef: company-contract
execution:
  requester: developer-a
  worker: local-worker
  approvalRef: human-decision-1
```

`project.id` identifies the Taxion project. Repository `provider`, `host`,
and `path` identify its source-code location; `account` names the expected
operation identity. Runtime `account` independently names the expected Copilot
identity, and `contractRef` identifies the intended usage contract.
`credentialRef` values refer to credentials held separately, rather than
containing tokens. Execution fields identify the requester, worker, and sourced
approval reference.

Authentication belongs to the provider tools: Copilot CLI for AI execution,
`gh` for GitHub, and `glab` for the planned GitLab adapter. Taxion does not
implement OAuth, a credential vault, token renewal or direct parsing of their
authentication files. Users log in through those tools; CI/worker environments
inject provider-supported credentials. Explicit `env:NAME` selection only
passes a credential to the provider, without persisting it in Taxion.
GitLab delegation is an accepted design, not an implemented adapter.
The planned `glab` adapter includes self-hosted GitLab: it must check the
explicit API host, account, namespace/repository and permissions without
falling back to another host. API and Git clone endpoints are distinct;
a nonstandard clone SSH port is not an API port. Internal deployment
identifiers must remain outside public records.

Taxion owns sourced work approval, expected-account and target checks, execution
limits and observations. Provider login is not approval to perform a Taxion task.
Service eligibility and billing decisions remain with the provider; official API
observations and Human-approved usage grounds must be distinguished. Taxion will
not implement an independent contract-authentication or billing system.

The execution contract distinguishes these facts:

| Fact | Required evidence and current boundary |
| --- | --- |
| Authentication | A fresh provider query confirms the selected effective account. Possessing a credential alone does not prove a match. |
| Authorization | The current Task and immutable Dispatch instruction carry the same sourced approval for the complete selected tuple. Provider access is checked separately. |
| AI eligibility | The provider determines service eligibility. Selected Dispatches check the official session quota API's explicit account access/capacity verdicts alongside their sourced Human approval. `contractRef` names the intended agreement; it does not select an SDK billing agreement or prove contractual eligibility. Independent contract authentication is out of scope. |
| Contractual payer | The party responsible for payment comes from the agreement or an authorized external record, not the account name or token owner. Provider payer/invoice information is unavailable through the implemented adapter. |
| Measured use | Tokens, model cost multipliers and AI credits are distinct quantities. Complete private SDK usage receipts can report measured credits; missing or incomplete reports are unconfirmed, never zero or free. The optional SDK credit ceiling is per session and soft, not a project budget or bill. |

Keep eligibility evidence, payer and measured use separate when approving an
experiment. An entitlement projection or a successful model response alone
does not establish which agreement paid for it. Configuration approval records
the allowed combination; it does not manufacture missing provider billing data.

`config check --capabilities` reports static implementation coverage without
credentials or selected identities. It can be combined with `--sources` and
approval checks; the default output is unchanged. It performs no network queries.

| Selected repository + runtime | Repository access gate | Runtime | Contract/payer verification |
| --- | --- | --- | --- |
| GitHub.com + Copilot | `GITHUB_COM_GH_API` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| GitHub Enterprise + Copilot | `UNIMPLEMENTED` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| GitLab + Copilot | `UNIMPLEMENTED` | `COPILOT_SDK` | `UNIMPLEMENTED` |
| Bitbucket + Copilot | `UNIMPLEMENTED` | `COPILOT_SDK` | `UNIMPLEMENTED` |

An implemented adapter still needs a supported credential reference, the
installed provider CLI, live access and current approval. Static coverage
does not grant execution; unsupported repository combinations stop at the
runtime gate.

The schema accepts `github`, `gitlab`, or `bitbucket` repository identities,
including nested namespace paths, and `copilot` runtime identities. This
describes configuration, not implemented adapter coverage. Checking a file
performs no network calls or credential resolution and does not validate
approval authority, account access, or contract eligibility. Runtime and workflow
commands bind the selected tuple and check its recorded approval, effective
Copilot account, repository permissions and provider service state as described
below. Contractual eligibility and payer remain unverified; the responsibility
boundary is tracked in [#69](https://github.com/hiono/taxion/issues/69).
TOML and JSON configuration inputs are future extensions; existing JSON APIs
and machine artifacts keep their current formats.

`config check --task <task-id>` additionally resolves `execution.approvalRef`
against a sourced decision in that Task in the selected workspace. Its `text`
holds a structured execution-configuration approval:

```json
{
  "kind": "taxion.execution-config.v1",
  "taskId": "task-id",
  "reason": "Human approved these identities and this contract for the task",
  "allowed": [
    {
      "version": 1,
      "project": {"id": "project-a"},
      "repository": {
        "provider": "github", "host": "github.com", "path": "owner/application",
        "account": "developer-a", "credentialRef": "gh:active"
      },
      "runtime": {
        "provider": "copilot", "account": "developer-b",
        "credentialRef": "copilot:logged-in", "contractRef": "company-contract"
      },
      "execution": {
        "requester": "developer-a", "worker": "local-worker",
        "approvalRef": "human-decision-1"
      }
    }
  ]
}
```

Store this object as the JSON string in the Task decision's `text`, with
`id: "human-decision-1"` and its actual Human decision `source`, using the
existing `task track --file` contract. Each `allowed` entry is a complete
configuration tuple; accounts and contracts from different entries cannot be
mixed. The Task repository path and approval reference must match, and pending
Human decisions and terminal Tasks are rejected. Checking preserves the Task
state. A recorded decision is local authorization evidence: this command does
not authenticate its author, validate external credentials, or authorize a
runtime operation. Runtime operations perform the independent checks below.

The Human approval's `reason` records the intended use and its approved basis
for the selected account and `contractRef`; its `source` identifies the actual
decision. Start/resume/send require this same sourced record in both the current
Task and issued Dispatch, before and after provider checks. A reference alone
or a successful provider query is not Human approval. Taxion checks the record
and tuple, not the legal truth of a free-text reason, and cannot identify a payer
from the provider quota response.

`config check --dispatch <dispatch-id>` checks the same fixed Global/Project
files and environment against both the current Task approval and the saved
Dispatch instruction. The approval must have been included when that Dispatch
was issued; a later Task approval cannot expand an existing Dispatch's
configuration choices. It rejects missing or stopped Dispatches, inactive
Tasks, mismatched repositories, and combinations absent from the saved
approval. `--task` and `--dispatch` are mutually exclusive.

### Saved-record side questions

```bash
taxion conversation ask --task <task-id> \
  --question "Which evidence supports the current review result?" \
  --max-tokens 20000 --timeout 2m --max-ai-credits 30
```

This is a paid, separately bounded Orchestrator session with **no tools**.
It verifies the Task's sourced configuration approval, effective Copilot
account, repository read access and provider service state, and rechecks
approval before sending and while waiting. The selected tuple must also match
the Task's issued configuration selections. All three ceilings are explicit:
token use is checked after reported calls, time limits waiting, and the SDK's
credit limit is a per-session soft ceiling. None guarantees a hard total bill.

The response separates `confirmed`, `assumptions` and `unconfirmed`. Confirmed
claims cite resolvable JSON pointers into the supplied saved Task, Dispatch,
Memory versions or review-flow records. They describe recorded evidence, not
independent verification of its truth. The context includes observation times
and a content hash. If records change while answering, the original context is
retained and `recordsChangedDuringAnswer` is true; the answer is not silently
retargeted. The context is limited to the selected Task and its flows.

Questions do not bind a Dispatch, update the graph, resolve feedback, edit
instructions, acquire Human approval, or enter Coder/Reviewer histories.
The result contains the separate session ID and reported token usage even on
a failed attempt, allowing private SDK credit reconciliation before another
paid attempt. Missing usage, invalid citations, provider failures and exceeded
ceilings are errors. The question session is stopped even after cancellation;
cleanup failures remain visible. Inputs over 128 KiB and final answers over
64 KiB are rejected rather than silently truncated.

This command reads saved records only. It does not perform additional live
investigation, hold/release a handoff, or apply an answer to an instruction.
Use the explicit `workflow handoff`, feedback and versioned draft operations
for those changes, or the watched console below. Request-to-plan conversation
and additional investigation remain part of M12's unfinished acceptance.

### Watching and explicitly continuing a saved flow

```bash
taxion conversation watch --flow <flow-id> --actor <human-id> \
  --source <actual-input-source> \
  --question-max-tokens 20000 --question-timeout 2m \
  --question-max-ai-credits 30 -- \
  --coder-prompt-file <path> --reviewer-prompt-file <path> \
  --coder-tools view,create,edit,apply_patch --reviewer-tools view \
  --max-ai-credits 30 --managed-commit --managed-stage \
  --strict-result-identities \
  --scanner /absolute/path/to/betterleaks
```

Opening the console persists a handoff hold **before reading input**. It blocks
the next role, not an already executing role. EOF, input errors and `/quit`
leave the hold unchanged. A timeout is never approval. Actor/source are supplied
decision metadata, not proof of an authenticated Human identity.

`/step`, `/continue` or `続行` explicitly release the displayed boundary and
execute at most one role turn. `workflow step` exposes the same shared runner
without the console. Unlike `workflow run`, a successful step saves the role's
result and releases the runner claim without issuing the next role. The next
explicit step consumes that result, dispatches the authorized role, or records
completion if review and acceptance already suffice. The console restores the
hold after a step while the flow remains running.

`/auto` explicitly selects the existing flow's bounded automatic mode. It does
not extend the saved deadline, token budget or repair limit. Existing
non-interactive `workflow run` behavior is unchanged. During a selected run,
event output is streamed; a second console/process can persist a hold while
the existing role continues. A hold is not cancellation; use `runtime stop`
to request and confirm interruption of the exact saved session.

`/draft TEXT` records or revises a versioned supplemental proposal. `/approve`
approves only the displayed pending proposal within the unchanged contract,
and `/discard` explicitly abandons it. Approval does not release the hold or
issue a Dispatch; later explicit continuation publishes the supplement under
the existing transaction checks. Changed scope, acceptance, identity, contract
or limits are not reauthorized by supplementary approval. A proposal before a
successful saved result cannot yet be approved.

Other text (or `/ask TEXT`) goes to the independent saved-record question
session, requiring the separate per-question ceilings. It does not become a
draft or approval. `/hold` or `保留` retains the handoff hold. Continuation checks
the displayed Dispatch target, role, result, latest draft, contract hash and
hold version transactionally; a changed target is rejected, not attached to
the new role. Command failures are printed explicitly and never interpreted as
consent. The console does not yet implement changed-contract reapproval,
natural-language cancellation or request-to-plan intake.

`--strict-result-identities` is available on `runtime` and `workflow run|step`.
It pins the final response schema's Dispatch ID, Task ID, role, repository and
execution-start SHA to the immutable instruction (including the repair start
SHA). It never guesses an output SHA, approval or verdict, and the controller
still independently verifies all reported facts. This option changes the SDK
configuration hash; repeat it when attaching or safely stopping such a session.
The default schema is unchanged so earlier sessions remain attachable with their
original options. A rejected response is not silently repaired or treated as
successful work.

`config bind --dispatch <dispatch-id>` saves one approved complete tuple, its
decision reference, field origins and recording time with the Dispatch in
SQLite. The current Task and issued instruction must authorize that tuple.
After selection, `config check --dispatch` and `runtime auth` reject another
tuple even if the same approval allowed it. Repeating the binding for identical
values preserves the original origins and time; changing origins alone does
not replace the recorded selection.

Binding requires an executable Dispatch before its first runtime session.
Existing sessions without a selection cannot be retrofitted; issue a newly
authorized Dispatch. Legacy records remain readable with no selection.
This command records candidate selection only: it does not authenticate actors,
resolve credentials or establish contract eligibility. Runtime execution uses
the selection in its SDK session hash as described below.

`config check --dispatch` uses one read-only workspace snapshot and does not
create a session or authenticate its actors. Its successful output names
those unchecked boundaries explicitly.

`runtime auth --dispatch <id> --system-prompt-file <path>` resolves those same
fixed settings and requires the current Task and saved Dispatch approvals
before contacting Copilot. It uses the runtime's `--cli`, `--base-directory`,
and assigned worktree, but only calls the SDK authentication-status RPC:
no session is created or resumed and no model request is sent. The reported
account must match `runtime.account` (case-insensitively for GitHub accounts),
and the authentication host must be explicitly `github.com` or the SDK's HTTPS
github.com origin (with an optional trailing slash). Other schemes, ports,
paths, user information, query strings and fragments are rejected. Missing fields,
unauthenticated status, a mismatch, and unavailable status are errors.

For selected configurations, `runtime.credentialRef: copilot:logged-in` selects
the SDK's stored-user authentication with ambient Copilot/GitHub token variables
removed from the child environment. `env:UPPERCASE_NAME` selects exactly that
environment credential, disables stored-user fallback and rejects missing,
empty or malformed values before launching the SDK client. Offline adapter
construction still succeeds so safe stop can save cancellation intent even
after credential loss. If attaching to the saved session fails, the Dispatch
remains `CANCEL_REQUESTED`; this is not confirmation that the runtime stopped.
Its value is never stored in the configuration or
session hash. Change the value under the same reference by constructing a new
adapter; the effective account and existing approvals are still checked.
Legacy arbitrary references retain the previous ambient SDK selection for
compatibility; their credential-store resolution is not implemented. These
policies do not verify contract eligibility or billing. Provider status messages and error
details are not included in returned authentication errors. Results are
point-in-time checks, are not persisted as execution authorization, and do
not change existing session hashes. Start/resume/send and safe stop remain
separate from the explicit probe.

`runtime start|resume|send|events|commit` now resolve and check the fixed
configuration before creating an adapter. A fresh Dispatch saves its approved
selection automatically if not already bound. An existing session without a
selection must be safely stopped and replaced with a newly authorized
Dispatch; it cannot silently migrate to a new configuration.

Shared runtime/workflow worker construction supplies the saved complete tuple
to the adapter. It becomes part of the session configuration hash; field
origins and recording time do not. Start, resume and each instruction send
check current/saved approvals and verify the effective Copilot account and
repository API access before SDK session work. Approval is checked again after these checks, when saving
a new session binding and before marking execution running. Selected
Dispatches also recheck their tuple and approval inside the managed Git
authorization transaction. These checks do not establish contract eligibility
or resolve arbitrary protected credential-store references.

Selected Dispatches also query the session's provider quota API after creation
or attachment, and again before every instruction send. The Copilot adapter
requires a successfully acquired account snapshot with explicit allowed (or
quota-not-required) access and available, unlimited, not-required or
not-applicable capacity. Missing observations, denied access, exhaustion,
unknown states and query failures stop execution; numeric balances, plan names
and model catalogs do not substitute for these verdicts. A rejected new session
is disconnected before binding; resumed sessions remain bound and can be safely
stopped. Admission observations from earlier turns are not reused as account
snapshots. This model-free check is a point-in-time provider observation, not a
guarantee that the next request will be admitted, a billing contract check or a
usage ceiling. Provider errors during the actual request remain authoritative.
The existing access receipts still describe only the preceding authentication
and repository checks; no service or payer verification is inferred from them.
Configuration-less legacy controllers retain their previous behavior.

Repository access currently supports GitHub.com through `gh api`. Set
`repository.credentialRef: gh:active` to use the stored `gh` login, with ambient
GitHub/Copilot tokens and host overrides removed from the subprocess environment.
`env:UPPERCASE_NAME` selects that credential alone as `GH_TOKEN`, without stored-login
fallback. Missing or malformed values and unsupported references are errors.
Each check queries `user` and then the exact repository, requiring the expected
`repository.account`, matching `full_name`, and explicit read permission.
Coder also requires explicit write permission; other roles require read permission.
Unavailable permissions, identity mismatches, missing `gh`, provider errors and
unsupported hosts/providers stop execution. GitLab, Bitbucket and GitHub Enterprise
remain schema-only targets for this gate.

These read-only API observations are not proof of the identity used by Git SSH/HTTPS
push or the author of a local commit, and do not publish anything. Successful
checks are appended to the local Dispatch's `configuration.accessChecks`,
with the requested operation, runtime configuration hash, observation interval,
role permission and `contractEligibility: UNVERIFIED`. The immutable selection
identifies the expected repository/runtime accounts, project, requester,
credential and contract references and approval. A receipt means these access
checks matched; it does not mean an SDK session was created or execution succeeded.
Failed access checks append no successful receipt and return an error. Approval is rechecked
in the recording transaction. The first receipt also fixes the checked runtime
hash before session creation, preventing competing configurations from sharing
a Dispatch. A different runtime configuration requires a newly authorized Dispatch.

Receipts are observations, not reusable authorization; start/resume/send repeat
the external checks even after restart. Older records without `accessChecks`
remain readable and carry no evidence of a prior check. Selection and session
hash formats are unchanged. `task graph` and `task plan`
include these records through the saved Dispatch. Keep them local with the
workspace database; they are not a public usage or billing receipt. Safe stop
does not require repository access. The explicit `runtime auth` probe still
checks only Copilot authentication. A read-only live gate check is available with
the following command; it makes no model calls:

```bash
TAXION_GITHUB_INTEGRATION=1 \
TAXION_TEST_GITHUB_ACCOUNT=<account> \
TAXION_TEST_GITHUB_REPOSITORY=<owner/repo> \
go test ./internal/forge -run '^TestLiveGitHubRepositoryAccess$'
```

`runtime stop` uses the saved tuple and original prompt/tool/scanner settings,
without loading current fixed settings or requiring the authentication probe.
It attempts to attach and stop even after approval or settings are revoked;
SDK attachment errors remain explicit. Legacy unselected session hashes are
preserved for safe stop. Unconfigured local/proc controller usage is unchanged.

`workflow run` applies the same selection and authentication gate to every
worker. Each generated Task needs an approval naming that Task in its issued
instruction. A parent Task's approval is not silently rewritten for a generated
Reviewer Task; missing Task-specific authorization stops the flow. Authorizing
named review Tasks uses the original approval's optional `additionalTaskIds`.
For a flow with ID `application-flow` and `maxRepairs: 1`, the Human approval
can name both possible review Tasks before Dispatch issuance by adding this
field to the complete approval object shown above (with `taskId` set to the
implementation Task ID):

```json
{
  "additionalTaskIds": ["application-flow-review-0", "application-flow-review-1"]
}
```

Keep `allowed` as complete configuration objects. The exact review IDs are `<flow-id>-review-0` through
`<flow-id>-review-<maxRepairs>`; a repair Coder retains the implementation Task
ID. List only the review Tasks intended to be authorized. The list rejects
empty IDs, whitespace, patterns, duplicates and the primary Task ID. An omitted
list keeps the previous single-Task scope. Another flow, an unlisted re-review,
or a similarly prefixed ID is not authorized.

The controller copies the sourced decision unchanged into generated review
Tasks and Dispatch instructions. The core and configuration layer verify exact
Task membership and the same complete allowed tuples. This authorizes
configuration selection only: workflow ceilings, review scope, role isolation
and per-operation authentication still apply. Adding names after issuance
does not expand an existing Dispatch; record the Human authorization before
starting a new flow.

Runtime commands require an active Task Graph Dispatch and a role-specific
system prompt file. The Task's worktree and Dispatch recipient select the
runtime role and workspace. The saved Dispatch snapshot is appended to the
role prompt as the session's durable contract, so it remains part of the system
context across turns and SDK compaction. SDK tools require an explicit
`--tools` allowlist. The SDK permission handler grants Coder file writes within
the assigned worktree and one-time use of allowlisted SDK tools; Reviewer
sessions receive individually approved read access to that worktree. Relative read paths resolve
against the assigned worktree, including a missing leaf under
an existing in-worktree directory so the tool can report file absence normally.
Dangling symlinks, escaping paths and unresolved parent directories are rejected.
The handler applies to SDK permission requests; host and external tools use their
own security controls. Resume verifies that the prompt, CLI, worktree, and tool configuration
match the persisted hash.

`runtime` commands and `workflow run` accept `--max-ai-credits` for an
optional SDK session soft ceiling. Zero (the default) leaves it unset; other
values must be finite and at least 30. Start and resume supply the same limit
to the SDK, and the value participates in the saved session hash. Repeat it
when sending, resuming, observing, committing or stopping that session.
Omitting or changing it cannot silently alter an existing binding.

The SDK measures credits after responses, so the final response can exceed
the ceiling before further model calls are blocked. Credits and token counts
are separate units. On `workflow run`, the flag applies independently to each
Coder or Reviewer session; it is **not an aggregate workflow budget**. A shared
experiment ceiling requires separate cumulative accounting and conservative
per-session allocations. Existing workflow token, time and repair ceilings
remain unchanged. Do not publish personal billing receipts in repository
documentation or execution evidence.

`task plan --repo <repo>` reconstructs follow-up actions from the SQLite Task
Graph and referenced Memory. It retains deferred Tasks, their sources and resume
conditions, existing decisions, and each Dispatch's issued contract and result.
Actions distinguish unresolved Human decisions, deferred-work reassessment,
evidence collection, review reconciliation, and execution candidates. Generating
a plan preserves the saved Task states and authorization. The same output is
available through `GET /api/tasks/plan?repo=<repo>` and ACP `taxion/plan` with
`{"repository":"<repo>"}`, including after a session is recreated.

`workflow start` saves an authorized Coder contract with explicit repair,
elapsed-time and token ceilings, then issues its initial Dispatch.
`workflow advance` uses the latest verified result to issue an independent
Reviewer Dispatch or a repair with the same acceptance and sourced findings.
Approval of the current output SHA records the implementation Task as
COMPLETED. `workflow run` creates isolated Coder/Reviewer SDK adapters and
executes successive Dispatches from SQLite without copying instructions or
results. It checks recorded usage after usage events, monitors contract and
Memory changes, and stops runtime execution at the saved deadline or observed
token ceiling. Role-specific `--coder-tools` and `--reviewer-tools` remain
explicit allowlists. A durable runner claim prevents overlapping runs; an
interrupted process's claim requires explicit reconciliation before reuse.
After confirming the old process has ended and recording completion or
cancellation for its Dispatches, `workflow recover --file <path>` compares the
observed runner ID and records the actor, reason and evidence before releasing
ownership. Saved ceilings and outcomes continue to govern subsequent actions.
See [Task Graph](docs/10-task-graph.md) for authorization and stop conditions.
`workflow handoff --file <path>` records a sourced `HOLD` or `RELEASE` for an
existing flow, comparing `expectedChangeId` with its latest saved handoff change.
Hold prevents subsequent Dispatch issuance and unstarted execution, but preserves
the current role's authorized work and final result. A runner returns a handoff-held
error and releases its claim at that boundary without cancelling the current result.
Explicit release permits later `workflow run` or `advance` under the unchanged
contract and ceilings; it neither starts work itself nor restores a safety-stopped flow.
The full request format and race limitations are in the Task Graph reference.
`workflow feedback-target --id <flow-id>` reads the latest saved Dispatch's
instruction and result identity. `workflow feedback --file <path>` compares that
exact target and records a sourced Human response together with a handoff hold
in one workspace transaction. It preserves the original result, verdict and
authorization; the text is not automatically sent to a worker or treated as an
approved contract change. A stale target fails explicitly without recording
feedback or a hold; use `workflow handoff` separately when the next handoff must
be held regardless of target changes.
Unresolved feedback blocks release of a running flow. `workflow feedback-resolve`
records an explicit sourced `KEEP_CONTRACT` decision against a freshly observed
target and hold ID. Resolve each pending feedback before requesting release;
resolution itself neither releases the hold nor sends a prompt. This first
disposition preserves the approved contract and does not implement application
of feedback to a new instruction. Releasing a safety-stopped flow does not
restore execution authority.
`workflow draft` saves an unissued instruction proposal with a new ID, an
expected previous draft ID, the observed target and current hold ID. It retains
each version's text, proposed next role and operation, evidence, unconfirmed
items and feedback references. The latest undiscarded draft also blocks release.
`workflow draft-discard` records a sourced decision to abandon that exact draft;
it does not resolve feedback or release the hold. These commands do not yet
approve, apply or publish the proposal as a Dispatch by themselves.
`workflow draft-approve` records `APPROVE_SUPPLEMENT` for the latest draft at a
successful Coder-to-review or review-to-repair boundary. It binds the exact draft,
result, hold and unchanged flow contract. After explicit release, `advance` or
`run` includes the approved supplement in the next Dispatch's `DECISIONS` and
records its instruction hash atomically. Referenced feedback is shown as
`APPROVED_FOR_HANDOFF` before issuance and `INSTRUCTION_PUBLISHED` afterward;
neither state proves that a worker has performed the requested checks.
Scope, acceptance, role, execution configuration and budget changes are not
authorized by supplementary approval. Natural-language intervention, changed
contract reapproval and side questions
remain tracked in #73 and #74.
Controller-managed Coder Git operations and live SDK acceptance remain tracked
in #53 and #54.

For a Reviewer Dispatch, the Task's worktree must be a new absolute path.
Start it with `--review-source-worktree <existing-repository-worktree>` to
create an independent detached worktree at the saved target SHA. Finalization
closes the SDK connection and removes the clean worktree before saving review
success. Dirty, locked, or unreleased worktrees leave the review incomplete
with an explicit error.

Usage events record the selected model and available token/cache counts in
SQLite; missing SDK measurements remain unknown. Infinite-session compaction
is enabled, and its context and outcome events are reported.

The Task's `validationCommands` authorize the checks run by the controller.
New Coder Dispatches require Conventional Commits subjects and contextual
commit bodies. The body records actual reasoning using `intent(scope):`,
`decision(scope):`, `rejected(scope):`, `constraint(scope):` or `learned(scope):`
action lines, followed by optional Git trailers. The controller checks every
commit after the saved execution-start SHA before accepting the result; the
independent Reviewer checks whether its reasoning matches the work.
At the end of a runtime turn, the controller checks the structured response,
verifies repository/worktree/SHA/scope against Git, executes those commands in
the assigned worktree, and saves the observed output and completion in SQLite.
Validation commands run as `sh -c` with the controller's host permissions;
approve their effects when authorizing the Task. The explicit
`dispatch transition --result-file` path performs the same verification and
validation, with a ten-minute default `--validation-timeout`.

The CLI is local-first. `taxion serve` provides the HTTP API, `taxion acp`
provides ACP over stdio, and `web/` provides the browser console.
