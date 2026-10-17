package main

import (
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestGoalPolicyCoversEveryRoleAndRuntimePrompt(t *testing.T) {
	prompts := []string{readyPrompt, requestPlannerPrompt, plannerSystemPrompt, questionSystemPrompt}
	for _, role := range []string{requestCoderPrompt, requestReviewerPrompt, "Custom role"} {
		for _, submission := range []bool{false, true} {
			prompts = append(prompts, runtimeSubmissionPrompt(role, "contract", submission))
		}
	}
	for index, prompt := range prompts {
		if strings.Count(prompt, goalExecutionPolicy) != 1 || strings.Count(prompt, goalRolePolicy) != 1 {
			t.Errorf("prompt %d lacks a single shared and role-specific policy", index)
		}
		for _, constraint := range []string{
			"not rewards", "reversible stated assumption", "Never omit safety",
			"new evidence", "material mismatch blocks completion", "Missing measurements are unknown",
			"PLANNER:", "CODER:", "REVIEWER:", "ORCHESTRATOR:",
			"safe reversible defaults", "without\nmicro-confirmations",
			"non-blocking advice in workReport", "Never\nrelabel an actual defect",
			"not repeated check-ins", "cannot change the outcome",
		} {
			if !strings.Contains(prompt, constraint) {
				t.Errorf("prompt %d lacks %q", index, constraint)
			}
		}
	}
	if !strings.Contains(readyPrompt, core.DocumentationAcceptance) {
		t.Fatal("outer orchestrator lacks the exact documentation acceptance contract")
	}
}
