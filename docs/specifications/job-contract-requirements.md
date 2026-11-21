# Job Contract and Approval Requirements

## 1. Purpose

A Job Contract is the immutable, human-reviewable statement of the work a Job may perform. Planner output is a proposal, not execution authority. A JobApproval binds a human decision to one exact JobContract revision.

## 2. Contract contents

Each JobContract revision records:

- purpose and constraints
- target repositories and target resources
- workspaces / work area
- acceptance criteria and required evidence
- budget and concurrency ceiling
- deadline
- prohibited effects and approval-required effects
- a secret-free configuration reference

The initial budget dimensions are `MaxTurns` and `MaxTokens`, matching the current Planning / Provider usage model. Monetary limits are not represented until Taxion defines a currency and cost-accounting contract; they must not be inferred from token limits.

The configuration reference is represented by a SHA-256 digest. Secret values, credentials, access tokens, and private configuration contents must not be stored in the contract.

## 3. Revisions and canonical identity

- A Job may contain multiple immutable JobContract revisions.
- Revision numbers start at 1 and increase without gaps.
- Canonicalization version 1 trims text fields, sorts set-valued string collections, rejects duplicates, normalizes the deadline to UTC, and serializes a fixed struct with deterministic JSON field order.
- The canonical hash is SHA-256 over the canonicalization version and canonical contract content.
- A revision stores both the canonicalization version and canonical hash.
- A DRAFT Planner proposal requires an accepted Start and a still-valid PlanningAuthorization.
- ACTIVE Job amendments are rejected by the DRAFT proposal command. A future amendment command must carry separate authority, append a new revision, return the Job to DRAFT, and require a new JobApproval. Until that authority path exists, do not reuse an old Planner execution to amend an ACTIVE Job.

## 4. JobApproval

A JobApproval is an append-only fact bound to the latest JobContract revision and its exact canonicalization version and hash.

It records:

- Job ID and contract revision
- canonicalization version and canonical hash
- authenticated HUMAN Principal ID
- identity source (the authenticated OIDC issuer)
- human-provided reason
- server-generated approval time

Only a HUMAN Principal may approve. The reason is required. An approval for an older revision, a different hash, or a different canonicalization version does not authorize the current contract.

Approving the latest revision transitions the Job from DRAFT to ACTIVE. ACTIVE requires a matching JobApproval for the latest revision. A new contract revision invalidates current approval by returning the Job to DRAFT.

## 5. Authority boundary

JobApproval is distinct from PlanningAuthorization and from ExecutionPermission. Approval records human acceptance of a contract; it does not by itself grant a provider capability, credential access, or permission for a prohibited / approval-required effect. Execution controllers must later verify the current JobApproval, scope, budget, deadline, Provider identity, and effect boundary.

## 6. Planner proposal Controller requirements

The Controller must require a validated AuthenticatedPrincipal matching the PlanningAuthorization owner, a DRAFT Job, and the Taskless PLANNER RoleExecution bound to that grant. The referenced execution must have an ACCEPTED Provider Start observation. New proposals must revalidate the grant deadline immediately before persistence.

The Controller must persist the proposal only through the JobAggregate Store command using the current expected revision. It must not call the Provider or create JobApproval / ExecutionPermission. Exact idempotent replay of an already committed proposal is allowed after grant expiry only when the same CommandID and canonical request are supplied; changed payload or proposer must be rejected.

## 7. References

- ADR-201: Job aggregate and command boundary
- ADR-202: Domain invariants across state paths
- ADR-500: Job authority and prohibited effects
- ADR-501: Trusted ingress and human Principal
- Product Contract §4.3
- Product Acceptance AT-004
