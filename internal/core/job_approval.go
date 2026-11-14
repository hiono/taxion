package core

import (
	"maps"
	"math"
	"slices"
	"time"
)

// JobContract is the immutable approval boundary for one Job revision.
// It declares resource scope and effect policy, not operation or Role allowlists.
type JobContract struct {
	Revision                int                   `json:"revision"`
	Roles                   []string              `json:"roles"`
	Acceptance              []string              `json:"acceptance"`
	RequiredEvidence        []string              `json:"requiredEvidence"`
	Repositories            []JobRepositoryScope  `json:"repositories"`
	ValidationCommands      []string              `json:"validationCommands,omitempty"`
	ProhibitedEffects       []JobEffectRule       `json:"prohibitedEffects"`
	ApprovalRequiredEffects []JobEffectRule       `json:"approvalRequiredEffects"`
	Budget                  JobBudget             `json:"budget"`
	Deadline                time.Time             `json:"deadline"`
	Configuration           ApprovalConfiguration `json:"configuration"`
}

// JobEffect identifies a security-relevant effect independently of provider tool names.
type JobEffect string

const (
	JobEffectRepositoryRead      JobEffect = "repository_read"
	JobEffectRepositoryWrite     JobEffect = "repository_write"
	JobEffectValidationExecution JobEffect = "validation_execution"
	JobEffectCrossJobAccess      JobEffect = "cross_job_access"
	JobEffectSecretDisclosure    JobEffect = "secret_disclosure"
	JobEffectPrivilegeEscalation JobEffect = "privilege_escalation"
	JobEffectSandboxEscape       JobEffect = "sandbox_escape"
	JobEffectIntegrityTampering  JobEffect = "integrity_tampering"
	JobEffectDuplicateExecution  JobEffect = "duplicate_execution"
	JobEffectProductionMutation  JobEffect = "production_mutation"
	JobEffectExternalMutation    JobEffect = "external_mutation"
	JobEffectPublication         JobEffect = "publication"
	JobEffectCredentialUse       JobEffect = "credential_use"
	JobEffectNetworkAccess       JobEffect = "network_access"
	JobEffectDestructiveChange   JobEffect = "destructive_change"
	JobEffectScopeChange         JobEffect = "scope_change"
)

// JobEffectRule optionally limits an effect rule to exact canonical targets.
// An empty target list applies to every target.
type JobEffectRule struct {
	Effect  JobEffect `json:"effect"`
	Targets []string  `json:"targets,omitempty"`
}

func DefaultJobProhibitedEffects() []JobEffectRule {
	return []JobEffectRule{
		{Effect: JobEffectCrossJobAccess}, {Effect: JobEffectSecretDisclosure},
		{Effect: JobEffectPrivilegeEscalation}, {Effect: JobEffectSandboxEscape},
		{Effect: JobEffectIntegrityTampering}, {Effect: JobEffectDuplicateExecution},
	}
}

func DefaultJobApprovalRequiredEffects() []JobEffectRule {
	return []JobEffectRule{
		{Effect: JobEffectProductionMutation}, {Effect: JobEffectExternalMutation},
		{Effect: JobEffectPublication}, {Effect: JobEffectCredentialUse},
		{Effect: JobEffectNetworkAccess}, {Effect: JobEffectDestructiveChange},
		{Effect: JobEffectScopeChange},
	}
}

type JobRepositoryScope struct {
	Repository string           `json:"repository"`
	FileAccess FileAccessPolicy `json:"fileAccess"`
}

type JobBudget struct {
	MaxRoleExecutions       int     `json:"maxRoleExecutions"`
	MaxConcurrentExecutions int     `json:"maxConcurrentExecutions"`
	MaxNanoAIU              float64 `json:"maxNanoAiu"`
	AccountQuotaOnly        bool    `json:"accountQuotaOnly,omitempty"`
	BudgetSource            string  `json:"budgetSource,omitempty"`
}

func (c JobContract) Validate() error {
	if c.Revision < 1 || !validList(c.Roles) || len(c.Roles) == 0 || !validList(c.Acceptance) || len(c.Acceptance) == 0 ||
		!validList(c.RequiredEvidence) || len(c.RequiredEvidence) == 0 || len(c.Repositories) == 0 ||
		!validList(c.ValidationCommands) || !validJobEffectRules(c.ProhibitedEffects) ||
		!validJobEffectRules(c.ApprovalRequiredEffects) ||
		c.Budget.MaxRoleExecutions < 1 || c.Budget.MaxConcurrentExecutions < 1 ||
		c.Budget.MaxConcurrentExecutions > c.Budget.MaxRoleExecutions ||
		math.IsNaN(c.Budget.MaxNanoAIU) || math.IsInf(c.Budget.MaxNanoAIU, 0) ||
		c.Budget.MaxNanoAIU < 0 ||
		(c.Budget.MaxNanoAIU == 0 && !BillingExternalBudget(c.Budget.BudgetSource, c.Budget.AccountQuotaOnly)) ||
		(c.Budget.MaxNanoAIU > 0 && BillingExternalBudget(c.Budget.BudgetSource, c.Budget.AccountQuotaOnly)) ||
		(c.Budget.AccountQuotaOnly && c.Budget.BudgetSource != "") ||
		(c.Budget.BudgetSource != "" && c.Budget.BudgetSource != ProviderStopBudgetSource) ||
		c.Deadline.IsZero() || !nonempty(c.Configuration.Digest) || len(c.Configuration.Selection) == 0 {
		return graphError("Job contract requires revision, roles, acceptance, evidence, resource scope, effects, budget, deadline and configuration")
	}
	repositories := map[string]bool{}
	for _, scope := range c.Repositories {
		if !nonempty(scope.Repository) || repositories[scope.Repository] || scope.FileAccess.Validate() != nil {
			return graphError("Job contract contains an invalid or duplicate repository scope")
		}
		repositories[scope.Repository] = true
	}
	if err := validateRequiredJobEffects(c.ProhibitedEffects, c.ApprovalRequiredEffects); err != nil {
		return err
	}
	selected, err := SelectApprovalConfiguration(c.Configuration.Selection, c.Configuration.Sources)
	if err != nil || selected.Digest != c.Configuration.Digest {
		return graphError("Job contract configuration digest is invalid")
	}
	return nil
}

func validJobEffectRules(rules []JobEffectRule) bool {
	seen := map[JobEffect]bool{}
	for _, rule := range rules {
		if !validJobEffect(rule.Effect) || seen[rule.Effect] || !validList(rule.Targets) {
			return false
		}
		seen[rule.Effect] = true
		targets := map[string]bool{}
		for _, target := range rule.Targets {
			if !nonempty(target) || targets[target] {
				return false
			}
			targets[target] = true
		}
	}
	return true
}

func validJobEffect(effect JobEffect) bool {
	switch effect {
	case JobEffectRepositoryRead, JobEffectRepositoryWrite, JobEffectValidationExecution,
		JobEffectCrossJobAccess, JobEffectSecretDisclosure, JobEffectPrivilegeEscalation,
		JobEffectSandboxEscape, JobEffectIntegrityTampering, JobEffectDuplicateExecution,
		JobEffectProductionMutation, JobEffectExternalMutation, JobEffectPublication,
		JobEffectCredentialUse, JobEffectNetworkAccess, JobEffectDestructiveChange, JobEffectScopeChange:
		return true
	default:
		return false
	}
}

func validateRequiredJobEffects(prohibited, approvalRequired []JobEffectRule) error {
	for _, rule := range prohibited {
		for _, other := range approvalRequired {
			if rule.Effect == other.Effect {
				return graphError("Job effect %q cannot be both prohibited and approval-required", rule.Effect)
			}
		}
	}
	for _, required := range DefaultJobProhibitedEffects() {
		if !hasUnboundedJobEffect(prohibited, required.Effect) {
			return graphError("Job contract must prohibit effect %q", required.Effect)
		}
	}
	for _, required := range DefaultJobApprovalRequiredEffects() {
		if !hasUnboundedJobEffect(approvalRequired, required.Effect) {
			return graphError("Job contract must require approval for effect %q", required.Effect)
		}
	}
	return nil
}

// CheckEffect applies the approved Job effect policy to a provider-observed effect.
// Resource effects require an exact canonical target; validation execution requires
// an exact command declared by the Job contract. Unknown or unbounded observations fail closed.
func (c JobContract) CheckEffect(effect JobEffect, target string) error {
	if !validJobEffect(effect) {
		return graphError("unknown Job effect %q", effect)
	}
	if (effect == JobEffectRepositoryRead || effect == JobEffectRepositoryWrite) && !nonempty(target) {
		return graphError("repository effect requires an exact canonical target")
	}
	if effect == JobEffectValidationExecution && !slices.Contains(c.ValidationCommands, target) {
		return graphError("execution is not an exact approved Job validation command")
	}
	check := func(rules []JobEffectRule, reason string) error {
		for _, rule := range rules {
			if rule.Effect != effect {
				continue
			}
			if len(rule.Targets) == 0 || target == "" {
				return graphError("%s: %q", reason, effect)
			}
			if slices.Contains(rule.Targets, target) {
				return graphError("%s: %q target %q", reason, effect, target)
			}
		}
		return nil
	}
	if err := check(c.ProhibitedEffects, "Job effect is prohibited"); err != nil {
		return err
	}
	if err := check(c.ApprovalRequiredEffects, "Job effect requires separate human approval"); err != nil {
		return err
	}
	return nil
}

func hasUnboundedJobEffect(rules []JobEffectRule, effect JobEffect) bool {
	for _, rule := range rules {
		if rule.Effect == effect && len(rule.Targets) == 0 {
			return true
		}
	}
	return false
}

func cloneJobContract(contract *JobContract) *JobContract {
	if contract == nil {
		return nil
	}
	copy := *contract
	copy.Acceptance = slices.Clone(contract.Acceptance)
	copy.RequiredEvidence = slices.Clone(contract.RequiredEvidence)
	copy.ValidationCommands = slices.Clone(contract.ValidationCommands)
	copy.ProhibitedEffects = cloneJobEffectRules(contract.ProhibitedEffects)
	copy.ApprovalRequiredEffects = cloneJobEffectRules(contract.ApprovalRequiredEffects)
	copy.Repositories = slices.Clone(contract.Repositories)
	for i := range copy.Repositories {
		copy.Repositories[i].FileAccess.Paths = slices.Clone(contract.Repositories[i].FileAccess.Paths)
	}
	copy.Configuration.Selection = slices.Clone(contract.Configuration.Selection)
	copy.Configuration.Sources = maps.Clone(contract.Configuration.Sources)
	return &copy
}

func cloneJobEffectRules(rules []JobEffectRule) []JobEffectRule {
	clone := slices.Clone(rules)
	for i := range clone {
		clone[i].Targets = slices.Clone(rules[i].Targets)
	}
	return clone
}

// JobApprovalProposal is the exact immutable value presented for approval.
type JobApprovalProposal struct {
	Version     int         `json:"version"`
	JobID       string      `json:"jobId"`
	Revision    int         `json:"revision"`
	Request     string      `json:"request"`
	Goal        string      `json:"goal"`
	Constraints []string    `json:"constraints"`
	Contract    JobContract `json:"contract"`
}

func JobProposal(job Job) (JobApprovalProposal, error) {
	if job.Contract == nil {
		return JobApprovalProposal{}, graphError("Job has no explicit approval contract")
	}
	if err := job.Contract.Validate(); err != nil {
		return JobApprovalProposal{}, err
	}
	proposal := JobApprovalProposal{
		Version: 2, JobID: job.ID, Revision: job.Contract.Revision,
		Request: job.Request, Goal: job.Goal,
		Constraints: slices.Clone(job.Constraints), Contract: *cloneJobContract(job.Contract),
	}
	if !nonempty(proposal.JobID) || !nonempty(proposal.Request) || !nonempty(proposal.Goal) ||
		!validList(proposal.Constraints) {
		return JobApprovalProposal{}, graphError("Job approval proposal has invalid identity or intent")
	}
	return proposal, nil
}

func (p JobApprovalProposal) Hash() (string, error) {
	if p.Version != 2 || !nonempty(p.JobID) || p.Revision < 1 ||
		!nonempty(p.Request) || !nonempty(p.Goal) || !validList(p.Constraints) {
		return "", graphError("invalid Job approval proposal")
	}
	if err := p.Contract.Validate(); err != nil || p.Contract.Revision != p.Revision {
		return "", graphError("Job approval proposal revision or contract is invalid")
	}
	return CanonicalSHA256(p)
}

// JobApproval is an append-only human decision over one exact Job revision.
type JobApproval struct {
	ID           string              `json:"id"`
	Proposal     JobApprovalProposal `json:"proposal"`
	ProposalHash string              `json:"proposalHash"`
	RecordHash   string              `json:"recordHash"`
	Actor        string              `json:"actor"`
	Source       string              `json:"source"`
	Reason       string              `json:"reason"`
	At           time.Time           `json:"at"`
}

func NewJobApproval(id, actor, source, reason string, proposal JobApprovalProposal, at time.Time) (JobApproval, error) {
	hash, err := proposal.Hash()
	if err != nil || !nonempty(id) || !nonempty(actor) || source != "user" ||
		!nonempty(reason) || at.IsZero() {
		return JobApproval{}, graphError("Job approval requires a valid proposal and explicit user decision")
	}
	approval := JobApproval{
		ID: id, Proposal: proposal, ProposalHash: hash,
		Actor: actor, Source: source, Reason: reason, At: at,
	}
	approval.RecordHash, err = approval.computeRecordHash()
	if err != nil {
		return JobApproval{}, err
	}
	return approval, nil
}

func (a JobApproval) computeRecordHash() (string, error) {
	copy := a
	copy.RecordHash = ""
	return CanonicalSHA256(copy)
}

func (a JobApproval) Verify() error {
	hash, err := a.Proposal.Hash()
	if err != nil || hash != a.ProposalHash || !nonempty(a.ID) ||
		!nonempty(a.Actor) || a.Source != "user" || !nonempty(a.Reason) || a.At.IsZero() {
		return graphError("invalid Job approval record")
	}
	recordHash, err := a.computeRecordHash()
	if err != nil || recordHash != a.RecordHash {
		return graphError("Job approval record hash mismatch")
	}
	return nil
}

func cloneJobApproval(approval JobApproval) JobApproval {
	approval.Proposal.Constraints = slices.Clone(approval.Proposal.Constraints)
	approval.Proposal.Contract = *cloneJobContract(&approval.Proposal.Contract)
	return approval
}

// AppendJobApproval stores an explicit approval only when it matches the
// current Job contract. It never imports or converts Task approval records.
func (g *TaskGraph) AppendJobApproval(approval JobApproval) error {
	if g == nil {
		return graphError("Job approval requires a Task Graph")
	}
	next := cloneGraph(*g)
	job, ok := next.Job(approval.Proposal.JobID)
	if !ok {
		return graphError("Job approval references an unknown Job")
	}
	proposal, err := JobProposal(job)
	if err != nil {
		return err
	}
	currentHash, err := proposal.Hash()
	if err != nil || currentHash != approval.ProposalHash ||
		approval.ProposalHash != mustProposalHash(approval.Proposal) {
		return graphError("Job approval does not match the current Job revision")
	}
	if err := approval.Verify(); err != nil {
		return err
	}
	for _, existing := range next.JobApprovals {
		if existing.ID == approval.ID || existing.Proposal.JobID == approval.Proposal.JobID &&
			existing.Proposal.Revision == approval.Proposal.Revision {
			return graphError("Job revision already has an approval or approval ID is duplicated")
		}
	}
	if approval.At.Before(job.CreatedAt) {
		return graphError("Job approval predates Job creation")
	}
	next.JobApprovals = append(next.JobApprovals, cloneJobApproval(approval))
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func mustProposalHash(proposal JobApprovalProposal) string {
	hash, _ := proposal.Hash()
	return hash
}

// CurrentJobApproval returns only an approval that matches the current Job
// revision. Historical approvals remain available in JobApprovals.
func (g TaskGraph) CurrentJobApproval(jobID string) (JobApproval, bool) {
	job, ok := g.Job(jobID)
	if !ok {
		return JobApproval{}, false
	}
	proposal, err := JobProposal(job)
	if err != nil {
		return JobApproval{}, false
	}
	hash, err := proposal.Hash()
	if err != nil {
		return JobApproval{}, false
	}
	for i := len(g.JobApprovals) - 1; i >= 0; i-- {
		approval := g.JobApprovals[i]
		if approval.Proposal.JobID == jobID && approval.ProposalHash == hash && approval.Verify() == nil {
			return cloneJobApproval(approval), true
		}
	}
	return JobApproval{}, false
}

func (g TaskGraph) validateJobApprovals() error {
	ids := map[string]bool{}
	revisions := map[string]map[int]bool{}
	jobs := map[string]Job{}
	for _, job := range g.Jobs {
		jobs[job.ID] = job
	}
	for _, approval := range g.JobApprovals {
		job, ok := jobs[approval.Proposal.JobID]
		if !ok || job.Contract == nil || approval.Proposal.Revision > job.Contract.Revision ||
			ids[approval.ID] || approval.Verify() != nil || approval.At.Before(job.CreatedAt) {
			return graphError("invalid or orphaned Job approval")
		}
		ids[approval.ID] = true
		if revisions[approval.Proposal.JobID] == nil {
			revisions[approval.Proposal.JobID] = map[int]bool{}
		}
		revision := approval.Proposal.Revision
		if revisions[approval.Proposal.JobID][revision] {
			return graphError("duplicate approval for a Job revision")
		}
		revisions[approval.Proposal.JobID][revision] = true
	}
	for _, job := range g.Jobs {
		if job.Contract != nil {
			if err := job.Contract.Validate(); err != nil {
				return err
			}
			current, err := JobProposal(job)
			if err != nil {
				return err
			}
			currentHash, err := current.Hash()
			if err != nil {
				return err
			}
			for _, approval := range g.JobApprovals {
				if approval.Proposal.JobID == job.ID && approval.Proposal.Revision == job.Contract.Revision &&
					approval.ProposalHash != currentHash {
					return graphError("Job contract changed without incrementing its approval revision")
				}
			}
		}
	}
	return nil
}

// TaskWithinJobScope checks the enforceable repository and file-access
// boundary. Natural-language Task scope is intentionally not treated as a grant.
// TaskWithinJobContract verifies every structured Task boundary against the
// approved Job contract. Task prose does not grant authority, but acceptance,
// evidence and validation requirements must remain subsets of the approved Job.
func TaskWithinJobContract(task GraphTask, contract JobContract) bool {
	if !TaskWithinJobScope(task, contract) {
		return false
	}
	for _, acceptance := range task.Acceptance {
		if !slices.Contains(contract.Acceptance, acceptance) {
			return false
		}
	}
	for _, evidence := range task.RequiredEvidence {
		if !slices.Contains(contract.RequiredEvidence, evidence) {
			return false
		}
	}
	if !slices.Equal(task.ValidationCommands, contract.ValidationCommands) {
		return false
	}
	// Task ownership and reviewer fields belong to the legacy Task assignment
	// model. Job participation is represented by RoleExecution and is checked
	// against the approved Job role scope at the execution boundary.
	// Exact command identity is part of the approved contract; executability
	// is decided by the isolated validation runner.
	return true
}

func TaskWithinJobScope(task GraphTask, contract JobContract) bool {
	var allowed *JobRepositoryScope
	for i := range contract.Repositories {
		if contract.Repositories[i].Repository == task.Repo {
			allowed = &contract.Repositories[i]
			break
		}
	}
	if allowed == nil {
		return false
	}
	taskAccess := task.FileAccess
	if taskAccess == nil {
		taskAccess = DefaultFileAccessPolicy()
	}
	return FileAccessPolicyWithin(*taskAccess, allowed.FileAccess)
}

// CheckRoleExecutionStart verifies Job authority before a Role session is created.
func (g TaskGraph) CheckRoleExecutionStart(executionID string, at time.Time) error {
	if at.IsZero() {
		return graphError("RoleExecution start requires a timestamp")
	}
	execution, ok := g.RoleExecution(executionID)
	if !ok || execution.Status != RoleExecutionCreated {
		return graphError("RoleExecution must be created before it can start")
	}
	job, ok := g.Job(execution.JobID)
	if !ok || job.Status != JobActive || job.Contract == nil {
		return graphError("RoleExecution start requires an active Job contract")
	}
	if at.After(job.Contract.Deadline) {
		return graphError("Job deadline has expired")
	}
	if !slices.Contains(job.Contract.Roles, execution.Role) {
		return graphError("RoleExecution role is outside the approved Job role scope")
	}
	if _, ok := g.CurrentJobApproval(job.ID); !ok {
		return graphError("RoleExecution start requires explicit approval of the current Job revision")
	}
	if execution.TaskID != "" {
		task, ok := g.GraphTask(execution.TaskID)
		if !ok || task.JobID != job.ID || !TaskWithinJobContract(task, *job.Contract) {
			return graphError("RoleExecution Task exceeds the approved Job scope")
		}
	}
	if job.Contract.Budget.MaxNanoAIU <= 0 {
		return graphError("Job Role runtime requires a native AIU ceiling; external-only budget monitoring is not available")
	}
	if g.JobUsageTotal(job.ID) >= job.Contract.Budget.MaxNanoAIU {
		return graphError("Job AIU budget is exhausted")
	}
	executions := g.RoleExecutionsForJob(job.ID)
	if len(executions) > job.Contract.Budget.MaxRoleExecutions {
		return graphError("Job RoleExecution budget is exhausted")
	}
	concurrent := 0
	for _, candidate := range executions {
		switch candidate.Status {
		case RoleExecutionCreated, RoleExecutionRunning, RoleExecutionWaiting, RoleExecutionCancelRequested:
			concurrent++
		}
	}
	if concurrent > job.Contract.Budget.MaxConcurrentExecutions {
		return graphError("Job concurrent RoleExecution limit is exceeded")
	}
	return nil
}

// CheckRoleExecutionResume validates authority before a waiting or suspended
// RoleExecution re-enters the Job.
func (g TaskGraph) CheckRoleExecutionResume(executionID string, at time.Time) error {
	if at.IsZero() {
		return graphError("RoleExecution resume requires a timestamp")
	}
	execution, ok := g.RoleExecution(executionID)
	if !ok || (execution.Status != RoleExecutionWaiting && execution.Status != RoleExecutionSuspended) ||
		execution.RuntimeSessionID == "" {
		return graphError("RoleExecution must be waiting or suspended with a bound session")
	}
	job, ok := g.Job(execution.JobID)
	if !ok || job.Status != JobActive || job.Contract == nil {
		return graphError("RoleExecution resume requires an active Job contract")
	}
	if at.After(job.Contract.Deadline) {
		return graphError("Job deadline has expired")
	}
	if _, ok := g.CurrentJobApproval(job.ID); !ok {
		return graphError("RoleExecution resume requires approval of the current Job revision")
	}
	if !slices.Contains(job.Contract.Roles, execution.Role) {
		return graphError("RoleExecution role is outside the approved Job role scope")
	}
	if job.Contract.Budget.MaxNanoAIU <= 0 {
		return graphError("Job Role runtime requires a native AIU ceiling; external-only budget monitoring is not available")
	}
	if g.JobUsageTotal(job.ID) >= job.Contract.Budget.MaxNanoAIU {
		return graphError("Job AIU budget is exhausted")
	}
	if execution.TaskID != "" {
		task, ok := g.GraphTask(execution.TaskID)
		if !ok || task.JobID != job.ID || !TaskWithinJobContract(task, *job.Contract) {
			return graphError("RoleExecution Task exceeds the approved Job scope")
		}
	}
	executions := g.RoleExecutionsForJob(job.ID)
	if len(executions) > job.Contract.Budget.MaxRoleExecutions {
		return graphError("Job RoleExecution budget is exhausted")
	}
	concurrent := 0
	for _, candidate := range executions {
		switch candidate.Status {
		case RoleExecutionCreated, RoleExecutionRunning, RoleExecutionWaiting, RoleExecutionCancelRequested:
			concurrent++
		case RoleExecutionSuspended:
			if candidate.ID == execution.ID {
				concurrent++
			}
		}
	}
	if concurrent > job.Contract.Budget.MaxConcurrentExecutions {
		return graphError("Job concurrent RoleExecution limit is exceeded")
	}
	return nil
}

// CheckRoleExecutionTurn validates the authority and remaining Job budget
// before a running Role receives another model turn.
func (g TaskGraph) CheckRoleExecutionTurn(executionID string, at time.Time) error {
	if at.IsZero() {
		return graphError("Role turn requires a timestamp")
	}
	execution, ok := g.RoleExecution(executionID)
	if !ok || execution.Status != RoleExecutionRunning {
		return graphError("Role turn requires a running RoleExecution")
	}
	job, ok := g.Job(execution.JobID)
	if !ok || job.Status != JobActive || job.Contract == nil {
		return graphError("Role turn requires an active Job contract")
	}
	if at.After(job.Contract.Deadline) {
		return graphError("Job deadline has expired")
	}
	if _, ok := g.CurrentJobApproval(job.ID); !ok {
		return graphError("Role turn requires approval of the current Job revision")
	}
	if !slices.Contains(job.Contract.Roles, execution.Role) {
		return graphError("RoleExecution role is outside the approved Job role scope")
	}
	if execution.TaskID != "" {
		task, ok := g.GraphTask(execution.TaskID)
		if !ok || task.JobID != job.ID || !TaskWithinJobContract(task, *job.Contract) {
			return graphError("RoleExecution Task exceeds the approved Job scope")
		}
	}
	if job.Contract.Budget.MaxNanoAIU <= 0 {
		return graphError("Job Role runtime requires a native AIU ceiling; external-only budget monitoring is not available")
	}
	if g.JobUsageTotal(job.ID) >= job.Contract.Budget.MaxNanoAIU {
		return graphError("Job AIU budget is exhausted")
	}
	executions := g.RoleExecutionsForJob(job.ID)
	if len(executions) > job.Contract.Budget.MaxRoleExecutions {
		return graphError("Job RoleExecution budget is exhausted")
	}
	concurrent := 0
	for _, candidate := range executions {
		switch candidate.Status {
		case RoleExecutionCreated, RoleExecutionRunning, RoleExecutionWaiting, RoleExecutionCancelRequested:
			concurrent++
		}
	}
	if concurrent > job.Contract.Budget.MaxConcurrentExecutions {
		return graphError("Job concurrent RoleExecution limit is exceeded")
	}
	return nil
}

// CheckRoleExecution validates the current Job approval and execution budget.
// Individual tool effects are evaluated by the Job-native Runtime evaluator.
func (g TaskGraph) CheckRoleExecution(executionID string, at time.Time) error {
	if at.IsZero() {
		return graphError("RoleExecution validation requires a timestamp")
	}
	execution, ok := g.RoleExecution(executionID)
	if !ok || execution.Status != RoleExecutionRunning {
		return graphError("Runtime operation requires a running RoleExecution")
	}
	job, ok := g.Job(execution.JobID)
	if !ok || job.Status != JobActive || job.Contract == nil {
		return graphError("Runtime operation requires an active Job contract")
	}
	if at.After(job.Contract.Deadline) {
		return graphError("Job deadline has expired")
	}
	if _, ok := g.CurrentJobApproval(job.ID); !ok {
		return graphError("Runtime operation requires explicit approval of the current Job revision")
	}
	if job.Contract.Budget.MaxNanoAIU <= 0 {
		return graphError("Job Role runtime requires a native AIU ceiling; external-only budget monitoring is not available")
	}
	if g.JobUsageTotal(job.ID) >= job.Contract.Budget.MaxNanoAIU {
		return graphError("Job AIU budget is exhausted")
	}
	if !slices.Contains(job.Contract.Roles, execution.Role) {
		return graphError("RoleExecution role is outside the approved Job role scope")
	}
	if execution.TaskID != "" {
		task, ok := g.GraphTask(execution.TaskID)
		if !ok || task.JobID != job.ID || !TaskWithinJobContract(task, *job.Contract) {
			return graphError("Runtime operation Task exceeds the approved Job scope")
		}
	}
	executions := g.RoleExecutionsForJob(job.ID)
	if len(executions) > job.Contract.Budget.MaxRoleExecutions {
		return graphError("Job RoleExecution budget is exhausted")
	}
	concurrent := 0
	for _, candidate := range executions {
		switch candidate.Status {
		case RoleExecutionCreated, RoleExecutionRunning, RoleExecutionWaiting, RoleExecutionCancelRequested:
			concurrent++
		}
	}
	if concurrent > job.Contract.Budget.MaxConcurrentExecutions {
		return graphError("Job concurrent RoleExecution limit is exceeded")
	}
	return nil
}

// HasUnapprovedJob reports whether the current Job contract lacks a matching
// explicit approval. A Task approval is never considered here.
func (g TaskGraph) HasUnapprovedJob(jobID string) bool {
	_, ok := g.CurrentJobApproval(jobID)
	return !ok
}
