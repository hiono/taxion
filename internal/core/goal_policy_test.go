package core

import "testing"

func TestDocumentationAcceptanceCannotBeCompensatedByOtherPasses(t *testing.T) {
	dispatch := GraphDispatch{Instruction: DispatchInstruction{
		Acceptance: []string{"behavior matches requirements", DocumentationAcceptance},
	}}
	changed := false
	passing := func() *DispatchResult {
		return &DispatchResult{
			ReviewVerdict: "APPROVE", WorktreeChanged: &changed,
			ChangedFiles: []string{}, Findings: []DispatchFinding{}, Evidence: []string{"behavior-check", "same-target-doc-check"},
			Acceptance: []DispatchAcceptance{
				{Criterion: dispatch.Instruction.Acceptance[0], Status: "PASS", Evidence: []string{"behavior-check"}},
				{Criterion: DocumentationAcceptance, Status: "PASS", Evidence: []string{"same-target-doc-check"}},
			},
		}
	}
	for _, problem := range []string{"stale-code", "stale-docs", "no-evidence", "missing", "unnecessary-review"} {
		t.Run(problem, func(t *testing.T) {
			result := passing()
			switch problem {
			case "stale-code":
				result.Acceptance[0].Status = "FAIL"
			case "stale-docs":
				result.Acceptance[1].Status = "FAIL"
			case "no-evidence":
				result.Acceptance[1].Evidence = nil
			case "missing":
				result.Acceptance = result.Acceptance[:1]
			case "unnecessary-review":
				result.ReviewVerdict = "REQUEST_CHANGES"
			}
			if err := validateReviewerResult(dispatch, result); err == nil {
				t.Fatal("accepted unmet documentation/quality or unsupported extra review")
			}
		})
	}
	if err := validateReviewerResult(dispatch, passing()); err != nil {
		t.Fatal("rejected supported completion", err)
	}
	advice := passing()
	advice.WorkReport = "Optional preference outside acceptance: consider renaming this helper in a future cleanup."
	if err := validateReviewerResult(dispatch, advice); err != nil {
		t.Fatal("non-blocking advice forced another repair/review cycle", err)
	}
	defect := passing()
	defect.Findings = []DispatchFinding{{Severity: "LOW", Title: "Observed incorrect output", Evidence: []string{"behavior-check"}}}
	if err := validateReviewerResult(dispatch, defect); err == nil {
		t.Fatal("cost policy waived an actual defect by severity")
	}
	result := passing()
	result.ReviewVerdict, result.Acceptance[1].Status = "REQUEST_CHANGES", "FAIL"
	if err := validateReviewerResult(dispatch, result); err != nil {
		t.Fatal("rejected evidenced documentation defect", err)
	}
}
