package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestFailedReviewHelpSeparatesNegativeRecordFromQualityRecovery(t *testing.T) {
	var output bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"record-failed-review", "--help"}, &output); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for _, text := range []string{"REQUEST_CHANGES", "expectedHash", "FAILED", "remain unchanged", "No model send", "RUNNING recovery", "CANCELLED", "Routine shutdown may follow", "failedChecks", "NOT_EVALUATED", "observation-timeout", "NOT_WRITTEN", "UNKNOWN", "COMMITTED"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("help omitted %q", text)
		}

	}
	if err := workflowCommand(t.Context(), []string{"record-failed-review"}, &output); err == nil {
		t.Fatal("missing explicit request accepted")
	}
}

func TestFailedReviewSetupFailureIsRedactedAndPhased(t *testing.T) {
	var output bytes.Buffer
	secretPath := "/private/path-that-must-not-be-reported.json"
	err := recordFailedReviewCommand(t.Context(), []string{"--file", secretPath, "--preview"}, &output)
	var failure *controller.FailedReviewOperationError
	if !errors.As(err, &failure) || failure.Phase != "file-open" || failure.Persistence != "NOT_WRITTEN" {
		t.Fatal("setup failure lacks safe diagnostics", err)
	}
	if strings.Contains(output.String(), secretPath) || strings.Contains(output.String(), "no such file") {
		t.Fatal("diagnostic exposed a native setup error", output.String())
	}
	var decoded controller.FailedReviewOperationError
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil ||
		decoded.Version != 1 || decoded.Phase != failure.Phase || decoded.Persistence != "NOT_WRITTEN" {
		t.Fatal("invalid failure JSON", output.String(), err)
	}
}

func TestFailedReviewCLISetupUsesSyntheticStoreAndEmitsPhaseJSON(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "workspace", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	if err := os.MkdirAll(filepath.Dir(workspaceDBPath()), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "decision.json")
	request := `{"flowId":"synthetic-flow","dispatchId":"synthetic-dispatch","expectedRunnerId":"","previousProcessStopped":true,"actor":"test","source":"test:synthetic","reason":"exercise public CLI setup","evidence":["synthetic-stop"]}`
	if err := os.WriteFile(requestPath, []byte(request), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = workflowCommand(t.Context(), []string{"record-failed-review", "--file", requestPath, "--preview"}, &output)
	var failure *controller.FailedReviewOperationError
	if !errors.As(err, &failure) || failure.Phase != "initial-read" ||
		failure.Persistence != "NOT_WRITTEN" || strings.Contains(output.String(), requestPath) {
		t.Fatal("synthetic CLI setup failure was not safely diagnosed", output.String(), err)
	}
}

func TestFailedReviewTimeoutOptionIsBounded(t *testing.T) {
	for _, value := range []string{"0", "-1s", "5m1s"} {
		var output bytes.Buffer
		err := recordFailedReviewCommand(t.Context(), []string{"--observation-timeout", value}, &output)
		var failure *controller.FailedReviewOperationError
		if !errors.As(err, &failure) || failure.Phase != "configuration" || failure.Persistence != "NOT_WRITTEN" {
			t.Fatalf("invalid timeout %q lacks safe configuration diagnostics: %v", value, err)
		}
		if strings.Contains(output.String(), value) {
			t.Fatalf("invalid timeout value leaked into diagnostics: %s", output.String())
		}
		if strings.Contains(output.String(), "effectiveDeadline") {
			t.Fatalf("configuration rejection reported an unstarted observation deadline: %s", output.String())
		}
	}
}

func TestFailedReviewOutputFailureRemainsCommitted(t *testing.T) {
	err := writeFailedReviewReport(failedReviewErrorWriter{}, controller.FailedReviewReport{StateChanged: true}, nil)
	var failure *controller.FailedReviewOperationError
	if !errors.As(err, &failure) || failure.Phase != "output" || failure.Persistence != "COMMITTED" {
		t.Fatal("output failure lost committed persistence state", err)
	}
	err = writeFailedReviewReport(failedReviewErrorWriter{}, controller.FailedReviewReport{}, nil)
	if !errors.As(err, &failure) || failure.Phase != "output" || failure.Persistence != "NOT_WRITTEN" {
		t.Fatal("preview output failure claimed persistence", err)
	}
}

type failedReviewErrorWriter struct{}

func (failedReviewErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("private output failure")
}

func TestFailedReviewCleanupRetainsPrimaryPhase(t *testing.T) {
	primary := controller.NewFailedReviewOperationError("native-read", "NOT_WRITTEN", errors.New("private native failure"))
	err := failedReviewCleanupFailure(primary, "observation", "NOT_WRITTEN", errors.New("private cleanup failure"))
	var output bytes.Buffer
	_ = writeFailedReviewReport(&output, controller.FailedReviewReport{}, err)
	var diagnostic controller.FailedReviewOperationError
	if json.Unmarshal(output.Bytes(), &diagnostic) != nil || diagnostic.Phase != "native-read" ||
		!diagnostic.CleanupFailed || strings.Contains(output.String(), "private") || primary.CleanupFailed {
		t.Fatalf("cleanup obscured or mutated the primary diagnostic: %s", output.String())
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
		len(diagnosis.FailedChecks) != 1 || diagnosis.RemainingEligibility != "NOT_EVALUATED" ||
		diagnosis.Phase != "native-read" || diagnosis.Cause != "timing-rejected" ||
		diagnosis.Persistence != "NOT_WRITTEN" || diagnosis.Message == "" {
		t.Fatal("invalid non-applying timing diagnosis", diagnosis, err)
	}
	output.Reset()
	withCleanup := failedReviewCleanupFailure(original, "native-timing", "NOT_WRITTEN", errors.New("private cleanup failure"))
	if err := writeFailedReviewReport(&output, controller.FailedReviewReport{}, withCleanup); !errors.Is(err, original) {
		t.Fatal("cleanup discarded the timing error identity", err)
	}
	if err := json.Unmarshal(output.Bytes(), &diagnosis); err != nil ||
		diagnosis.Code != original.Code || !diagnosis.Timing.SubmissionDeadline.Equal(deadline) ||
		diagnosis.DeadlineSource != original.DeadlineSource || len(diagnosis.FailedChecks) != 1 ||
		diagnosis.FailedChecks[0] != original.FailedChecks[0] || !diagnosis.CleanupFailed ||
		diagnosis.Phase != "native-timing" || diagnosis.Persistence != "NOT_WRITTEN" ||
		original.CleanupFailed || original.Phase != "" || strings.Contains(output.String(), "private") {
		t.Fatal("cleanup obscured or mutated the timing diagnostic", output.String(), err)
	}
	output.Reset()
	wrapped := controller.NewFailedReviewOperationError("native-read", "NOT_WRITTEN", original)
	wrapped.ObservationTimeout = "30s"
	wrapped.EffectiveDeadline = deadline
	wrapped.ElapsedMilliseconds = 42
	_ = writeFailedReviewReport(&output, controller.FailedReviewReport{}, wrapped)
	if err := json.Unmarshal(output.Bytes(), &diagnosis); err != nil ||
		diagnosis.ObservationTimeout != "30s" || !diagnosis.EffectiveDeadline.Equal(deadline) ||
		diagnosis.ElapsedMilliseconds != 42 || original.ObservationTimeout != "" ||
		!strings.Contains(wrapped.Error(), "phase=native-read") ||
		!strings.Contains(wrapped.Error(), "persistence=NOT_WRITTEN") {
		t.Fatal("timing projection lost operation diagnostics", output.String(), err)
	}
	output.Reset()
	unrelated := errors.New("unrelated rejection")
	if err := writeFailedReviewReport(&output, controller.FailedReviewReport{}, unrelated); !errors.Is(err, unrelated) || output.Len() == 0 {
		t.Fatal("unrelated rejection lacks safe failure diagnostics", output.String(), err)
	}
	var operation controller.FailedReviewOperationError
	if err := json.Unmarshal(output.Bytes(), &operation); err != nil ||
		operation.Phase != "observation" || operation.Persistence != "NOT_WRITTEN" ||
		strings.Contains(output.String(), "unrelated rejection") {
		t.Fatal("generic failure diagnostic was unsafe", output.String(), err)
	}
	output.Reset()
	withCleanup = failedReviewCleanupFailure(unrelated, "runtime-setup", "NOT_WRITTEN", errors.New("private cleanup failure"))
	if err := writeFailedReviewReport(&output, controller.FailedReviewReport{}, withCleanup); !errors.Is(err, unrelated) {
		t.Fatal("cleanup discarded the setup error identity", err)
	}
	if err := json.Unmarshal(output.Bytes(), &operation); err != nil ||
		operation.Phase != "runtime-setup" || operation.Persistence != "NOT_WRITTEN" ||
		!operation.CleanupFailed || strings.Contains(output.String(), "unrelated rejection") ||
		strings.Contains(output.String(), "private") {
		t.Fatal("cleanup obscured the safe setup diagnostic", output.String(), err)
	}
}
