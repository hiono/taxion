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

func TestGateCommandValidatesInputs(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: nil, want: "usage:"},
		{args: []string{"unknown"}, want: "unknown gate"},
		{args: []string{"contract"}, want: "--task"},
		{args: []string{"resume"}, want: "--flow"},
		{args: []string{"resume", "--task", "t"}, want: "--flow"},
		{args: []string{"execution"}, want: "--flow"},
		{args: []string{"publication"}, want: "--flow"},
		{args: []string{"contract", "--task", "t", "extra"}, want: "positional"},
	} {
		var output bytes.Buffer
		err := gateCommand(test.args, &output)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("gate %v returned %v, want error containing %q", test.args, err, test.want)
		}
	}
}

func TestGateCommandDoesNotCreateWorkspace(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "absent", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	var output bytes.Buffer
	if err := gateCommand([]string{"contract", "--task", "missing"}, &output); err == nil {
		t.Fatal("gate report accepted an absent workspace")
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
		t.Fatal("gate report created state", err)
	}
}

func TestGateCommandReportsBlockedExitCode(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = previous })
	draft := core.GraphTask{ID: "draft-task", Title: "Draft", Repo: "owner/repo", Type: "INVESTIGATION",
		Status: core.Draft, CreatedFrom: "human:1", Issue: "Issue #1"}
	if err := (core.Ledger{Tasks: []core.Task{{ID: draft.ID, Title: draft.Title, Repo: draft.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(draft, "human", "record draft"); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph,
		Ledger: core.Ledger{Tasks: []core.Task{{ID: draft.ID, Title: draft.Title, Repo: draft.Repo}}},
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = gateCommand([]string{"contract", "--task", draft.ID}, &output)
	exit, ok := err.(interface{ ExitCode() int })
	if !ok || exit.ExitCode() != 2 {
		t.Fatalf("blocked Task returned %v, want exit code 2", err)
	}
	if !strings.Contains(output.String(), "blocking: task-not-ready") ||
		!strings.Contains(output.String(), "state changed: false") {
		t.Fatalf("blocked report is incomplete: %s", output.String())
	}
	output.Reset()
	err = gateCommand([]string{"contract", "--task", draft.ID, "--json"}, &output)
	if exit, ok := err.(interface{ ExitCode() int }); !ok || exit.ExitCode() != 2 {
		t.Fatalf("blocked JSON report returned %v, want exit code 2", err)
	}
	if !strings.Contains(output.String(), `"version": 1`) || !strings.Contains(output.String(), `"stateChanged": false`) {
		t.Fatalf("JSON report is incomplete: %s", output.String())
	}
}
