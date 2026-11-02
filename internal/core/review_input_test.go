package core

import (
	"encoding/json"
	"testing"
)

func TestReviewInputRejectsPolicyDowngradeAndAmbiguousPurpose(t *testing.T) {
	input := ReviewInput{
		Kind: ReviewInputKindV2, Purpose: "diagnostic", Source: "human:recorded-attempt",
		ValidationMode: "observe", Questions: []string{"Why did this attempt fail?"},
		KnownFailures: []string{"Synthetic assertion failure"}, ValidationSummary: "Failed; no retry requested",
		Evidence: []string{"Observed command exit 7; cause remains unknown"},
	}
	for _, change := range []func(*ReviewInput){
		func(r *ReviewInput) { r.Kind = ReviewInputKind },
		func(r *ReviewInput) { r.Kind = "taxion.review-input.v3" },
		func(r *ReviewInput) { r.Purpose = "quality" },
		func(r *ReviewInput) { r.ValidationMode = "" },
		func(r *ReviewInput) { r.ValidationMode = "ignore" },
	} {
		bad := input
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid review contract accepted", bad)
		}
	}
	data, _ := json.Marshal(input)
	decisions := []TaskDecision{{ID: "review", Source: "human", Text: string(data)}}
	if CheckReviewValidationCommands(decisions, nil) != nil {
		t.Fatal("explicit recorded-evidence diagnosis required an invented command")
	}
	if _, err := ReviewInputFromDecisions(append(decisions, decisions[0])); err == nil {
		t.Fatal("ambiguous review purpose accepted")
	}
}
