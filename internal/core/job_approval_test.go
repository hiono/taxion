package core

import (
	"encoding/json"
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
		Revision: 1, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
		Repositories:         []JobRepositoryScope{{Repository: "example/repo", FileAccess: FileAccessPolicy{Level: FileAccessCommitted}}},
		Operations:           []string{"repository.read", "repository.write", "test.run"},
		Budget:               JobBudget{MaxRoleExecutions: 10, MaxConcurrentExecutions: 2, MaxNanoAIU: 100},
		Deadline:             time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		SecurityRestrictions: []string{"no-secret-export"}, Configuration: config,
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
	approval.Proposal.Contract.Operations = append(approval.Proposal.Contract.Operations, "network.access")
	if err := approval.Verify(); err == nil {
		t.Fatal("modified approval proposal must invalidate its record")
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
	graph.Jobs[0].Contract.Operations = append(graph.Jobs[0].Contract.Operations, "network.access")
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


func TestCheckRoleExecutionOperationRequiresCurrentJobApprovalAndScope(t *testing.T) {
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
	if err := graph.CheckRoleExecutionOperation("exec-a", "repository.read", now.Add(4*time.Second)); err != nil {
		t.Fatalf("approved in-scope operation rejected: %v", err)
	}
	if err := graph.CheckRoleExecutionOperation("exec-a", "network.access", now.Add(4*time.Second)); err != nil {
		t.Fatalf("explicitly approved operation rejected: %v", err)
	}
	if err := graph.CheckRoleExecutionOperation("exec-a", "unknown.operation", now.Add(4*time.Second)); err == nil {
		t.Fatal("unknown operation must fail closed")
	}
	if err := graph.CheckRoleExecutionOperation("exec-a", "repository.read", job.Contract.Deadline.Add(time.Second)); err == nil {
		t.Fatal("expired Job must not authorize Runtime operations")
	}
	graph.Jobs[0].Contract.Revision = 2
	if err := graph.CheckRoleExecutionOperation("exec-a", "repository.read", now.Add(4*time.Second)); err == nil {
		t.Fatal("new Job revision must require new approval")
	}
}
