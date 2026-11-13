package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestPlanObservationIdentifiesReturnedRecordsWithoutChangingWorkspace(t *testing.T) {
	db, path, dispatchID := dispatchStore(t)
	ledger := filepath.Join(filepath.Dir(path), "ledger.json")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	plan, err := Plan(db, ledger, "owner/repo")
	end := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	observation := plan.Observation
	if observation == nil || observation.StartedAt.Before(start) ||
		observation.CompletedAt.After(end) || observation.CompletedAt.Before(observation.StartedAt) ||
		observation.StartedAt.Location() != time.UTC || observation.CompletedAt.Location() != time.UTC {
		t.Fatalf("invalid observation interval: %+v", observation)
	}
	plan.Observation = nil
	content, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if expected := fmt.Sprintf("sha256:%x", sha256.Sum256(content)); observation.ContentHash != expected {
		t.Fatalf("hash does not identify the returned content: got %s, want %s", observation.ContentHash, expected)
	}
	repeated, err := Plan(db, ledger, "owner/repo")
	if err != nil || repeated.Observation.ContentHash != observation.ContentHash {
		t.Fatalf("unchanged content received a different hash: %+v %v", repeated.Observation, err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("observation changed workspace authority: %v", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, _ := state.Graph.GraphDispatch(dispatchID)
		return state.Graph.AddConcern(core.Concern{
			ID: "missing-investigation", TaskID: dispatch.TaskID,
			Source: "human:side-question", Text: "Compatibility has not been investigated.",
		})
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := Plan(db, ledger, "owner/repo")
	if err != nil || changed.Observation.ContentHash == observation.ContentHash {
		t.Fatalf("new concern was not reflected in observation identity: %+v %v", changed.Observation, err)
	}
	if plan.Tasks[0].Concerns == nil || len(plan.Tasks[0].Concerns) != 0 ||
		len(changed.Tasks[0].Concerns) != 1 || changed.Tasks[0].Concerns[0].Source != "human:side-question" {
		t.Fatal("observation replaced the previously observed target or lost provenance")
	}
}
