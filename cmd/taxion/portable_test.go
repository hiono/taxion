package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/portable"
	"github.com/hiono/taxion/internal/store"
)

func TestWorkspaceMigrationCLI(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = previous })
	if err := initCmd(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "portable.db")
	if err := workspaceMigrationCmd([]string{"snapshot", "--output", archive}); err != nil {
		t.Fatal(err)
	}
	if err := workspaceMigrationCmd([]string{"inspect-snapshot", "--file", archive}); err != nil {
		t.Fatal(err)
	}
	report, err := portable.Inspect(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	args := []string{"restore", "--file", archive, "--output", target,
		"--expected-hash", report.Hash, "--actor", "human", "--source", "human:move"}
	if err := workspaceMigrationCmd(args); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenReadOnly(filepath.Join(target, ".taxion", "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil || state.Records.Restore == nil || state.Records.Restore.SnapshotHash != report.Hash {
		t.Fatal("CLI did not persist restore approval", err)
	}
	if err := workspaceMigrationCmd(args); err == nil {
		t.Fatal("existing directory was replaced")
	}
	for _, invalid := range [][]string{
		nil, {"unknown"}, {"snapshot"}, {"snapshot", "--output", "relative"},
		{"inspect-snapshot", "--file", archive, "unexpected"},
		{"restore", "--file", archive, "--output", filepath.Join(t.TempDir(), "unapproved")},
	} {
		if err := workspaceMigrationCmd(invalid); err == nil {
			t.Fatal("invalid CLI accepted", invalid)
		}
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("restored directory is not private", err)
	}
}
