package domain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type TaskRequest struct {
	JobID                   string            `json:"job_id"`
	CommandID               string            `json:"command_id"`
	ParentTaskID            string            `json:"parent_task_id,omitempty"`
	DependsOnTaskIDs        []string          `json:"depends_on_task_ids,omitempty"`
	Title                   string            `json:"title"`
	Purpose                 string            `json:"purpose"`
	TargetRepositories      []string          `json:"target_repositories,omitempty"`
	TargetResources         []string          `json:"target_resources,omitempty"`
	Workspaces              []string          `json:"workspaces,omitempty"`
	AcceptanceCriteria      []string          `json:"acceptance_criteria"`
	RequiredEvidence        []string          `json:"required_evidence,omitempty"`
	ProhibitedEffects       []string          `json:"prohibited_effects,omitempty"`
	ApprovalRequiredEffects []string          `json:"approval_required_effects,omitempty"`
	Budget                  JobContractBudget `json:"budget,omitempty"`
	ConcurrencyCeiling      uint32            `json:"concurrency_ceiling,omitempty"`
	Deadline                time.Time         `json:"deadline,omitempty"`
}

type Task struct {
	ID                      string            `json:"id"`
	CommandID               string            `json:"command_id"`
	JobID                   string            `json:"job_id"`
	ParentTaskID            string            `json:"parent_task_id,omitempty"`
	DependsOnTaskIDs        []string          `json:"depends_on_task_ids,omitempty"`
	Title                   string            `json:"title"`
	Purpose                 string            `json:"purpose"`
	JobApprovalID           string            `json:"job_approval_id"`
	ContractRevision        uint64            `json:"contract_revision"`
	CanonicalizationVersion uint32            `json:"canonicalization_version"`
	CanonicalHash           string            `json:"canonical_hash"`
	TargetRepositories      []string          `json:"target_repositories"`
	TargetResources         []string          `json:"target_resources"`
	Workspaces              []string          `json:"workspaces"`
	AcceptanceCriteria      []string          `json:"acceptance_criteria"`
	RequiredEvidence        []string          `json:"required_evidence,omitempty"`
	ProhibitedEffects       []string          `json:"prohibited_effects,omitempty"`
	ApprovalRequiredEffects []string          `json:"approval_required_effects,omitempty"`
	Budget                  JobContractBudget `json:"budget"`
	ConcurrencyCeiling      uint32            `json:"concurrency_ceiling"`
	Deadline                time.Time         `json:"deadline"`
	CreatedAt               time.Time         `json:"created_at"`
	CreatedRevision         uint64            `json:"created_revision"`
}

type taskScope struct {
	repositories []string
	resources    []string
	workspaces   []string
	budget       JobContractBudget
	concurrency  uint32
	deadline     time.Time
	prohibited   []string
	approval     []string
}
func TaskID(commandID string) string {
	return planningOperationID("task", commandID)
}

func NewTask(job Job, request TaskRequest, now time.Time) (Task, error) {
	if err := job.Validate(); err != nil {
		return Task{}, fmt.Errorf("validate Job before Task creation: %w", err)
	}
	if !validCommandID(request.CommandID) || request.JobID != job.ID ||
		!validExecutionText(request.Title, 256) || !validExecutionText(request.Purpose, 4096) ||
		now.IsZero() {
		return Task{}, errors.New("Task command identity, Job, title, purpose, or timestamp is invalid")
	}
	if job.State != JobActive || len(job.JobContractRevisions) == 0 {
		return Task{}, errors.New("Task creation requires an ACTIVE Job with an approved JobContract")
	}
	revision := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	approval, found := jobApprovalForRevision(job, revision.Revision)
	if !found || approval.Validate(job, revision) != nil {
		return Task{}, errors.New("Task creation requires the exact latest JobApproval")
	}
	now = now.UTC().Round(0)
	if now.Before(approval.ApprovedAt) || !now.Before(revision.Contract.Deadline) {
		return Task{}, errors.New("Task creation is outside the approved JobContract deadline")
	}
	parentScope, err := taskParentScope(job, revision, request.ParentTaskID)
	if err != nil {
		return Task{}, err
	}
	dependencies, err := canonicalContractSet("Task DependsOnTaskIDs", request.DependsOnTaskIDs, false)
	if err != nil {
		return Task{}, err
	}
	for _, dependencyID := range dependencies {
		if dependencyID == TaskID(request.CommandID) {
			return Task{}, errors.New("Task cannot depend on itself")
		}
		dependency, ok := taskByID(job, dependencyID)
		if !ok || dependency.ContractRevision != revision.Revision {
			return Task{}, errors.New("Task dependency must reference an existing Task in the same JobContract revision")
		}
	}
	repositories, err := resolveTaskScope("TargetRepositories", request.TargetRepositories, parentScope.repositories)
	if err != nil {
		return Task{}, err
	}
	resources, err := resolveTaskScope("TargetResources", request.TargetResources, parentScope.resources)
	if err != nil {
		return Task{}, err
	}
	workspaces, err := resolveTaskScope("Workspaces", request.Workspaces, parentScope.workspaces)
	if err != nil {
		return Task{}, err
	}
	acceptance, err := canonicalContractSet("Task AcceptanceCriteria", request.AcceptanceCriteria, true)
	if err != nil {
		return Task{}, err
	}
	evidence, err := canonicalContractSet("Task RequiredEvidence", request.RequiredEvidence, false)
	if err != nil {
		return Task{}, err
	}
	additionalProhibited, err := canonicalContractSet("Task ProhibitedEffects", request.ProhibitedEffects, false)
	if err != nil {
		return Task{}, err
	}
	additionalApproval, err := canonicalContractSet("Task ApprovalRequiredEffects", request.ApprovalRequiredEffects, false)
	if err != nil {
		return Task{}, err
	}
	prohibited := mergeTaskSet(parentScope.prohibited, additionalProhibited)
	approvalRequired := mergeTaskSet(parentScope.approval, additionalApproval)
	budget := JobContractBudget{
		MaxTurns: request.Budget.MaxTurns, MaxTokens: request.Budget.MaxTokens,
	}
	if budget.MaxTurns == 0 {
		budget.MaxTurns = parentScope.budget.MaxTurns
	}
	if budget.MaxTokens == 0 {
		budget.MaxTokens = parentScope.budget.MaxTokens
	}
	if budget.MaxTurns == 0 || budget.MaxTokens == 0 ||
		budget.MaxTurns > parentScope.budget.MaxTurns || budget.MaxTokens > parentScope.budget.MaxTokens {
		return Task{}, errors.New("Task budget is invalid or expands its parent scope")
	}
	concurrency := request.ConcurrencyCeiling
	if concurrency == 0 {
		concurrency = parentScope.concurrency
	}
	if concurrency == 0 || concurrency > parentScope.concurrency {
		return Task{}, errors.New("Task concurrency ceiling expands its parent scope")
	}
	deadline := parentScope.deadline
	if !request.Deadline.IsZero() {
		deadline = request.Deadline.UTC().Round(0)
	}
	if !deadline.After(now) || deadline.After(parentScope.deadline) {
		return Task{}, errors.New("Task deadline is expired or expands its parent scope")
	}
	task := Task{
		ID: TaskID(request.CommandID), CommandID: request.CommandID, JobID: job.ID,
		ParentTaskID: request.ParentTaskID, DependsOnTaskIDs: dependencies,
		Title: strings.TrimSpace(request.Title), Purpose: strings.TrimSpace(request.Purpose),
		JobApprovalID: approval.ID, ContractRevision: revision.Revision,
		CanonicalizationVersion: revision.CanonicalizationVersion, CanonicalHash: revision.CanonicalHash,
		TargetRepositories: repositories, TargetResources: resources, Workspaces: workspaces,
		AcceptanceCriteria: acceptance, RequiredEvidence: evidence,
		ProhibitedEffects: prohibited, ApprovalRequiredEffects: approvalRequired,
		Budget: budget, ConcurrencyCeiling: concurrency, Deadline: deadline,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := validateTaskContent(job, task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (t Task) Validate(job Job) error {
	if !validCommandID(t.CommandID) || t.ID != TaskID(t.CommandID) || t.JobID != job.ID ||
		!validExecutionText(t.Title, 256) || !validExecutionText(t.Purpose, 4096) ||
		!validPlanningIdentity(t.JobApprovalID) || t.ContractRevision == 0 ||
		t.CanonicalizationVersion != JobContractCanonicalizationVersion ||
		!validSHA256(t.CanonicalHash) || t.CreatedAt.IsZero() || t.CreatedRevision == 0 {
		return errors.New("Task identity or metadata is invalid")
	}
	return validateTaskContent(job, t)
}

func validateTaskContent(job Job, task Task) error {
	var revision *JobContractRevision
	for i := range job.JobContractRevisions {
		if job.JobContractRevisions[i].Revision == task.ContractRevision {
			revision = &job.JobContractRevisions[i]
			break
		}
	}
	if revision == nil || revision.CanonicalHash != task.CanonicalHash ||
		revision.CanonicalizationVersion != task.CanonicalizationVersion {
		return errors.New("Task does not bind an existing exact JobContract revision")
	}
	approval, found := jobApprovalForRevision(job, task.ContractRevision)
	if !found || approval.ID != task.JobApprovalID || approval.Validate(job, *revision) != nil {
		return errors.New("Task does not bind the JobApproval for its contract revision")
	}
	if task.CreatedRevision <= approval.CreatedRevision || task.CreatedAt.Before(approval.ApprovedAt) ||
		!task.CreatedAt.Before(task.Deadline) || task.Deadline.After(revision.Contract.Deadline) {
		return errors.New("Task creation time or deadline exceeds approved authority")
	}
	parentScope, err := taskParentScope(job, *revision, task.ParentTaskID)
	if err != nil {
		return err
	}
	for name, values := range map[string][]string{
		"TargetRepositories": task.TargetRepositories,
		"TargetResources":    task.TargetResources,
		"Workspaces":         task.Workspaces,
	} {
		required := name == "Workspaces"
		canonical, canonicalErr := canonicalContractSet("Task "+name, values, required)
		if canonicalErr != nil || !equalTaskStrings(canonical, values) {
			return fmt.Errorf("Task %s is not canonical", name)
		}
	}
	if len(task.TargetRepositories) == 0 && len(task.TargetResources) == 0 {
		return errors.New("Task requires at least one target repository or resource")
	}
	if !taskSubset(task.TargetRepositories, parentScope.repositories) ||
		!taskSubset(task.TargetResources, parentScope.resources) ||
		!taskSubset(task.Workspaces, parentScope.workspaces) {
		return errors.New("Task resource scope expands its parent")
	}
	if task.Budget.MaxTurns == 0 || task.Budget.MaxTokens == 0 ||
		task.Budget.MaxTurns > parentScope.budget.MaxTurns ||
		task.Budget.MaxTokens > parentScope.budget.MaxTokens ||
		task.ConcurrencyCeiling == 0 || task.ConcurrencyCeiling > parentScope.concurrency ||
		task.Deadline.After(parentScope.deadline) {
		return errors.New("Task budget, concurrency, or deadline expands its parent")
	}
	for name, values := range map[string][]string{
		"AcceptanceCriteria":      task.AcceptanceCriteria,
		"RequiredEvidence":        task.RequiredEvidence,
		"ProhibitedEffects":       task.ProhibitedEffects,
		"ApprovalRequiredEffects": task.ApprovalRequiredEffects,
		"DependsOnTaskIDs":        task.DependsOnTaskIDs,
	} {
		canonical, canonicalErr := canonicalContractSet("Task "+name, values, name == "AcceptanceCriteria")
		if canonicalErr != nil || !equalTaskStrings(canonical, values) {
			return fmt.Errorf("Task %s is not canonical", name)
		}
	}
	if !taskSubset(parentScope.prohibited, task.ProhibitedEffects) ||
		!taskSubset(parentScope.approval, task.ApprovalRequiredEffects) {
		return errors.New("Task removed an inherited prohibited or approval-required effect")
	}
	if task.ParentTaskID != "" {
		parent, ok := taskByID(job, task.ParentTaskID)
		if !ok || parent.ContractRevision != task.ContractRevision ||
			parent.CreatedRevision >= task.CreatedRevision {
			return errors.New("Task parent must be an earlier Task in the same JobContract revision")
		}
	}
	seenDependencies := make(map[string]struct{}, len(task.DependsOnTaskIDs))
	for _, dependencyID := range task.DependsOnTaskIDs {
		if dependencyID == task.ID {
			return errors.New("Task cannot depend on itself")
		}
		if _, exists := seenDependencies[dependencyID]; exists {
			return errors.New("Task dependency list contains duplicates")
		}
		seenDependencies[dependencyID] = struct{}{}
		dependency, ok := taskByID(job, dependencyID)
		if !ok || dependency.ContractRevision != task.ContractRevision ||
			dependency.CreatedRevision >= task.CreatedRevision {
			return errors.New("Task dependency must be an earlier Task in the same JobContract revision")
		}
	}
	return nil
}

func taskParentScope(job Job, revision JobContractRevision, parentTaskID string) (taskScope, error) {
	if parentTaskID == "" {
		contract := revision.Contract
		return taskScope{
			repositories: contract.TargetRepositories, resources: contract.TargetResources,
			workspaces: contract.Workspaces, budget: contract.Budget,
			concurrency: contract.ConcurrencyCeiling, deadline: contract.Deadline,
			prohibited: contract.ProhibitedEffects, approval: contract.ApprovalRequiredEffects,
		}, nil
	}
	parent, found := taskByID(job, parentTaskID)
	if !found || parent.ContractRevision != revision.Revision {
		return taskScope{}, errors.New("ParentTaskID must reference an existing Task in the same JobContract revision")
	}
	return taskScope{
		repositories: parent.TargetRepositories, resources: parent.TargetResources,
		workspaces: parent.Workspaces, budget: parent.Budget,
		concurrency: parent.ConcurrencyCeiling, deadline: parent.Deadline,
		prohibited: parent.ProhibitedEffects, approval: parent.ApprovalRequiredEffects,
	}, nil
}

func resolveTaskScope(name string, requested, inherited []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), inherited...), nil
	}
	values, err := canonicalContractSet("Task "+name, requested, true)
	if err != nil {
		return nil, err
	}
	if !taskSubset(values, inherited) {
		return nil, fmt.Errorf("Task %s expands its parent scope", name)
	}
	return values, nil
}

func mergeTaskSet(inherited, additional []string) []string {
	merged := make(map[string]struct{}, len(inherited)+len(additional))
	for _, value := range inherited {
		merged[value] = struct{}{}
	}
	for _, value := range additional {
		merged[value] = struct{}{}
	}
	result := make([]string, 0, len(merged))
	for value := range merged {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func taskSubset(values, parent []string) bool {
	allowed := make(map[string]struct{}, len(parent))
	for _, value := range parent {
		allowed[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := allowed[value]; !ok {
			return false
		}
	}
	return true
}

func equalTaskStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func taskByID(job Job, id string) (Task, bool) {
	for _, task := range job.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return Task{}, false
}

func validateTaskFacts(job Job) error {
	seenIDs := make(map[string]struct{}, len(job.Tasks))
	seenCommands := make(map[string]struct{}, len(job.Tasks))
	previousRevision := uint64(0)
	previousAt := time.Time{}
	for _, task := range job.Tasks {
		if err := task.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid Task: %v", ErrInvalidJob, err)
		}
		if task.CreatedAt.After(job.UpdatedAt) || task.CreatedRevision > job.Revision ||
			task.CreatedRevision <= previousRevision ||
			(!previousAt.IsZero() && task.CreatedAt.Before(previousAt)) {
			return fmt.Errorf("%w: Task append order is invalid", ErrInvalidJob)
		}
		if _, exists := seenIDs[task.ID]; exists {
			return fmt.Errorf("%w: duplicate Task ID", ErrInvalidJob)
		}
		if _, exists := seenCommands[task.CommandID]; exists {
			return fmt.Errorf("%w: duplicate Task CommandID", ErrInvalidJob)
		}
		seenIDs[task.ID] = struct{}{}
		seenCommands[task.CommandID] = struct{}{}
		previousRevision = task.CreatedRevision
		previousAt = task.CreatedAt
	}
	visiting := make(map[string]bool, len(job.Tasks))
	visited := make(map[string]bool, len(job.Tasks))
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("%w: Task dependency cycle", ErrInvalidJob)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		task, _ := taskByID(job, id)
		for _, dependencyID := range task.DependsOnTaskIDs {
			if err := visit(dependencyID); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for _, task := range job.Tasks {
		if err := visit(task.ID); err != nil {
			return err
		}
	}
	return nil
}
