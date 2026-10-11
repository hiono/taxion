package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLedgerRoundTrip(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	want := core.Ledger{
		Tasks:       []core.Task{{ID: "t1", Title: "first", Repo: "hiono/taxion", CreatedAt: now}},
		Dispatches:  []core.Dispatch{{ID: "d1", TaskID: "t1", Repo: "hiono/taxion", Active: true, CreatedAt: now}},
		Evidence:    []core.Evidence{{SHA: "abc123", TaskID: "t1", CreatedAt: now}},
		Checkpoints: []core.Checkpoint{{Name: "plan", Done: true}, {Name: "review", Done: false}},
	}
	if err := s.SaveLedger(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.LoadLedger()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Repo != "hiono/taxion" {
		t.Fatalf("tasks mismatch: %+v", got.Tasks)
	}
	if len(got.Dispatches) != 1 || !got.Dispatches[0].Active {
		t.Fatalf("dispatches mismatch: %+v", got.Dispatches)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].SHA != "abc123" {
		t.Fatalf("evidence mismatch: %+v", got.Evidence)
	}
	if len(got.Checkpoints) != 2 || !got.Checkpoints[0].Done || got.Checkpoints[1].Done {
		t.Fatalf("checkpoints mismatch: %+v", got.Checkpoints)
	}
	if err := core.GuardActiveDispatch(got.Dispatches); err != core.ErrActiveDispatchExists {
		t.Fatalf("DSP-001 must survive sqlite round-trip, got %v", err)
	}
}

func TestSaveReplaces(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	if err := s.SaveLedger(core.Ledger{Tasks: []core.Task{{ID: "t1", CreatedAt: now}}}); err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := s.SaveLedger(core.Ledger{}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got, err := s.LoadLedger()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Tasks) != 0 {
		t.Fatalf("expected empty ledger after replace, got %+v", got.Tasks)
	}
}

func TestWorkspaceInitializeAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Workspace{Ledger: core.Ledger{Tasks: []core.Task{{ID: "t1", Title: "task", Repo: "owner/repo"}}}}
	inserted, err := s.InitializeWorkspace(want)
	if err != nil || !inserted {
		t.Fatalf("initialize: inserted=%v err=%v", inserted, err)
	}
	inserted, err = s.InitializeWorkspace(Workspace{})
	if err != nil || inserted {
		t.Fatalf("initialize twice: inserted=%v err=%v", inserted, err)
	}
	if err := s.UpdateWorkspace(func(state *Workspace) error {
		state.Ledger.Tasks[0].Title = "uncommitted"
		return fmt.Errorf("reject update")
	}); err == nil {
		t.Fatal("expected callback failure")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ledger.Tasks) != 1 || got.Ledger.Tasks[0].Title != "task" {
		t.Fatalf("rollback/reopen lost state: %+v", got)
	}
}

func TestRuntimeSessionBindingPersistsInWorkspaceSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	now := time.Now().UTC()
	task := core.GraphTask{
		ID: "t1", Title: "task", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #50",
		Owner: "CODER", Status: core.Draft, CreatedFrom: "request:1", Objective: "implement",
		Scope: []string{"src"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"report"},
		ValidationCommands: []string{"go test ./..."},
		Worktree:           "/work/t1", Branch: "work/t1", BaselineSHA: strings.Repeat("a", 40),
		ExitCondition: "report", AuthorizationID: "decision:1",
		Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "approved"}},
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "register"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition("t1", core.Ready, "human", "approve", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := core.NewGraphDispatch(graph, "d1", "t1", "owner/repo", core.RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state := Workspace{
		Ledger: core.Ledger{
			Tasks: []core.Task{{ID: "t1", Title: "task", Repo: "owner/repo", CreatedAt: now}},
			Dispatches: []core.Dispatch{{
				ID: "d1", TaskID: "t1", Repo: "owner/repo", Active: true, CreatedAt: now,
			}},
		},
		Graph: graph, HasGraph: true,
	}
	if _, err := store.InitializeWorkspace(state); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.UpdateWorkspace(func(state *Workspace) error {
		return state.Graph.BindRuntimeSession("d1", "sdk-session-1", "role-config-hash")
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := got.Graph.GraphDispatch("d1")
	if !ok || bound.RuntimeSessionID != "sdk-session-1" || bound.RuntimeConfigHash != "role-config-hash" {
		t.Fatalf("runtime binding did not survive SQLite reopen: %+v", bound)
	}
}

func TestOutbox(t *testing.T) {
	s := openTest(t)
	id1, err := s.Enqueue("dispatch.created", `{"id":"d1"}`)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := s.Enqueue("evidence.bound", `{"sha":"abc"}`); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := s.Claim(10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 || claimed[0].ID != id1 || claimed[0].Type != "dispatch.created" {
		t.Fatalf("claim mismatch: %+v", claimed)
	}
	if err := s.MarkSent(id1); err != nil {
		t.Fatalf("mark: %v", err)
	}
	claimed, err = s.Claim(10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Type != "evidence.bound" {
		t.Fatalf("expected one remaining entry, got %+v", claimed)
	}
}
