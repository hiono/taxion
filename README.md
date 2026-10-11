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
taxion workflow recover --file <recovery-confirmation.json>
taxion dispatch --task <id> --repo <repo>
taxion runtime start --dispatch <id> --system-prompt-file <path>
taxion runtime send --dispatch <id> --system-prompt-file <path> --prompt-file <path>
taxion runtime resume --dispatch <id> --system-prompt-file <path>
taxion runtime events --dispatch <id> --system-prompt-file <path>
taxion runtime stop --dispatch <id> --system-prompt-file <path>
taxion runtime commit --dispatch <id> --system-prompt-file <path> --message-file <path> --scanner <path>
taxion review --task <id> --sha <coder-sha> --repo <repo>
```

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

`workflow run --managed-commit --scanner PATH` supplies the same tool only to
Coder sessions; Reviewer sessions receive none. The tool accepts just a
complete `message` string, not a repository, branch, scanner, or Dispatch
selected by the agent. The SDK registers it on creation and resume without
skipping permission checks. Its handler checks the active root session and
the controller verifies that invoking session against the saved Dispatch.
Host cancellation and saved review-flow time/known-usage ceilings apply.
Scanner and authorization failures remain failed tool calls.

The tool commits **already-staged content only**. Managed staging and signed-in
SDK acceptance remain in #53/#54/#72; arbitrary shell commands, sub-agent commit
calls, and automatic staging are not enabled. The registered SDK handler,
controller, and actual Git tree were checked together locally without a
signed-in Copilot request.

```yaml
version: 1
project:
  id: project-a
repository:
  provider: github
  host: github.com
  path: owner/application
  account: developer-a
  credentialRef: scm-credential
runtime:
  provider: copilot
  account: developer-b
  credentialRef: ai-credential
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

The schema accepts `github`, `gitlab`, or `bitbucket` repository identities,
including nested namespace paths, and `copilot` runtime identities. This
describes configuration, not implemented adapter coverage. Checking a file
performs no network calls or credential resolution and does not validate
approval authority, account access, or contract eligibility. Existing runtime
and workflow commands do not yet consume this configuration. Automatic
durable execution bindings and authentication
preflight remain tracked in [#69](https://github.com/hiono/taxion/issues/69).
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
        "account": "developer-a", "credentialRef": "scm-credential"
      },
      "runtime": {
        "provider": "copilot", "account": "developer-b",
        "credentialRef": "ai-credential", "contractRef": "company-contract"
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
runtime operation. Those gates and durable runtime bindings remain in #69.

Runtime commands require an active Task Graph Dispatch and a role-specific
system prompt file. The Task's worktree and Dispatch recipient select the
runtime role and workspace. The saved Dispatch snapshot is appended to the
role prompt as the session's durable contract, so it remains part of the system
context across turns and SDK compaction. SDK tools require an explicit
`--tools` allowlist. The SDK permission handler grants Coder file writes within
the assigned worktree and one-time use of allowlisted SDK tools; Reviewer
sessions receive read access to that worktree. The handler applies to SDK
permission requests, while host and external tools use their own security
controls. Resume verifies that the prompt, CLI, worktree, and tool configuration
match the persisted hash.

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
