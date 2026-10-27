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
			"this does not complete the\nuser's larger request",
			"Do not declare the request complete by redefining it",
			"When final acceptance is supported, reconcile requested ADR, Issue, milestone and",
			"verify required publication before declaring completion",
			"Do not close\nunmet criteria or invent records or publication requirements",
			"not stop\nall work unless the user requests a pause",
			"implement the correction before commissioning",
			"If one path is blocked, continue independent approved work",
			"only when no safe approved action remains",
			"Public GitHub records are for users",
			"private audit evidence",
			"never rewrite it after",
			"user-visible behavior, impact, problems and",
			"not merely internal assignments or procedures",
			"Keep the primary\nlanguage consistent",
			"Communication Profile before public text preparation",
			"Never inherit Taxion's language or audience",
			"without the shared public-text gate",
			"consumer-project records read-only",
			"Verify each referenced record exists and reached the claimed state",
			"keep dated later updates separate",
			"Apply adaptive behavior throughout planning, coding, validation, review, recovery",
			"Model responses can vary with conversation context",
			"do not prove output quality",
			"another internal\nrepresentation of consent already recorded",
			"do not infer missing approval or bypass a failed gate",
			"Compatible tool updates and routine method changes alone are not new permissions",
			"actual required capabilities and saved-session compatibility",
			"does not require re-entering the total quota",
			"do not replay uncertain sends or extend deadlines and budgets",
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
