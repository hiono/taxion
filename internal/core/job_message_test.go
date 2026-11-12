package core

import (
	"testing"
	"time"
)

func TestJobConversationAllowsNaturalLanguageWithoutQuestionSchema(t *testing.T) {
	graph := TaskGraph{
		Version: 1,
		Tasks:   []GraphTask{{ID: "job-1"}},
		JobMessages: []JobMessage{
			{ID: "m1", TaskID: "job-1", FromRole: "CODER", ToRole: "PLANNER",
				Body:   "この仕様の意図を確認したい。現状の情報から実装できる部分は先に進める。",
				Source: "dispatch:coder-1", At: time.Now().UTC()},
			{ID: "m2", TaskID: "job-1", FromRole: "PLANNER", ToRole: "CODER",
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
		Version: 1,
		Tasks:   []GraphTask{{ID: "job-1"}, {ID: "job-2"}},
		JobMessages: []JobMessage{
			{ID: "m1", TaskID: "job-1", FromRole: "CODER", ToRole: "PLANNER",
				Body: "質問", Source: "dispatch:coder-1", At: time.Now().UTC()},
			{ID: "m2", TaskID: "job-2", FromRole: "PLANNER", ToRole: "CODER",
				Body: "回答", Source: "planner-session", ReplyTo: "m1", At: time.Now().UTC()},
		},
	}
	if err := graph.validateJobMessages(); err == nil {
		t.Fatal("cross-Job reply must be rejected")
	}
}
