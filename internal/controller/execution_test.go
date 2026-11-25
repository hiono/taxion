package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type fakeExecutionEffectAssessor struct {
	err        error
	assessment domain.ExecutionEffectAssessment
	calls      int
}

func (a *fakeExecutionEffectAssessor) Assess(
	_ context.Context,
	request domain.ExecutionPermissionRequest,
	requestSHA string,
) (domain.ExecutionEffectAssessment, error) {
	a.calls++
	if a.err != nil {
		return domain.ExecutionEffectAssessment{}, a.err
	}
	assessment := a.assessment
	assessment.Role = request.Role
	assessment.Effect = request.Effect
	assessment.Resource = request.Resource
	assessment.ProviderID = request.ProviderID
	assessment.AccountID = request.AccountID
	assessment.ConfigurationSHA256 = request.ConfigurationSHA256
	assessment.RequestSHA256 = requestSHA
	if assessment.ValidUntil.IsZero() {
		assessment.ValidUntil = request.ExpiresAt.Add(time.Minute)
	}
	if assessment.EvidenceSHA256 == "" {
		assessment.EvidenceSHA256 = strings.Repeat("e", 64)
	}
	return assessment, nil
}

func approvedExecutionControllerFixture(t *testing.T) (*store.Store, string, domain.JobContractRevision) {
	t.Helper()
	_, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	ctx := context.Background()
	jobContractController, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	executionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	_, revision, err := jobContractController.Propose(
		ctx, jobID, executionID, sourceResult.ID,
		"cmd_90909090909090909090909090909090", principal,
	)
	if err != nil {
		t.Fatalf("Propose JobContract: %v", err)
	}
	if _, _, err := jobContractController.Approve(
		ctx, jobID, "cmd_91919191919191919191919191919191",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve the bounded implementation", principal,
	); err != nil {
		t.Fatalf("Approve JobContract: %v", err)
	}
	return database, jobID, revision
}

func approvedExecutionPermissionControllerFixture(t *testing.T) (*store.Store, string, domain.JobContractRevision, domain.Task) {
	t.Helper()
	database, jobID, revision := approvedExecutionControllerFixture(t)
	taskController, err := NewTaskController(database)
	if err != nil {
		t.Fatalf("NewTaskController: %v", err)
	}
	_, task, err := taskController.Create(context.Background(), domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_81818181818181818181818181818181",
		Title: "Bounded execution task", Purpose: "Provide a task-scoped permission boundary",
		AcceptanceCriteria: []string{"The task-scoped operation is valid"},
	})
	if err != nil {
		t.Fatalf("create Task for ExecutionPermission: %v", err)
	}
	return database, jobID, revision, task
}

func testWorktreeSnapshot(jobID string, task domain.Task) domain.WorktreeSnapshot {
	temporaryRoot := os.TempDir()
	return domain.WorktreeSnapshot{
		RepositoryID: task.TargetRepositories[0], RepositoryPath: filepath.Join(temporaryRoot, "taxion-test-repository"),
		WorktreePath:   filepath.Join(temporaryRoot, "taxion-test-worktrees", domain.WorktreeID(jobID, task.ID)),
		BaselineCommit: strings.Repeat("a", 40),
	}
}

type fakeTaskWorktreeVerifier struct {
	err          error
	failErr      error
	failAt       int
	prepareCalls int
	verifyCalls  int
}

type fakeTaskWorktreeAdopter struct {
	*fakeTaskWorktreeVerifier
	snapshot     domain.WorktreeSnapshot
	expectedPath string
	adoptErr     error
	releaseErr   error
	adoptCalls   int
	releaseCalls int
}

func (a *fakeTaskWorktreeAdopter) Adopt(
	_ context.Context, _ string, _ domain.Task, path string,
) (domain.WorktreeSnapshot, error) {
	a.adoptCalls++
	if a.adoptErr != nil {
		return domain.WorktreeSnapshot{}, a.adoptErr
	}
	expectedPath := a.expectedPath
	if expectedPath == "" {
		expectedPath = a.snapshot.WorktreePath
	}
	if path != expectedPath {
		return domain.WorktreeSnapshot{}, errors.New("unexpected adopted Worktree path")
	}
	return a.snapshot, nil
}

func (a *fakeTaskWorktreeAdopter) ReleaseAdoptionReservation(
	_ context.Context, _ string, _ domain.Task, path string,
) error {
	a.releaseCalls++
	if a.releaseErr != nil {
		return a.releaseErr
	}
	if path != a.expectedPath {
		return errors.New("unexpected released Worktree path")
	}
	return nil
}

type failingTaskExecutionStore struct {
	ExecutionPermissionStore
	startErr error
}

func (s *failingTaskExecutionStore) StartTaskExecution(
	context.Context, uint64, string, string, string, string, string, domain.WorktreeSnapshot,
) (store.CommandReceipt, domain.TaskExecutionStart, error) {
	return store.CommandReceipt{}, domain.TaskExecutionStart{}, s.startErr
}

func (v *fakeTaskWorktreeVerifier) Prepare(_ context.Context, jobID string, task domain.Task) (domain.WorktreeSnapshot, error) {
	v.prepareCalls++
	if v.err != nil {
		return domain.WorktreeSnapshot{}, v.err
	}
	return testWorktreeSnapshot(jobID, task), nil
}

func (v *fakeTaskWorktreeVerifier) Verify(context.Context, string, domain.Task, domain.WorktreeSnapshot) error {
	v.verifyCalls++
	if v.failAt != 0 && v.verifyCalls == v.failAt {
		return v.failErr
	}
	return v.err
}

func TestExecutionControllerAuthorizesAndRevalidatesExactPermission(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	controller, err := NewExecutionControllerWithWorktree(database, assessor, &fakeTaskWorktreeVerifier{})
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	controller.now = func() time.Time { return now }
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_92929292929292929292929292929292",
		Role: "CODER", Effect: "repository.write",
		Resource:   revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	receipt, permission, err := controller.Authorize(context.Background(), request)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if receipt.Revision == 0 || permission.JobApprovalID == "" ||
		permission.ContractRevision != revision.Revision || permission.OperationID == "" ||
		assessor.calls != 1 {
		t.Fatalf("permission is not bound to the approved operation: receipt=%#v permission=%#v calls=%d",
			receipt, permission, assessor.calls)
	}
	validated, err := controller.ValidatePermission(
		context.Background(), permission.ID, request,
	)
	if err != nil {
		t.Fatalf("ValidatePermission: %v", err)
	}
	if validated.ID != permission.ID {
		t.Fatalf("validated permission = %q, want %q", validated.ID, permission.ID)
	}
	startReceipt, startedExecution, startIntent, err := controller.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a7070707070707070707070707070707", request,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	if startReceipt.Revision == 0 || startedExecution.TaskID != task.ID ||
		startedExecution.ExecutionPermissionID != permission.ID ||
		startIntent.ExecutionPermissionID != permission.ID ||
		startIntent.ID != permission.OperationID {
		t.Fatalf("Task execution start lost its permission binding: receipt=%#v execution=%#v intent=%#v",
			startReceipt, startedExecution, startIntent)
	}
	snapshot := testWorktreeSnapshot(jobID, task)
	if startedExecution.WorktreeID != domain.WorktreeID(jobID, task.ID) ||
		startIntent.WorktreeID != startedExecution.WorktreeID {
		t.Fatalf("Task execution start lost its Worktree binding: execution=%#v intent=%#v", startedExecution, startIntent)
	}
	jobWithWorktree, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Task execution start: %v", err)
	}
	if len(jobWithWorktree.WorktreeAssignments) != 1 {
		t.Fatalf("Worktree assignment count = %d, want 1", len(jobWithWorktree.WorktreeAssignments))
	}
	assignment := jobWithWorktree.WorktreeAssignments[0]
	if assignment.OwnerRoleExecutionID != startedExecution.ID ||
		assignment.ID != startedExecution.WorktreeID || assignment.Snapshot() != snapshot {
		t.Fatalf("Worktree assignment does not match its owner or snapshot: %#v", assignment)
	}
	replayedReceipt, replayedExecution, replayedIntent, err := controller.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a7070707070707070707070707070707", request,
	)
	if err != nil || replayedReceipt != startReceipt || replayedExecution != startedExecution ||
		replayedIntent != startIntent {
		t.Fatalf("exact Task execution replay changed persisted facts: receipt=%#v execution=%#v intent=%#v err=%v",
			replayedReceipt, replayedExecution, replayedIntent, err)
	}
	jobAfterStart, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Task execution start: %v", err)
	}
	revisionAfterStart := jobAfterStart.Revision
	if _, _, _, err := controller.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a8080808080808080808080808080808", request,
	); err == nil {
		t.Fatal("a second Start command consumed the same ExecutionPermission")
	}
	jobAfterSecondStart, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after rejected second Start: %v", err)
	}
	if jobAfterSecondStart.Revision != revisionAfterStart ||
		len(jobAfterSecondStart.RoleExecutions) != len(jobAfterStart.RoleExecutions) ||
		len(jobAfterSecondStart.ProviderStartIntents) != len(jobAfterStart.ProviderStartIntents) {
		t.Fatalf("rejected second Start mutated Job: %#v", jobAfterSecondStart)
	}
	changedResource := request
	changedResource.Resource = revision.Contract.Workspaces[0]
	if _, err := controller.ValidatePermission(context.Background(), permission.ID, changedResource); err == nil {
		t.Fatal("permission accepted a different resource under the original OperationID")
	}
	if _, err := controller.ValidatePermission(
		context.Background(), permission.ID, func() domain.ExecutionPermissionRequest {
			foreign := request
			foreign.CommandID = "cmd_f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0"
			return foreign
		}(),
	); err == nil {
		t.Fatal("permission was reused for another operation")
	}
	controller.now = func() time.Time { return request.ExpiresAt }
	if _, err := controller.ValidatePermission(
		context.Background(), permission.ID, request,
	); err == nil {
		t.Fatal("expired permission was accepted")
	}
}

func TestExecutionControllerRejectsReviewerBeforePreparingMutableWorktree(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	worktrees := &fakeTaskWorktreeVerifier{}
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: false, ValidUntil: now.Add(45 * time.Minute),
	}}
	controller, err := NewExecutionControllerWithWorktree(database, assessor, worktrees)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree: %v", err)
	}
	controller.now = func() time.Time { return now }
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_b7070707070707070707070707070707",
		Role: "REVIEWER", Effect: "repository.read",
		Resource:   revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := controller.Authorize(context.Background(), request)
	if err != nil {
		t.Fatalf("Authorize read-only Reviewer permission: %v", err)
	}
	before, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before rejected Reviewer start: %v", err)
	}
	if _, _, _, err := controller.StartTaskExecution(
		context.Background(), permission.ID, "cmd_b8080808080808080808080808080808", request,
	); err == nil {
		t.Fatal("Reviewer was started on a mutable Coder Worktree")
	}
	after, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after rejected Reviewer start: %v", err)
	}
	if worktrees.prepareCalls != 0 || worktrees.verifyCalls != 0 {
		t.Fatalf("rejected Reviewer start touched mutable Worktree: prepare=%d verify=%d",
			worktrees.prepareCalls, worktrees.verifyCalls)
	}
	if after.Revision != before.Revision || len(after.RoleExecutions) != len(before.RoleExecutions) ||
		len(after.ProviderStartIntents) != len(before.ProviderStartIntents) ||
		len(after.WorktreeAssignments) != len(before.WorktreeAssignments) ||
		len(after.ExecutionPermissions) != len(before.ExecutionPermissions) {
		t.Fatalf("rejected Reviewer start mutated Job: before=%#v after=%#v", before, after)
	}
}

func TestExecutionControllerRejectsScopeAndAssessmentFailuresWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.ExecutionPermissionRequest, *fakeExecutionEffectAssessor)
	}{
		{name: "foreign resource", mutate: func(r *domain.ExecutionPermissionRequest, _ *fakeExecutionEffectAssessor) { r.Resource += "/outside" }},
		{name: "prohibited effect", mutate: func(r *domain.ExecutionPermissionRequest, _ *fakeExecutionEffectAssessor) { r.Effect = "publish" }},
		{name: "assessor failure", mutate: func(_ *domain.ExecutionPermissionRequest, a *fakeExecutionEffectAssessor) {
			a.err = errors.New("assessment unavailable")
		}},
		{name: "uncontained effect", mutate: func(_ *domain.ExecutionPermissionRequest, a *fakeExecutionEffectAssessor) {
			a.assessment.Reversible = false
			a.assessment.Contained = false
		}},
		{name: "reviewer mutation", mutate: func(r *domain.ExecutionPermissionRequest, a *fakeExecutionEffectAssessor) {
			r.Role = "REVIEWER"
			a.assessment.Mutating = true
			a.assessment.Reversible = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
			now := time.Now().UTC().Round(0)
			assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
				Reversible: true, ValidUntil: now.Add(time.Hour),
			}}
			controller, err := NewExecutionController(database, assessor)
			if err != nil {
				t.Fatalf("NewExecutionController: %v", err)
			}
			controller.now = func() time.Time { return now }
			request := domain.ExecutionPermissionRequest{
				JobID: jobID, TaskID: task.ID, CommandID: "cmd_93939393939393939393939393939393",
				Role: "CODER", Effect: "repository.write",
				Resource:   revision.Contract.TargetRepositories[0],
				ProviderID: "provider.test", AccountID: "account.primary",
				ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
				ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
			}
			test.mutate(&request, assessor)
			if _, _, err := controller.Authorize(context.Background(), request); err == nil {
				t.Fatal("invalid ExecutionPermission request was accepted")
			}
			job, readErr := database.GetJob(context.Background(), jobID)
			if readErr != nil {
				t.Fatalf("GetJob after rejection: %v", readErr)
			}
			if len(job.ExecutionPermissions) != 0 {
				t.Fatalf("rejected request mutated Job: %#v", job.ExecutionPermissions)
			}
		})
	}
}

func TestExecutionControllerExactReplayAndCommandConflict(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, ValidUntil: now.Add(time.Hour),
	}}
	controller, err := NewExecutionController(database, assessor)
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	controller.now = func() time.Time { return now }
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_94949494949494949494949494949494",
		Role: "CODER", Effect: "repository.write",
		Resource:   revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, first, err := controller.Authorize(context.Background(), request)
	if err != nil {
		t.Fatalf("first Authorize: %v", err)
	}
	_, replay, err := controller.Authorize(context.Background(), request)
	if err != nil {
		t.Fatalf("exact Authorize replay: %v", err)
	}
	if first.ID != replay.ID || first.OperationID != replay.OperationID {
		t.Fatalf("exact replay changed permission: first=%#v replay=%#v", first, replay)
	}
	changed := request
	changed.Resource = revision.Contract.Workspaces[0]
	if _, _, err := controller.Authorize(context.Background(), changed); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed request under same CommandID error = %v, want idempotency conflict", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after replay: %v", err)
	}
	if len(job.ExecutionPermissions) != 1 {
		t.Fatalf("permission count = %d, want exactly one", len(job.ExecutionPermissions))
	}
}

type fakeJobOutputReplayStore struct {
	*store.Store
	job      domain.Job
	output   domain.JobOutput
	addCalls int
}

func (s *fakeJobOutputReplayStore) GetJob(_ context.Context, jobID string) (domain.Job, error) {
	if jobID != s.job.ID {
		return domain.Job{}, errors.New("unexpected Job ID")
	}
	return s.job, nil
}

func (s *fakeJobOutputReplayStore) AddJobOutput(
	_ context.Context, jobID string, expectedRevision uint64,
	taskID, roleExecutionID, turnID, commitSHA, treeSHA string,
) (store.CommandReceipt, domain.JobOutput, error) {
	s.addCalls++
	if jobID != s.job.ID || expectedRevision != s.job.Revision ||
		taskID != s.output.TaskID || roleExecutionID != s.output.RoleExecutionID ||
		turnID != s.output.TurnID || commitSHA != s.output.CommitSHA || treeSHA != s.output.TreeSHA {
		return store.CommandReceipt{}, domain.JobOutput{}, errors.New("JobOutput replay identity changed")
	}
	return store.CommandReceipt{CommandID: "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Revision: expectedRevision}, s.output, nil
}

func TestExecutionControllerReplaysExistingJobOutputWithoutRecapturingWorktree(t *testing.T) {
	_, database, _, jobID, _, _, _ := startContractPlanner(t)
	task := domain.Task{ID: "task_1111111111111111"}
	execution := domain.RoleExecution{ID: "role_exec_1111111111111111", TaskID: task.ID}
	output := domain.JobOutput{
		TaskID: task.ID, RoleExecutionID: execution.ID, TurnID: "turn_1111111111111111",
		CommitSHA: strings.Repeat("c", 40), TreeSHA: strings.Repeat("d", 40),
	}
	fakeStore := &fakeJobOutputReplayStore{
		Store: database,
		job: domain.Job{
			ID: jobID, Revision: 17, Tasks: []domain.Task{task},
			RoleExecutions: []domain.RoleExecution{execution},
			JobOutputs:     []domain.JobOutput{output},
		},
		output: output,
	}
	worktrees := &fakeTaskWorktreeVerifier{}
	controller, err := NewExecutionControllerWithWorktree(
		fakeStore, &fakeExecutionEffectAssessor{}, worktrees,
	)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree: %v", err)
	}
	receipt, replayed, err := controller.CaptureTaskOutput(
		context.Background(), jobID, execution.ID, output.TurnID,
	)
	if err != nil || replayed != output || receipt.Revision != fakeStore.job.Revision ||
		fakeStore.addCalls != 1 || worktrees.verifyCalls != 0 {
		t.Fatalf("existing JobOutput was recaptured instead of replayed: receipt=%#v output=%#v addCalls=%d verifyCalls=%d err=%v",
			receipt, replayed, fakeStore.addCalls, worktrees.verifyCalls, err)
	}
}

func TestExecutionControllerStartsCoderOnApprovedAdoptedWorktree(t *testing.T) {
	ctx := context.Background()
	_, database, principal, jobID, _, sourceResult, _ := startContractPlanner(t)
	worktreePath := filepath.Join(t.TempDir(), "taxion-approved-existing-worktree")
	repositoryPath := filepath.Join(t.TempDir(), "taxion-test-repository")
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before JobContract proposal: %v", err)
	}
	now := time.Now().UTC().Round(0)
	contract := domain.JobContract{
		Purpose:                 "Continue work in the approved existing Worktree",
		Constraints:             []string{"Do not discard existing changes"},
		TargetRepositories:      []string{"repo_taxion"},
		TargetResources:         []string{worktreePath},
		Workspaces:              []string{"workspace_taxion"},
		AcceptanceCriteria:      []string{"Existing changes remain available"},
		RequiredEvidence:        []string{"Linux tests"},
		Budget:                  domain.JobContractBudget{MaxTurns: 3, MaxTokens: 1000},
		ConcurrencyCeiling:      1,
		Deadline:                now.Add(time.Hour),
		ProhibitedEffects:       []string{"production deployment"},
		ApprovalRequiredEffects: []string{"publish release"},
		ConfigurationSHA256:     strings.Repeat("f", 64),
	}
	_, revision, err := database.AddJobContractRevision(
		ctx, jobID, job.Revision, "cmd_93939393939393939393939393939393",
		sourceResult.RoleExecutionID, sourceResult.ID, sourceResult.ContentSHA256, contract,
	)
	if err != nil {
		t.Fatalf("AddJobContractRevision: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before JobApproval: %v", err)
	}
	if _, _, err := database.AddJobApproval(
		ctx, jobID, job.Revision, "cmd_94949494949494949494949494949494",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve the exact existing Worktree resource", principal,
	); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	taskController, err := NewTaskController(database)
	if err != nil {
		t.Fatalf("NewTaskController: %v", err)
	}
	_, task, err := taskController.Create(ctx, domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_95959595959595959595959595959595",
		Title: "Adopt existing Worktree", Purpose: "Continue the approved working state",
		TargetRepositories: []string{"repo_taxion"}, TargetResources: []string{worktreePath},
		AcceptanceCriteria: []string{"Existing changes are preserved"},
	})
	if err != nil {
		t.Fatalf("Create Task: %v", err)
	}
	now = time.Now().UTC().Round(0)
	adopter := &fakeTaskWorktreeAdopter{
		fakeTaskWorktreeVerifier: &fakeTaskWorktreeVerifier{},
		snapshot: domain.WorktreeSnapshot{
			RepositoryID: "repo_taxion", RepositoryPath: repositoryPath,
			WorktreePath: worktreePath, BaselineCommit: strings.Repeat("a", 40),
			Origin: domain.WorktreeOriginAdopted, InitialStateSHA256: strings.Repeat("b", 64),
			HeadMode: domain.WorktreeHeadDetached,
		},
	}
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	controller, err := NewExecutionControllerWithWorktree(database, assessor, adopter)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree: %v", err)
	}
	controller.now = func() time.Time { return now }
	adopter.expectedPath = worktreePath
	request := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_96969696969696969696969696969696",
		Role: domain.RoleCoder, Effect: "repository.write", Resource: worktreePath,
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1,
		ReservedTokens:      100,
		ExpiresAt:           now.Add(30 * time.Minute),
		WorktreePath:        worktreePath,
	}
	_, permission, err := controller.Authorize(ctx, request)
	if err != nil {
		t.Fatalf("Authorize adopted Worktree: %v", err)
	}
	if permission.WorktreePath != worktreePath {
		t.Fatalf("ExecutionPermission WorktreePath = %q, want %q", permission.WorktreePath, worktreePath)
	}
	startCommand := "cmd_97979797979797979797979797979797"
	beforeRejectedStart, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before mismatched Worktree Start: %v", err)
	}
	adopter.snapshot.WorktreePath = worktreePath + "-mismatch"
	if _, _, _, err := controller.StartTaskExecution(ctx, permission.ID, startCommand, request); err == nil {
		t.Fatal("Task execution accepted a Worktree snapshot outside the approved ExecutionPermission path")
	}
	afterRejectedStart, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after mismatched Worktree Start: %v", err)
	}
	if afterRejectedStart.Revision != beforeRejectedStart.Revision ||
		len(afterRejectedStart.RoleExecutions) != len(beforeRejectedStart.RoleExecutions) ||
		len(afterRejectedStart.WorktreeAssignments) != len(beforeRejectedStart.WorktreeAssignments) {
		t.Fatal("mismatched Worktree snapshot mutated Job state")
	}
	adopter.snapshot.WorktreePath = worktreePath
	failingController, err := NewExecutionControllerWithWorktree(
		&failingTaskExecutionStore{ExecutionPermissionStore: database, startErr: errors.New("forced Task Start persistence failure")},
		assessor, adopter,
	)
	if err != nil {
		t.Fatalf("NewExecutionControllerWithWorktree for failed Start: %v", err)
	}
	failingController.now = func() time.Time { return now }
	if _, _, _, err := failingController.StartTaskExecution(ctx, permission.ID, startCommand, request); err == nil {
		t.Fatal("Task Start succeeded despite forced persistence failure")
	}
	if adopter.releaseCalls != 1 || adopter.adoptCalls != 2 {
		t.Fatalf("uncommitted adopted Worktree reservation was not released: adopt=%d release=%d",
			adopter.adoptCalls, adopter.releaseCalls)
	}
	receipt, execution, intent, err := controller.StartTaskExecution(ctx, permission.ID, startCommand, request)
	if err != nil {
		t.Fatalf("StartTaskExecution with adopted Worktree: %v", err)
	}
	if receipt.Revision == 0 || execution.TaskID != task.ID || intent.WorktreeID != execution.WorktreeID ||
		adopter.adoptCalls != 3 || adopter.releaseCalls != 1 || adopter.verifyCalls != 0 || adopter.prepareCalls != 0 {
		t.Fatalf("adopted Worktree was not used for the exact Task start: receipt=%#v execution=%#v intent=%#v adopter=%#v",
			receipt, execution, intent, adopter)
	}
	persisted, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after adopted Worktree start: %v", err)
	}
	if len(persisted.WorktreeAssignments) != 1 {
		t.Fatalf("Worktree assignment count = %d, want 1", len(persisted.WorktreeAssignments))
	}
	assignment := persisted.WorktreeAssignments[0]
	if assignment.Origin != domain.WorktreeOriginAdopted ||
		assignment.InitialStateSHA256 != adopter.snapshot.InitialStateSHA256 ||
		assignment.HeadMode != adopter.snapshot.HeadMode ||
		assignment.WorktreePath != worktreePath ||
		assignment.OwnerRoleExecutionID != execution.ID {
		t.Fatalf("JobAggregate did not persist adopted Worktree identity: %#v", assignment)
	}
	invalidBaseline := persisted
	invalidBaseline.WorktreeAssignments = append([]domain.WorktreeAssignment(nil), persisted.WorktreeAssignments...)
	invalidBaseline.WorktreeAssignments[0].BaselineCommit = "not-a-git-commit"
	if err := invalidBaseline.Validate(); err == nil {
		t.Fatal("Job accepted a Worktree assignment with an invalid BaselineCommit")
	}
	if _, _, _, err := controller.StartTaskExecution(ctx, permission.ID, startCommand, request); err != nil {
		t.Fatalf("exact adopted Worktree Start replay: %v", err)
	}
	if adopter.adoptCalls != 3 || adopter.releaseCalls != 1 || adopter.verifyCalls != 1 {
		t.Fatalf("exact replay re-adopted Worktree: adopt=%d release=%d verify=%d",
			adopter.adoptCalls, adopter.releaseCalls, adopter.verifyCalls)
	}
}
