package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestTaskArchiveCLIReviewHashAndVisibility(t *testing.T) {
	old := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	t.Cleanup(func() { storePath = old })
	task := core.GraphTask{ID: "trial", Title: "Obsolete attempt", Repo: "group/subgroup/project",
		Type: "INVESTIGATION", Status: core.Draft, CreatedFrom: "human:trial", Issue: "Issue #1"}
	if err := (core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.HasGraph = true
		state.Graph = core.TaskGraph{Version: 1}
		state.Ledger.Tasks = []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}
		state.Ledger.Dispatches = []core.Dispatch{{ID: "legacy-ended", TaskID: task.ID, Repo: task.Repo}}
		return state.Graph.Track(task, "human", "record trial")
	}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		file, err := os.CreateTemp(t.TempDir(), "output")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		before := os.Stdout
		os.Stdout = file
		if args[0] == "dispatch" {
			err = dispatchCmd(args[1:])
		} else {
			err = taskCmd(args)
		}
		os.Stdout = before
		content, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(content), err
	}
	args := []string{"archive", "--tasks", task.ID, "--actor", "human", "--source", "human:discard", "--reason", "obsolete"}
	output, err := run(append(args, "--dry-run")...)
	if err != nil {
		t.Fatal(err)
	}
	var preview controller.ArchivePreview
	if err := json.Unmarshal([]byte(output), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.DispatchIDs) != 1 || preview.DispatchIDs[0] != "legacy-ended" {
		t.Fatal("preview omitted related legacy Dispatch", preview)
	}
	if _, err := run(args...); err == nil {
		t.Fatal("archive without reviewed hash accepted")
	}
	if _, err := run(append(args, "--expected-hash", preview.Hash)...); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"list", "--json"}, {"plan", "--repo", task.Repo}} {
		output, err := run(command...)
		if err != nil || strings.Contains(output, task.Title) {
			t.Fatal("archived trial visible by default", output, err)
		}
		output, err = run(append(command, "--include-archived")...)
		if err != nil || !strings.Contains(output, task.Title) {
			t.Fatal("explicit archived lookup failed", output, err)
		}
	}
	output, err = run("dispatch", "list", "--json")
	if err != nil || strings.Contains(output, "legacy-ended") {
		t.Fatal("archived Dispatch visible by default", output, err)
	}
	output, err = run("dispatch", "list", "--json", "--include-archived")
	if err != nil || !strings.Contains(output, "legacy-ended") {
		t.Fatal("explicit Dispatch history missing", output, err)
	}
	output, err = run("graph")
	if err != nil || !strings.Contains(output, `"archives"`) || !strings.Contains(output, `"human:discard"`) {
		t.Fatal("archive receipt missing from graph", output, err)
	}
}
