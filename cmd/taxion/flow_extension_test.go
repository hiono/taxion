package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestWorkflowExtendParsesFileAndPreviewsWithoutWorkspaceChanges(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = previous })
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.HasGraph, state.Graph = true, core.TaskGraph{Version: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "extension.json")
	for _, test := range []struct {
		body    string
		preview bool
		want    string
	}{
		{`{"flowId":"missing","unknown":true}`, true, "unknown field"},
		{`{"flowId":"missing","previousDeadline":"2026-01-01T00:00:00Z","deadline":"2026-01-02T00:00:00Z","previousMaxNanoAiu":0,"maxNanoAiu":0,"actor":"human","source":"human:approval","reason":"finish review"}`, true, "same RUNNING Flow"},
		{`{"flowId":"missing"}`, false, "exact preview expectedHash"},
	} {
		if err := os.WriteFile(file, []byte(test.body), 0600); err != nil {
			t.Fatal(err)
		}
		args := []string{"extend", "--file", file}
		if test.preview {
			args = append(args, "--preview")
		}
		var output bytes.Buffer
		if err := workflowCommand(t.Context(), args, &output); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("extension command did not enforce %q: %v", test.want, err)
		}
		after, err := os.ReadFile(workspaceDBPath())
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("extension refusal changed workspace", err)
		}
	}
}
