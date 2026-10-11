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
taxion config check --file <config.yaml>
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
taxion review --task <id> --sha <coder-sha> --repo <repo>
```

`config check` validates default or explicit YAML settings against the
versioned configuration schema. All fields below are required in the resolved
configuration. `version` is an
integer; the remaining values are nonempty strings without whitespace.
Unknown fields, duplicate keys, implicit non-string values, aliases, and
multiple documents are rejected.

By default, the user file is `taxion/config.yaml` under Go's `os.UserConfigDir()`:
`$XDG_CONFIG_HOME` or `~/.config` on Linux, `~/Library/Application Support`
on macOS, and `%AppData%` on Windows. The project file is
`.taxion/config.yaml` in the current working directory. Parent directories are
not searched. Missing default files are skipped; unreadable or invalid files
are errors. If no files are found, the command reports an error.

Optional `--user-file` and `--project-file` replace their respective default
paths. A missing explicit file is an error. `--file` supplies an optional
highest-priority invocation file.
Precedence is `--file` (invocation) > supported environment values >
`--project-file` > `--user-file`.
Every file requires `version: 1`; other fields can be omitted in a layer and
are inherited from lower layers. Explicit empty or null values are errors.
Each layer is checked before merging, so a higher-priority value cannot hide
an invalid lower-priority value. `--sources` prints each field's winning layer
without printing its value. An environment override is reported as
`environment`. At least one default or explicit YAML file is required;
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
An explicitly empty or invalid value is an error even when `--file` overrides
that field. Unset variables preserve the lower layer. Only the listed variables
are read for configuration overrides; `TAXION_TOKEN`, `GH_TOKEN`,
`GITHUB_TOKEN`, and `COPILOT_GITHUB_TOKEN` retain their separate credential
uses and are not copied into this configuration.

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
