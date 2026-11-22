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
- The DRAFT Planner proposal command rejects ACTIVE Jobs and cannot reuse a prior Planner execution to amend one.
- An ACTIVE Job amendment is a separate authenticated HUMAN command. It must identify the exact current contract revision and canonical hash, include a non-empty reason, and use a validated HUMAN Principal. The Principal authentication timestamp must be at or after the current JobApproval time; an older authenticated context cannot amend the approved scope.
- The amendment command is accepted only when the Job is ACTIVE and the exact latest revision has a matching JobApproval. It appends a new immutable JobContractRevision with origin `HUMAN_AMENDMENT`, authenticated issuer / subject / Principal ID, reason, and the superseded revision / hash.
- The amendment command atomically transitions the Job to DRAFT. Previous JobApproval facts remain historical and never authorize the new revision. A new JobApproval is required before the Job returns to ACTIVE.
- A human amendment does not create ExecutionPermission, start a Provider operation, or grant Planner / Coder / Reviewer execution authority.

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

## 6. ExecutionPermission requirements

An ExecutionPermission is an immutable, one-operation authorization fact. It is not a reusable Job-wide grant and is not implied by JobApproval, ACTIVE state, Observation, Conformance, or Admission.

The execution request identifies:
- Job ID and a stable CommandID / OperationID
- execution Role (`CODER` or `REVIEWER`)
- exact effect and resource identity
- Provider ID, account ID, and configuration SHA-256
- requested token reservation and requested expiry
- a canonical request digest

The Controller must:
- require an ACTIVE Job and the exact latest JobContractRevision / JobApproval pair;
- verify the approval's Job ID, revision, canonicalization version, canonical hash, and deadline;
- bind the permission to the exact JobApproval ID, revision, canonical hash, OperationID, effect, resource, Role, Provider / account / configuration, request digest, token reservation, and expiry;
- require the configuration digest to equal the approved JobContract configuration reference;
- require the resource identity to exactly match one approved target repository, target resource, or workspace;
- reject effects listed in prohibited effects or approval-required effects;
- use a trusted effect assessor to evaluate reversibility, containment, and approval requirement. An unknown effect name is not rejected solely because it is unknown; it is denied when its impact cannot be assessed or contained;
- reserve one turn and the requested token amount against the approved MaxTurns / MaxTokens budget, counting existing permissions and their observed Usage without double-counting reservations;
- fail closed when approval, scope, provider identity, assessment evidence, deadline, or budget cannot be verified.

The Store must repeat all JobAggregate-dependent checks in the same transaction that appends the permission. Exact CommandID replay returns the original permission; changed request identity or digest is an idempotency conflict. A permission is valid only for its exact OperationID and request digest. A later contract revision or expired deadline invalidates its use.

A permission does not itself call a Provider or prove that an effect occurred. Provider execution must consume the exact permission before the external side effect; the execution integration and runtime containment remain separate gates.

## 7. Planner proposal Controller requirements

The Controller must require a validated AuthenticatedPrincipal matching the PlanningAuthorization owner, a DRAFT Job, and the Taskless PLANNER RoleExecution bound to that grant. The referenced execution must have an ACCEPTED Provider Start observation. New proposals must revalidate the grant deadline immediately before persistence.

The Controller must persist the proposal only through the JobAggregate Store command using the current expected revision. It must not call the Provider or create JobApproval / ExecutionPermission. Exact idempotent replay of an already committed proposal is allowed after grant expiry only when the same CommandID and canonical request are supplied; changed payload or proposer must be rejected.

## 7. Human approval ingress requirements

The approval ingress must use the existing OIDC Trusted Ingress and accept only a validated AuthenticatedPrincipal. It must require the caller to provide the exact Job ID, contract revision, canonical hash, canonicalization version, and a non-empty human reason. It must not silently select or approve the latest revision.

The Controller must verify that the supplied identity matches the latest immutable revision and persist approval only through the JobAggregate Store command. The Store generates the approval timestamp and atomically appends JobApproval while transitioning DRAFT to ACTIVE. A different hash, version, revision, duplicate approval, invalid Principal, or changed payload under a reused CommandID must be rejected without mutation.

An exact retry of an already committed approval may return its original receipt after the Job becomes ACTIVE. JobApproval records human acceptance only; it does not create ExecutionPermission, authorize Provider execution, or bypass effect / resource checks.

## 8. Planner Result provenance requirements

A Planner proposal must identify the exact final Provider Result from which its contract was derived. The revision must append the Result observation ID, Result content SHA-256, and producing RoleExecution ID as immutable provenance.

The Planner's final Result content must be exactly one UTF-8 JSON object representing JobContract, using the defined lower_snake_case JSON field names and nested `budget` object. The Controller derives the contract from this persisted Result; callers must not submit a separate contract payload. Reject malformed JSON, non-object roots, unknown or duplicate keys (including nested keys), trailing JSON values, and fenced Markdown / surrounding prose. Apply the normal JobContract canonicalization and validation after decoding.

The Controller must accept only a Result observation already persisted in the same JobAggregate that:
- is final and passes the Result observation's digest and identity validation;
- belongs to the same Taskless PLANNER RoleExecution supplied for the proposal;
- belongs to that execution's accepted Provider Start / Session and exact Turn identity;
- is not an observation from another Job, RoleExecution, Session, or Turn.

The Store / Domain mutation boundary must revalidate the exact Result observation ID and digest against the current aggregate in the same transaction that appends the JobContractRevision. A caller-supplied digest without a matching persisted Result observation is insufficient. The canonical JobContract hash remains distinct from the source Result content digest. A human amendment is a distinct revision origin and records authenticated HUMAN provenance plus the exact prior approved revision / hash; it does not pretend to have a Planner Result source.

Exact proposal replay must preserve the original Result provenance. Reuse of the CommandID with a different Result ID, Result digest, RoleExecution, or contract must fail without mutation. A later Result observation must not silently replace the source of an existing revision.

## 9. Planner workflow orchestration

The upper-level Planner workflow must invoke the JobContract proposal only after the exact planning Turn is terminal and its final Provider Result has been persisted.

Before proposing, the workflow must verify that the Turn belongs to the same Job, Taskless PLANNER RoleExecution, accepted Provider Start Session, and Provider operation. The latest Turn observation must be COMPLETED, and the latest Usage observation for that same operation / Session / Turn must be Complete=true. UNKNOWN, FAILED, missing, mismatched, or incomplete observations must stop the proposal without creating a JobContract revision.

The workflow must retrieve and persist the final Provider Result when no matching Result observation exists, then call PlanningController.ProposeJobContract with that exact persisted Result observation ID. If Result persistence succeeds but proposal persistence fails, retain the Result observation. A retry must reuse the same persisted Result identity and must not fetch or substitute a different Result.

The workflow must not issue JobApproval, ExecutionPermission, or start another Provider operation. Proposal retries must use the same CommandID and exact Result provenance; changed identities or payloads must be rejected by the existing idempotency boundary.

## 10. References

- ADR-201: Job aggregate and command boundary
- ADR-202: Domain invariants across state paths
- ADR-500: Job authority and prohibited effects
- ADR-501: Trusted ingress and human Principal
- Product Contract §4.3
- Product Acceptance AT-004
