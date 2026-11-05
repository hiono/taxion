package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestFailedReviewHelpSeparatesNegativeRecordFromQualityRecovery(t *testing.T) {
	var output bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"record-failed-review", "--help"}, &output); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for _, text := range []string{"REQUEST_CHANGES", "expectedHash", "FAILED", "remain unchanged", "No model send", "RUNNING recovery", "CANCELLED"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("help omitted %q", text)
		}
	}
	if err := workflowCommand(t.Context(), []string{"record-failed-review"}, &output); err == nil {
		t.Fatal("missing explicit request accepted")
	}
}
