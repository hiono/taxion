package copilot

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestSavedTurnNativeObservation(t *testing.T) {
	path := os.Getenv("TAXION_TEST_SAVED_REVIEW_GRAPH")
	if path == "" {
		t.Skip("set TAXION_TEST_SAVED_REVIEW_GRAPH to a preserved single-review Task Graph for model-free native observation")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var graph core.TaskGraph
	if err := json.Unmarshal(body, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Dispatches) != 1 || graph.Dispatches[0].Recipient != "REVIEWER" {
		t.Fatal("native observation requires exactly the identified saved Reviewer")
	}
	dispatch := graph.Dispatches[0]
	// This local copy permits observation of an already-ended session. Neither
	// its store nor its result is changed or accepted by this test.
	dispatch.Status = core.DispatchRunning
	adapter, err := NewForStop(dispatch, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	turn, err := adapter.ReadSavedTurn(t.Context(), dispatch.RuntimeSessionID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session=%s history=%s events=%d submitted=%s finished=%s",
		turn.SessionID, turn.HistoryHash, len(turn.Events), turn.SubmittedAt, turn.FinishedAt)
	for _, usage := range turn.UsageTotals {
		body, _ := json.Marshal(usage)
		t.Logf("native shutdown totals: %s", body)
	}
}
