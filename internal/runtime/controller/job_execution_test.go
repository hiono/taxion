package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type jobRoleRuntimeFake struct {
	role       string
	configHash string
	selection  json.RawMessage
	taskID     string
	sessionID  string
	prompt     string
	startCount int
	events     chan runtime.Event
	stopErr        error
	authErr        error
	expectedAccount string
}

func (f *jobRoleRuntimeFake) Role() string       { return f.role }
func (f *jobRoleRuntimeFake) ConfigHash() string { return f.configHash }
func (f *jobRoleRuntimeFake) Worktree() string   { return "/tmp/job-role-worktree" }
func (f *jobRoleRuntimeFake) Start(_ context.Context, taskID string) (string, error) {
	f.startCount++
	f.taskID = taskID
	f.sessionID = "job-session-" + taskID
	return f.sessionID, nil
}
func (f *jobRoleRuntimeFake) Send(_ context.Context, sessionID, prompt string) error {
	if sessionID != f.sessionID {
		return context.Canceled
	}
	f.prompt = prompt
	return nil
}
func (f *jobRoleRuntimeFake) Resume(_ context.Context, sessionID string) error {
	if sessionID != f.sessionID {
		return context.Canceled
	}
	f.resumeCount++
	return nil
}
func (f *jobRoleRuntimeFake) Stop(_ context.Context, sessionID string) error {
	if sessionID != f.sessionID {
		return context.Canceled
	}
	return f.stopErr
}
func (f *jobRoleRuntimeFake) Events(_ context.Context, sessionID string) (<-chan runtime.Event, error) {
	if sessionID != f.sessionID {
		return nil, context.Canceled
	}
	return f.events, nil
}
func (f *jobRoleRuntimeFake) ExecutionConfiguration() json.RawMessage { return f.selection }
func (f *jobRoleRuntimeFake) CheckAuthentication(_ context.Context, account string) error {
	f.expectedAccount = account
	return f.authErr
}
func (f *jobRoleRuntimeFake) CheckRepositoryAccess(context.Context) error { return nil }
func (f *jobRoleRuntimeFake) CheckServiceAccess(context.Context, string) error { return nil }

func jobRoleFixture(t *testing.T, approve bool) (*store.Store, core.Job, core.GraphTask, json.RawMessage) {
	t.Helper()
	db, request := intakeApprovalFixture(t, false)
	selection := json.RawMessage(`{"runtime":{"profile":"job-role-test","account":"test-account"}}`)
	sources := map[string]string{"runtime.profile": "project", "runtime.account": "project"}
	configuration, err := core.SelectApprovalConfiguration(selection, sources)
	if err != nil {
		t.Fatal(err)
	}
	var job core.Job
	var task core.GraphTask
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		var ok bool
		job, ok = state.Graph.JobForTask(request.TaskID)
		if !ok {
			t.Fatal("intake Job is missing")
		}
		task, ok = state.Graph.GraphTask(request.TaskID)
		if !ok {
			t.Fatal("intake Task is missing")
		}
		now := time.Now().UTC()
		if job.Status != core.JobActive {
			if err := state.Graph.TransitionJob(job.ID, core.JobActive, "controller", "Job Role fixture activated", now); err != nil {
				return err
			}
		}
		for i := range state.Graph.Jobs {
			if state.Graph.Jobs[i].ID == job.ID {
				state.Graph.Jobs[i].Contract = &core.JobContract{
					Revision: 1, Roles: []string{"PLANNER", "CODER", "REVIEWER"},
					Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
					Repositories: []core.JobRepositoryScope{{
						Repository: task.Repo, FileAccess: *task.FileAccess,
					}},
					Operations: []string{"repository.read", "repository.write", "test.run"},
					Budget: core.JobBudget{MaxRoleExecutions: 8, MaxConcurrentExecutions: 3, MaxNanoAIU: 100},
					Deadline: now.Add(time.Hour), Configuration: configuration,
				}
				job = state.Graph.Jobs[i]
				break
			}
		}
		if approve {
			proposal, err := core.JobProposal(job)
			if err != nil {
				return err
			}
			approval, err := core.NewJobApproval("job-role-approval", "human", "user", "approve Job Role fixture", proposal, now.Add(time.Second))
			if err != nil {
				return err
			}
			if err := state.Graph.AppendJobApproval(approval); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	job, _ = state.Graph.JobForTask(request.TaskID)
	task, _ = state.Graph.GraphTask(request.TaskID)
	return db, job, task, selection
}

func TestJobRoleControllerStartsSendsAndAuthorizesAgainstJob(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "job-role-exec", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := controller.Start(context.Background(), "job-role-exec")
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != worker.sessionID || worker.taskID != task.ID || worker.startCount != 1 {
		t.Fatalf("unexpected Runtime binding: session=%q task=%q starts=%d", sessionID, worker.taskID, worker.startCount)
	}
	if worker.expectedAccount != "test-account" {
		t.Fatalf("Runtime authentication expected account = %q, want test-account", worker.expectedAccount)
	}
	firstUsage := 40.0
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &firstUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "usage-1"},
		ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// A replay of the same provider observation is idempotent.
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &firstUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "usage-1"},
		ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("identical usage replay must be idempotent: %v", err)
	}
	conflictingUsage := 41.0
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &conflictingUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "usage-1"},
		ObservedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("same provider identity with different usage content must be rejected")
	}
	otherRuntime, err := NewJobRoleController(db, &jobRoleRuntimeFake{
		role: "REVIEWER", configHash: worker.configHash, selection: selection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := otherRuntime.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &firstUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 3, UsageID: "usage-other-runtime"},
		ObservedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("another Role Runtime must not record usage for this execution")
	}
	usageState, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(usageState.Graph.JobUsageRecords(job.ID)); got != 1 {
		t.Fatalf("idempotent usage replay created %d records, want 1", got)
	}
	if err := controller.AuthorizeOperation("job-role-exec", "repository.write", time.Now().UTC()); err != nil {
		t.Fatalf("approved Job operation rejected: %v", err)
	}
	if err := controller.AuthorizeOperation("job-role-exec", "network.access", time.Now().UTC()); err == nil {
		t.Fatal("operation outside Job approval must be rejected")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "job-role-reviewer", JobID: job.ID, TaskID: task.ID, Role: "REVIEWER",
			Status: core.RoleExecutionCreated, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.PostMessage("job-role-exec", "job-role-reviewer", "job-role-message",
		"Please review the implementation and test evidence.", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Send(context.Background(), "job-role-exec", "Implement the approved Task."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(worker.prompt, "Current Job context") ||
		!strings.Contains(worker.prompt, job.Request) ||
		!strings.Contains(worker.prompt, "repository.write") ||
		!strings.Contains(worker.prompt, "Please review the implementation") {
		t.Fatalf("Runtime did not receive the approved Job context: %s", worker.prompt)
	}
	secondUsage := 60.0
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &secondUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 2, UsageID: "usage-2"},
		ObservedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.AuthorizeOperation("job-role-exec", "repository.write", time.Now().UTC()); err == nil {
		t.Fatal("Job AIU budget exhaustion must block further operations")
	}
	if err := controller.Send(context.Background(), "job-role-exec", "another turn"); err == nil {
		t.Fatal("Job AIU budget exhaustion must block further model turns")
	}
	if _, err := controller.Events(context.Background(), "job-role-exec"); err != nil {
		t.Fatal(err)
	}
	if err := controller.Finish("job-role-exec", core.RoleExecutionSucceeded, "controller", "turn completed", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	execution, _ := state.Graph.RoleExecution("job-role-exec")
	if execution.Status != core.RoleExecutionSucceeded || execution.RuntimeSessionID != sessionID ||
		len(execution.ContextHash) != 64 {
		t.Fatalf("RoleExecution did not persist its terminal state and context binding: %+v", execution)
	}
}

func TestJobRoleControllerDoesNotStartUnapprovedJob(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, false)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "job-role-unapproved", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{role: "CODER", configHash: "runtime-config-hash", selection: selection}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), "job-role-unapproved"); err == nil {
		t.Fatal("unapproved Job must not create a Runtime session")
	}
	if worker.startCount != 0 {
		t.Fatalf("Runtime started %d times for an unapproved Job", worker.startCount)
	}
}


func TestJobRoleControllerStartsPlannerWithoutTaskAssignment(t *testing.T) {
	db, job, _, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "job-level-planner", JobID: job.ID, Role: "PLANNER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "PLANNER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), "job-level-planner"); err != nil {
		t.Fatal(err)
	}
	if worker.taskID != job.ID {
		t.Fatalf("Job-level Planner runtime binding = %q, want Job ID %q", worker.taskID, job.ID)
	}
}

func TestJobRoleControllerDoesNotStartWithAuthenticationMismatch(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "job-role-auth-failure", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		authErr: context.Canceled,
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), "job-role-auth-failure"); err == nil {
		t.Fatal("Runtime authentication mismatch must block session creation")
	}
	if worker.startCount != 0 {
		t.Fatalf("Runtime started %d times after authentication failure", worker.startCount)
	}
}

func TestJobRoleControllerResumeRevalidatesCurrentJobApproval(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "job-role-resume", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), "job-role-resume"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.TransitionRoleExecution("job-role-resume", core.RoleExecutionSuspended,
			"controller", "awaiting follow-up", time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Resume(context.Background(), "job-role-resume"); err != nil {
		t.Fatalf("approved suspended execution should resume: %v", err)
	}
	if worker.resumeCount != 1 {
		t.Fatalf("Runtime resume count = %d, want 1", worker.resumeCount)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := state.Graph.TransitionRoleExecution("job-role-resume", core.RoleExecutionSuspended,
			"controller", "awaiting another follow-up", time.Now().UTC()); err != nil {
			return err
		}
		for i := range state.Graph.Jobs {
			if state.Graph.Jobs[i].ID == job.ID {
				state.Graph.Jobs[i].Contract.Revision++
				state.Graph.Jobs[i].Constraints = append(state.Graph.Jobs[i].Constraints, "new unapproved constraint")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Resume(context.Background(), "job-role-resume"); err == nil {
		t.Fatal("stale Job approval must block Runtime resume")
	}
	if worker.resumeCount != 1 {
		t.Fatalf("Runtime resumed %d times after approval became stale", worker.resumeCount)
	}
}
