package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestInputObservationDoesNotCreateWorkspace(t *testing.T) {
	previous := storePath
	dir := t.TempDir()
	storePath = filepath.Join(dir, "absent", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	request := filepath.Join(dir, "input.json")
	if err := os.WriteFile(request, []byte(`{"taskId":"source"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"observe-input", "--help"}, {"observe-input", "--file", request, "--preview"},
		{"observe-input", "--file", request}, {"assess-input", "--file", request},
	} {
		var output bytes.Buffer
		if err := workflowCommand(t.Context(), args, &output); err == nil {
			t.Fatal("absent input workspace was accepted", args)
		}
		if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
			t.Fatal("observation created an unapproved workspace", args, err)
		}
	}
}

func TestInputObservationPreviewReturnsAllTypedBlockingConditions(t *testing.T) {
	previous := storePath
	dir := t.TempDir()
	storePath = filepath.Join(dir, "ledger.json")
	t.Cleanup(func() { storePath = previous })
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "observation.json")
	if err := os.WriteFile(file, []byte(`{"taskId":"missing"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"observe-input", "--file", file, "--preview"}, &output); err == nil {
		t.Fatal("missing source and authority were accepted")
	}
	var report controller.InputObservationReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal("blocked operation lost its machine-readable decision", output.String(), err)
	}
	if report.StateChanged || report.Decision.Verdict != "blocked" || len(report.Decision.Conditions) < 3 {
		t.Fatal("independent missing evidence was hidden", report)
	}
	after, err := os.ReadFile(workspaceDBPath())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked preview modified the database", err)
	}
}
