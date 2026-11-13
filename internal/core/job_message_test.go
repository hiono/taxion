package core

import (
	"testing"
	"time"
)

func TestJobConversationAllowsNaturalLanguageWithoutQuestionSchema(t *testing.T) {
	graph := TaskGraph{
		Version:    1,
		Jobs:       []Job{{ID: "job-1", Request: "request", Goal: "goal", Status: JobActive, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}},
		Tasks:      []GraphTask{{ID: "task-1", JobID: "job-1"}},
		Dispatches: []GraphDispatch{{ID: "d1", TaskID: "task-1", Recipient: "CODER"}},
		JobMessages: []JobMessage{
			{ID: "m1", TaskID: "task-1", FromRole: "CODER", ToRole: "PLANNER",
				Body:   "この仕様の意図を確認したい。現状の情報から実装できる部分は先に進める。",
				Source: "dispatch:d1", SourceDispatchID: "d1", At: time.Now().UTC()},
			{ID: "m2", TaskID: "task-1", FromRole: "PLANNER", ToRole: "CODER",
				Body:   "既存仕様を優先する。例外条件は現状のままとする。",
				Source: "planner-session:job-1", ReplyTo: "m1", At: time.Now().UTC()},
		},
	}
	if err := graph.validateJobMessages(); err != nil {
		t.Fatalf("natural-language exchange should be accepted: %v", err)
	}
	if got := len(graph.JobConversation("job-1")); got != 2 {
		t.Fatalf("conversation length = %d, want 2", got)
	}
}

func TestJobConversationRejectsReplyToDifferentJobOrRole(t *testing.T) {
	graph := TaskGraph{
		Version:    1,
		Tasks:      []GraphTask{{ID: "job-1"}, {ID: "job-2"}},
		Dispatches: []GraphDispatch{{ID: "d1", TaskID: "job-1", Recipient: "CODER"}},
		JobMessages: []JobMessage{
			{ID: "m1", TaskID: "job-1", FromRole: "CODER", ToRole: "PLANNER",
				Body: "質問", Source: "dispatch:d1", SourceDispatchID: "d1", At: time.Now().UTC()},
			{ID: "m2", TaskID: "job-2", FromRole: "PLANNER", ToRole: "CODER",
				Body: "回答", Source: "planner-session", ReplyTo: "m1", At: time.Now().UTC()},
		},
	}
	if err := graph.validateJobMessages(); err == nil {
		t.Fatal("cross-Job reply must be rejected")
	}
}


func TestJobMessageReplyBindsExactRoleExecutions(t *testing.T) {
	now := time.Now().UTC()
	graph := TaskGraph{
		Version: 1,
		Jobs: []Job{{ID: "job-reply", Request: "request", Goal: "goal",
			Status: JobActive, CreatedAt: now, UpdatedAt: now}},
	}
	for _, execution := range []RoleExecution{
		{ID: "coder", JobID: "job-reply", Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "coder-2", JobID: "job-reply", Role: "CODER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "planner-1", JobID: "job-reply", Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		{ID: "planner-2", JobID: "job-reply", Role: "PLANNER", Status: RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
	} {
		if err := graph.AppendRoleExecution(execution); err != nil {
			t.Fatalf("append execution %s: %v", execution.ID, err)
		}
	}
	if err := graph.AppendJobMessage(JobMessage{
		ID: "request-1", JobID: "job-reply", FromRole: "CODER", ToRole: "PLANNER",
		Body: "Please check this assumption.", Source: "role-execution:coder",
		SourceExecutionID: "coder", TargetExecutionID: "planner-1", At: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("append request: %v", err)
	}

	wrongSender := JobMessage{
		ID: "reply-wrong-sender", JobID: "job-reply", FromRole: "PLANNER", ToRole: "CODER",
		Body: "Answer from another Planner execution.", Source: "role-execution:planner-2",
		SourceExecutionID: "planner-2", TargetExecutionID: "coder",
		ReplyTo: "request-1", At: now.Add(2 * time.Second),
	}
	if err := graph.AppendJobMessage(wrongSender); err == nil {
		t.Fatal("reply from a different execution of the same Role must be rejected")
	}

	wrongTarget := wrongSender
	wrongTarget.ID = "reply-wrong-target"
	wrongTarget.SourceExecutionID = "planner-1"
	wrongTarget.TargetExecutionID = "coder-2"
	if err := graph.AppendJobMessage(wrongTarget); err == nil {
		t.Fatal("reply targeting a different originating execution must be rejected")
	}

	validReply := wrongSender
	validReply.ID = "reply-valid"
	validReply.SourceExecutionID = "planner-1"
	if err := graph.AppendJobMessage(validReply); err != nil {
		t.Fatalf("reply from and to the exact addressed executions should be accepted: %v", err)
	}
}
