package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type jobRoleRuntimeFake struct {
	role                   string
	configHash             string
	selection              json.RawMessage
	taskID                 string
	sessionID              string
	prompt                 string
	startCount             int
	resumeCount            int
	sendCount              int
	onResume               func()
	stopCount              int
	events                 chan runtime.Event
	stopErr                error
	authErr                error
	expectedAccount        string
	repository             string
	fileAccess             *core.FileAccessPolicy
	jobOperationAuthorizer func(context.Context, string, string, string) error
}

func (f *jobRoleRuntimeFake) Role() string       { return f.role }
func (f *jobRoleRuntimeFake) ConfigHash() string { return f.configHash }
func (f *jobRoleRuntimeFake) Worktree() string   { return "/tmp/job-role-worktree" }
func (f *jobRoleRuntimeFake) JobRepository() string {
	if f.repository != "" {
		return f.repository
	}
	return "owner/repo"
}
func (f *jobRoleRuntimeFake) JobFileAccessPolicy() *core.FileAccessPolicy {
	if f.fileAccess != nil {
		return core.CloneFileAccessPolicy(f.fileAccess)
	}
	return &core.FileAccessPolicy{Level: core.FileAccessCommitted}
}
func (f *jobRoleRuntimeFake) Start(_ context.Context, taskID string) (string, error) {
	f.startCount++
	f.taskID = taskID
	f.sessionID = "job-session-" + f.role + "-" + taskID
	return f.sessionID, nil
}
func (f *jobRoleRuntimeFake) Send(_ context.Context, sessionID, prompt string) error {
	if sessionID != f.sessionID {
		return context.Canceled
	}
	f.prompt = prompt
	f.sendCount++
	return nil
}
func (f *jobRoleRuntimeFake) Resume(_ context.Context, sessionID string) error {
	if sessionID != f.sessionID {
		return context.Canceled
	}
	f.resumeCount++
	if f.onResume != nil {
		f.onResume()
	}
	return nil
}
func (f *jobRoleRuntimeFake) Stop(_ context.Context, sessionID string) error {
	if sessionID != f.sessionID {
		return context.Canceled
	}
	f.stopCount++
	return f.stopErr
}
func (f *jobRoleRuntimeFake) Events(_ context.Context, sessionID string) (<-chan runtime.Event, error) {
	if sessionID != f.sessionID {
		return nil, context.Canceled
	}
	return f.events, nil
}
func (f *jobRoleRuntimeFake) ExecutionConfiguration() json.RawMessage { return f.selection }
func (f *jobRoleRuntimeFake) SetJobOperationAuthorizer(authorizer func(context.Context, string, string, string) error) {
	f.jobOperationAuthorizer = authorizer
}
func (f *jobRoleRuntimeFake) CheckAuthentication(_ context.Context, account string) error {
	f.expectedAccount = account
	return f.authErr
}
func (f *jobRoleRuntimeFake) CheckRepositoryAccess(context.Context) error      { return nil }
func (f *jobRoleRuntimeFake) CheckServiceAccess(context.Context, string) error { return nil }


func jobRoleFixture(t *testing.T, approve bool) (*store.Store, core.Job, core.GraphTask, json.RawMessage) {
	t.Helper()
	db, request := jobIntakeApprovalFixture(t)
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
					Acceptance: append([]string(nil), task.Acceptance...), RequiredEvidence: append([]string(nil), task.RequiredEvidence...),
					Repositories:            []core.JobRepositoryScope{{
						Repository:              task.Repo, FileAccess: *task.FileAccess,
					}},
					ValidationCommands:      append([]string(nil), task.ValidationCommands...),
					ProhibitedEffects:       core.DefaultJobProhibitedEffects(),
					ApprovalRequiredEffects: core.DefaultJobApprovalRequiredEffects(),
					Budget:                  core.JobBudget{MaxRoleExecutions: 8, MaxConcurrentExecutions: 3, MaxNanoAIU: 100},
					Deadline:                now.Add(time.Hour), Configuration: configuration,
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
	if worker.jobOperationAuthorizer == nil {
		t.Fatal("Job controller did not install the provider operation preflight")
	}
	if err := worker.jobOperationAuthorizer(context.Background(), sessionID, "repository.write", ""); err != nil {
		t.Fatalf("approved Coder write operation rejected: %v", err)
	}
	if len(task.ValidationCommands) > 0 {
		if err := worker.jobOperationAuthorizer(context.Background(), sessionID, "process.exec", task.ValidationCommands[0]); err != nil {
			t.Fatalf("exact approved validation command rejected: %v", err)
		}
		if err := worker.jobOperationAuthorizer(context.Background(), sessionID, "process.exec", "test -f README.md"); err == nil {
			t.Fatal("modified validation command must not use the generic process.exec grant")
		}
		if err := worker.jobOperationAuthorizer(context.Background(), sessionID, "process.exec", "printf unauthorized"); err == nil {
			t.Fatal("unlisted shell command must not fall through to the generic process.exec grant")
		}
	}
	if err := worker.jobOperationAuthorizer(context.Background(), sessionID, "process.exec", ""); err == nil {
		t.Fatal("shell authorization must reject a missing command payload")
	}
	if err := worker.jobOperationAuthorizer(context.Background(), "different-session", "repository.write", ""); err == nil {
		t.Fatal("operation preflight must reject a different provider session")
	}
	if err := worker.jobOperationAuthorizer(context.Background(), sessionID, "network.access", ""); err == nil {
		t.Fatal("operation preflight must reject an unapproved network operation")
	}
	if worker.expectedAccount != "test-account" {
		t.Fatalf("Runtime authentication expected account = %q, want test-account", worker.expectedAccount)
	}
	observedAt := time.Now().UTC()
	firstUsage := 40.0
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &firstUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "usage-1"},
		ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
	// A replay of the same provider observation is idempotent.
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &firstUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "usage-1"},
		ObservedAt: observedAt,
	}); err != nil {
		t.Fatalf("identical usage replay must be idempotent: %v", err)
	}
	conflictingUsage := 41.0
	if err := controller.RecordUsage("job-role-exec", core.DispatchUsage{
		Model: "test-model", TotalNanoAIU: &conflictingUsage,
		Accounting: &core.UsageAccountingIdentity{SourceSessionID: sessionID, Sequence: 1, UsageID: "usage-1"},
		ObservedAt: observedAt,
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
	reviewerRuntime := &jobRoleRuntimeFake{
		role: "REVIEWER", configHash: "reviewer-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	reviewerController, err := NewJobRoleController(db, reviewerRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewerController.Start(context.Background(), "job-role-reviewer"); err != nil {
		t.Fatal(err)
	}
	if reviewerRuntime.jobOperationAuthorizer == nil {
		t.Fatal("Reviewer Job controller did not install the provider operation preflight")
	}
	if err := reviewerRuntime.jobOperationAuthorizer(context.Background(), reviewerRuntime.sessionID, "process.exec", ""); err == nil {
		t.Fatal("Reviewer provider preflight must reject process execution")
	}
	if err := reviewerController.AuthorizeOperation("job-role-reviewer", "repository.read", time.Now().UTC()); err != nil {
		t.Fatalf("Reviewer read operation rejected: %v", err)
	}
	for _, operation := range []string{"repository.write", "process.exec", "test.run"} {
		if err := reviewerController.AuthorizeOperation("job-role-reviewer", operation, time.Now().UTC()); err == nil {
			t.Fatalf("Reviewer must not receive Coder operation %q", operation)
		}
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
		strings.Contains(worker.prompt, "repository.write") ||
		!strings.Contains(worker.prompt, "Please review the implementation") {
		t.Fatalf("Runtime did not receive the approved Job context without legacy operation ACLs: %s", worker.prompt)
	}
	go func() {
		worker.events <- runtime.Event{
			Type:    runtime.EventUsage,
			Payload: `{"model":"test-model","accounting":{"sourceSessionId":"` + sessionID + `","sequence":2,"usageId":"usage-2"},"totalNanoAIU":60}`,
		}
		worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
		close(worker.events)
	}()
	eventStream, err := controller.Events(context.Background(), "job-role-exec")
	if err != nil {
		t.Fatal(err)
	}
	usageEventObserved := false
	for event := range eventStream {
		if event.Type == runtime.EventUsage {
			usageEventObserved = true
		}
		if event.Type == runtime.EventFailed {
			t.Fatalf("Runtime usage event was rejected: %s", event.Payload)
		}
	}
	if !usageEventObserved {
		t.Fatal("Runtime usage event was not forwarded")
	}
	if err := controller.AuthorizeOperation("job-role-exec", "repository.write", time.Now().UTC()); err == nil {
		t.Fatal("Job AIU budget exhaustion must block further operations")
	}
	if err := controller.Send(context.Background(), "job-role-exec", "another turn"); err == nil {
		t.Fatal("Job AIU budget exhaustion must block further model turns")
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

func TestJobContractDoesNotRequireRoleOperationOrToolAllowlists(t *testing.T) {
	_, job, _, _ := jobRoleFixture(t, true)
	if err := job.Contract.Validate(); err != nil {
		t.Fatalf("Job contract must be independent of Role operation and tool ACL maps: %v", err)
	}
}

func TestJobRoleControllerDoesNotUseProviderToolPolicyAsJobAuthorization(t *testing.T) {
	db, _, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "without-provider-tool-policy", JobID: task.JobID, TaskID: task.ID, Role: "CODER",
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
	if _, err := controller.Start(context.Background(), "without-provider-tool-policy"); err != nil {
		t.Fatalf("provider tool availability must not be an additional Job authorization gate: %v", err)
	}
	if worker.startCount != 1 {
		t.Fatalf("Runtime started %d times, want 1", worker.startCount)
	}
}

func TestJobRoleControllerSendRefreshesContextAfterRuntimeResume(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "send-refresh-exec", JobID: job.ID, TaskID: task.ID, Role: "CODER",
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
	if _, err := controller.Start(context.Background(), "send-refresh-exec"); err != nil {
		t.Fatal(err)
	}
	worker.onResume = func() {
		err := db.UpdateWorkspace(func(state *store.Workspace) error {
			return state.Graph.AppendJobMessage(core.JobMessage{
				ID: "message-during-resume", JobID: job.ID, FromRole: "HUMAN", ToRole: "CODER",
				Body: "Use the newly confirmed requirement.", Source: "human:test", At: time.Now().UTC(),
			})
		})
		if err != nil {
			t.Errorf("append concurrent Job message: %v", err)
		}
	}
	if err := controller.Send(context.Background(), "send-refresh-exec", "Continue the work."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(worker.prompt, "Use the newly confirmed requirement.") {
		t.Fatalf("Send used a stale Job context: %s", worker.prompt)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	execution, ok := state.Graph.RoleExecution("send-refresh-exec")
	if !ok {
		t.Fatal("RoleExecution disappeared")
	}
	_, expectedHash, err := BuildJobExecutionContext(state.Graph, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.ContextHash != expectedHash {
		t.Fatalf("stored context hash = %q, want latest snapshot %q", execution.ContextHash, expectedHash)
	}
}

func TestJobRoleControllerSendRejectsApprovalRevokedDuringResume(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "send-revoked-exec", JobID: job.ID, TaskID: task.ID, Role: "CODER",
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
	if _, err := controller.Start(context.Background(), "send-revoked-exec"); err != nil {
		t.Fatal(err)
	}
	worker.onResume = func() {
		err := db.UpdateWorkspace(func(state *store.Workspace) error {
			for i := range state.Graph.Jobs {
				if state.Graph.Jobs[i].ID == job.ID {
					state.Graph.Jobs[i].Contract.Revision++
					return nil
				}
			}
			return fmt.Errorf("Job %s disappeared", job.ID)
		})
		if err != nil {
			t.Errorf("revoke current Job approval: %v", err)
		}
	}
	if err := controller.Send(context.Background(), "send-revoked-exec", "Continue the work."); err == nil {
		t.Fatal("Send must revalidate the current Job approval after Runtime.Resume")
	}
	if worker.sendCount != 0 {
		t.Fatalf("Runtime received %d turns after approval revocation", worker.sendCount)
	}
	if worker.stopCount != 1 {
		t.Fatalf("Runtime stop count after approval revocation = %d, want 1", worker.stopCount)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	execution, ok := state.Graph.RoleExecution("send-revoked-exec")
	if !ok || execution.Status != core.RoleExecutionSuspended {
		t.Fatalf("revoked RoleExecution status = %+v, want SUSPENDED", execution)
	}
}

func TestJobRoleControllerKeepsExecutionRunningWhenStaleRuntimeCannotBeStopped(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "send-revoked-stop-fails", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	worker := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		stopErr: fmt.Errorf("provider could not stop session"),
	}
	controller, err := NewJobRoleController(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Start(context.Background(), "send-revoked-stop-fails"); err != nil {
		t.Fatal(err)
	}
	worker.onResume = func() {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			for i := range state.Graph.Jobs {
				if state.Graph.Jobs[i].ID == job.ID {
					state.Graph.Jobs[i].Contract.Revision++
					return nil
				}
			}
			return fmt.Errorf("Job %s disappeared", job.ID)
		}); err != nil {
			t.Errorf("revoke current Job approval: %v", err)
		}
	}
	if err := controller.Send(context.Background(), "send-revoked-stop-fails", "Continue the work."); err == nil {
		t.Fatal("Send must report failed revalidation and failed Runtime stop")
	}
	if worker.sendCount != 0 || worker.stopCount != 1 {
		t.Fatalf("send count=%d stop count=%d, want 0 and 1", worker.sendCount, worker.stopCount)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	execution, ok := state.Graph.RoleExecution("send-revoked-stop-fails")
	if !ok || execution.Status != core.RoleExecutionRunning {
		t.Fatalf("execution status = %+v, want RUNNING while provider stop is unconfirmed", execution)
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

func TestJobRoleControllerEnforcesApprovedResourceBoundary(t *testing.T) {
	cases := []struct {
		name       string
		repository string
		fileAccess *core.FileAccessPolicy
		wantStart  bool
	}{
		{
			name:       "repository outside Job",
			repository: "other/repo",
			fileAccess: &core.FileAccessPolicy{Level: core.FileAccessCommitted},
		},
		{
			name:       "file access wider than Task",
			repository: "owner/repo",
			fileAccess: &core.FileAccessPolicy{Level: core.FileAccessAll},
		},
		{
			name:       "file access narrower than Task",
			repository: "owner/repo",
			fileAccess: &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"README.md"}},
			wantStart:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, job, task, selection := jobRoleFixture(t, true)
			now := time.Now().UTC()
			const executionID = "job-boundary-execution"
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				return state.Graph.AppendRoleExecution(core.RoleExecution{
					ID: executionID, JobID: job.ID, TaskID: task.ID, Role: "CODER",
					Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
				})
			}); err != nil {
				t.Fatal(err)
			}
			worker := &jobRoleRuntimeFake{
				role: "CODER", configHash: "runtime-config-hash", selection: selection,
				repository: tc.repository, fileAccess: tc.fileAccess, events: make(chan runtime.Event),
			}
			controller, err := NewJobRoleController(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			_, err = controller.Start(context.Background(), executionID)
			if !tc.wantStart {
				if err == nil || worker.startCount != 0 {
					t.Fatalf("out-of-boundary Runtime started: starts=%d err=%v", worker.startCount, err)
				}
				return
			}
			if err != nil || worker.startCount != 1 {
				t.Fatalf("in-boundary Runtime did not start: starts=%d err=%v", worker.startCount, err)
			}
			worker.fileAccess = &core.FileAccessPolicy{Level: core.FileAccessAll}
			if err := controller.AuthorizeOperation(executionID, "repository.read", now.Add(time.Second)); err == nil {
				t.Fatal("operation preflight accepted a Runtime boundary widened after start")
			}
		})
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

func TestJobRolesExchangeMessagesAndResumeWithinOneJob(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		for _, execution := range []core.RoleExecution{
			{ID: "collab-coder", JobID: job.ID, TaskID: task.ID, Role: "CODER", Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
			{ID: "collab-reviewer", JobID: job.ID, TaskID: task.ID, Role: "REVIEWER", Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now},
		} {
			if err := state.Graph.AppendRoleExecution(execution); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	coderRuntime := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	reviewerRuntime := &jobRoleRuntimeFake{
		role: "REVIEWER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	coder, err := NewJobRoleController(db, coderRuntime)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := NewJobRoleController(db, reviewerRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coder.Start(context.Background(), "collab-coder"); err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.Start(context.Background(), "collab-reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := coder.PostMessage("collab-coder", "collab-reviewer", "collab-request",
		"Review the implementation against the Job acceptance criteria.", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := coder.Wait("collab-coder", "waiting for independent review", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := reviewer.Send(context.Background(), "collab-reviewer", "Review the pending implementation."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reviewerRuntime.prompt, "Review the implementation against the Job acceptance criteria.") {
		t.Fatal("Reviewer did not receive the addressed Job request")
	}
	if err := reviewer.PostMessage("collab-reviewer", "collab-coder", "collab-reply",
		"Review passed; retain the evidence reference and proceed.", "collab-request", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := reviewer.Wait("collab-reviewer", "waiting for Coder follow-up", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := coder.Resume(context.Background(), "collab-coder"); err != nil {
		t.Fatal(err)
	}
	if err := coder.Send(context.Background(), "collab-coder", "Apply the review response."); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(coderRuntime.prompt, "Review passed; retain the evidence reference and proceed.") {
		t.Fatal("Coder did not receive the Reviewer reply after resuming")
	}
	if err := reviewer.Resume(context.Background(), "collab-reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := reviewer.Finish("collab-reviewer", core.RoleExecutionSucceeded, "controller", "review complete", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := coder.Finish("collab-coder", core.RoleExecutionSucceeded, "controller", "implementation complete", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	messages := state.Graph.JobConversation(job.ID)
	if len(messages) != 2 || messages[0].TargetExecutionID != "collab-reviewer" ||
		messages[1].TargetExecutionID != "collab-coder" || messages[1].ReplyTo != "collab-request" {
		t.Fatalf("Job conversation did not preserve addressed request/reply provenance: %+v", messages)
	}
}

func TestJobRoleControlsRejectAnotherRuntimeIdentity(t *testing.T) {
	db, job, task, selection := jobRoleFixture(t, true)
	now := time.Now().UTC()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AppendRoleExecution(core.RoleExecution{
			ID: "identity-bound-exec", JobID: job.ID, TaskID: task.ID, Role: "CODER",
			Status: core.RoleExecutionCreated, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatal(err)
	}
	coderRuntime := &jobRoleRuntimeFake{
		role: "CODER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	coder, err := NewJobRoleController(db, coderRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coder.Start(context.Background(), "identity-bound-exec"); err != nil {
		t.Fatal(err)
	}
	reviewerRuntime := &jobRoleRuntimeFake{
		role: "REVIEWER", configHash: "runtime-config-hash", selection: selection,
		events: make(chan runtime.Event),
	}
	reviewer, err := NewJobRoleController(db, reviewerRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if err := reviewer.AuthorizeOperation("identity-bound-exec", "repository.write", time.Now().UTC()); err == nil {
		t.Fatal("another Role Runtime must not authorize this execution's operations")
	}
	if err := reviewer.Wait("identity-bound-exec", "wait from wrong Role", time.Now().UTC()); err == nil {
		t.Fatal("another Role Runtime must not change this execution to WAITING")
	}
	if err := reviewer.Finish("identity-bound-exec", core.RoleExecutionSucceeded, "reviewer", "wrong owner", time.Now().UTC()); err == nil {
		t.Fatal("another Role Runtime must not finish this execution")
	}
	if err := reviewer.Stop(context.Background(), "identity-bound-exec", "wrong owner"); err == nil {
		t.Fatal("another Role Runtime must not stop this execution")
	}
	if _, err := reviewer.Events(context.Background(), "identity-bound-exec"); err == nil {
		t.Fatal("another Role Runtime must not read this execution's events")
	}
	if coderRuntime.stopCount != 0 || reviewerRuntime.stopCount != 0 {
		t.Fatal("wrong Runtime identity caused a session stop")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	execution, ok := state.Graph.RoleExecution("identity-bound-exec")
	if !ok || execution.Status != core.RoleExecutionRunning {
		t.Fatalf("wrong Runtime changed execution state: %+v", execution)
	}
}
