package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

type configuredRuntime struct {
	*fakeRuntime
	selection        json.RawMessage
	accounts         []string
	authErr          error
	repositoryErr    error
	repositoryChecks int
	onRepository     func()
	onAuth           func()
	onStart          func()
	onResume         func()
	serviceErr       error
	serviceChecks    int
	onService        func()
}

func (f *configuredRuntime) ExecutionConfiguration() json.RawMessage { return f.selection }
func (f *configuredRuntime) CheckServiceAccess(context.Context, string) error {
	f.serviceChecks++
	if f.onService != nil {
		f.onService()
	}
	return f.serviceErr
}
func (f *configuredRuntime) CheckRepositoryAccess(context.Context) error {
	f.repositoryChecks++
	if f.onRepository != nil {
		f.onRepository()
	}
	return f.repositoryErr
}
func (f *configuredRuntime) CheckAuthentication(_ context.Context, account string) error {
	f.accounts = append(f.accounts, account)
	if f.onAuth != nil {
		f.onAuth()
	}
	return f.authErr
}
func (f *configuredRuntime) Start(ctx context.Context, taskID string) (string, error) {
	id, err := f.fakeRuntime.Start(ctx, taskID)
	if f.onStart != nil {
		f.onStart()
	}
	return id, err
}
func (f *configuredRuntime) Resume(ctx context.Context, id string) error {
	err := f.fakeRuntime.Resume(ctx, id)
	if f.onResume != nil {
		f.onResume()
	}
	return err
}

func executionConfigurationFixture(t *testing.T) (*store.Store, string, *configuredRuntime, config.Resolution) {
	return executionConfigurationCredentialFixture(t, "ai")
}

func executionConfigurationCredentialFixture(t *testing.T, credentialRef string) (*store.Store, string, *configuredRuntime, config.Resolution) {
	t.Helper()
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
	resolved, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(strings.ReplaceAll(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`, "credentialRef: ai", "credentialRef: "+credentialRef))}})
	if err != nil {
		t.Fatal(err)
	}
	approval, err := json.Marshal(config.Approval{
		Kind: config.ApprovalKind, TaskID: "task", Reason: "approved execution", Allowed: []config.Config{resolved.Config},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := core.GraphTask{
		ID: "task", Title: "execution configuration", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #69",
		Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER", CreatedFrom: "human:request",
		Objective: "approved work", Scope: []string{"README.md"}, Acceptance: []string{"tests pass"},
		ValidationCommands: []string{"test -s README.md"}, RequiredEvidence: []string{"report"}, ExitCondition: "tests pass",
		Worktree: root, Branch: "work/task", BaselineSHA: gitTest(t, root, "rev-parse", "HEAD"),
		AuthorizationID: "approval", Decisions: []core.TaskDecision{{ID: "approval", Source: "human:selection", Text: string(approval)}},
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "register"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := core.NewGraphDispatch(graph, "dispatch", task.ID, task.Repo, core.RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph,
		Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}},
			Dispatches: []core.Dispatch{{ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true, CreatedAt: dispatch.CreatedAt}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := BindConfiguration(db, dispatch.ID, resolved); err != nil {
		t.Fatal(err)
	}
	selection, err := json.Marshal(resolved.Config)
	if err != nil {
		t.Fatal(err)
	}
	return db, dispatch.ID, &configuredRuntime{
		fakeRuntime: &fakeRuntime{role: "CODER", hash: "selected-config", worktree: root}, selection: selection,
	}, resolved
}

func TestMissingExplicitCredentialPreservesCancellationIntent(t *testing.T) {
	db, id, worker, _ := executionConfigurationCredentialFixture(t, "env:TAXION_TEST_STOP_TOKEN")
	t.Setenv("TAXION_TEST_STOP_TOKEN", "private-stop-fixture")
	cfg := copilot.Config{
		Role: worker.Role(), SystemPrompt: "saved stop configuration",
		WorkingDirectory: worker.Worktree(), CLIPath: "/bin/false",
		BaseDirectory: t.TempDir(), Configuration: worker.selection,
	}
	bound, err := copilot.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	worker.hash = bound.ConfigHash()
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), id, "authorized execution"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_TEST_STOP_TOKEN", "")
	missing, err := copilot.New(cfg)
	if err != nil {
		t.Fatal("credential loss prevented offline safe-stop construction", err)
	}
	defer missing.Close()
	control, err = New(db, missing)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := control.Stop(context.Background(), id); err == nil ||
			!strings.Contains(err.Error(), "selected runtime credential") {
			t.Fatal("missing credential attached or reported successful runtime stop", err)
		}
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		dispatch, _ := state.Graph.GraphDispatch(id)
		if dispatch.Status != core.DispatchCancelRequested || dispatch.Result != nil {
			t.Fatal("failed attachment lost cancellation intent or fabricated a stopped result", dispatch)
		}
	}
}

func TestConfiguredExecutionAuthenticatesAndPreservesSafeStop(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(worker.accounts, []string{"ai-user", "ai-user", "ai-user"}) {
		t.Fatal("execution did not verify the selected account on every operation", worker.accounts)
	}
	if worker.repositoryChecks != 3 {
		t.Fatal("execution did not verify repository access on every operation", worker.repositoryChecks)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := before.Graph.GraphDispatch(id)
	checks := dispatch.Configuration.AccessChecks
	if len(checks) != 3 {
		t.Fatal("successful access checks were not durably recorded", checks)
	}
	for i, operation := range []string{"START", "RESUME", "SEND"} {
		check := checks[i]
		if check.Operation != operation || check.RuntimeConfigHash != worker.hash ||
			check.RepositoryPermission != "READ_WRITE" || check.ContractEligibility != "UNVERIFIED" ||
			check.StartedAt.IsZero() || check.CompletedAt.Before(check.StartedAt) {
			t.Fatal("access observation claims the wrong operation, identity, permissions or contract", check)
		}
	}
	dispatch.Configuration.AccessChecks[0].Operation = "MUTATED"
	reloaded, err := db.LoadWorkspace()
	reloadedDispatch, _ := reloaded.Graph.GraphDispatch(id)
	if err != nil || reloadedDispatch.Configuration.AccessChecks[0].Operation != "START" {
		t.Fatal("returned access observations mutated durable state", err)
	}
	before = reloaded
	worker.authErr = errors.New("authentication revoked")
	resumes, sends := worker.resumes, len(worker.sends)
	for _, operation := range []func() error{
		func() error { _, err := control.Start(context.Background(), id); return err },
		func() error { return control.Resume(context.Background(), id) },
		func() error { return control.Send(context.Background(), id, "continue") },
	} {
		if err := operation(); !errors.Is(err, worker.authErr) {
			t.Fatal("revoked authentication did not block execution", err)
		}
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) || worker.resumes != resumes || len(worker.sends) != sends {
		t.Fatal("rejected execution reached SDK work or changed durable state", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.Transition("task", core.Blocked, "human", "revoke", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	published := false
	if _, err := control.managedGit(context.Background(), id, worker.sessionID, "test", func(context.Context, core.GraphDispatch) (string, error) {
		published = true
		return "", nil
	}); err == nil || published {
		t.Fatal("revoked configuration reached a managed Git publication")
	}
	authCalls := len(worker.accounts)
	if err := control.Stop(context.Background(), id); err != nil {
		t.Fatal("revoked approval/authentication prevented safe stop", err)
	}
	if len(worker.accounts) != authCalls || worker.stops != 1 {
		t.Fatal("safe stop required authentication or failed to stop")
	}
}

func TestConfiguredExecutionRejectsMissingChangedAndRevokedSelection(t *testing.T) {
	for _, failure := range []string{"missing", "changed", "auth", "repository", "revoked-during-auth", "revoked-during-repository", "revoked-during-start"} {
		t.Run(failure, func(t *testing.T) {
			db, id, worker, resolved := executionConfigurationFixture(t)
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}

			block := func() {
				t.Helper()
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					return state.Graph.Transition("task", core.Blocked, "human", "revoke", core.TransitionProof{})
				}); err != nil {
					t.Fatal(err)
				}
			}
			switch failure {
			case "missing":
				worker.selection = nil
			case "changed":
				resolved.Config.Runtime.ContractRef = "other-contract"
				worker.selection, err = json.Marshal(resolved.Config)
				if err != nil {
					t.Fatal(err)
				}
			case "auth":
				worker.authErr = errors.New("not authenticated")
			case "repository":
				worker.repositoryErr = errors.New("repository permission denied")
			case "revoked-during-auth":
				worker.onAuth = block
			case "revoked-during-repository":
				worker.onRepository = block
			case "revoked-during-start":
				worker.onStart = block
			}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(context.Background(), id); err == nil {
				t.Fatal("invalid execution configuration accepted")
			}
			if failure == "revoked-during-start" {
				if worker.starts != 1 || worker.stops != 1 {
					t.Fatal("session created during revocation was not stopped")
				}
			} else if worker.starts != 0 {
				t.Fatal("rejected configuration reached session creation")
			}
			after, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			dispatch, _ := after.Graph.GraphDispatch(id)
			if dispatch.RuntimeSessionID != "" {
				t.Fatal("rejected configuration bound a session")
			}
			if failure != "revoked-during-auth" && failure != "revoked-during-repository" && failure != "revoked-during-start" && !reflect.DeepEqual(before, after) {
				t.Fatal("rejected configuration mutated durable state")
			}
		})
	}
}

func TestRecordedAccessChecksRejectChangedIdentityAndSurviveRestart(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.authorizeExecution(context.Background(), id, "START"); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(id)
	if dispatch.RuntimeSessionID != "" || len(dispatch.Configuration.AccessChecks) != 1 {
		t.Fatal("access check claimed a created SDK session")
	}
	original, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, operation := range []func() error{
		func() error { return state.Graph.RecordDispatchAccessCheck(id, "STOP", worker.hash, now, now) },
		func() error { return state.Graph.RecordDispatchAccessCheck(id, "START", "different-hash", now, now) },
		func() error {
			return state.Graph.RecordDispatchAccessCheck(id, "START", worker.hash, now, now.Add(-time.Second))
		},
		func() error { return state.Graph.BindRuntimeSession(id, "new-session", "different-hash") },
	} {
		if err := operation(); err == nil {
			t.Fatal("invalid observation or changed session identity accepted")
		}
		content, err := json.Marshal(state)
		if err != nil || string(content) != string(original) {
			t.Fatal("rejected access record or binding partially mutated the graph", err)
		}
	}
	path := filepath.Join(t.TempDir(), "restart.db")
	saved, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saved.InitializeWorkspace(state); err != nil {
		saved.Close()
		t.Fatal(err)
	}
	if err := saved.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reloaded, err := reopened.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(state, reloaded) {
		t.Fatal("access observation did not survive SQLite restart", err)
	}
	control, err = New(reopened, worker)
	if err != nil {
		t.Fatal(err)
	}
	worker.repositoryErr = errors.New("revoked after restart")
	if _, err := control.Start(context.Background(), id); !errors.Is(err, worker.repositoryErr) || worker.starts != 0 {
		t.Fatal("persisted success replaced a fresh external access check", err)
	}
}

func TestConfiguredRepositoryRevocationPreservesSafeStop(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
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
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	worker.repositoryErr = errors.New("repository credential revoked")
	resumes, sends := worker.resumes, len(worker.sends)
	for _, operation := range []func() error{
		func() error { _, err := control.Start(context.Background(), id); return err },
		func() error { return control.Resume(context.Background(), id) },
		func() error { return control.Send(context.Background(), id, "continue") },
	} {
		if err := operation(); !errors.Is(err, worker.repositoryErr) {
			t.Fatal("revoked repository access did not block execution", err)
		}
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) || worker.resumes != resumes || len(worker.sends) != sends {
		t.Fatal("rejected repository access changed state or reached SDK work", err)
	}
	checks := worker.repositoryChecks
	if err := control.Stop(context.Background(), id); err != nil || worker.repositoryChecks != checks || worker.stops != 1 {
		t.Fatal("safe stop depended on repository access", err)
	}
}

func TestConfiguredSendRechecksApprovalAfterSessionAttachment(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	worker.onResume = func() {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			return state.Graph.Transition("task", core.Blocked, "human", "revoke", core.TransitionProof{})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := control.Send(context.Background(), id, "continue"); err == nil || len(worker.sends) != 0 {
		t.Fatal("revoked approval during attachment reached instruction send")
	}
}

func TestSelectedDispatchCannotUseUnconfiguredRuntimeOrChangedSessionHash(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
	unconfigured, err := New(db, worker.fakeRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unconfigured.Start(context.Background(), id); err == nil || worker.starts != 0 {
		t.Fatal("selected Dispatch accepted a runtime without configuration identity")
	}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	authCalls, resumes := len(worker.accounts), worker.resumes
	worker.hash = "changed-session-configuration"
	for _, operation := range []func() error{
		func() error { _, err := control.Start(context.Background(), id); return err },
		func() error { return control.Resume(context.Background(), id) },
		func() error { return control.Send(context.Background(), id, "continue") },
	} {
		if err := operation(); err == nil {
			t.Fatal("changed hash accepted an existing session")
		}
	}
	if len(worker.accounts) != authCalls || worker.resumes != resumes {
		t.Fatal("changed hash reached authentication or session attachment")
	}
}
