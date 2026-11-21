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
