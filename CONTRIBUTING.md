# Contributing

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
| `docs/adr/` | Decisions, alternatives, reasons, and subsequent clarifications | Duplicated current command references |
| `web/README.md` | Web console setup and proxy behavior | A second API specification |
| `SECURITY.md` | Private reporting, controls, and known limits | A promise of complete protection |
| Issues / PRs / Milestones | Scope, progress, implementation results, and completion evidence | The only reference for current usage |
| Code comments | API contracts and reasons for non-obvious local decisions | Work history, usage guides, or narration of visible code |

Link to the owning document instead of copying its explanation. Put an
implementation clarification beside a historical ADR when its assumptions
were incorrect; put current behavior in the reference document. Do not
silently rewrite the original decision or leave contradictory versions
without identifying which describes the current implementation.

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
