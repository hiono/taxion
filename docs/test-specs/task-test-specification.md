# Task Test Specification

| ID | Scenario | Input / setup | Expected result |
|---|---|---|---|
| TK-001 | Create under current approval | ACTIVE Job, exact latest JobApproval, valid Task request | Append one immutable Task bound to current approval |
| TK-002 | DRAFT Job | Job is DRAFT | Reject without mutation |
| TK-003 | Missing / stale approval | No approval or approval for older revision / wrong hash | Reject without mutation |
| TK-004 | Expired contract | Current time at or after JobContract deadline | Reject without mutation |
| TK-005 | Inherited scope | Omit target repository / resource / workspace lists | Persist exact effective parent scope |
| TK-006 | Narrower scope | Explicit target lists are exact subsets | Accept and persist canonical effective scope |
| TK-007 | Foreign resource | Resource / repository / workspace not in parent scope | Reject without mutation |
| TK-008 | Prefix confusion | URI shares host or prefix but is not exactly in scope | Reject without mutation |
| TK-009 | Child scope expansion | Child target exceeds ParentTask scope | Reject without mutation |
| TK-010 | Budget inheritance | Omit budget values | Persist parent effective budget |
| TK-011 | Budget boundary | Explicit MaxTurns / MaxTokens exactly equal parent | Accept |
| TK-012 | Budget expansion | Either budget dimension exceeds parent | Reject without mutation |
| TK-013 | Deadline inheritance | Omit deadline | Persist parent deadline |
| TK-014 | Deadline expansion | Task deadline exceeds parent / JobContract | Reject without mutation |
| TK-015 | Concurrency narrowing | Explicit concurrency ceiling at or below parent | Accept |
| TK-016 | Concurrency expansion | Task concurrency ceiling exceeds parent | Reject without mutation |
| TK-017 | Prohibited effects inheritance | Omit effect lists | Preserve all parent prohibitions / approval requirements |
| TK-018 | Prohibition expansion | Add additional prohibited / approval-required effects | Accept additive constraints |
| TK-019 | Prohibition removal | Request attempts to remove inherited prohibited effect | Reject |
| TK-020 | Parent identity | Parent does not exist, belongs to another Job, or another contract revision | Reject without mutation |
| TK-021 | Dependency identity | Dependency missing, foreign Job / revision, self-reference, or duplicate | Reject without mutation |
| TK-022 | Dependency cycle | Persisted dependency graph contains a cycle | Job validation / restore rejects |
| TK-023 | Parent-child separation | Parent relation exists without dependency edge | Do not infer a dependency |
| TK-024 | Exact replay | Same CommandID and identical Task request | Return original receipt and Task |
| TK-025 | Command conflict | Same CommandID with changed scope / budget / dependency | Reject without mutation |
| TK-026 | Store rollback | Inject failure while appending Task / receipt | No partial Task or receipt |
| TK-027 | Restart restoration | Reopen Store after Task append | Task and approval provenance remain valid |
| TK-028 | Authority separation | Successful Task creation | No RoleExecution, ExecutionPermission, Provider operation, or Worktree is created |
