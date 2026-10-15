package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

func TestDefaultWorkspaceUsesWorktreeRoot(t *testing.T) {
	root := t.TempDir()
	runGit := func(directory string, args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	runGit(root, "init", "-q")
	runGit(root, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-qm", "initial")
	child := filepath.Join(root, "nested", "directory")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	expected, err := defaultWorkspaceStorePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	actual, err := defaultWorkspaceStorePath()
	if err != nil || actual != expected || filepath.Dir(filepath.Dir(actual)) != root {
		t.Fatal("subdirectory selected a different workspace", actual, expected, err)
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	runGit(root, "worktree", "add", "-q", "--detach", worktree)
	t.Chdir(worktree)
	actual, err = defaultWorkspaceStorePath()
	if err != nil || actual != filepath.Join(worktree, ".taxion", "ledger.json") || actual == expected {
		t.Fatal("linked worktree shared the parent workspace", actual, err)
	}
	t.Setenv("PATH", "")
	if _, err := defaultWorkspaceStorePath(); err == nil {
		t.Fatal("failed Git lookup silently selected another workspace")
	}
	if _, err := os.Stat(filepath.Join(worktree, ".taxion")); !os.IsNotExist(err) {
		t.Fatal("workspace resolution wrote state", err)
	}
}

func TestDefaultWorkspacePreservesStandaloneDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PATH", "")
	actual, err := defaultWorkspaceStorePath()
	if err != nil || actual != filepath.Join(root, ".taxion", "ledger.json") {
		t.Fatal("standalone workspace default changed", actual, err)
	}
}

func TestWorkspaceRejectsDatabaseAsLedgerBeforeCreatingFiles(t *testing.T) {
	previous := storePath
	t.Cleanup(func() { storePath = previous })
	for _, action := range []string{"init", "open", "serve", "memory"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			storePath = filepath.Join(root, "not-created", "workspace.db")
			var err error
			switch action {
			case "init":
				err = initCmd()
			case "open":
				var db *store.Store
				db, err = openWorkspace()
				if db != nil {
					db.Close()
				}
			case "serve":
				err = serveCmd([]string{"--audit", filepath.Join(root, "audit", "events.jsonl")})
			case "memory":
				var db *memory.Store
				db, err = openMemory()
				if db != nil {
					db.Close()
				}
			}
			if err == nil || !strings.Contains(err.Error(), "--store") || !strings.Contains(err.Error(), "ledger.json") {
				t.Fatal("missing actionable store rejection", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid store created workspace or audit files", entries, err)
			}
		})
	}
}

func TestWorkspaceRejectsLedgerAliasesWithoutChangingDatabase(t *testing.T) {
	previous := storePath
	t.Cleanup(func() { storePath = previous })
	for _, alias := range []string{"same-path", "hard-link", "symlink", "dangling-symlink"} {
		t.Run(alias, func(t *testing.T) {
			root := t.TempDir()
			storePath = filepath.Join(root, "ledger.json")
			dbPath := workspaceDBPath()
			if alias != "dangling-symlink" {
				if err := initCmd(); err != nil {
					t.Fatal(err)
				}
			}
			switch alias {
			case "same-path":
				storePath = dbPath
			case "hard-link":
				if err := os.Link(dbPath, storePath); err != nil {
					t.Fatal(err)
				}
			case "symlink", "dangling-symlink":
				if err := os.Symlink(dbPath, storePath); err != nil {
					if runtime.GOOS == "windows" {
						t.Skip("symlink creation is unavailable")
					}
					t.Fatal(err)
				}
			}
			before, readErr := os.ReadFile(dbPath)
			for _, open := range []bool{false, true} {
				var err error
				if open {
					var db *store.Store
					db, err = openWorkspace()
					if db != nil {
						db.Close()
					}
				} else {
					err = initCmd()
				}
				if err == nil || !strings.Contains(err.Error(), "ledger.json") {
					t.Fatal("accepted database alias", err)
				}
				after, err := os.ReadFile(dbPath)
				if alias == "dangling-symlink" {
					if !os.IsNotExist(readErr) || !os.IsNotExist(err) {
						t.Fatal("dangling ledger link created database", err)
					}
				} else if err != nil || !bytes.Equal(before, after) {
					t.Fatal("rejected alias changed database", err)
				}
			}
		})
	}
}

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
