# Contributing

## Local setup

`go.mod` declares the required Go version, and `mise.toml` pins the matching
Go toolchain. Install the repository tools before building:

```bash
mise install
go version
go test ./...
```

Use `go test ./...` for the full suite. Before opening a pull request, run the
format, vet, test and build checks below. A change to behavior also needs a
guardrail and a regression test.

1. Pick one small roadmap item (`docs/04-roadmap.md`).
2. Keep `internal/core` stdlib-only (SDK-001). Put integrations under `internal/runtime/<name>/`.
3. Every behavior change needs a guardrail ID + test.
4. No personal / internal / secret data in commits (PRIV-001). Check `git diff` before push.
5. Verify locally:
   ```bash
   test -z "$(gofmt -l cmd internal)" && go vet ./... && go test ./... && go build ./...
   ```

## Technical descriptions

Use Japanese prose for Issue bodies and Milestone descriptions. Keep
established technical terms, feature names, and identifiers in their
existing form: Memory, sanitizer, rate limiting, lockout, and audit
hash-chain do not need invented translations. Use the actual command
names and flags, and describe what the operation does rather than
substituting a metaphor or an approximate term.

Distinguish proposals, recorded decisions, and current implementation.
Preserve historical requirements and results; add a clarification when
they could be mistaken for current behavior. Check related Issues,
documentation, code comments, and displayed text before marking wording
maintenance complete.

## Where information belongs

| Location | Content | Do not use it for |
| --- | --- | --- |
| `README.md` | Project overview, minimal setup, links to the reference documents | Detailed specifications or work reports |
| `docs/user-guide.md` | User installation and a first approved Coder/Reviewer flow | Complete option inventory |
| `docs/configuration.md` | Current config paths, precedence, environment variables and preflight limits | Credential storage or proof of contract eligibility |
| `docs/cli-reference.md` | CLI operations and their execution boundaries | Product requirements or design rationale |
| `docs/00-charter.md` | Founding scope and project constraints | A current feature inventory |
| `docs/01-guardrails.md` | Required invariants and their enforcement points | Claims that an unimplemented requirement is enforced |
| `docs/02-privacy-provenance.md` | Rules for public information and review | Individual cleanup reports |
| `docs/03-runtime-contract.md` | Runtime contract and current adapter boundaries | Runtime evaluation history |
| `docs/04-roadmap.md` | Planned work and links to completed work | Detailed implementation results |
| `docs/05-ledger-schema.md` | Wire format, API requests, responses, and validation limits | API design rationale |
| `docs/06-remote-access.md` | Serving configuration and connection procedures | Authentication design decisions |
| `docs/07-audit-log.md` | Audit fields, verification limits, and retention procedures | Audit implementation history |
| `docs/08-memory-model.md` | Current Memory storage, scopes, promotion, search, and export | The original proposed model |
| `docs/09-orchestration-requirements.md` | Accepted product requirements, staged acceptance, and implementation gaps | Current command reference or completion evidence |
| `docs/10-task-graph.md` | Current Task Graph commands, persistence, transition checks and limits | Runtime execution claims or Memory design rationale |
| `docs/adr/` | Decisions, alternatives, reasons, and subsequent clarifications | Duplicated current command references |
| `docs/14-decision-traceability.md` | Links from ADRs and milestones to implementation issues and current reference documents | A replacement for issue-level evidence or the current behavior references |
| `web/README.md` | Web console setup and proxy behavior | A second API specification |
| `SECURITY.md` | Private reporting, controls, and known limits | A promise of complete protection |
| Issues / PRs / Milestones | Scope, progress, implementation results, and completion evidence | The only reference for current usage |
| Code comments | API contracts and reasons for non-obvious local decisions | Work history, usage guides, or narration of visible code |

Link to the owning document instead of copying its explanation. Put an
implementation clarification beside a historical ADR when its assumptions
were incorrect; put current behavior in the reference document. Do not
silently rewrite the original decision or leave contradictory versions
without identifying which describes the current implementation.

## Model-free regression and recovery evidence

File-access approval, retained-tree review and cancellation-only attachment
are separate decisions in ADRs 0026–0028. Changes in these areas must preserve
FILE-001, SHA-001, STOP-001, LED-002 and BUD-001 in `docs/01-guardrails.md`.
Run the bounded regressions before any signed-in coding/review acceptance:

```bash
go test ./cmd/taxion ./internal/runtime/copilot ./internal/runtime/controller \
  -run 'TestUserGuideColdStartExamplesWithoutSDK|TestStopOnly|TestSuspendedResultCanBeCancelled|TestReviewFlowUsesUnchangedExplicitMultiTaskConfigurationApproval'
go test ./internal/gitproof ./internal/core
```

These tests use temporary workspaces, actual local Git and a synthetic SDK
protocol peer; they do not send model instructions. Signed-in SDK integration
tests remain opt-in. Local regression success does not establish account
eligibility, provider billing limits, or full coding/review acceptance.

Skill regressions use independently written synthetic instructions in temporary
directories and the repository-owned skill-only plugin fixture. Do not require
named external writing skills, personal home paths, global installations, login,
network access or a paid model in ordinary tests. Test role-bound approval,
multiple assets, create/resume bindings, the `skill` tool surface, success/failure
evidence and rejection of missing, changed or unapproved assets:

```bash
go test ./internal/tools ./internal/runtime/copilot ./internal/runtime/controller \
  -run 'TestAssetsApprovalsAndPluginSurface|TestApprovedSkillsWithoutModelUse|TestSkillToolRequiresApprovedAssets|TestSDKToolsCreateResumeRoleAndImmutableHash|TestReviewFlowPreflightsFutureSkillAssetsWithoutModelUse'
```

For local native plugin discovery only, explicitly opt in with
`TAXION_TEST_LOCAL_PLUGIN_DISCOVERY=1 go test ./internal/runtime/copilot -run TestLocalPluginDiscoveryWithoutModelUse`.
It sends no model instructions; a missing CLI fails this requested check.
Neither synthetic hook results nor native discovery prove real skill loading
or document quality. Issue #78 separately requires an authorized real document
session, selected skill identities, loading results, inspection evidence and
independent review of the same artifact. External skills are operator-supplied
dogfood inputs, not Taxion dependencies; do not copy their bodies into fixtures.

For a persisted incident, preserve a consistent SQLite backup and original
worktree before applying recovery. Check SDK cancellation and the subsequent
workspace transaction separately: cancellation can succeed while result
persistence fails. Keep suspension and cancellation receipts, artifact hashes
and observed usage; do not rewrite the old contract or budget to manufacture
success. Private databases, raw logs and credential values stay outside commits.
Publish the generalized finding and chosen boundary in the ADR, current
behavior in its owning reference, and operator actions in the user guide.

## Budget-aware instructions (design)

Budget-aware instruction design is accepted in
[ADR-0029](docs/adr/0029-separate-budget-guidance-from-provider-quota.md), not yet
implemented. Keep flow-token accounting, session soft credit ceilings, context
capacity and account quotas separate. Provider-specific quota APIs and billing
types belong in adapters; unknown or stale observations must remain explicit.
Test defaults and per-request precedence, units, partial accounting, quota
failures, account binding, bounded non-model reads and unchanged stop gates with
synthetic fixtures before adding a signed-in acceptance. Dynamic observations
belong in send receipts, not mutations of the bound system prompt. Do not add
OpenCode to supported configuration before its adapter contract is implemented.

## Code comments

Comments explain why a constraint, order, boundary, or tradeoff is needed.
Read the surrounding code and its callers before adding a reason; do not
invent intent from the implementation alone. Remove comments that merely
repeat a name, assignment, branch, test assertion, or sequence of calls.
English comments receive the same review as Japanese prose.

Keep Go documentation for exported APIs and packages, including return
values, defaults, ownership, and limitations that callers cannot infer
from a signature. Those are contracts, not line-by-line narration.
Keep protocol-required behavior and non-obvious test-fixture choices close
to the relevant code. Longer design rationale belongs in an ADR, linked
from the local constraint when useful.

## Release

Do not publish `v0.1.0` until M15 Issue #79 and release Issue #78 meet their
acceptance conditions. The release notes in `docs/release-notes-0.1.0.md`
remain a draft until the clean-workspace dogfood and paid-runtime checks are
approved and complete.

Before tagging, run the full verification commands from Local setup, check
`git diff` and `git diff --cached` for private data, and confirm the release
notes match the verified behavior and known limits. Then create and push the
tag, and publish that exact tag as a GitHub Release:

```bash
git tag -a v0.1.0 -m "Taxion 0.1.0"
git push origin v0.1.0
gh release create v0.1.0 --title "Taxion 0.1.0" \
  --notes-file docs/release-notes-0.1.0.md
gh release view v0.1.0
```

Test the published installation separately with the Go version and `go:`
backend command in the release notes. Do not claim installation verification
until that command succeeds against the published tag.
