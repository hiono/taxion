package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type fakeRuntime struct {
	role      string
	hash      string
	worktree  string
	starts    int
	resumes   int
	sends     []string
	stops     int
	sessionID string
	sendErr   error
}

func (f *fakeRuntime) Role() string       { return f.role }
func (f *fakeRuntime) ConfigHash() string { return f.hash }
func (f *fakeRuntime) Worktree() string   { return f.worktree }
func (f *fakeRuntime) Start(_ context.Context, _ string) (string, error) {
	f.starts++
	if f.sessionID == "" {
		f.sessionID = "sdk-session"
	}
	return f.sessionID, nil
}
func (f *fakeRuntime) Send(_ context.Context, _, prompt string) error {
	f.sends = append(f.sends, prompt)
	return f.sendErr
}
func (f *fakeRuntime) Resume(context.Context, string) error {
	f.resumes++
	return nil
}
func (f *fakeRuntime) Stop(context.Context, string) error {
	f.stops++
	return nil
}
func (f *fakeRuntime) Events(context.Context, string) (<-chan runtime.Event, error) {
	return make(chan runtime.Event), nil
}

func TestControllerBindsAndReusesDurableSession(t *testing.T) {
	db, path, dispatch := dispatchStore(t)
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "config-v1", worktree: "/tmp/task"}
	controller, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := controller.Start(context.Background(), dispatch)
	if err != nil || sessionID != "sdk-session" {
		t.Fatalf("Start() = %q, %v", sessionID, err)
	}
	if err := controller.Send(context.Background(), dispatch, "continue"); err != nil {
		t.Fatal(err)
	}
	if err := controller.Resume(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}
	input, output, cacheRead, cacheWrite := int64(120), int64(30), int64(100), int64(20)
	auto := true
	if err := controller.RecordUsage(dispatch, core.DispatchUsage{
		Model: "gpt-6-luna", IsAuto: &auto, InputTokens: &input, OutputTokens: &output,
		CacheReadTokens: &cacheRead, CacheWriteTokens: &cacheWrite,
	}); err != nil {
		t.Fatal(err)
	}
	events, err := controller.Events(context.Background(), dispatch)
	if err != nil || events == nil {
		t.Fatalf("Events() = %v, %v", events, err)
	}
	if worker.starts != 1 || worker.resumes != 2 || len(worker.sends) != 1 {
		t.Fatalf("runtime calls = starts:%d resumes:%d sends:%v", worker.starts, worker.resumes, worker.sends)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := state.Graph.GraphDispatch(dispatch)
	if !ok || len(bound.RuntimeUsage) != 1 || bound.RuntimeUsage[0].Model != "gpt-6-luna" {
		t.Fatalf("runtime usage was not persisted: %+v", bound.RuntimeUsage)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restartedController, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	resumedID, err := restartedController.Start(context.Background(), dispatch)
	if err != nil || resumedID != sessionID {
		t.Fatalf("Start() after database reopen = %q, %v", resumedID, err)
	}
	if worker.starts != 1 || worker.resumes != 3 {
		t.Fatalf("restart did not reuse binding: starts:%d resumes:%d", worker.starts, worker.resumes)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	bound, _ = state.Graph.GraphDispatch(dispatch)
	if len(bound.RuntimeUsage) != 1 || *bound.RuntimeUsage[0].CacheReadTokens != 100 {
		t.Fatalf("usage did not survive database restart: %+v", bound.RuntimeUsage)
	}
}

func TestControllerRejectsChangedRoleAndConfiguration(t *testing.T) {
	db, _, dispatch := dispatchStore(t)
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "config-v1", worktree: "/tmp/task"}
	controller, _ := New(db, worker)
	if _, err := controller.Start(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}
	changedConfig, _ := New(db, &fakeRuntime{role: "CODER", hash: "config-v2", worktree: "/tmp/task"})
	if err := changedConfig.Resume(context.Background(), dispatch); err == nil {
		t.Fatal("changed configuration resumed a bound session")
	}
	reviewer, _ := New(db, &fakeRuntime{role: "REVIEWER", hash: "config-v1", worktree: "/tmp/task"})
	if err := reviewer.Resume(context.Background(), dispatch); err == nil {
		t.Fatal("different role resumed a bound session")
	}
	wrongWorktree, _ := New(db, &fakeRuntime{role: "CODER", hash: "config-v1", worktree: "/tmp/other"})
	if err := wrongWorktree.Resume(context.Background(), dispatch); err == nil {
		t.Fatal("different worktree resumed a bound session")
	}
	if worker.resumes != 0 {
		t.Fatalf("rejected resume reached runtime: %d calls", worker.resumes)
	}
}

func dispatchStore(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	nowTask := core.Task{ID: "task-1", Title: "test", Repo: "owner/repo"}
	graphTask := core.GraphTask{
		ID: "task-1", Title: "test", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #50",
		Owner: "CODER", Reviewer: "reviewer", Status: core.Draft, Objective: "test controller",
		Scope: []string{"internal"}, Acceptance: []string{"tests pass"},
		RequiredEvidence: []string{"test-report"}, ExitCondition: "tests pass",
		Branch: "work/task", Worktree: "/tmp/task",
		BaselineSHA: strings.Repeat("a", 40), CreatedFrom: "request:1", AuthorizationID: "decision:1",
		Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "authorize test"}},
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(graphTask, "human", "register"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(graphTask.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	gd, err := core.NewGraphDispatch(graph, "dispatch-1", graphTask.ID, nowTask.Repo, core.RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.IssueDispatch(gd, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	ledger := core.Ledger{
		Tasks:      []core.Task{nowTask},
		Dispatches: []core.Dispatch{{ID: gd.ID, TaskID: gd.TaskID, Repo: gd.Repository, Active: true, CreatedAt: gd.CreatedAt}},
		Evidence:   []core.Evidence{}, Checkpoints: []core.Checkpoint{},
	}
	if _, err := db.InitializeWorkspace(store.Workspace{Ledger: ledger, Graph: graph, HasGraph: true}); err != nil {
		t.Fatal(err)
	}
	return db, path, gd.ID
}

var _ runtime.Identity = (*fakeRuntime)(nil)

func TestControllerRejectsInactiveOrUnknownDispatch(t *testing.T) {
	db, _, dispatch := dispatchStore(t)
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "config-v1", worktree: "/tmp/task"}
	controller, _ := New(db, worker)
	if _, err := controller.Start(context.Background(), "missing"); err == nil {
		t.Fatal("unknown dispatch accepted")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		result := &core.DispatchResult{
			DispatchID: dispatch, TaskID: "task-1", Recipient: "CODER", Repository: "owner/repo",
			Status: core.DispatchCancelled, StartedSHA: strings.Repeat("a", 40), Evidence: []string{"cancelled"},
			NextTransition: core.Blocked,
		}
		if err := state.Graph.TransitionDispatch(dispatch, core.DispatchCancelled, "controller", "cancel", false, false, result); err != nil {
			return fmt.Errorf("end Dispatch: %w", err)
		}
		state.Ledger.Dispatches[0].Active = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), dispatch); err == nil {
		t.Fatal("inactive dispatch accepted")
	}
	if worker.starts != 0 {
		t.Fatalf("inactive dispatch started runtime: %d", worker.starts)
	}
}

func TestControllerRecordsRuntimeFailureAndCancellation(t *testing.T) {
	db, _, dispatch := dispatchStore(t)
	failedWorker := &fakeRuntime{
		role: "CODER", hash: "config-v1", worktree: "/tmp/task",
		sendErr: errors.New("send failed"),
	}
	failed, _ := New(db, failedWorker)
	if _, err := failed.Start(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}
	if err := failed.Send(context.Background(), dispatch, "execute"); err == nil {
		t.Fatal("runtime send failure was hidden")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	ended, _ := state.Graph.GraphDispatch(dispatch)
	if ended.Status != core.DispatchFailed || ended.Result == nil || len(ended.Result.Blockers) != 1 ||
		state.Ledger.Dispatches[0].Active {
		t.Fatalf("failed execution was not recorded as failed: %+v", ended)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, _, dispatch = dispatchStore(t)
	defer db.Close()
	cancelWorker := &fakeRuntime{role: "CODER", hash: "config-v1", worktree: "/tmp/task"}
	cancelled, _ := New(db, cancelWorker)
	if _, err := cancelled.Start(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}
	if err := cancelled.Send(context.Background(), dispatch, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := cancelled.Stop(context.Background(), dispatch); err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	ended, _ = state.Graph.GraphDispatch(dispatch)
	if ended.Status != core.DispatchCancelled || ended.Result == nil || ended.Result.Status != core.DispatchCancelled ||
		state.Ledger.Dispatches[0].Active || cancelWorker.stops != 1 {
		t.Fatalf("cancellation was not recorded: %+v", ended)
	}
	if err := cancelled.Send(context.Background(), dispatch, "must reject"); err == nil {
		t.Fatal("cancelled dispatch accepted another instruction")
	}
}
