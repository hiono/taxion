package core

import (
	"testing"
	"time"
)

func TestJobCompletionReadyBlocksSuspendedRoleExecution(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	job := Job{
		ID: "job-completion", Request: "request", Goal: "goal",
		Status: JobActive, CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	task := GraphTask{
		ID: "task-completion", Title: "Completion Task", JobID: job.ID, Repo: "example/repo",
		Type: "IMPLEMENTATION", Status: Draft, CreatedFrom: "test:job-artifact",
		Issue:      "NOT_REQUIRED: isolated Job completion unit test",
		FileAccess: &FileAccessPolicy{Level: FileAccessCommitted},
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}, Tasks: []GraphTask{task}}
	if err := graph.Track(task, "test", "track Job completion Task"); err != nil {
		t.Fatal(err)
	}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-completion", "human", "user", "approve", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}

	executions := []RoleExecution{
		{ID: "exec-coder", JobID: job.ID, TaskID: task.ID, Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "exec-reviewer", JobID: job.ID, TaskID: task.ID, Role: "REVIEWER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "exec-suspended", JobID: job.ID, TaskID: task.ID, Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	}
	for _, execution := range executions {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("append %s: %v", execution.ID, err)
		}
	}
	for _, transition := range []struct {
		id     string
		status RoleExecutionStatus
		at     time.Time
	}{
		{"exec-coder", RoleExecutionRunning, now.Add(3 * time.Second)},
		{"exec-coder", RoleExecutionSucceeded, now.Add(4 * time.Second)},
		{"exec-reviewer", RoleExecutionRunning, now.Add(3 * time.Second)},
		{"exec-reviewer", RoleExecutionSucceeded, now.Add(5 * time.Second)},
		{"exec-suspended", RoleExecutionRunning, now.Add(3 * time.Second)},
		{"exec-suspended", RoleExecutionSuspended, now.Add(4 * time.Second)},
	} {
		if err := graph.TransitionRoleExecution(transition.id, transition.status, "controller", "test transition", transition.at); err != nil {
			t.Fatalf("transition %s to %s: %v", transition.id, transition.status, err)
		}
	}
	output, err := NewJobOutput(JobOutput{
		ID: "output-completion", JobID: job.ID, TaskID: task.ID,
		RoleExecutionID: "exec-coder", Repository: task.Repo, Kind: "test-report",
		ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:     now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobOutput(output); err != nil {
		t.Fatal(err)
	}
	evidence, err := NewJobEvidence(JobEvidence{
		ID: "evidence-completion", JobID: job.ID, TaskID: task.ID,
		ProducedByExecutionID: "exec-coder", OutputID: output.ID,
		Acceptance: "tests pass", Kind: "test-report",
		ContentSHA256: output.ContentSHA256, Status: "passed",
		VerifiedBy: "REVIEWER", VerifiedByExecutionID: "exec-reviewer",
		CreatedAt: now.Add(6 * time.Second), VerifiedAt: now.Add(7 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEvidence(evidence); err != nil {
		t.Fatal(err)
	}

	ready, missing := graph.JobCompletionReady(job.ID)
	if ready {
		t.Fatal("Job must not complete while a RoleExecution is suspended")
	}
	found := false
	for _, item := range missing {
		if item == "unresolved RoleExecution: exec-suspended" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("suspended execution was not reported as unresolved: %v", missing)
	}
}

func TestJobArtifactsSupportRoleExecutionsWithoutTask(t *testing.T) {
	now := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	job := Job{
		ID: "job-without-task", Request: "request", Goal: "goal",
		Status: JobActive, CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-without-task", "human", "user", "approve", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}

	for _, execution := range []RoleExecution{
		{ID: "exec-without-task-producer", JobID: job.ID, Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "exec-without-task-reviewer", JobID: job.ID, Role: "REVIEWER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "exec-without-task-planner", JobID: job.ID, Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	} {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("append %s: %v", execution.ID, err)
		}
	}
	for _, transition := range []struct {
		id     string
		status RoleExecutionStatus
		at     time.Time
	}{
		{"exec-without-task-producer", RoleExecutionRunning, now.Add(3 * time.Second)},
		{"exec-without-task-producer", RoleExecutionSucceeded, now.Add(4 * time.Second)},
		{"exec-without-task-reviewer", RoleExecutionRunning, now.Add(3 * time.Second)},
		{"exec-without-task-reviewer", RoleExecutionSucceeded, now.Add(5 * time.Second)},
		{"exec-without-task-planner", RoleExecutionRunning, now.Add(3 * time.Second)},
		{"exec-without-task-planner", RoleExecutionSucceeded, now.Add(5 * time.Second)},
	} {
		if err := graph.TransitionRoleExecution(transition.id, transition.status, "controller", "test transition", transition.at); err != nil {
			t.Fatalf("transition %s to %s: %v", transition.id, transition.status, err)
		}
	}

	output, err := NewJobOutput(JobOutput{
		ID: "output-without-task", JobID: job.ID,
		RoleExecutionID: "exec-without-task-producer", Repository: "example/repo",
		Kind:          "test-report",
		ContentSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedAt:     now.Add(5 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobOutput(output); err != nil {
		t.Fatalf("append Job-level output without Task: %v", err)
	}

	evidence, err := NewJobEvidence(JobEvidence{
		ID: "evidence-without-task", JobID: job.ID,
		ProducedByExecutionID: "exec-without-task-producer", OutputID: output.ID,
		Acceptance: "tests pass", Kind: "test-report",
		ContentSHA256: output.ContentSHA256, Status: "passed",
		VerifiedBy: "REVIEWER", VerifiedByExecutionID: "exec-without-task-reviewer",
		CreatedAt: now.Add(6 * time.Second), VerifiedAt: now.Add(7 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEvidence(evidence); err != nil {
		t.Fatalf("append Job-level evidence without Task: %v", err)
	}
	if err := graph.Validate(); err != nil {
		t.Fatalf("valid Job-level artifacts without Tasks must validate: %v", err)
	}

	outOfScope, err := NewJobOutput(JobOutput{
		ID: "output-outside-job-scope", JobID: job.ID,
		RoleExecutionID: "exec-without-task-producer", Repository: "unapproved/repo",
		Kind:          "test-report",
		ContentSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CreatedAt:     now.Add(8 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobOutput(outOfScope); err == nil {
		t.Fatal("Job-level output outside the approved repository scope must be rejected")
	}
	if len(graph.JobOutputs) != 1 {
		t.Fatal("rejected Job-level output must not mutate the graph")
	}

	mismatchedTask, err := NewJobOutput(JobOutput{
		ID: "output-task-mismatch", JobID: job.ID, TaskID: "unrelated-task",
		RoleExecutionID: "exec-without-task-producer", Repository: "example/repo",
		Kind:          "test-report",
		ContentSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedAt:     now.Add(9 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobOutput(mismatchedTask); err == nil {
		t.Fatal("Job output TaskID must match the producing RoleExecution")
	}
	if len(graph.JobOutputs) != 1 {
		t.Fatal("rejected Task-mismatched output must not mutate the graph")
	}
}

// TestJobEvidenceRequiresReviewerExecution prevents a completed non-Reviewer
// Role from self-identifying as the verifier through the free-form VerifiedBy field.
func TestJobEvidenceRequiresReviewerExecution(t *testing.T) {
	now := time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC)
	job := Job{
		ID: "job-verifier-policy", Request: "request", Goal: "goal",
		Status: JobActive, CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	graph := TaskGraph{Version: 1, Jobs: []Job{job}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("approval-verifier-policy", "human", "user", "approve", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}

	for _, execution := range []RoleExecution{
		{ID: "producer", JobID: job.ID, Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "reviewer", JobID: job.ID, Role: "REVIEWER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "planner", JobID: job.ID, Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
	} {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("append %s: %v", execution.ID, err)
		}
	}
	for _, id := range []string{"producer", "reviewer", "planner"} {
		for _, status := range []RoleExecutionStatus{RoleExecutionRunning, RoleExecutionSucceeded} {
			if err := graph.TransitionRoleExecution(id, status, "controller", "test transition", now.Add(3*time.Second)); err != nil {
				t.Fatalf("transition %s to %s: %v", id, status, err)
			}
		}
	}

	newEvidence := func(id, verifiedBy, verifierID string) JobEvidence {
		evidence, err := NewJobEvidence(JobEvidence{
			ID: id, JobID: job.ID, ProducedByExecutionID: "producer",
			Acceptance: "tests pass", Kind: "test-report",
			ContentSHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			Status:        "passed", VerifiedBy: verifiedBy, VerifiedByExecutionID: verifierID,
			CreatedAt: now.Add(4 * time.Second), VerifiedAt: now.Add(5 * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		return evidence
	}

	if err := graph.AppendJobEvidence(newEvidence("non-reviewer-verifier", "PLANNER", "planner")); err == nil {
		t.Fatal("a completed non-Reviewer execution must not verify Job evidence")
	}
	if err := graph.AppendJobEvidence(newEvidence("producer-self-verification", "CODER", "producer")); err == nil {
		t.Fatal("the evidence producer must not verify its own evidence")
	}
	if err := graph.AppendJobEvidence(newEvidence("missing-verifier-execution", "REVIEWER", "")); err == nil {
		t.Fatal("a free-form Reviewer name without an execution must not verify evidence")
	}
	if len(graph.JobEvidence) != 0 {
		t.Fatal("rejected verification attempts must not mutate Job evidence")
	}
	if err := graph.AppendJobEvidence(newEvidence("reviewer-verification", "REVIEWER", "reviewer")); err != nil {
		t.Fatalf("an independent completed Reviewer execution must verify evidence: %v", err)
	}
	for _, verifier := range []string{"human", "controller"} {
		if err := graph.AppendJobEvidence(newEvidence("explicit-"+verifier, verifier, "")); err != nil {
			t.Fatalf("explicit %s verification must remain supported: %v", verifier, err)
		}
	}
}
