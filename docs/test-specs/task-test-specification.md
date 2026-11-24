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
| TK-029 | Explicit empty scope dimension | Request includes an explicit empty target list while another target dimension remains in scope | Persist the empty dimension; do not inherit access from the parent |
| TK-030 | Trusted Worktree registry | Task targets a registered repository and a canonical workspace root | Create a detached Worktree at the source HEAD without changing source HEAD |
| TK-031 | Worktree replay and concurrency | Same Job / Task is provisioned concurrently or retried | Resolve to one deterministic Worktree path and the same baseline |
| TK-032 | Repository scope rejection | Task repository is unregistered, outside approved scope, or multiple repositories are requested | Reject without Provider dispatch |
| TK-033 | Worktree path isolation | Workspace root overlaps source repository, or expected Worktree path is a symlink | Reject before Provider dispatch |
| TK-034 | Worktree baseline integrity | HEAD descends from baseline; then switch to unrelated history or attach a branch | Accept descendant commits; reject unrelated history and non-detached Worktree |
| TK-035 | Atomic Worktree assignment | StartTaskExecution persists WorktreeAssignment, RoleExecution, ProviderStartIntent | All facts commit in one Job revision or none commit |
| TK-036 | Worktree assignment replay | Exact Start command is replayed before and after SQLite restart | Return original assignment, RoleExecution, ProviderStartIntent, and receipt |
| TK-037 | Worktree owner exclusion | Another command attempts to start a different RoleExecution for the same Task | Reject until an explicit safe handoff exists; preserve assignment |
| TK-038 | Missing Worktree manager / verifier | ExecutionController or ProviderController lacks trusted Worktree integration | Fail closed before Provider side effects |
| TK-039 | Provider Worktree binding | Task StartRequest is built from persisted WorktreeAssignment | Pass exact RepositoryID, WorktreePath, and baseline commit to Adapter |
| TK-040 | Worktree capability evidence | Provider lacks Task Worktree capability or evidence is below E4 | Reject before credential acquisition / Provider Start |
| TK-041 | Reviewer isolation | Reviewer Start is requested without immutable read-only snapshot enforcement | Reject before Lookup or Provider Start |
| TK-042 | Worktree revalidation after lookup | Provider Start lookup runs before dispatch and the Worktree changes before Start | Revalidate the exact persisted assignment and physical Worktree immediately before Start; reject without Provider Start or observation |
| TK-043 | Task Coder Turn authority | ACTIVE Job、accepted Task Coder Start、matching ExecutionPermission | Append TaskTurnIntent bound to the exact Task / RoleExecution / permission / Session |
| TK-044 | Planner authority separation | Task Coder execution has no PlanningAuthorization | TaskTurnIntent uses the matching ExecutionPermission, not a Planner grant |
| TK-045 | Turn Session identity | Existing TaskTurn uses another Provider Session | Reject without Job mutation |
| TK-046 | Unresolved previous Turn | Previous Turn has no observation or latest UNKNOWN | Reject new Turn until same-Turn reconciliation |
| TK-047 | Turn budget | Cumulative observed tokens plus new reservation exceeds permission | Reject without mutation |
| TK-048 | UNKNOWN reconciliation | First observation UNKNOWN, then terminal evidence for same Turn | Append terminal observation with ReconcilesUnknown; reject blind replay |
| TK-049 | Restart restoration | TaskTurn facts are persisted and Store is reopened | Restore exact facts and preserve aggregate validation |
| TK-050 | Task Provider Send intent-first | ACTIVE Job、accepted Coder Start、current ExecutionPermission、safe Send capability | Persist one TaskTurnIntent and receipt before calling Provider Send |
| TK-051 | UNKNOWN Send reconciliation | Existing TaskTurn is UNKNOWN after timeout or process interruption | Lookup the same OperationID / TurnID first; retry only with verified Send idempotency and the exact same identity |
| TK-052 | Terminal Result and Usage ordering | Provider reports COMPLETED / FAILED | Persist final Result and matching complete Usage before terminal TaskTurnObservation; keep UNKNOWN if either is absent, partial, or mismatched |
| TK-053 | Task Provider authority | Caller supplies another Task, permission, JobApproval, Session, or Provider identity | Reject before Provider side effect or observation mutation |
| TK-054 | Worktree revalidation before Send | Worktree assignment or physical Worktree changes during pre-dispatch checks | Re-read authority and assignment, reverify the exact Worktree immediately before Send, and reject without Provider Send |
| TK-055 | Task Coder Stop authority and evidence | ACTIVE Job、accepted Coder Session、matching JobApproval / ExecutionPermission、E4 StopProof | Persist Stop intent before Runtime.Stop and append only matching Stopped / Quiescent evidence |
| TK-056 | Task Coder Stop revalidation | Principal、permission、Session、Worktree ownerまたは物理Worktreeが不一致 | Reject before Provider Stop; do not append a terminal observation |
| TK-057 | Stop capability evidence | StopProof capability missing or below E4 | Reject before Provider Stop |
| TK-058 | UNKNOWN Stop replay | Existing Stop intent has UNKNOWN or no observation | Reuse the same intent and retain UNKNOWN. Do not call Runtime.Stop again unless verified E4 Stop Lookup returns NOT_FOUND; then retry only with the same OperationID / SessionID |
| TK-059 | Turn after Stop | TaskTurnIntent is created after the same Coder Session Stop intent | Reject without Job mutation |
| TK-060 | Task Coder Resume authority | ACTIVE Job、matching JobApproval / ExecutionPermission、accepted Task Coder Session | Append Task-bound Resume intent with TaskID / ExecutionPermissionID and same Session |
| TK-061 | Session identity preservation | Resume request supplies another Session or mismatched Start identity | Reject; never replace the RoleExecution's persisted Session |
| TK-062 | Durable reattachment evidence | SessionReattach capability missing, wrong identity, or below E5 | Reject before Runtime.Resume |
| TK-063 | Intent-first Resume and Worktree | Resume is requested after process interruption | Persist intent / receipt first; revalidate permission and physical Worktree immediately before Runtime.Resume |
| TK-064 | UNKNOWN Resume reconciliation | Runtime.Resume times out or process stops after intent commit | Retry / reconcile the same Resume OperationID and Session; append terminal evidence only for that intent |
| TK-065 | Turn gate after Resume | Latest Resume observation is absent, UNKNOWN, or FAILED | Reject a new TaskTurnIntent; allow only after ATTACHED |
| TK-066 | Expired or stale authority | JobApproval, ExecutionPermission, JobContract revision, or deadline is no longer current | Reject before Resume side effect |
| TK-067 | Resume after Stop | Same RoleExecution / Session has a ProviderStopIntent | Reject without Runtime.Resume or new Session |
| TK-068 | Stop / Resume concurrency | Same Session has a Resume intent with no observation or UNKNOWN | Reject Stop intent and do not call Runtime.Stop until Resume is terminal |
| TK-069 | Ambiguous Task Coder Stop | Runtime.Stop returns an ambiguous timeout after Stop intent commit | Persist UNKNOWN; same CommandID replay reuses the intent and does not call Runtime.Stop again unless verified E4 Stop Lookup returns NOT_FOUND; a new Stop identity is rejected |
| TK-070 | Stop response identity and evidence | Stop response has wrong OperationID / SessionID, Stopped=false, Quiescent=false, missing or invalid evidence digest | Persist UNKNOWN; never report Stopped / Quiescent success |
| TK-071 | Stop preconditions | StopProof is absent or mismatched, Provider identity differs, or Worktree verification fails before intent | Reject without Stop intent, observation, or Provider side effect |
| TK-072 | Final Worktree revalidation after Stop intent | Worktree verification fails after Stop intent commit but immediately before Runtime.Stop | Preserve intent; do not call Provider; retry remains conservative UNKNOWN and never blindly repeats Stop |
| TK-073 | Conflicting Stop replay | Reuse Stop CommandID with a different Stop reason or payload after initial request | Reject with idempotency conflict; preserve original intent / observation and do not repeat Runtime.Stop |
| TK-074 | Task Coder Resume response validation | Provider Resume returns mismatched OperationID / SessionID, Attached=false, or missing / invalid evidence | Persist UNKNOWN; retry the same OperationID only and accept ATTACHED only with matching Session evidence |
| TK-075 | E5 reattachment capability | CrossProcessSessionReattach absent, Provider identity mismatch, E5 evidence missing or below E5 | Reject before Resume intent and Provider side effect |
| TK-076 | Resume intent-first ordering | Runtime.Resume inspects JobAggregate at invocation | Exact Resume intent and receipt are already persisted; no observation is claimed before Provider response |
| TK-077 | Resume Worktree revalidation | Worktree changes before intent or immediately before Runtime.Resume | Reject before Provider side effect; preserve intent if already committed and retry only the same Session / OperationID |
| TK-078 | ATTACHED Resume replay | Same Resume CommandID is replayed after ATTACHED observation | Return the same intent / observation without another Runtime.Resume call |
| TK-079 | Task Coder Resume / Stop durable recovery | Task Coder Turn, Resume UNKNOWN → ATTACHED, Stop UNKNOWN → Stopped / Quiescent, then SQLite restart | Restore exact Task / permission / Session-bound intents and append-only observations; aggregate validation succeeds |
| TK-080 | Stop side-effect recovery after restart | Stop intent committed, Provider completed Stop, process crashed before terminal Observation | E4 Stop Lookup confirms STOPPED / Stopped / Quiescent; append terminal Observation without another Stop call |
| TK-081 | Stop retry before dispatch-start | Stop intent and dispatch claim are committed, process crashes before ProviderStopDispatchStart; E4 Stop Lookup returns NOT_FOUND | Persist NOT_FOUND evidence in not_dispatched release, acquire a new claim, persist one dispatch-start fact, then call Runtime.Stop once with the original OperationID / SessionID |
| TK-082 | Reviewer read-only capability gate | Reviewer StartRequest binds JobOutput / commit / tree but Provider lacks E4 reviewer.read_only_snapshot evidence | Reject before credential acquisition and Adapter.Start |
| TK-083 | Reviewer snapshot identity binding | Reviewer StartRequest has a mismatched commit / tree / JobOutput identity | Reject as invalid request before capability lookup or credential acquisition |
| TK-084 | Adopt existing linked Worktree | Existing linked Worktree belongs to the exact trusted Repository and approved Task scope; path, Git metadata, HEAD / tree, baseline ancestry, HEAD mode / ref, and initial state are verifiable | Manager records adoption identity and WorktreeAssignment atomically with TaskExecutionStart without changing the existing Worktree |
| TK-085 | Preserve adopted working state | Existing Worktree contains staged, unstaged, and untracked changes | Record a complete initial state digest; preserve all files and index entries; do not reset, checkout, clean, or stash |
| TK-086 | Reject unsafe Worktree adoption | Repository / path / common Git metadata mismatch, symlink or alias path, out-of-scope repository, invalid ancestry, or incomplete state capture | Reject without Job mutation or filesystem modification |
| TK-087 | Worktree adoption replay and exclusivity | Same adoption Command is replayed, or another Job / Task attempts to adopt the same physical Worktree | Exact replay returns the same registration; conflicting owner or second Job / Task is rejected |
| TK-088 | Append-only ownership transfer | Current owner has Stopped / Quiescent, terminal event, final Result, complete Usage, and matching output identity | Append one transfer fact for the new RoleExecution; preserve the original WorktreeAssignment |
| TK-089 | Ownership transfer with UNKNOWN evidence | Provider stop, result, Usage, or Worktree identity is UNKNOWN / incomplete | Keep the current owner; do not dispatch the next Role; retain or quarantine the Worktree |
| TK-090 | Reviewer isolation from mutable Worktree | Reviewer receives a Coder JobOutput target | Create a separate immutable commit / tree snapshot and independent read-only RoleExecution / Context / Session; never expose the mutable Worktree |
| TK-091 | Coder resumes after Reviewer | Reviewer has terminal stop / result / complete Usage evidence and a matching snapshot identity | Append an explicit ownership handoff back to Coder before mutable execution resumes |
| TK-092 | Stop claim crash before dispatch | Stop intent and dispatch claim are committed; process crashes before ProviderStopDispatchStart | E4 Stop Lookup returns NOT_FOUND; persist the verified NOT_FOUND evidence digest in the not_dispatched release, acquire a new claim, persist one start fact, then call Runtime.Stop once with the original OperationID / SessionID |
| TK-093 | Stop dispatch-start fencing | A stale caller attempts to start a claim after another controller released it as not_dispatched | Reject the stale start transaction; no Provider Stop call is allowed |
| TK-094 | Stop claim crash after dispatch-start | ProviderStopDispatchStart is committed; process crashes before or during Runtime.Stop; Lookup returns NOT_FOUND | With verified E4 Stop Dispatch Fencing, release as fenced_not_found, advance the claim revision token, and retry the same OperationID / SessionID; without that capability, retain the active claim and UNKNOWN |
| TK-095 | Task Turn dispatch claim recovery | TaskTurnIntent and active dispatch claim are persisted; process crashes before Send; E4 SendIdempotency is verified and Lookup returns NOT_FOUND | Release the old claim with the SendIdempotency evidence digest, acquire a new claim, and retry the exact same OperationID / TurnID / SessionID / PromptSHA256. Late old-controller Send requests converge to the same Provider operation; SendLookup alone cannot authorize retry |
| TK-096 | Terminal Turn Usage guard | A terminal TaskTurnObservation exists and a new UsageID is requested | Reject the new Usage before Provider retrieval / append; exact replay of an existing UsageID returns the stored observation without another Provider Usage call |
