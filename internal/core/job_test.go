package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func newJobGraph() TaskGraph {
	now := time.Now().UTC()
	return TaskGraph{
		Version: 1,
		Jobs:    []Job{{
			ID:     "job-a", Request: "Original request", Goal: "Complete the requested work",
			Status: JobActive, CreatedAt: now, UpdatedAt: now,
		}},
		Tasks: []GraphTask{{ID: "task-a", JobID: "job-a"}},
	}
}

func TestLegacyJobTaskMembershipMigratesToTaskJobID(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"jobs": [{
			"id": "job-legacy",
			"request": "Original request",
			"goal": "Complete work",
			"rootTaskId": "task-legacy",
			"taskIds": ["task-legacy", "task-child"],
			"status": "ACTIVE",
			"createdAt": "2026-11-13T00:00:00Z",
			"updatedAt": "2026-11-13T00:00:00Z"
		}],
		"tasks": [
			{"id": "task-legacy"},
			{"id": "task-child"}
		]
	}`)
	var graph TaskGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		t.Fatalf("migrate legacy Job membership: %v", err)
	}
	for _, taskID := range []string{"task-legacy", "task-child"} {
		task, ok := graph.GraphTask(taskID)
		if !ok || task.JobID != "job-legacy" {
			t.Fatalf("Task %s JobID = %q, want job-legacy", taskID, task.JobID)
		}
	}
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"taskIds"`) || strings.Contains(string(encoded), `"rootTaskId"`) {
		t.Fatalf("legacy reverse membership leaked into current model: %s", encoded)
	}
}

func TestLegacyJobMigrationRejectsMissingTaskReferences(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"jobs": [{
			"id": "job-legacy",
			"request": "Original request",
			"goal": "Complete work",
			"rootTaskId": "task-missing",
			"taskIds": ["task-missing"],
			"status": "ACTIVE",
			"createdAt": "2026-11-13T00:00:00Z",
			"updatedAt": "2026-11-13T00:00:00Z"
		}],
		"tasks": []
	}`)
	var graph TaskGraph
	if err := json.Unmarshal(data, &graph); err == nil {
		t.Fatal("legacy Job references to missing Tasks must not be silently discarded")
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
		ID:        "exec-coder", JobID: "job-a", TaskID: "task-a", Role: "CODER", Status: RoleExecutionCreated,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create Coder execution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-coder", RoleExecutionRunning, "controller", "authorized start", now.Add(time.Second)); err != nil {
		t.Fatalf("start Coder execution: %v", err)
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID:                "m1", JobID: "job-a", FromRole: "CODER", ToRole: "PLANNER",
		Body:              "この仕様の意図を確認したい。", Source: "role-execution:exec-coder",
		SourceExecutionID: "exec-coder", At: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("append request: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-coder", RoleExecutionWaiting, "controller", "delegated to Planner", now.Add(3*time.Second)); err != nil {
		t.Fatalf("wait for delegated Role: %v", err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID:               "exec-planner", JobID: "job-a", Role: "PLANNER", ParentExecutionID: "exec-coder",
		TriggerMessageID: "m1", Status: RoleExecutionCreated,
		CreatedAt:        now.Add(4 * time.Second), UpdatedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("create Planner execution: %v", err)
	}
	if err := graph.TransitionRoleExecution("exec-planner", RoleExecutionRunning, "controller", "authorized delegated execution", now.Add(5*time.Second)); err != nil {
		t.Fatalf("start Planner execution: %v", err)
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID:                "m2", JobID: "job-a", FromRole: "PLANNER", ToRole: "CODER",
		Body:              "既存仕様を優先する。", Source: "role-execution:exec-planner",
		SourceExecutionID: "exec-planner", TargetExecutionID: "exec-coder",
		ReplyTo:           "m1", At: now.Add(6 * time.Second),
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
		ID:     "job-b", Request: "Other request", Goal: "Other goal",
		Status: JobActive, CreatedAt: now, UpdatedAt: now,
	})
	graph.Tasks = append(graph.Tasks, GraphTask{ID: "task-b", JobID: "job-b"})
	if err := graph.AppendRoleExecution(RoleExecution{
		ID:     "exec-a", JobID: "job-a", TaskID: "task-a", Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create parent execution: %v", err)
	}
	err := graph.AppendRoleExecution(RoleExecution{
		ID:                "exec-b", JobID: "job-b", TaskID: "task-b", Role: "PLANNER",
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
		ID:     "exec-1", JobID: "job-a", TaskID: "task-a", Role: "CODER",
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
		ID:                "m1", JobID: "job-a", FromRole: "ARCHITECT", ToRole: "DOMAIN_EXPERT",
		Body:              "Check this assumption", Source: "role-execution:exec-a",
		SourceExecutionID: "exec-a", TargetExecutionID: "exec-b", At: now,
	}); err != nil {
		t.Fatalf("dynamic Role names with valid provenance should be accepted: %v", err)
	}
}

func TestJobMessageRejectsCrossJobExecutionReply(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	graph.Jobs = append(graph.Jobs, Job{
		ID:     "job-b", Request: "Other request", Goal: "Other goal",
		Status: JobActive, CreatedAt: now, UpdatedAt: now,
	})
	graph.Tasks = append(graph.Tasks, GraphTask{ID: "task-b", JobID: "job-b"})
	for _, execution := range []RoleExecution{
		{ID: "exec-a", JobID: "job-a", Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "exec-b", JobID: "job-b", Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
	} {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("create execution: %v", err)
		}
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID:   "m1", JobID: "job-a", FromRole: "CODER", ToRole: "PLANNER",
		Body: "Question", Source: "role-execution:exec-a", SourceExecutionID: "exec-a", At: now,
	}); err != nil {
		t.Fatalf("append request: %v", err)
	}
	err := graph.AppendJobMessage(JobMessage{
		ID:      "m2", JobID: "job-b", FromRole: "PLANNER", ToRole: "CODER",
		Body:    "Answer", Source: "role-execution:exec-b", SourceExecutionID: "exec-b",
		ReplyTo: "m1", At: now.Add(time.Second),
	})
	if err == nil {
		t.Fatal("cross-Job reply must be rejected")
	}
}

func TestCloneGraphCopiesTaskOwnership(t *testing.T) {
	graph := newJobGraph()
	cloned := cloneGraph(graph)
	cloned.Tasks[0].JobID = "changed"
	if graph.Tasks[0].JobID != "job-a" {
		t.Fatal("cloneGraph shares Task ownership state")
	}
}

func TestJobConversationUsesFirstClassJobID(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	graph.JobMessages = []JobMessage{{
		ID:   "message-1", JobID: "job-a", FromRole: "HUMAN", ToRole: "PLANNER",
		Body: "Please assess this assumption.", Source: "human:request", At: now,
	}}
	if got := len(graph.JobConversation("job-a")); got != 1 {
		t.Fatalf("JobConversation(JobID) = %d, want 1", got)
	}
}

func TestJobExistsWithoutTasksAndMultipleTasksShareIt(t *testing.T) {
	now := time.Now().UTC()
	graph := TaskGraph{Version: 1}
	if err := graph.CreateJob(Job{ID: "job-empty", Request: "Request", Goal: "Goal", Status: JobDraft, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("Job must exist independently of Tasks: %v", err)
	}
	first := graphTask("task-one")
	first.JobID = "job-empty"
	if err := graph.Track(first, "test", "first Task"); err != nil {
		t.Fatalf("track first Task: %v", err)
	}
	second := graphTask("task-two")
	second.JobID = "job-empty"
	second.AuthorizationID = "decision:2"
	second.Decisions[0].ID = "decision:2"
	if err := graph.Track(second, "test", "second Task"); err != nil {
		t.Fatalf("track second Task: %v", err)
	}
	if got, ok := graph.JobForTask("task-one"); !ok || got.ID != "job-empty" {
		t.Fatal("Task.JobID did not resolve its Job")
	}
	if got, ok := graph.JobForTask("task-two"); !ok || got.ID != "job-empty" {
		t.Fatal("second Task did not resolve the shared Job")
	}
}

func TestJobStateTransitionsAreAudited(t *testing.T) {
	now := time.Now().UTC()
	graph := TaskGraph{Version: 1, Tasks: []GraphTask{{ID: "task-root"}}}
	if err := graph.CreateJob(Job{
		ID:     "job-root", Request: "Original request", Goal: "Complete work",
		Status: JobDraft, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	for i, status := range []JobStatus{JobActive, JobWaiting, JobActive} {
		if err := graph.TransitionJob("job-root", status, "controller", "test transition", now.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatalf("TransitionJob to %s: %v", status, err)
		}
	}
	if len(graph.JobHistory) != 4 || graph.Jobs[0].Status != JobActive {
		t.Fatalf("Job history does not match current state: %+v", graph.JobHistory)
	}
	if err := graph.TransitionJob("job-root", JobDraft, "controller", "invalid regression", now.Add(5*time.Second)); err == nil {
		t.Fatal("invalid Job state regression must be rejected")
	}
}

func TestJobConversationIncludesLegacyTaskMessagesForJob(t *testing.T) {
	graph := newJobGraph()
	now := time.Now().UTC()
	graph.JobMessages = []JobMessage{{
		ID:   "legacy-message", TaskID: "task-a", FromRole: "HUMAN", ToRole: "PLANNER",
		Body: "Legacy task-scoped message", Source: "human:legacy", At: now,
	}}
	if got := len(graph.JobConversation("job-a")); got != 1 {
		t.Fatalf("JobConversation(JobID) omitted legacy Task message: %d", got)
	}
}
