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
| JC-221 | Strict Planner Result JSON | Final Result contains exactly one valid JobContract JSON object | Decode and persist the exact contract with Result provenance |
| JC-222 | Unknown / non-canonical key | Result JSON contains unknown key or incorrect key casing | Reject without mutation |
| JC-223 | Duplicate JSON key | Duplicate root or nested budget key | Reject without mutation |
| JC-224 | Trailing content | Result contains a second JSON value, prose, or Markdown fence | Reject without mutation |
| JC-225 | Invalid JSON shape | Malformed JSON, non-object root, or field type mismatch | Reject without mutation |
| JC-226 | Separate caller contract | Caller attempts to submit a JobContract independently of the persisted Result | No such Controller input is accepted |
| JC-227 | Planning Controller entrypoint | Planner calls `PlanningController.ProposeJobContract` with the persisted final Result ID | Delegate to strict Result decoding and append the bound revision |
| JC-228 | Active Job amendment | ACTIVE Job, exact latest approved revision / hash, authenticated HUMAN Principal, and non-empty reason | Append HUMAN_AMENDMENT revision and transition atomically to DRAFT |
| JC-229 | Amendment base mismatch | Supplied revision or canonical hash differs from the latest approved revision | Reject without mutation |
| JC-230 | Amendment authority | DRAFT Job, non-HUMAN Principal, or missing / invalid reason | Reject without mutation |
| JC-231 | Reapproval after amendment | HUMAN_AMENDMENT revision appended | Prior approval remains historical; new revision is DRAFT and requires a new JobApproval |
| JC-232 | Amendment idempotency | Same CommandID and identical base / contract / reason / Principal | Return original receipt and revision |
| JC-233 | Amendment idempotency conflict | Same CommandID with changed base / contract / reason / Principal | Reject with idempotency conflict |
| JC-234 | Amendment side-effect boundary | Human amendment command is accepted | No ExecutionPermission or Provider operation is created |
| JC-235 | Amendment CLI ingress | OIDC-authenticated `taxion job amend` with exact revision / hash / reason and strict JSON contract file | Append HUMAN_AMENDMENT and return DRAFT |
| JC-236 | Amendment CLI validation | Missing flags, invalid digest, invalid UTF-8, oversized file, or malformed JSON | Reject without opening / mutating Job Store |
| JC-237 | Amendment CLI retry | Same CommandID / request retried after OIDC reauthentication and later reapproval | Return original amendment fact without duplicate revision |
| JC-238 | Fresh amendment authentication | HUMAN Principal authenticated before the current JobApproval | Reject without mutation |

## Upper-level Planner workflow tests

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| JC-239 | Completed Planner workflow | Exact Taskless PLANNER Turn is COMPLETED, Usage is complete, and no Result is persisted | Persist final Result first, then append JobContractRevision bound to that Result |
| JC-240 | Existing final Result | Exact final Result observation already exists for the same Job / RoleExecution / Session / Turn | Reuse the persisted Result; do not call Provider Result again |
| JC-241 | Turn not completed | Turn observation is missing, UNKNOWN, or FAILED | Reject before Provider Result retrieval; no JobContract mutation |
| JC-242 | Usage incomplete | Usage is missing or latest observation has Complete=false | Reject before Provider Result retrieval; no JobContract mutation |
| JC-243 | Turn identity mismatch | Turn, RoleExecution, Session, or operation differs | Reject without Provider call or JobContract mutation |
| JC-244 | Proposal persistence failure | Final Result append succeeds but JobContract Store command fails | Keep the Result observation; no partial JobContract revision |
| JC-245 | Retry after proposal failure | Retry with the same CommandID after Result persistence | Reuse the exact Result observation and append at most one revision |
| JC-246 | Exact proposal replay | Retry after successful proposal with the same CommandID and identities | Return the original receipt and revision without Provider Result retrieval |
| JC-247 | Changed Result on retry | Reuse CommandID with a different Result observation identity | Reject with idempotency conflict; preserve original provenance |

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

## ExecutionPermission tests

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| JC-248 | Exact approved scope | ACTIVE Job, exact latest approval, target resource, assessable reversible effect | Append one permission bound to approval / revision / OperationID / request digest |
| JC-249 | Missing or stale approval | DRAFT Job, missing approval, older approval, or mismatched canonical hash | Reject without mutation |
| JC-250 | Stale contract revision | Contract revision changes after permission issuance | Permission revalidation rejects before effect |
| JC-251 | Expired permission | Current time is at or after ExpiresAt or contract deadline | Reject without effect |
| JC-252 | Foreign resource | Resource is not an exact target repository / target resource / workspace | Reject without mutation |
| JC-253 | Resource prefix confusion | Resource shares a prefix or host with an approved resource but is not equal | Reject without mutation |
| JC-254 | Prohibited effect | Effect appears in ProhibitedEffects | Reject without mutation |
| JC-255 | Approval-required effect | Effect appears in ApprovalRequiredEffects or assessor requires human approval | Reject without mutation |
| JC-256 | Unknown but contained effect | Unknown effect name with trusted assessment proving reversibility or containment and no approval requirement | Allow only the exact operation |
| JC-257 | Unassessable effect | Missing / invalid assessment or impact is neither reversible nor contained | Reject without mutation |
| JC-258 | Provider identity mismatch | Configuration digest differs from approved configuration reference | Reject without mutation |
| JC-259 | Budget upper boundary | Reservation exactly reaches MaxTurns / MaxTokens | Accept |
| JC-260 | Budget overflow | Reservation exceeds either approved limit | Reject without mutation |
| JC-261 | Usage reconciliation | Completed Usage replaces reservation; partial / missing Usage retains reservation | Recompute budget without double-counting |
| JC-262 | Exact permission replay | Same CommandID and identical request | Return original receipt and permission |
| JC-263 | Permission idempotency conflict | Same CommandID with changed effect / resource / provider / digest | Reject without mutation |
| JC-264 | Permission operation binding | Reuse permission with another OperationID or request digest | Reject before effect |
| JC-265 | Store rollback | Inject failure while appending permission / receipt | No partial permission or receipt |
| JC-266 | Restart restoration | Reopen Store after permission append | Permission and approval provenance remain valid |
| JC-267 | Concurrent budget race | Two requests reserve the last budget concurrently | At most one commits; loser sees current revision / budget and is rejected |
| JC-268 | Permission authority separation | Successful permission issuance | No Provider call, JobApproval mutation, or JobContract revision |