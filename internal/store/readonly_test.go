package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyWorkspaceDoesNotCreateOrMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if db, err := OpenReadOnly(path); err == nil {
		db.Close()
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read-only open created database", err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.LoadWorkspace(); err != nil {
		t.Fatal(err)
	}
	if err := reader.UpdateWorkspace(func(state *Workspace) error { return nil }); err == nil {
		t.Fatal("diagnostic connection can write state")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("read-only observation changed database", err)
	}
}
