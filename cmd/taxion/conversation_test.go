package main

import (
	"bytes"
	"testing"
)

func TestConversationQuestionRequiresExplicitLimitsBeforeOpeningWorkspace(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"run"},
		{"ask"},
		{"ask", "--task", "task", "--question", "今どうなっていますか？"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "1m"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "1m", "--max-ai-credits", "1"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "-1", "--timeout", "1m", "--max-ai-credits", "30"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "0s", "--max-ai-credits", "30"},
		{"ask", "--task", "task", "--question", "status?", "--max-tokens", "1000", "--timeout", "1m", "--max-ai-credits", "30", "extra"},
	} {
		var output bytes.Buffer
		if err := conversationCmd(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid conversation invocation created a result", args, err)
		}
	}
}
