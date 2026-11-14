package core

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func validJobContract(t *testing.T) *JobContract {
	t.Helper()
	selection := json.RawMessage(`{"repository":{"path":"example/repo"},"execution":{"approvalRef":"config:v1"}}`)
	config, err := SelectApprovalConfiguration(selection, map[string]string{
		"repository.path": "project", "execution.approvalRef": "project",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &JobContract{
		Revision: 1, Roles: []string{"PLANNER", "CODER", "REVIEWER"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
		Repositories:      []JobRepositoryScope{{Repository: "example/repo", FileAccess: FileAccessPolicy{Level: FileAccessCommitted}}},
		Budget:            JobBudget{MaxRoleExecutions: 10, MaxConcurrentExecutions: 2, MaxNanoAIU: 100},
		Deadline:          time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		ProhibitedEffects: DefaultJobProhibitedEffects(), ApprovalRequiredEffects: DefaultJobApprovalRequiredEffects(), Configuration: config,
	}
}

func TestJobApprovalBindsExactJobRevision(t *testing.T) {
	now := time.Now().UTC()
	job := Job{ID: "job-a", Request: "request", Goal: "goal", Status: JobActive, CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t)}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-1", "human", "user", "approve bounded work", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := approval.Verify(); err != nil {
		t.Fatalf("valid approval rejected: %v", err)
	}
	approval.Proposal.Contract.Acceptance = append(approval.Proposal.Contract.Acceptance, "changed")
	if err := approval.Verify(); err == nil {
		t.Fatal("modified approval proposal must invalidate its record")
	}
}

func TestTaskLegacyRoleAssignmentDoesNotConstrainJobContract(t *testing.T) {
	contract := *validJobContract(t)
	task := GraphTask{ID: "task-a", Repo: "example/repo", Acceptance: []string{"tests pass"},
		RequiredEvidence: []string{"test-report"}, FileAccess: &FileAccessPolicy{Level: FileAccessCommitted}}
	if !TaskWithinJobContract(task, contract) {
		t.Fatal("in-scope Task without an assigned owner should be accepted")
	}
	assignedOwner := task
	assignedOwner.Owner = "CODER"
	if !TaskWithinJobContract(assignedOwner, contract) {
		t.Fatal("legacy Task.Owner must not constrain Job participation")
	}
	unapprovedOwner := task
	unapprovedOwner.Owner = "DEPLOYER"
	if !TaskWithinJobContract(unapprovedOwner, contract) {
		t.Fatal("legacy Task.Owner must not constrain Job participation")
	}
	legacyReviewer := task
	legacyReviewer.Reviewer = "DEPLOYER"
	if !TaskWithinJobContract(legacyReviewer, contract) {
		t.Fatal("legacy Task.Reviewer must not constrain Job participation")
	}
	widenedAcceptance := task
	widenedAcceptance.Acceptance = []string{"deploy production"}
	if TaskWithinJobContract(widenedAcceptance, contract) {
		t.Fatal("Task acceptance must remain within approved Job criteria")
	}
	widenedEvidence := task
	widenedEvidence.RequiredEvidence = []string{"production credentials"}
	if TaskWithinJobContract(widenedEvidence, contract) {
		t.Fatal("Task evidence must remain within approved Job criteria")
	}
	commands := task
	commands.ValidationCommands = []string{"go test ./..."}
	if TaskWithinJobContract(commands, contract) {
		t.Fatal("unbound validation commands must be rejected")
	}
	contract.ValidationCommands = append([]string(nil), commands.ValidationCommands...)
	contract.ValidationCommands = slices.Clone(commands.ValidationCommands)
	if !TaskWithinJobContract(commands, contract) {
		t.Fatal("exactly approved validation command should be accepted")
	}
	changedCommand := commands
	changedCommand.ValidationCommands = []string{"go test ./... -race"}
	if TaskWithinJobContract(changedCommand, contract) {
		t.Fatal("Task must not replace the exact approved validation command")
	}

	readOnly := contract
	readOnly.ValidationCommands = nil
	if !TaskWithinJobContract(task, readOnly) {
		t.Fatal("a Task must not be forced to receive repository.write when its Job contract grants read-only access")
	}
}

func TestTaskGraphRejectsTaskContractOutsideApprovedJob(t *testing.T) {
	now := time.Now().UTC()
	job := Job{ID: "job-a", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t)}
	task := GraphTask{ID: "task-a", JobID: job.ID, Repo: "example/repo",
		Acceptance: []string{"unapproved acceptance"}, RequiredEvidence: []string{"test-report"},
		FileAccess: &FileAccessPolicy{Level: FileAccessCommitted}}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}, Tasks: []GraphTask{task}}
	if err := graph.validateJobModel(); err == nil {
		t.Fatal("Graph validation must reject Task acceptance outside the Job contract")
	}
}

func TestTaskScopeCannotWidenJobRepositoryScope(t *testing.T) {
	contract := *validJobContract(t)
	allowed := GraphTask{ID: "task-a", Repo: "example/repo", FileAccess: &FileAccessPolicy{Level: FileAccessExact, Paths: []string{"src/main.go"}}}
	if !TaskWithinJobScope(allowed, contract) {
		t.Fatal("narrower exact file scope should be accepted")
	}
	tooBroad := allowed
	tooBroad.FileAccess = &FileAccessPolicy{Level: FileAccessAll}
	if TaskWithinJobScope(tooBroad, contract) {
		t.Fatal("Task must not widen committed Job access")
	}
	otherRepo := allowed
	otherRepo.Repo = "other/repo"
	if TaskWithinJobScope(otherRepo, contract) {
		t.Fatal("Task must not add an unapproved repository")
	}
}

func TestLegacyTaskRoleAssignmentsDoNotConstrainJobRoleScope(t *testing.T) {
	contract := *validJobContract(t)
	task := GraphTask{ID: "task-a", Repo: "example/repo", Owner: "UNLISTED", Reviewer: "ALSO_UNLISTED"}
	if !TaskWithinJobContract(task, contract) {
		t.Fatal("legacy Task role assignments must not constrain Job Role participation")
	}
}

func TestJobContractRequiresTypedSecurityEffects(t *testing.T) {
	contract := validJobContract(t)
	contract.ProhibitedEffects = contract.ProhibitedEffects[1:]
	if err := contract.Validate(); err == nil {
		t.Fatal("Job contract must retain non-overridable prohibited effects")
	}
	contract = validJobContract(t)
	contract.ApprovalRequiredEffects[0].Effect = JobEffect("unknown-effect")
	if err := contract.Validate(); err == nil {
		t.Fatal("unknown effect identifiers must be rejected")
	}
	contract = validJobContract(t)
	contract.ApprovalRequiredEffects[0].Effect = JobEffectExternalMutation
	if err := contract.Validate(); err == nil {
		t.Fatal("duplicate effect identifiers must be rejected")
	}
}

func TestJobApprovalRequiresExplicitUserSource(t *testing.T) {
	now := time.Now().UTC()
	job := Job{ID: "job-a", Request: "request", Goal: "goal", Status: JobActive, CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t)}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewJobApproval("approval-1", "agent", "role", "approve", proposal, now.Add(time.Second)); err == nil {
		t.Fatal("Role must not approve its own Job authority")
	}
}

func TestJobContractRejectsUnknownAndUnboundedScope(t *testing.T) {
	contract := validJobContract(t)
	contract.Budget.MaxConcurrentExecutions = 11
	if err := contract.Validate(); err == nil {
		t.Fatal("concurrency cannot exceed total execution ceiling")
	}
	contract = validJobContract(t)
	contract.Repositories = append(contract.Repositories, contract.Repositories[0])
	if err := contract.Validate(); err == nil {
		t.Fatal("duplicate repository scope must be rejected")
	}
}

func TestJobApprovalAppendAndRevisionInvalidation(t *testing.T) {
	now := time.Now().UTC()
	job := Job{
		ID: "job-a", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-1", "human", "user", "approve", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatalf("append Job approval: %v", err)
	}
	if _, ok := graph.CurrentJobApproval("job-a"); !ok {
		t.Fatal("current Job approval was not found")
	}
	graph.Jobs[0].Contract.Acceptance = append(graph.Jobs[0].Contract.Acceptance, "changed")
	if err := graph.validateJobModel(); err == nil {
		t.Fatal("changing an approved contract without a new revision must be rejected")
	}
	graph.Jobs[0].Contract.Revision = 2
	if err := graph.validateJobModel(); err != nil {
		t.Fatalf("new revision should preserve historical approval: %v", err)
	}
	if !graph.HasUnapprovedJob("job-a") {
		t.Fatal("new Job revision must require explicit approval")
	}
}

func TestTaskGraphRejectsTaskScopeOutsideApprovedJobContract(t *testing.T) {
	now := time.Now().UTC()
	job := Job{
		ID: "job-a", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	task := GraphTask{
		ID: "task-a", JobID: "job-a", Repo: "example/repo",
		FileAccess: &FileAccessPolicy{Level: FileAccessAll},
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}, Tasks: []GraphTask{task}}
	if err := graph.validateJobModel(); err == nil {
		t.Fatal("Task must not widen Job file access")
	}
}

func TestCheckRoleExecutionRequiresCurrentJobApprovalAndScope(t *testing.T) {
	now := time.Now().UTC()
	job := Job{
		ID: "job-a", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	task := GraphTask{
		ID: "task-a", JobID: job.ID, Repo: "example/repo",
		FileAccess: &FileAccessPolicy{Level: FileAccessExact, Paths: []string{"src/main.go"}},
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}, Tasks: []GraphTask{task}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-a", "human", "user", "approve", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-a", JobID: job.ID, TaskID: task.ID, Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("exec-a", RoleExecutionRunning, "controller", "start", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.CheckRoleExecution("exec-a", now.Add(4*time.Second)); err != nil {
		t.Fatalf("approved RoleExecution rejected: %v", err)
	}
	if err := graph.CheckRoleExecution("exec-a", now.Add(4*time.Second)); err != nil {
		t.Fatalf("RoleExecution was incorrectly coupled to an operation name: %v", err)
	}
	graph.Tasks[0].Acceptance = []string{"unapproved acceptance"}
	if err := graph.CheckRoleExecution("exec-a", now.Add(4*time.Second)); err == nil {
		t.Fatal("Runtime operation must reject a Task whose acceptance exceeds the approved Job contract")
	}
	graph.Tasks[0].Acceptance = nil
	graph.Tasks[0].Repo = "other/repo"
	if err := graph.CheckRoleExecution("exec-a", now.Add(4*time.Second)); err == nil {
		t.Fatal("Task outside the approved repository scope must be rejected")
	}
	graph.Tasks[0].Repo = "example/repo"
	if err := graph.CheckRoleExecution("exec-a", now.Add(4*time.Second)); err != nil {
		t.Fatalf("restored in-scope Task should remain authorized: %v", err)
	}
	if err := graph.CheckRoleExecution("exec-a", job.Contract.Deadline.Add(time.Second)); err == nil {
		t.Fatal("expired Job must not authorize Runtime operations")
	}
	graph.Jobs[0].Contract.Revision = 2
	if err := graph.CheckRoleExecution("exec-a", now.Add(4*time.Second)); err == nil {
		t.Fatal("new Job revision must require new approval")
	}
}

func TestCheckRoleExecutionStartRequiresCurrentJobApproval(t *testing.T) {
	now := time.Now().UTC()
	job := Job{
		ID: "job-start", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	task := GraphTask{
		ID: "task-start", Title: "Start Task", JobID: job.ID, Repo: "example/repo",
		Type: "IMPLEMENTATION", Status: Draft, CreatedFrom: "test:job-approval",
		Issue:      "NOT_REQUIRED: isolated Job approval unit test",
		FileAccess: &FileAccessPolicy{Level: FileAccessExact, Paths: []string{"src/main.go"}},
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}, Tasks: []GraphTask{task}}
	if err := graph.Track(task, "test", "track Job approval Task"); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-start", JobID: job.ID, TaskID: task.ID, Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.CheckRoleExecutionStart("exec-start", now.Add(2*time.Second)); err == nil {
		t.Fatal("unapproved Job must not start a RoleExecution")
	}
	taskApproval, err := ComposeApprovalBundle(
		"task-approval-only", task, task, job.Contract.Configuration,
		"human", "user", "approve Task work", now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendApproval(taskApproval); err != nil {
		t.Fatal(err)
	}
	if err := graph.CheckRoleExecutionStart("exec-start", now.Add(2*time.Second)); err == nil {
		t.Fatal("Task approval must not grant Job execution authority")
	}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-start", "human", "user", "approve", proposal, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}
	if err := graph.CheckRoleExecutionStart("exec-start", now.Add(3*time.Second)); err != nil {
		t.Fatalf("approved in-scope RoleExecution rejected: %v", err)
	}
	if err := graph.CheckRoleExecutionStart("exec-start", job.Contract.Deadline.Add(time.Second)); err == nil {
		t.Fatal("expired Job must not start a RoleExecution")
	}
}

func TestCheckRoleExecutionStartAllowsJobLevelPlannerBeforeTasksExist(t *testing.T) {
	now := time.Now().UTC()
	job := Job{
		ID: "job-planning", Request: "request", Goal: "plan the work", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-planning", "human", "user", "approve planning", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-planner", JobID: job.ID, Role: "PLANNER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.CheckRoleExecutionStart("exec-planner", now.Add(3*time.Second)); err != nil {
		t.Fatalf("Job-level Planner must start before any Task exists: %v", err)
	}
}
