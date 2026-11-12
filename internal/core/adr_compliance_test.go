package core

import (
	"strings"
	"testing"
	"time"
)

func TestADRComplianceBindsExactSourceAndReviewerEvidence(t *testing.T) {
	spec := Specification{
		ID: "ADR-0075", Title: "Authorization denials", Source: "repository",
		Location: "docs/adr/0075.md", Version: "approved-1", RetrievedAt: time.Now().UTC(),
		Body: "Preserve explicit denials and immutable evidence.", Usage: "independent ADR compliance review",
	}
	criterion, err := ADRComplianceCriterion(spec)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := SpecificationHash(spec)
	spec.Ingestion = &SpecificationApproval{Actor: "human", Source: "human:source-approval", Hash: hash}
	if _, err := ADRComplianceRequirements([]string{criterion}, []Specification{spec}); err != nil {
		t.Fatal(err)
	}
	changed := spec
	changed.Body += " Changed decision."
	if _, err := ADRComplianceRequirements([]string{criterion}, []Specification{changed}); err == nil {
		t.Fatal("changed ADR body retained the old acceptance binding")
	}
	if _, err := ADRComplianceRequirements([]string{criterion, ADRNotApplicableCriterion}, []Specification{spec}); err == nil {
		t.Fatal("accepted contradictory ADR applicability")
	}
	dispatch := GraphDispatch{Instruction: DispatchInstruction{
		Acceptance: []string{criterion}, Specifications: []Specification{spec},
	}}
	unchanged := false
	result := &DispatchResult{
		ReviewVerdict: "APPROVE", WorktreeChanged: &unchanged, Findings: []DispatchFinding{},
		Evidence:   []string{criterion},
		Acceptance: []DispatchAcceptance{{Criterion: criterion, Status: "PASS", Evidence: []string{criterion}}},
	}
	if err := validateReviewerResult(dispatch, result); err != nil {
		t.Fatal(err)
	}
	result.Evidence, result.Acceptance[0].Evidence = []string{"generic-test-pass"}, []string{"generic-test-pass"}
	if err := validateReviewerResult(dispatch, result); err == nil || !strings.Contains(err.Error(), "exact bound criterion") {
		t.Fatalf("generic evidence passed ADR acceptance: %v", err)
	}
	result.ReviewVerdict, result.Acceptance[0].Status = "REQUEST_CHANGES", "FAIL"
	if err := validateReviewerResult(dispatch, result); err == nil {
		t.Fatal("an unsupported negative declaration was treated as an ADR violation")
	}
	result.Evidence, result.Acceptance[0].Evidence = []string{criterion}, []string{criterion}
	if err := validateReviewerResult(dispatch, result); err != nil {
		t.Fatal(err)
	}
}

func TestADRNonApplicabilityRequiresIndependentExplanation(t *testing.T) {
	dispatch := GraphDispatch{Instruction: DispatchInstruction{Acceptance: []string{ADRNotApplicableCriterion}}}
	unchanged := false
	result := &DispatchResult{
		ReviewVerdict: "APPROVE", WorktreeChanged: &unchanged, Findings: []DispatchFinding{},
		Evidence:   []string{ADRNotApplicableCriterion},
		Acceptance: []DispatchAcceptance{{Criterion: ADRNotApplicableCriterion, Status: "PASS", Evidence: []string{ADRNotApplicableCriterion}}},
	}
	if err := validateReviewerResult(dispatch, result); err == nil {
		t.Fatal("silent ADR non-applicability passed")
	}
	result.WorkReport = "The approved fixture has no applicable architecture decisions."
	if err := validateReviewerResult(dispatch, result); err != nil {
		t.Fatal(err)
	}
}
