package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	closes    int
	closeErr  error
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
func (f *fakeRuntime) Close() error {
	f.closes++
	return f.closeErr
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

func dispatchStore(t *testing.T, worktree ...string) (*store.Store, string, string) {
	t.Helper()
	return dispatchStoreWithEvidence(t, "test-report", worktree...)
}

func dispatchStoreWithEvidence(t *testing.T, evidence string, worktree ...string) (*store.Store, string, string) {
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
		ValidationCommands: []string{"go test ./..."},
		RequiredEvidence:   []string{evidence}, ExitCondition: "tests pass",
		Branch: "work/task", Worktree: "/tmp/task",
		BaselineSHA: strings.Repeat("a", 40), CreatedFrom: "request:1", AuthorizationID: "decision:1",
		Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "authorize test"}},
	}
	if len(worktree) != 0 {
		graphTask.Worktree = worktree[0]
		graphTask.BaselineSHA = gitTest(t, worktree[0], "rev-parse", "HEAD")
		graphTask.ValidationCommands = []string{"printf 'controller validated'; test -s README.md"}
		if len(worktree) > 1 {
			graphTask.Owner = "REVIEWER"
			graphTask.Type = "REVIEW"
			graphTask.Worktree = worktree[1]
			graphTask.TargetSHA = graphTask.BaselineSHA
		}
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
	if len(worktree) == 0 {
		// M11 binding tests exercise persisted sessions without Git execution.
		graph.Dispatches[0].Instruction.Version = 3
		encoded, err := json.Marshal(graph.Dispatches[0].Instruction)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(encoded)
		graph.Dispatches[0].InstructionHash = hex.EncodeToString(hash[:])
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

func TestControllerFinishesAndRestoresVerifiedResult(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "work/task")
	gitTest(t, root, "config", "user.name", "Taxion Test")
	gitTest(t, root, "config", "user.email", "taxion-test@example.com")
	gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "README.md")
	gitTest(t, root, "commit", "-m", "baseline")
	baseline := gitTest(t, root, "rev-parse", "HEAD")
	db, path, id := dispatchStoreWithEvidence(t, "controller-validation", root)
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "config-v1", worktree: root}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := control.RecordUsage(id, core.DispatchUsage{Model: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "app.go"), []byte("package app"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "internal/app.go")
	gitTest(t, root, "commit", "-m", "feat(app): implement application", "-m", "intent(app): satisfy the saved implementation criterion")
	head := gitTest(t, root, "rev-parse", "HEAD")
	result := &core.DispatchResult{
		DispatchID: id, TaskID: "task-1", Recipient: "CODER", Repository: "owner/repo",
		Status: core.DispatchSucceeded, StartedSHA: baseline, OutputSHA: head,
		ChangedFiles: []string{"internal/app.go"}, Evidence: []string{"test-report"},
		Validations:    []core.DispatchValidation{{Command: "not executed", Worktree: root, SHA: head}},
		NextTransition: core.WaitingForEvidence,
	}
	payload := func(value any) string {
		t.Helper()
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	wrong := *result
	wrong.DispatchID = "other-dispatch"
	if _, err := control.Finish(context.Background(), id, payload(wrong)); err == nil {
		t.Fatal("accepted another Dispatch's response")
	}
	if _, err := control.Finish(context.Background(), id, payload(result)+" {}"); err == nil {
		t.Fatal("accepted multiple JSON responses")
	}
	submission := *result
	submission.Validations = []core.DispatchValidation{}
	submission.Evidence = []string{}
	accepted, err := control.Finish(context.Background(), id, payload(submission))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.GitEvidence == nil || accepted.GitEvidence.HeadSHA != head ||
		accepted.Validations[0].Output != "controller validated" ||
		accepted.Validations[0].StartedAt.IsZero() ||
		len(accepted.Evidence) != 1 || accepted.Evidence[0] != "controller-validation" {
		t.Fatalf("controller evidence missing: %+v", accepted)
	}
	if _, err := control.Finish(context.Background(), id, payload(result)); err == nil {
		t.Fatal("accepted a stale response after completion")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	state, err := reopened.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := state.Graph.GraphDispatch(id)
	if !ok || saved.Status != core.DispatchSucceeded || saved.Result.GitEvidence.HeadSHA != head ||
		saved.ExecutionStartSHA != baseline ||
		saved.Result.Validations[0].Output != "controller validated" ||
		len(saved.Result.Evidence) != 1 || saved.Result.Evidence[0] != "controller-validation" ||
		state.Ledger.Dispatches[0].Active || len(saved.RuntimeUsage) != 1 {
		t.Fatalf("verified completion lost after restart: %+v", state)
	}
	reviewPath := filepath.Join(t.TempDir(), "review")
	var reviewDispatch core.GraphDispatch
	if err := reopened.UpdateWorkspace(func(state *store.Workspace) error {
		task := core.GraphTask{
			ID: "review-task", Title: "Review coder output", Repo: saved.Repository,
			Type: "REVIEW", Owner: "REVIEWER", Status: core.Draft,
			Issue: "INHERITED: Issue #50", CreatedFrom: "dispatch:" + id,
			Objective: "Review the verified coder SHA", Scope: []string{"internal"},
			Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
			ValidationCommands: []string{"test -f internal/app.go"}, ExitCondition: "review verdict",
			Worktree: reviewPath, Branch: "review/task", BaselineSHA: head, TargetSHA: head,
			AuthorizationID: "review-authorization",
			Decisions:       []core.TaskDecision{{ID: "review-authorization", Source: "human:1", Text: "authorize independent review"}},
		}
		if err := state.Graph.Track(task, "controller", "review the verified coder output"); err != nil {
			return err
		}
		if err := state.Graph.Transition(task.ID, core.Ready, "controller", "review authorized", core.TransitionProof{}); err != nil {
			return err
		}
		var err error
		reviewDispatch, err = core.NewGraphDispatch(state.Graph, "review-dispatch", task.ID, task.Repo,
			core.RelationshipIndependent, id, "review the recorded coder result")
		if err != nil {
			return err
		}
		if err := state.Graph.IssueDispatch(reviewDispatch, "controller", "dispatch independent review"); err != nil {
			return err
		}
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
			ID: task.ID, Title: task.Title, Repo: task.Repo, CreatedAt: time.Now().UTC(),
		})
		state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
			ID: reviewDispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true,
			CreatedAt: time.Now().UTC(),
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareReview(reopened, reviewDispatch.ID, root); err != nil {
		t.Fatal(err)
	}
	reviewer, err := New(reopened, &fakeRuntime{
		role: "REVIEWER", hash: "independent-review", worktree: reviewPath, sessionID: "review-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.Start(context.Background(), reviewDispatch.ID); err != nil {
		t.Fatal(err)
	}
	if err := reviewer.Send(context.Background(), reviewDispatch.ID, "review recorded output"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.Finish(context.Background(), reviewDispatch.ID, payload(result)); err == nil {
		t.Fatal("Reviewer accepted a Coder result")
	}
	changed := false
	reviewResult := core.DispatchResult{
		DispatchID: reviewDispatch.ID, TaskID: reviewDispatch.TaskID, Recipient: "REVIEWER",
		Repository: saved.Repository, Status: core.DispatchSucceeded, StartedSHA: head, OutputSHA: head,
		ChangedFiles: []string{}, Evidence: []string{"test-report"}, NextTransition: core.WaitingForEvidence,
		ReviewVerdict: "APPROVE", WorktreeChanged: &changed, Findings: []core.DispatchFinding{},
		Acceptance: []core.DispatchAcceptance{{Criterion: "tests pass", Status: "PASS", Evidence: []string{"test-report"}}},
	}
	if _, err := reviewer.Finish(context.Background(), reviewDispatch.ID, payload(reviewResult)); err != nil {
		t.Fatal(err)
	}
	state, err = reopened.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	coderRecord, _ := state.Graph.GraphDispatch(id)
	reviewRecord, _ := state.Graph.GraphDispatch(reviewDispatch.ID)
	if coderRecord.Result.OutputSHA != reviewRecord.Result.OutputSHA ||
		reviewRecord.RelatedID != coderRecord.ID ||
		coderRecord.Result.GitEvidence.Worktree == reviewRecord.Result.GitEvidence.Worktree ||
		!reviewRecord.Result.GitEvidence.WorktreeReleased {
		t.Fatal("Coder and independent review facts were not preserved separately")
	}
	plan, err := Plan(reopened, filepath.Join(filepath.Dir(path), "ledger.json"), saved.Repository)
	if err != nil || len(plan.Tasks) != 2 || len(plan.Tasks[1].Dispatches) != 1 {
		t.Fatalf("review evidence missing from follow-up plan: %+v %v", plan, err)
	}
	reviewContext := plan.Tasks[1].Dispatches[0]
	if reviewContext.ID != reviewDispatch.ID || reviewContext.Result.OutputSHA != head ||
		reviewContext.Result.ReviewVerdict != "APPROVE" || reviewContext.Instruction.Acceptance[0] != "tests pass" ||
		reviewContext.RelatedID != id || reviewContext.InstructionHash != reviewRecord.InstructionHash {
		t.Fatalf("review plan lost source contract or SHA: %+v", reviewContext)
	}
}

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestReviewerResultRequiresRuntimeCloseAndWorktreeRelease(t *testing.T) {
	for _, failure := range []string{"", "dirty", "locked", "close"} {
		t.Run("failure="+failure, func(t *testing.T) {
			root := t.TempDir()
			gitTest(t, root, "init", "-b", "work/task")
			gitTest(t, root, "config", "user.name", "Taxion Test")
			gitTest(t, root, "config", "user.email", "taxion-test@example.com")
			gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("target"), 0600); err != nil {
				t.Fatal(err)
			}
			gitTest(t, root, "add", "README.md")
			gitTest(t, root, "commit", "-m", "target")
			head := gitTest(t, root, "rev-parse", "HEAD")
			reviewPath := filepath.Join(t.TempDir(), "review")
			db, path, id := dispatchStore(t, root, reviewPath)
			defer db.Close()
			prepared, err := PrepareReview(db, id, root)
			if err != nil || prepared.ReviewWorktreeSource != root {
				t.Fatalf("prepare Reviewer: %+v %v", prepared, err)
			}
			worker := &fakeRuntime{role: "REVIEWER", hash: "review-config", worktree: reviewPath}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			if err := control.Send(context.Background(), id, "review target"); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "dirty":
				if err := os.WriteFile(filepath.Join(reviewPath, "untracked.txt"), []byte("dirty"), 0600); err != nil {
					t.Fatal(err)
				}
			case "locked":
				gitTest(t, root, "worktree", "lock", reviewPath)
			case "close":
				worker.closeErr = errors.New("disconnect failed")
			}
			changed := false
			result := core.DispatchResult{
				DispatchID: id, TaskID: "task-1", Recipient: "REVIEWER", Repository: "owner/repo",
				Status: core.DispatchSucceeded, StartedSHA: head, OutputSHA: head,
				ChangedFiles: []string{}, Validations: []core.DispatchValidation{{
					Command: "reported validation", Worktree: reviewPath, SHA: head,
				}},
				Evidence: []string{"test-report"}, NextTransition: core.WaitingForEvidence,
				ReviewVerdict: "APPROVE", WorktreeChanged: &changed, Findings: []core.DispatchFinding{},
				Acceptance: []core.DispatchAcceptance{{Criterion: "tests pass", Status: "PASS", Evidence: []string{"test-report"}}},
			}
			if failure == "" {
				result.ReviewVerdict = "REQUEST_CHANGES"
				result.Acceptance[0].Status = "FAIL"
				result.Findings = []core.DispatchFinding{{
					Severity: "HIGH", Title: "Improve the acceptance coverage",
					File: "README.md", Lines: "1", Evidence: []string{"test-report"},
				}}
			}
			payload, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			accepted, finishErr := control.Finish(context.Background(), id, string(payload))
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := state.Graph.GraphDispatch(id)
			if failure != "" {
				if finishErr == nil || accepted != nil || saved.Status != core.DispatchRunning ||
					saved.Result != nil || !state.Ledger.Dispatches[0].Active {
					t.Fatalf("adopted incomplete review: %+v %v %+v", accepted, finishErr, saved)
				}
				if _, err := os.Stat(reviewPath); err != nil {
					t.Fatal("failed review worktree must remain for inspection", err)
				}
				return
			}
			if finishErr != nil || accepted == nil || !accepted.GitEvidence.WorktreeReleased ||
				saved.Status != core.DispatchSucceeded || worker.closes != 1 {
				t.Fatalf("Reviewer finalization: %+v %v closes=%d", accepted, finishErr, worker.closes)
			}
			if _, err := os.Stat(reviewPath); !os.IsNotExist(err) {
				t.Fatal("review worktree was not removed", err)
			}
			if strings.Contains(gitTest(t, root, "worktree", "list", "--porcelain"), reviewPath) {
				t.Fatal("review worktree remains registered")
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			state, err = reopened.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			saved, _ = state.Graph.GraphDispatch(id)
			if !saved.Result.GitEvidence.WorktreeReleased || saved.ReviewWorktreeSource != root {
				t.Fatal("review release evidence lost after database restart")
			}
			plan, err := Plan(reopened, filepath.Join(filepath.Dir(path), "ledger.json"), "owner/repo")
			if err != nil || len(plan.Tasks) != 1 || len(plan.Tasks[0].Dispatches) != 1 {
				t.Fatalf("review findings lost in follow-up planning: %+v %v", plan, err)
			}
			prior := plan.Tasks[0].Dispatches[0]
			if prior.ID != id || prior.Result.ReviewVerdict != "REQUEST_CHANGES" ||
				prior.Result.OutputSHA != head || len(prior.Result.Findings) != 1 ||
				prior.Result.Findings[0].Title != result.Findings[0].Title ||
				prior.Result.Findings[0].Evidence[0] != "test-report" ||
				prior.Instruction.Acceptance[0] != result.Acceptance[0].Criterion {
				t.Fatalf("review findings lost their target and source contract: %+v", prior)
			}
		})
	}
}

func TestControllerPersistsObservedValidationFailure(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "work/task")
	gitTest(t, root, "config", "user.name", "Taxion Test")
	gitTest(t, root, "config", "user.email", "taxion-test@example.com")
	gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "README.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "README.md")
	gitTest(t, root, "commit", "-m", "empty baseline")
	head := gitTest(t, root, "rev-parse", "HEAD")
	db, _, id := dispatchStore(t, root)
	defer db.Close()
	control, err := New(db, &fakeRuntime{role: "CODER", hash: "config-v1", worktree: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	result := core.DispatchResult{
		DispatchID: id, TaskID: "task-1", Recipient: "CODER", Repository: "owner/repo",
		Status: core.DispatchSucceeded, StartedSHA: head, OutputSHA: head, ChangedFiles: []string{},
		Evidence: []string{}, NextTransition: core.WaitingForEvidence,
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	observed, finishErr := control.Finish(context.Background(), id, string(payload))
	if finishErr == nil || observed == nil || observed.Status != core.DispatchFailed {
		t.Fatalf("failed validation appeared successful: %+v %v", observed, finishErr)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := state.Graph.GraphDispatch(id)
	if saved.Status != core.DispatchFailed || state.Ledger.Dispatches[0].Active ||
		len(saved.Result.Validations) != 1 || saved.Result.Validations[0].SHA != head ||
		saved.Result.Validations[0].ExitCode != 1 || saved.Result.Validations[0].Output != "controller validated" ||
		saved.Result.GitEvidence != nil || len(saved.Result.Evidence) != 0 {
		t.Fatalf("observed validation failure was lost: %+v", saved)
	}
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
