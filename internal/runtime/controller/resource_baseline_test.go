package controller

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// Optional read-only measurement against an operator's existing offline evidence.
func TestSavedWorkspaceResourceBaseline(t *testing.T) {
	path := os.Getenv("TAXION_RESOURCE_BASELINE_DB")
	if path == "" {
		t.Skip("set TAXION_RESOURCE_BASELINE_DB to measure saved offline evidence without model calls")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil || !state.HasGraph {
		t.Fatal("baseline requires an initialized Task Graph", err)
	}
	for _, dispatch := range state.Graph.Dispatches {
		prior := dispatch
		prior.Instruction.Decisions, err = compactResultDecisions(state.Graph, dispatch.Instruction.Decisions)
		if err != nil {
			t.Fatal(err)
		}
		old, err := core.RenderDispatchInstruction(prior)
		if err != nil {
			t.Fatal(err)
		}
		old = "CONTEXT PROJECTION: prior result decisions include exact results and source hashes; nested prior instructions remain in the saved Graph. This projection changes no authorization or verdict.\n" + old
		current, err := RenderDispatchContext(db, dispatch, true)
		if err != nil || len(current) >= len(old) {
			t.Fatalf("%s projection did not shrink versus old compact: %d -> %d, %v", dispatch.Recipient, len(old), len(current), err)
		}
		t.Logf("%s UTF-8 context bytes: old compact=%d, current=%d", dispatch.Recipient, len(old), len(current))
	}
	copyPath := filepath.Join(t.TempDir(), "workspace.db")
	copyDB, err := store.Open(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copyDB.InitializeWorkspace(state); err != nil {
		copyDB.Close()
		t.Fatal(err)
	}
	roundTrip, err := copyDB.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(roundTrip, state) {
		copyDB.Close()
		t.Fatal("normalized persistence changed saved evidence", err)
	}
	if err := copyDB.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SQLite physical bytes: original=%d, fresh normalized=%d (original freelist retained; not a token or cost measure)", len(before), info.Size())
	migratedPath := filepath.Join(t.TempDir(), "workspace.db")
	if err := os.WriteFile(migratedPath, before, 0600); err != nil {
		t.Fatal(err)
	}
	migratedDB, err := store.Open(migratedPath)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := migratedDB.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(migrated, state) {
		migratedDB.Close()
		t.Fatal("migration of saved evidence changed contracts or receipts", err)
	}
	if err := migratedDB.Close(); err != nil {
		t.Fatal(err)
	}
	migratedInfo, err := os.Stat(migratedPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Migrated physical bytes=%d; migration does not automatically reclaim free pages", migratedInfo.Size())
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("read-only baseline changed source evidence", err)
	}
}
