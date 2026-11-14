package core

import (
	"testing"
	"time"
)

func TestJobUsageAccumulatesAndBlocksFurtherOperationsAtBudget(t *testing.T) {
	now := time.Now().UTC()
	job := Job{
		ID: "job-usage", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	task := GraphTask{
		ID: "task-usage", JobID: job.ID, Repo: "example/repo",
		FileAccess: &FileAccessPolicy{Level: FileAccessExact, Paths: []string{"src/main.go"}},
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}, Tasks: []GraphTask{task}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-usage", "human", "user", "approve usage test", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-usage", JobID: job.ID, TaskID: task.ID, Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("exec-usage", RoleExecutionRunning, "controller", "start", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.BindRoleExecutionSession("exec-usage", "session-usage", "config-hash"); err != nil {
		t.Fatal(err)
	}
	total := 100.0
	usage, err := NewJobUsage(JobUsage{
		ID: "session-usage:usage-1", JobID: job.ID, RoleExecutionID: "exec-usage",
		Usage: DispatchUsage{
			Model: "test-model", TotalNanoAIU: &total,
			Accounting: &UsageAccountingIdentity{SourceSessionID: "session-usage", Sequence: 1, UsageID: "usage-1"},
			ObservedAt: now.Add(4 * time.Second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobUsage(usage); err != nil {
		t.Fatal(err)
	}
	if graph.JobUsageTotal(job.ID) != 100 {
		t.Fatalf("Job usage total = %v, want 100", graph.JobUsageTotal(job.ID))
	}
	if err := graph.CheckRoleExecution("exec-usage", now.Add(5*time.Second)); err == nil {
		t.Fatal("Role operation must be blocked when the Job AIU budget is exhausted")
	}
	usage.Usage.TotalNanoAIU = nil
	if err := usage.Verify(); err == nil {
		t.Fatal("mutated Job usage record must fail its immutable hash")
	}
}
