package core

import (
	"testing"
	"time"
)

func newJobGraph() TaskGraph {
	now := time.Now().UTC()
	return TaskGraph{
		Version: 1,
		Jobs: []Job{{
			ID: "job-a", Request: "Original request", Goal: "Complete the requested work",
			RootTaskID: "task-a", TaskIDs: []string{"task-a"}, Status: JobActive, CreatedAt: now, UpdatedAt: now,
		}},
		Tasks: []GraphTask{{ID: "task-a"}},
	}
}

func TestJobAndTaskHaveDistinctIdentity(t *testing.T) {
	graph := newJobGraph()
	if err := graph.validateJobModel(); err != nil {
		t.Fatalf("valid Job model rejected: %v", err)
	}
	if graph.Jobs[0].ID == graph.Tasks[0].ID {
		t.Fatal("Job and Task identity must be distinct")
	}
}

func TestRoleExecutionsCollaborateAndResumeWithinOneJob(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-coder", JobID: "job-a", TaskID: "task-a", Role: "CODER", Status: RoleExecutionCreated,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create Coder execution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-coder", RoleExecutionRunning, "controller", "authorized start", now.Add(time.Second)); err != nil {
		t.Fatalf("start Coder execution: %v", err)
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID: "m1", JobID: "job-a", FromRole: "CODER", ToRole: "PLANNER",
		Body: "この仕様の意図を確認したい。", Source: "role-execution:exec-coder",
		SourceExecutionID: "exec-coder", At: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("append request: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-coder", RoleExecutionWaiting, "controller", "delegated to Planner", now.Add(3*time.Second)); err != nil {
		t.Fatalf("wait for delegated Role: %v", err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-planner", JobID: "job-a", Role: "PLANNER", ParentExecutionID: "exec-coder",
		TriggerMessageID: "m1", Status: RoleExecutionCreated,
		CreatedAt: now.Add(4 * time.Second), UpdatedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("create Planner execution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-planner", RoleExecutionRunning, "controller", "authorized delegated execution", now.Add(5*time.Second)); err != nil {
		t.Fatalf("start Planner execution: %v", err)
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID: "m2", JobID: "job-a", FromRole: "PLANNER", ToRole: "CODER",
		Body: "既存仕様を優先する。", Source: "role-execution:exec-planner",
		SourceExecutionID: "exec-planner", TargetExecutionID: "exec-coder",
		ReplyTo: "m1", At: now.Add(6 * time.Second),
	}); err != nil {
		t.Fatalf("append response: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-planner", RoleExecutionSucceeded, "controller", "response recorded", now.Add(7*time.Second)); err != nil {
		t.Fatalf("complete Planner execution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-coder", RoleExecutionRunning, "controller", "response received", now.Add(8*time.Second)); err != nil {
		t.Fatalf("resume original Coder execution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-coder", RoleExecutionSucceeded, "controller", "work completed", now.Add(9*time.Second)); err != nil {
		t.Fatalf("complete Coder execution: %v", err)
	}
	if got := len(graph.RoleExecutionsForJob("job-a")); got != 2 {
		t.Fatalf("RoleExecutionsForJob = %d, want 2", got)
	}
	if got := len(graph.JobConversation("job-a")); got != 2 {
		t.Fatalf("JobConversation = %d, want 2", got)
	}
}

func TestRoleExecutionRejectsCrossJobParentAndTask(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	graph.Jobs = append(graph.Jobs, Job{
		ID: "job-b", Request: "Other request", Goal: "Other goal", RootTaskID: "task-b", TaskIDs: []string{"task-b"},
		Status: JobActive, CreatedAt: now, UpdatedAt: now,
	})
	graph.Tasks = append(graph.Tasks, GraphTask{ID: "task-b"})
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-a", JobID: "job-a", TaskID: "task-a", Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create parent execution: %v", err)
	}
	err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-b", JobID: "job-b", TaskID: "task-b", Role: "PLANNER",
		ParentExecutionID: "exec-a", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		t.Fatal("cross-Job parent reference must be rejected")
	}
}

func TestRoleExecutionLifecycleAndSessionBinding(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "exec-1", JobID: "job-a", TaskID: "task-a", Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("AppendRoleExecution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-1", RoleExecutionRunning, "controller", "authorized start", now.Add(time.Second)); err != nil {
		t.Fatalf("start transition: %v", err)
	}
	if err := graph.BindRoleExecutionSession("exec-1", "session-1", "config-hash"); err != nil {
		t.Fatalf("BindRoleExecutionSession: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-1", RoleExecutionWaiting, "controller", "delegated to another Role", now.Add(2*time.Second)); err != nil {
		t.Fatalf("waiting transition: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-1", RoleExecutionSucceeded, "controller", "invalid direct completion", now.Add(3*time.Second)); err == nil {
		t.Fatal("waiting execution must resume before completion")
	}
	if err := graph.TransitionRoleExecution("exec-1", RoleExecutionRunning, "controller", "response received", now.Add(4*time.Second)); err != nil {
		t.Fatalf("resume transition: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-1", RoleExecutionSucceeded, "controller", "result saved", now.Add(5*time.Second)); err != nil {
		t.Fatalf("completion transition: %v", err)
	}
	if err := graph.BindRoleExecutionSession("exec-1", "different-session", "config-hash"); err == nil {
		t.Fatal("terminal execution must not be rebound")
	}
}

func TestJobMessagesDoNotRequireFixedRoleAllowlist(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	for _, execution := range []RoleExecution{
		{ID: "exec-a", JobID: "job-a", Role: "ARCHITECT", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "exec-b", JobID: "job-a", Role: "DOMAIN_EXPERT", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
	} {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("create dynamic Role execution: %v", err)
		}
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID: "m1", JobID: "job-a", FromRole: "ARCHITECT", ToRole: "DOMAIN_EXPERT",
		Body: "Check this assumption", Source: "role-execution:exec-a",
		SourceExecutionID: "exec-a", TargetExecutionID: "exec-b", At: now,
	}); err != nil {
		t.Fatalf("dynamic Role names with valid provenance should be accepted: %v", err)
	}
}

func TestJobMessageRejectsCrossJobExecutionReply(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	graph.Jobs = append(graph.Jobs, Job{
		ID: "job-b", Request: "Other request", Goal: "Other goal", RootTaskID: "task-b", TaskIDs: []string{"task-b"},
		Status: JobActive, CreatedAt: now, UpdatedAt: now,
	})
	graph.Tasks = append(graph.Tasks, GraphTask{ID: "task-b"})
	for _, execution := range []RoleExecution{
		{ID: "exec-a", JobID: "job-a", Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "exec-b", JobID: "job-b", Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
	} {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("create execution: %v", err)
		}
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID: "m1", JobID: "job-a", FromRole: "CODER", ToRole: "PLANNER",
		Body: "Question", Source: "role-execution:exec-a", SourceExecutionID: "exec-a", At: now,
	}); err != nil {
		t.Fatalf("append request: %v", err)
	}
	err := graph.AppendJobMessage(JobMessage{
		ID: "m2", JobID: "job-b", FromRole: "PLANNER", ToRole: "CODER",
		Body: "Answer", Source: "role-execution:exec-b", SourceExecutionID: "exec-b",
		ReplyTo: "m1", At: now.Add(time.Second),
	})
	if err == nil {
		t.Fatal("cross-Job reply must be rejected")
	}
}
