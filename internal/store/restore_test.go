package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func TestRestoredPlacementCannotBeMutatedOrExecuteOldTask(t *testing.T) {
	db := openTest(t)
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	record := &RestoreRecord{SnapshotHash: strings.Repeat("a", 64), Actor: "human", Source: "human:move",
		At: time.Now().UTC(), TaskIDs: []string{"old"},
		Paths: []RestoredPath{{Original: filepath.Join(t.TempDir(), "old"), Local: filepath.Join(t.TempDir(), "new")}}}
	if err := db.InstallRestoreRecord(record); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Records.Restore.Paths[0].Local = filepath.Join(t.TempDir(), "changed")
		return nil
	}); err == nil {
		t.Fatal("placement changed through a shared pointer")
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Graph.Dispatches = append(state.Graph.Dispatches, core.GraphDispatch{ID: "new", TaskID: "old"})
		return nil
	}); err == nil || !strings.Contains(err.Error(), "newly approved successor") {
		t.Fatal("old Task allowed a fresh execution", err)
	}
	if err := db.InstallRestoreRecord(record); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Records.Restores[0].TaskIDs[0] = "changed"
		return nil
	}); err == nil {
		t.Fatal("historical placement changed through a shared slice")
	}
	if err := db.UpdateWorkspace(func(state *Workspace) error {
		state.Records.Restores = nil
		return nil
	}); err == nil {
		t.Fatal("historical placement was removed")
	}
}
