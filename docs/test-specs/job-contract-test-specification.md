# Job Contract Test Specification

## Domain tests

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| JC-001 | Canonical stability | Same contract with set-valued slices in different orders | Same canonical content and hash |
| JC-002 | Canonical sensitivity | Change purpose, scope, budget, deadline, effect boundary, or configuration digest | Hash changes |
| JC-003 | Canonicalization version | Change version | Hash preimage and revision identity differ |
| JC-004 | Invalid contract | Missing purpose / scope / workspace / acceptance / evidence / budget / deadline / config digest | Reject |
| JC-005 | Duplicate set values | Duplicate after trimming | Reject |
| JC-006 | Revision sequence | Append revisions 1, 2, 3 | Accept contiguous order |
| JC-007 | Revision proposer | Nonexistent or non-PLANNER RoleExecution | Reject |
| JC-008 | Approval binding | Approve latest revision with exact hash and version | Accept and set Job ACTIVE |
| JC-009 | Stale approval | Older revision, wrong hash, or wrong canonicalization version | Reject without mutation |
| JC-010 | HUMAN Principal | Agent / invalid Principal attempts approval | Reject |
| JC-011 | Approval provenance | Principal ID does not match issuer and subject | Reject |
| JC-012 | Approval reason | Empty, invalid UTF-8, or over 2,000 bytes | Reject |
| JC-013 | Active invariant | ACTIVE Job without approval for latest revision | Reject Job validation |
| JC-014 | ACTIVE amendment gate | Attempt a revision with the prior DRAFT Planner authority after approval | Reject; ACTIVE state and prior approval remain unchanged |
| JC-016 | Planning grant deadline | Propose after the referenced PlanningAuthorization deadline | Reject before appending a revision |
| JC-015 | Append-only | Mutate or remove a stored revision / approval | Reject Job validation |
| JC-017 | Result provenance required | Propose without a source Result observation | Reject |
| JC-018 | Result belongs to another Job | Supply a Result ID from another Job | Reject without mutation |
| JC-019 | Result RoleExecution mismatch | Supply a Result from a different RoleExecution in the same Job | Reject without mutation |
| JC-020 | Result identity mismatch | Alter Result ID or content digest | Reject without mutation |
| JC-021 | Non-final Result | Supply a non-final Result observation | Reject without mutation |
| JC-022 | Result Start / Session / Turn mismatch | Result does not trace to the proposer's accepted Start and Turn | Reject without mutation |
| JC-023 | Result digest tampering | Stored Result content no longer matches its digest | Reject Job validation / restoration |
| JC-024 | Immutable source provenance | Mutate or replace source Result ID / digest after revision append | Reject Job validation |
| JC-025 | Canonical hash separation | Contract canonical hash differs from source Result content digest | Preserve and validate both independently |

## Store tests

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| JC-101 | Contract revision transaction | Add revision with current expected Job revision | Receipt and fact committed atomically |
| JC-102 | Contract revision idempotency | Replay same CommandID and request | Original receipt and fact returned |
| JC-103 | Contract revision conflict | Reuse CommandID with different request | Idempotency conflict; no mutation |
| JC-104 | Approval transaction | Approve latest revision | Approval appended and Job becomes ACTIVE atomically |
| JC-105 | Approval replay | Replay same CommandID | Original receipt and approval returned |
| JC-106 | Duplicate approval | New CommandID for already approved revision | Reject; no mutation |
| JC-107 | Stale expected revision | Add revision / approval against stale Job revision | Reject; no mutation |
| JC-108 | Restart restoration | Close and reopen Store after revision and approval | Facts, hash, state, and receipts preserved |
| JC-109 | Revision after approval | Add a new revision to ACTIVE Job | State changes to DRAFT in same transaction |
| JC-110 | Rollback | Inject failure before aggregate commit | No partial contract / approval fact or receipt |
| JC-111 | Source Result atomic binding | Append revision with an exact persisted final Result ID / digest | Revision and receipt commit atomically |
| JC-112 | Source Result absent | Supply unknown Result ID | Reject; no revision or receipt |
| JC-113 | Source Result mismatch | Reuse Result ID with wrong digest or RoleExecution | Reject; no mutation |
| JC-114 | Source Result replay conflict | Reuse CommandID with another Result ID / digest | Idempotency conflict; original provenance remains unchanged |
| JC-115 | Source Result restoration | Restart Store after proposal | Source Result ID / digest and RoleExecution binding remain valid |

## Controller tests

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| JC-201 | Authorized proposal | Matching HUMAN Principal, DRAFT Job, current grant, Taskless PLANNER, accepted Start, exact final Result | Append one revision through Store |
| JC-202 | Principal mismatch | Different HUMAN Principal from the grant owner | Reject without mutation |
| JC-203 | Missing accepted Start | PLANNER execution without ACCEPTED observation | Reject without mutation |
| JC-204 | Expired grant | New proposal after PlanningAuthorization deadline | Reject without mutation |
| JC-205 | ACTIVE Job | New proposal after approval | Reject without mutation |
| JC-206 | Exact replay after expiry | Same CommandID, canonical request, and source Result provenance for an existing revision | Return original receipt and revision |
| JC-207 | Idempotency conflict | Reuse CommandID with changed contract, Result ID / digest, or proposer | Reject without mutation |
| JC-208 | Authority separation | Successful proposal | Job remains DRAFT; no JobApproval or ExecutionPermission is created |
| JC-218 | Planner Result binding | Result is final and belongs to exact Planner execution | Proposal records exact Result ID and digest |
| JC-219 | Foreign Result | Result belongs to another Job or RoleExecution | Reject without mutation |
| JC-220 | Invalid Result evidence | Result is non-final or digest / Start / Session / Turn binding is invalid | Reject without mutation |

## Approval Controller and CLI tests

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| JC-209 | Exact approval | Authenticated HUMAN Principal and exact latest revision / hash / version | Append JobApproval and transition DRAFT to ACTIVE |
| JC-210 | Stale or altered proposal | Wrong revision, hash, or canonicalization version | Reject without mutation |
| JC-211 | Approval reason | Empty or invalid reason | Reject without mutation |
| JC-212 | Exact approval replay | Same CommandID and identical payload after ACTIVE transition | Return original receipt and approval |
| JC-213 | Approval CommandID conflict | Reuse CommandID with different Principal or payload | Reject without mutation |
| JC-214 | Duplicate approval | New CommandID for an already approved revision | Reject without mutation |
| JC-215 | Authority separation | Successful approval | No ExecutionPermission or Provider operation is created |
| JC-216 | CLI authentication | Missing / invalid OIDC Principal | Reject before opening or mutating the Store |
| JC-217 | Explicit revision confirmation | CLI request omits revision, hash, version, or reason | Reject; do not auto-approve latest |
