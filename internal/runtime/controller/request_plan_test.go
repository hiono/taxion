package controller

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestQuestionsPreserveExplicitEmptyAndLegacyWireShapes(t *testing.T) {
	for _, questions := range [][]string{nil, {}} {
		data, err := json.Marshal(QuestionAnswer{Questions: questions})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"questions"`) != (questions != nil) {
			t.Fatal("request lost explicit questions or changed the legacy answer shape", string(data))
		}
	}
}

func TestRequestPlanRequiresExecutableProposalOrExplicitQuestions(t *testing.T) {
	for _, mode := range []string{"ready", "question", "empty", "missing-questions", "missing-commands", "unsafe-scope", "duplicate", "multiline-command", "execution-action"} {
		t.Run(mode, func(t *testing.T) {
			answer := QuestionAnswer{Recommendation: "HOLD", Questions: []string{}, Proposal: &RequestProposal{
				Title: "Search", Objective: "Add search", Scope: []string{"src"},
				OutOfScope: []string{}, Acceptance: []string{"search matches"}, ValidationCommands: []string{"go test ./..."},
			}}
			switch mode {
			case "question":
				answer.Proposal = nil
				answer.Recommendation = "HUMAN_REQUIRED"
				answer.Questions = []string{"Which match behavior?"}
			case "empty":
				answer.Proposal = nil
			case "missing-questions":
				answer.Questions = nil
			case "missing-commands":
				answer.Proposal.ValidationCommands = nil
			case "unsafe-scope":
				answer.Proposal.Scope = []string{"../outside"}
			case "duplicate":
				answer.Proposal.Acceptance = []string{"same", "same"}
			case "multiline-command":
				answer.Proposal.ValidationCommands = []string{"go test ./...\nother"}
			case "execution-action":
				answer.Recommendation = "REPAIR"
			}
			err := ValidateRequestPlan(answer)
			if (err == nil) != (mode == "ready" || mode == "question") {
				t.Fatal("invalid planning response accepted or valid advice rejected", mode, err)
			}
		})
	}
}
