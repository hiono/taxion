package core

import (
	"strings"
	"testing"
)

func TestContextSourceReadsExactUnicodeOriginalAndRejectsSubstitution(t *testing.T) {
	original := strings.Repeat("原文🙂\r\n", 2000) + "This final condition is absent from the summary."
	document, err := CaptureContextDocument(ContextDocument{
		ID: "design", Kind: "design", Source: "human:design", Version: "revision-2", Text: original,
	})
	if err != nil {
		t.Fatal(err)
	}
	var read strings.Builder
	for offset := 0; ; {
		page, err := ReadContextDocument([]ContextDocument{document}, document.ID, document.Hash, offset, 7)
		if err != nil || page.Source != document.Source || page.Version != document.Version {
			t.Fatal("source provenance lost", page, err)
		}
		read.WriteString(page.Text)
		if page.EOF {
			break
		}
		offset = page.NextOffset
	}
	if read.String() != original {
		t.Fatal("original text was truncated, normalized or split inside Unicode")
	}
	for _, change := range []string{"text", "version", "source", "requested-hash", "unavailable"} {
		changed, id, hash := document, document.ID, document.Hash
		switch change {
		case "text":
			changed.Text += "substituted"
		case "version":
			changed.Version = "revision-3"
		case "source":
			changed.Source = "other-task"
		case "requested-hash":
			hash = "different"
		case "unavailable":
			id = "/private/controller.db"
		}
		if _, err := ReadContextDocument([]ContextDocument{changed}, id, hash, 0, 7); err == nil {
			t.Fatal("accepted source substitution", change)
		}
	}
}

func TestContextDecisionPinsOriginalButProjectsOnlyIndex(t *testing.T) {
	document := ContextDocument{ID: "open-matters", Kind: "questions", Source: "human:questions",
		Version: "1", Text: "Unresolved original wording, not a model summary."}
	decision, err := ContextDocumentDecision(document, "context-source:task:open-matters")
	if err != nil {
		t.Fatal(err)
	}
	documents, err := ContextDocumentsFromDecisions([]TaskDecision{decision})
	if err != nil || len(documents) != 1 || documents[0].Text != document.Text {
		t.Fatal("source capture lost the supplied wording", documents, err)
	}
	projected, err := ProjectContextSourceDecisions([]TaskDecision{decision})
	if err != nil || strings.Contains(projected[0].Text, document.Text) ||
		!strings.Contains(projected[0].Text, documents[0].Hash) {
		t.Fatal("projection repeated the body or lost the binding", err)
	}
	document.Text = "ghp_" + strings.Repeat("a", 30)
	if _, err := ContextDocumentDecision(document, "context-source:task:secret"); err == nil ||
		strings.Contains(err.Error(), document.Text) {
		t.Fatal("credential was captured or echoed", err)
	}
}

func TestLegacyVersionFiveFailedReviewSupplementRemainsReadable(t *testing.T) {
	unchanged := false
	for _, version := range []int{4, 5, 6, 7} {
		dispatch := GraphDispatch{ID: "review", TaskID: "task", Recipient: "REVIEWER", Repository: "owner/repo",
			Status: DispatchFailed, ExecutionStartSHA: strings.Repeat("a", 40),
			Instruction: DispatchInstruction{Version: version, OutputMode: OutputModeWorktreeTreeSnapshot,
				BaselineSHA: strings.Repeat("a", 40), Acceptance: []string{"criterion"}, RequiredEvidence: []string{"work-report"}}}
		result := &DispatchResult{DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
			Repository: dispatch.Repository, Status: DispatchSucceeded, StartedSHA: dispatch.ExecutionStartSHA,
			OutputSHA: dispatch.ExecutionStartSHA, Evidence: []string{"work-report"}, WorkReport: "Original finding.",
			NextTransition: WaitingForReview, ReviewVerdict: "REQUEST_CHANGES", WorktreeChanged: &unchanged,
			Acceptance: []DispatchAcceptance{{Criterion: "criterion", Status: "FAIL", Evidence: []string{"work-report"}}},
			Findings:   []DispatchFinding{{Severity: "HIGH", Title: "Original finding", Evidence: []string{"work-report"}}}}
		err := dispatch.ValidateFailedReviewSupplement(result)
		if (err == nil) != (version == 5 || version == 6) {
			t.Fatal("new source capability changed existing evidence support", version, err)
		}
	}
}
