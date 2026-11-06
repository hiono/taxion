package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
)

func TestFailedReviewHelpSeparatesNegativeRecordFromQualityRecovery(t *testing.T) {
	var output bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"record-failed-review", "--help"}, &output); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for _, text := range []string{"REQUEST_CHANGES", "expectedHash", "FAILED", "remain unchanged", "No model send", "RUNNING recovery", "CANCELLED", "Routine shutdown may follow", "failedChecks", "NOT_EVALUATED"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("help omitted %q", text)
		}

	}
	if err := workflowCommand(t.Context(), []string{"record-failed-review"}, &output); err == nil {
		t.Fatal("missing explicit request accepted")
	}
}

func TestFailedReviewTimingRejectionEmitsDiagnosticsAndRetainsError(t *testing.T) {
	deadline := time.Date(2026, 11, 5, 21, 37, 8, 475923000, time.UTC)
	original := &controller.FailedReviewTimingError{
		Version: 1, Code: "failed-review-timing-rejected",
		Timing:         core.FailedReviewTiming{SubmissionDeadline: deadline},
		DeadlineSource: "flow.extensions[last].deadline", ReservedSeconds: 1230,
		FailedResultSource: "verified-result.at", FailedChecks: []string{"turn-end-not-after-failed-result"},
		RemainingEligibility: "NOT_EVALUATED",
	}
	var output bytes.Buffer
	if err := writeFailedReviewReport(&output, controller.FailedReviewReport{}, original); !errors.Is(err, original) {
		t.Fatal("timing failure lost its nonzero exit", err)
	}
	var diagnosis controller.FailedReviewTimingError
	if err := json.Unmarshal(output.Bytes(), &diagnosis); err != nil ||
		diagnosis.Code != original.Code || !diagnosis.Timing.SubmissionDeadline.Equal(deadline) ||
		diagnosis.StateChanged || diagnosis.QualityAccepted || diagnosis.ExecutionAuthorized ||
		diagnosis.DeadlineSource != original.DeadlineSource || diagnosis.ReservedSeconds != 1230 ||
		len(diagnosis.FailedChecks) != 1 || diagnosis.RemainingEligibility != "NOT_EVALUATED" {
		t.Fatal("invalid non-applying timing diagnosis", diagnosis, err)
	}
	output.Reset()
	unrelated := errors.New("unrelated rejection")
	if err := writeFailedReviewReport(&output, controller.FailedReviewReport{}, unrelated); err != unrelated || output.Len() != 0 {
		t.Fatal("unrelated rejection changed behavior", output.String(), err)
	}
}
