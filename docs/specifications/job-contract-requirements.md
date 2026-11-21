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

## 7. Human approval ingress requirements

The approval ingress must use the existing OIDC Trusted Ingress and accept only a validated AuthenticatedPrincipal. It must require the caller to provide the exact Job ID, contract revision, canonical hash, canonicalization version, and a non-empty human reason. It must not silently select or approve the latest revision.

The Controller must verify that the supplied identity matches the latest immutable revision and persist approval only through the JobAggregate Store command. The Store generates the approval timestamp and atomically appends JobApproval while transitioning DRAFT to ACTIVE. A different hash, version, revision, duplicate approval, invalid Principal, or changed payload under a reused CommandID must be rejected without mutation.

An exact retry of an already committed approval may return its original receipt after the Job becomes ACTIVE. JobApproval records human acceptance only; it does not create ExecutionPermission, authorize Provider execution, or bypass effect / resource checks.

## 8. Planner Result provenance requirements

A Planner proposal must identify the exact final Provider Result from which its contract was derived. The revision must append the Result observation ID, Result content SHA-256, and producing RoleExecution ID as immutable provenance.

The Controller must accept only a Result observation already persisted in the same JobAggregate that:
- is final and passes the Result observation's digest and identity validation;
- belongs to the same Taskless PLANNER RoleExecution supplied for the proposal;
- belongs to that execution's accepted Provider Start / Session and exact Turn identity;
- is not an observation from another Job, RoleExecution, Session, or Turn.

The Store / Domain mutation boundary must revalidate the exact Result observation ID and digest against the current aggregate in the same transaction that appends the JobContractRevision. A caller-supplied digest without a matching persisted Result observation is insufficient. The canonical JobContract hash remains distinct from the source Result content digest.

Exact proposal replay must preserve the original Result provenance. Reuse of the CommandID with a different Result ID, Result digest, RoleExecution, or contract must fail without mutation. A later Result observation must not silently replace the source of an existing revision.

## 9. References

- ADR-201: Job aggregate and command boundary
- ADR-202: Domain invariants across state paths
- ADR-500: Job authority and prohibited effects
- ADR-501: Trusted ingress and human Principal
- Product Contract §4.3
- Product Acceptance AT-004
