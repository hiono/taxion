package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestLegacyWorkspaceMigratesOnce(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), ".taxion", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := core.Ledger{Tasks: []core.Task{{ID: "t1", Title: "legacy", Repo: "owner/repo", CreatedAt: time.Now().UTC()}}}
	if err := legacy.Save(storePath); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Ledger.Tasks) != 1 || state.Ledger.Tasks[0].Title != "legacy" {
		t.Fatalf("legacy state not imported: %+v", state.Ledger)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.Ledger.Tasks[0].Title = "sqlite"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	db, state, err = loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if state.Ledger.Tasks[0].Title != "sqlite" {
		t.Fatalf("legacy snapshot overwrote SQLite state: %+v", state.Ledger.Tasks[0])
	}
	got, err := core.LoadLedger(storePath)
	if err != nil || got.Tasks[0].Title != "legacy" {
		t.Fatalf("migration modified its input: %+v %v", got, err)
	}
}
