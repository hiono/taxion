package core

import (
	"testing"
	"time"
)

func TestJobUsageBindsSessionAndEnforcesBudget(t *testing.T) {
	now := time.Now().UTC()
	graph := newJobGraph()
	graph.Tasks[0].Repo = "example/repo"
	graph.Tasks[0].FileAccess = &FileAccessPolicy{Level: FileAccessExact, Paths: []string{"src/main.go"}}
	graph.Jobs[0].Contract = validJobContract(t)

	proposal, err := JobProposal(graph.Jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("usage-approval", "human", "user", "approve usage test", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}

	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "usage-exec", JobID: "job-a", TaskID: "task-a", Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("usage-exec", RoleExecutionRunning, "controller", "start", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.BindRoleExecutionSession("usage-exec", "usage-session", "runtime-config"); err != nil {
		t.Fatal(err)
	}

	amount := 100.0
	usage, err := NewJobUsage(JobUsage{
		ID: "usage-record", JobID: "job-a", RoleExecutionID: "usage-exec",
		Usage: DispatchUsage{
			Model: "test-model", TotalNanoAIU: &amount,
			Accounting: &UsageAccountingIdentity{SourceSessionID: "usage-session", Sequence: 1, UsageID: "provider-usage-1"},
			ObservedAt: now.Add(4 * time.Second),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobUsage(usage); err != nil {
		t.Fatalf("append bound usage: %v", err)
	}
	if got := graph.JobUsageTotal("job-a"); got != amount {
		t.Fatalf("JobUsageTotal = %v, want %v", got, amount)
	}
	if err := graph.CheckRoleExecutionOperation("usage-exec", "repository.read", now.Add(5*time.Second)); err == nil {
		t.Fatal("privileged operation must stop when the Job AIU ceiling is exhausted")
	}

	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "next-exec", JobID: "job-a", Role: "PLANNER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(6 * time.Second), UpdatedAt: now.Add(6 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.CheckRoleExecutionStart("next-exec", now.Add(7*time.Second)); err == nil {
		t.Fatal("new RoleExecution must not start after the Job AIU ceiling is exhausted")
	}
}

func TestJobUsageRejectsDuplicateProviderAccountingIdentity(t *testing.T) {
	now := time.Now().UTC()
	graph := newJobGraph()
	graph.Tasks[0].Repo = "example/repo"
	graph.Tasks[0].FileAccess = &FileAccessPolicy{Level: FileAccessExact, Paths: []string{"src/main.go"}}
	graph.Jobs[0].Contract = validJobContract(t)
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "usage-exec", JobID: "job-a", TaskID: "task-a", Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("usage-exec", RoleExecutionRunning, "controller", "start", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.BindRoleExecutionSession("usage-exec", "usage-session", "runtime-config"); err != nil {
		t.Fatal(err)
	}
	amount := 1.0
	makeUsage := func(id string) JobUsage {
		usage, err := NewJobUsage(JobUsage{
			ID: id, JobID: "job-a", RoleExecutionID: "usage-exec",
			Usage: DispatchUsage{
				Model: "test-model", TotalNanoAIU: &amount,
				Accounting: &UsageAccountingIdentity{SourceSessionID: "usage-session", Sequence: 1, UsageID: "same-provider-record"},
				ObservedAt: now.Add(2 * time.Second),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return usage
	}
	if err := graph.AppendJobUsage(makeUsage("usage-one")); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobUsage(makeUsage("usage-two")); err == nil {
		t.Fatal("duplicate provider accounting identity must be rejected")
	}
}

func TestCloneJobUsageCopiesProviderUsagePointers(t *testing.T) {
	isAuto := true
	input, output, cacheRead, cacheWrite := int64(1), int64(2), int64(3), int64(4)
	amount := 5.0
	credits := "available"
	original := JobUsage{Usage: DispatchUsage{
		IsAuto: &isAuto, InputTokens: &input, OutputTokens: &output,
		CacheReadTokens: &cacheRead, CacheWriteTokens: &cacheWrite,
		TotalNanoAIU: &amount, AICreditsStatus: &credits,
		Accounting: &UsageAccountingIdentity{SourceSessionID: "session", Sequence: 1, UsageID: "usage"},
	}}
	cloned := cloneJobUsage(original)
	*cloned.Usage.IsAuto = false
	*cloned.Usage.InputTokens = 10
	*cloned.Usage.OutputTokens = 20
	*cloned.Usage.CacheReadTokens = 30
	*cloned.Usage.CacheWriteTokens = 40
	*cloned.Usage.TotalNanoAIU = 50
	*cloned.Usage.AICreditsStatus = "exhausted"
	cloned.Usage.Accounting.Sequence = 2

	if !*original.Usage.IsAuto || *original.Usage.InputTokens != 1 ||
		*original.Usage.OutputTokens != 2 || *original.Usage.CacheReadTokens != 3 ||
		*original.Usage.CacheWriteTokens != 4 || *original.Usage.TotalNanoAIU != 5 ||
		*original.Usage.AICreditsStatus != "available" || original.Usage.Accounting.Sequence != 1 {
		t.Fatal("cloned JobUsage shares mutable Provider usage pointers with the original")
	}
}
