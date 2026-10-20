package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"

	_ "modernc.org/sqlite"
)

func TestTaskValidateIsSaveFreeAndMatchesTrack(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	worktree, baseline := createGitWorktree(t, "work/contract", "https://github.com/owner/repo.git")
	valid := core.GraphTask{
		ID: "contract", Title: "contract validation", Repo: "owner/repo", Type: "IMPLEMENTATION", Status: core.Draft,
		Issue: "Issue #108", CreatedFrom: "human:1", Owner: "CODER", Objective: "validate contracts",
		Scope: []string{"internal/core"}, Acceptance: []string{"report"}, RequiredEvidence: []string{"report"},
		ExitCondition: "report", NextAction: "implement", ValidationCommands: []string{"go test ./..."},
		Worktree: worktree, Branch: "work/contract", BaselineSHA: baseline,
	}
	file := filepath.Join(t.TempDir(), "manifest.json")
	writeManifest := func(task core.GraphTask) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"task": task, "actor": "human", "reason": "draft"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
		t.Fatalf("workspace store unexpectedly exists: %v", err)
	}
	writeManifest(valid)
	if err := taskCmd([]string{"validate", "--manifest", file}); err == nil {
		t.Fatal("validate accepted a contract with no ledger task")
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
		t.Fatal("task validate created the workspace store")
	}
	assertNoSQLiteSidecars(t)
	if err := (core.Ledger{Tasks: []core.Task{{ID: "contract", Title: valid.Title, Repo: valid.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	if err := taskCmd([]string{"validate", "--manifest", file}); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
		t.Fatal("task validate created the workspace store")
	}
	if err := taskCmd([]string{"track", "--file", file}); err != nil {
		t.Fatalf("track rejected the manifest that validate accepted: %v", err)
	}
	before, err := os.ReadFile(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := taskCmd([]string{"validate", "--file", file}); err != nil {
		t.Fatalf("validate rejected an already stored contract: %v", err)
	}
	after, err := os.ReadFile(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	assertNoSQLiteSidecars(t)
	if !bytes.Equal(before, after) {
		t.Fatal("task validate changed the workspace store")
	}
	broken := valid
	broken.Status = core.Ready
	broken.Type = "not-a-type"
	writeManifest(broken)
	if err := taskCmd([]string{"validate", "--manifest", file}); err == nil {
		t.Fatal("invalid manifest was accepted by validate")
	}
	if err := taskCmd([]string{"track", "--file", file}); err == nil {
		t.Fatal("invalid manifest was accepted by track")
	}
	if err := taskCmd([]string{"validate"}); err == nil {
		t.Fatal("validate ran without a manifest")
	}
	if err := taskCmd([]string{"validate", "--manifest", file, "--file", file}); err == nil {
		t.Fatal("validate accepted both manifest flags")
	}
}

func assertNoSQLiteSidecars(t *testing.T) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(workspaceDBPath() + suffix); !os.IsNotExist(err) {
			t.Fatalf("task validate created %s", suffix)
		}
	}
}

func TestOpenWorkspaceSnapshotKeepsWalFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.db")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE t (x INTEGER)", "INSERT INTO t VALUES (1)"} {
		if _, err := writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotFiles(t, dir)
	db, done, err := openWorkspaceSnapshot(path)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	if _, err := db.WorkspaceInitialized(); err == nil {
		t.Log("snapshot schema unexpectedly present; readability is not required")
	}
	done()
	after := snapshotFiles(t, dir)
	if len(before) != len(after) {
		t.Fatalf("snapshot changed the workspace file set: %v -> %v", before, after)
	}
	for name, data := range before {
		if !bytes.Equal(data, after[name]) {
			t.Fatalf("snapshot modified %s", name)
		}
	}
}

func snapshotFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = data
	}
	return files
}

func contractDraftTask(t *testing.T) core.GraphTask {
	t.Helper()
	worktree, baseline := createGitWorktree(t, "work/contract", "https://github.com/owner/repo.git")
	return core.GraphTask{
		ID: "contract", Title: "contract validation", Repo: "owner/repo", Type: "IMPLEMENTATION", Status: core.Draft,
		Issue: "Issue #108", CreatedFrom: "human:1", Owner: "CODER", Objective: "validate contracts",
		Scope: []string{"internal/core"}, Acceptance: []string{"report"}, RequiredEvidence: []string{"report"},
		ExitCondition: "report", NextAction: "implement", ValidationCommands: []string{"go test ./..."},
		Worktree: worktree, Branch: "work/contract", BaselineSHA: baseline,
	}
}

func writeValidateManifest(t *testing.T, request map[string]any) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "manifest.json")
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestTaskValidateRejectsLegacyLedgerMismatch(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	task := contractDraftTask(t)
	if err := (core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: "new", Repo: task.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	stale := task
	stale.Title = "old"
	data, err := json.Marshal(core.TaskGraph{Version: 1, Tasks: []core.GraphTask{stale}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(core.GraphPath(storePath), data, 0o600); err != nil {
		t.Fatal(err)
	}
	file := writeValidateManifest(t, map[string]any{"task": task, "actor": "human", "reason": "draft"})
	if err := taskCmd([]string{"validate", "--manifest", file}); err == nil {
		t.Fatal("validate accepted a legacy workspace whose graph and ledger disagree")
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
		t.Fatal("task validate created the workspace store")
	}
}

func TestTaskValidateRedactsDecoderErrors(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	token := "ghp_" + strings.Repeat("q", 36)
	file := filepath.Join(t.TempDir(), "manifest.json")
	body := `{"task":{"id":"contract"},"actor":"human","reason":"draft","` + token + `":1}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := taskCmd([]string{"validate", "--manifest", file})
	if err == nil {
		t.Fatal("manifest with an unknown field was accepted")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("decoder error leaked a credential-like value: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("decoder error was not redacted: %v", err)
	}
}

func TestTaskValidateFallsBackFromSchemaLessDatabase(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	task := contractDraftTask(t)
	if err := (core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(workspaceDBPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceDBPath(), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	file := writeValidateManifest(t, map[string]any{"task": task, "actor": "human", "reason": "draft"})
	if err := taskCmd([]string{"validate", "--manifest", file}); err != nil {
		t.Fatalf("schema-less workspace was not read through the legacy fallback: %v", err)
	}
	if data, err := os.ReadFile(workspaceDBPath()); err != nil || len(data) != 0 {
		t.Fatalf("schema-less workspace file was modified: %d bytes, %v", len(data), err)
	}
}
