# Job-native authorization replacement plan

Date: 2026-11-14
Status: Implementation plan for ADR-0080

## Goal

Replace the transitional Job-native operation/Role allowlists with one approval-boundary and prohibited-effect evaluator. The replacement is cohesive across the Job contract, controller, Runtime callback, provider adapters, tests, and documentation. No Job-to-Task/Role-ACL compatibility translation is introduced.

## Normative invariants

1. A Job approval authorizes one immutable Job revision and its declared intent, acceptance/evidence requirements, resource boundaries, budget, deadline, security prohibitions, approval-required effects, and non-secret Runtime configuration references.
2. Ordinary reversible development operations inside the approved resource boundary are available without enumerating operation names or assigning static operation grants to Roles.
3. Role is responsibility metadata, not an operation ACL. Provider tool availability is Runtime capability metadata, not Job authority.
4. Every privileged tool request is evaluated against the current approval, execution/session binding, Task-narrowed scope (where a Task exists), runtime identity, remaining budget/deadline, resource boundary, prohibited effects, and approval-required effects.
5. The same evaluator is used for Start, Resume, Send revalidation, and per-tool preflight. Lifecycle operations must re-read mutable state after provider calls before persisting transitions.
6. Exact approved ValidationCommands remain exact-value constraints. A generic shell/process capability never grants arbitrary command execution.
7. Unknown tool names are not denied solely for being unknown. If actual effects and enforceable containment cannot be established, request human approval when a concrete bounded approval can be presented; otherwise deny with the missing evidence/boundary stated.
8. Secret access/exfiltration, cross-Job access, privilege escalation, sandbox escape, out-of-bound resources, unapproved production/external mutation, unauthorized commit/push/release/publish, data/history tampering, and duplicate/ambiguous execution remain prohibited.
9. High-risk, irreversible, external, or approval-scope-changing effects require an explicit human decision when they are not already covered by the current Job approval. The decision is bound to the concrete target and impact. A Role message cannot grant that decision.
10. Provider adapters report only metadata they actually observe. Missing metadata is represented as unknown, never fabricated as a safe value.

## Replacement domain model

Replace JobContract.Operations, RoleOperations, RoleToolPolicies, and free-form SecurityRestrictions with structured approval dimensions:

- ResourceScopes: canonical repository/resource identity, approved worktree/sandbox identity, FileAccessPolicy ceiling, and explicitly bounded network/service targets where required.
- ProhibitedEffects: typed, validated effect identifiers and optional target constraints. Defaults include cross-job access, secret disclosure/exfiltration, privilege escalation, sandbox escape, unapproved production mutation, unauthorized publication, integrity/provenance tampering, and duplicate execution.
- ApprovalRequiredEffects: typed effect classes for effects not already covered by the approved Job intent and scope, including irreversible/destructive changes, external mutation, production changes, credential use beyond the selected Runtime binding, and scope changes. Commit/push/release/publish are checked against the explicitly approved target, repository, actor, and effect; they are not blanket-denied when already included in the approved scope.
- ValidationCommands: exact approved command strings, preserved unchanged.
- Existing budget, deadline, configuration, acceptance, evidence, revision, and repository file-access dimensions remain approval-bound.

Do not preserve old JSON fields as active authority inputs in the new Job model. Do not add deprecated aliases, adapter shims, or runtime conversions. If existing persisted Job records need migration, migration must be explicit, loss-aware, and must not silently reinterpret old allowlists as the new security boundary. Until a safe migration is defined, reject unsupported legacy Job approval records rather than widening authority.

## Provider-neutral operation request

Replace the string callback with a structured request carrying:

- session ID and provider identity;
- provider tool/capability name and action category (read, write, execute, network, credential, external mutation, unknown);
- target resource(s), normalized path/URI/host/repository identity where observed;
- command text and executable identity where observed;
- data-flow/secret access and outbound destination indicators where observed;
- effect class, reversibility, and whether the adapter/runtime can enforce containment;
- metadata provenance/capability flags so absent fields remain explicitly unknown.

The request is an observation, not an authorization assertion. Adapters must not map arbitrary MCP/custom tools to process.exec or assume that a tool name proves its effects. Copilot permission payloads and ACP permission requests are mapped only from fields actually supplied. Unknown fields remain unknown.

## Single evaluator

Introduce a pure evaluator over the current Job approval, RoleExecution/session binding, runtime identity, and structured operation request. Its result is one of:
- allow: all observed effects are ordinary/reversible and contained by enforced Job boundaries, with no prohibited effect;
- require approval: concrete high-risk/external/irreversible effect is identified and a bounded human approval can be presented;
- deny: prohibited effect, invalid identity/boundary, or uninspectable/uncontained effect for which approval cannot safely define a bounded action.

Start/Resume use the same evaluator for lifecycle capability checks and validate the full current Job boundary before opening/reactivating a provider session. Send revalidates immediately before a turn. Tool preflight invokes the evaluator immediately before each provider tool execution. Approval requests are persisted separately and do not mutate the base Job approval or implicitly approve later operations. An already-approved effect may proceed only when its concrete target and actor remain within the exact approved intent and resource scope.

## Resource enforcement

The evaluator is not a substitute for sandbox enforcement. File paths are normalized and checked against the approved repository and FileAccessPolicy; symlinks and path traversal must not escape the resolved worktree. Network and credentials require enforceable egress/credential broker boundaries. If the adapter/provider cannot expose or enforce a required boundary, the operation is not silently allowed. Runtime ToolPolicy and AvailableTools remain configuration/capability controls only.

## Cohesive implementation sequence

1. Replace JobContract schema, validation, clone/hash/serialization behavior, fixtures, constructors, and approval tests. Remove active legacy authorization fields and all Job-native references to them.
2. Define structured Runtime operation request/result and replace JobOperationAuthorizer callback without a string-operation compatibility overload.
3. Replace JobRoleController's transitional checks with the shared evaluator; preserve immutable approval, configuration/account, repository, session, budget, deadline, concurrency, and exact ValidationCommands checks. Remove Role ACL checks from Job-native Start/Resume/Send/preflight.
4. Update Copilot and OpenACP adapters to emit honest provider-neutral metadata. Remove arbitrary custom-tool-to-process.exec mappings and do not treat provider capability lists as authorization.
5. Update all dependent fixtures, tests, docs, and ADR-0077 wording. Keep independent legacy Task/ReviewFlow authorization unchanged until Job-native enforcement is verified; do not route Job requests through legacy checks.
6. Add tests for normal unlisted in-bound operations, out-of-bound resources, prohibited effects, unknown/uninspectable tools, approval-required effects, exact command matching, no Role ACL influence, provider metadata fidelity, Start/Resume/Send/tool-preflight consistency, interruption/resume, and real multi-Role Job completion.
7. Run formatting, focused tests, full tests, race, vet/build, secret scan, and CI. Report unavailable checks explicitly. Keep PR #292 Draft and do not merge.

## Acceptance criteria

- No Job-native code reads or writes Operations, RoleOperations, RoleToolPolicies, SecurityRestrictions, ToolPolicyIdentity as authorization, or Graph.CheckRoleExecutionOperation as a static grant.
- No Job-native adapter maps an arbitrary tool into an unrelated legacy operation to pass authorization.
- One evaluator and one structured request contract govern lifecycle and tool preflight.
- Job boundary enforcement is concrete and fail-closed for missing/uninspectable effects; ordinary in-bound reversible work is not blocked merely because a tool or operation name was not predeclared.
- ValidationCommands retain exact matching.
- Old Task/ReviewFlow path remains independently safe without being injected into Job-native authorization.
- Tests demonstrate actual Job progress and collaboration, not only isolated authorization predicates.
