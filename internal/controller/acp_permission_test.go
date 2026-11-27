package controller

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

func bindACPTestWorktree(
	t *testing.T,
	database *store.Store,
	controller *ExecutionController,
	jobID, commandID, repositoryID string,
	execution domain.RoleExecution,
) (domain.RoleExecution, domain.ProviderStartIntent, domain.WorktreeAssignment) {
	t.Helper()
	assignment := domain.WorktreeAssignment{
		ID:    domain.WorktreeAssignmentID(jobID, execution.TaskID, repositoryID),
		JobID: jobID, TaskID: execution.TaskID, RepositoryID: repositoryID,
		RepositoryPath: t.TempDir(), WorktreePath: t.TempDir(),
		BaselineCommit: strings.Repeat("a", 40), OwnerRoleExecutionID: execution.ID,
	}
	if _, _, err := controller.BindWorktree(context.Background(), execution.ID, commandID, assignment); err != nil {
		t.Fatalf("BindWorktree: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Worktree binding: %v", err)
	}
	var updatedExecution domain.RoleExecution
	var intent domain.ProviderStartIntent
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == execution.ID {
			updatedExecution = candidate
			break
		}
	}
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			intent = candidate
			break
		}
	}
	for _, candidate := range job.WorktreeAssignments {
		if candidate.ID == assignment.ID {
			assignment = candidate
			break
		}
	}
	return updatedExecution, intent, assignment
}

type fakeACPToolCallEvaluator struct {
	effect   string
	resource string
	err      error
}

func (e *fakeACPToolCallEvaluator) Evaluate(_ context.Context, _ provider.ACPToolPermissionRequest) (string, string, error) {
	return e.effect, e.resource, e.err
}

type revokedACPExecutionPermissionStore struct {
	ProviderExecutionStore
	revokedAt time.Time
	reads     int
}

func (s *revokedACPExecutionPermissionStore) GetJob(ctx context.Context, jobID string) (domain.Job, error) {
	job, err := s.ProviderExecutionStore.GetJob(ctx, jobID)
	if err != nil {
		return domain.Job{}, err
	}
	s.reads++
	if s.reads > 1 {
		for i := range job.ExecutionPermissions {
			job.ExecutionPermissions[i].ExpiresAt = s.revokedAt
		}
	}
	return job, nil
}

func TestACPExecutionPermissionAuthorizerRevalidatesExactTaskAuthority(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionController(database, assessor)
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	executionController.now = func() time.Time { return now }
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_92929292929292929292929292929292",
		Role: "CODER", Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1,
		ReservedTokens:      100,
		ExpiresAt:           now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(context.Background(), permissionRequest)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, execution, intent, err := executionController.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a7070707070707070707070707070707", permissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}

	unboundJob, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Worktree binding: %v", err)
	}
	worktreeCandidate := domain.WorktreeAssignment{
		ID:    domain.WorktreeAssignmentID(jobID, task.ID, revision.Contract.TargetRepositories[0]),
		JobID: jobID, TaskID: task.ID, RepositoryID: revision.Contract.TargetRepositories[0],
		RepositoryPath: t.TempDir(), WorktreePath: t.TempDir(),
		BaselineCommit: strings.Repeat("a", 40), OwnerRoleExecutionID: execution.ID,
	}
	observationAt := time.Now().UTC().Round(0)
	startAttempt, err := domain.NewProviderStartObservation(
		unboundJob, intent.ID, "", domain.ProviderStartObservationCommandID(intent.ID, domain.ProviderStartUnknown, "", "", "", false),
		domain.ProviderStartUnknown, "", "", "", false, observationAt,
	)
	if err != nil {
		t.Fatalf("NewProviderStartObservation for Worktree binding guard: %v", err)
	}
	attemptedJob := unboundJob
	attemptedJob.ProviderStartObservations = append(
		append([]domain.ProviderStartObservation(nil), unboundJob.ProviderStartObservations...), startAttempt,
	)
	attemptedJob.Revision = startAttempt.CreatedRevision
	attemptedJob.UpdatedAt = observationAt
	if _, err := domain.NewWorktreeAssignmentBinding(attemptedJob, execution.ID, worktreeCandidate, observationAt); err == nil {
		t.Fatal("NewWorktreeAssignmentBinding accepted retroactive binding after an UNKNOWN Provider Start")
	}
	invalidPath := filepath.Join(t.TempDir(), string([]byte{0xff}))
	for _, field := range []string{"repository", "worktree"} {
		candidate := domain.WorktreeAssignment{
			ID:    domain.WorktreeAssignmentID(jobID, task.ID, revision.Contract.TargetRepositories[0]),
			JobID: jobID, TaskID: task.ID, RepositoryID: revision.Contract.TargetRepositories[0],
			RepositoryPath: t.TempDir(), WorktreePath: t.TempDir(),
			BaselineCommit: strings.Repeat("a", 40), OwnerRoleExecutionID: execution.ID,
		}
		if field == "repository" {
			candidate.RepositoryPath = invalidPath
		} else {
			candidate.WorktreePath = invalidPath
		}
		if _, err := domain.NewWorktreeAssignmentBinding(unboundJob, execution.ID, candidate, now); err == nil {
			t.Fatalf("NewWorktreeAssignmentBinding accepted invalid UTF-8 %s path", field)
		}
	}

	execution, intent, assignment := bindACPTestWorktree(
		t, database, executionController, jobID,
		"cmd_b1111111111111111111111111111111",
		revision.Contract.TargetRepositories[0], execution,
	)
	persistedJob, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Worktree binding: %v", err)
	}
	for _, field := range []string{"repository", "worktree"} {
		candidate := assignment
		if field == "repository" {
			candidate.RepositoryPath = invalidPath
		} else {
			candidate.WorktreePath = invalidPath
		}
		if err := candidate.Validate(persistedJob); err == nil {
			t.Fatalf("WorktreeAssignment.Validate accepted invalid UTF-8 %s path", field)
		}
	}
	evaluator := &fakeACPToolCallEvaluator{effect: permission.Effect, resource: permission.Resource}
	authorizer, err := NewACPExecutionPermissionAuthorizer(database, evaluator)
	if err != nil {
		t.Fatalf("NewACPExecutionPermissionAuthorizer: %v", err)
	}
	authorizer.now = func() time.Time { return now }
	request := provider.ACPToolPermissionRequest{
		OperationID: intent.ID, JobID: jobID, RoleExecutionID: execution.ID,
		TaskID: task.ID, ExecutionPermissionID: permission.ID,
		WorktreeAssignmentID: assignment.ID, WorktreePath: assignment.WorktreePath, SessionID: "session_1",
		RequestSHA256: permission.RequestSHA256,
		ProviderIdentity: provider.Identity{
			ProviderID: permission.ProviderID, AccountID: permission.AccountID,
			SDKVersion: "copilot_cli_acp", ConfigurationSHA256: permission.ConfigurationSHA256,
		},
		ToolCall: []byte(`{"kind":"execute","rawInput":{"command":"git status"}}`),
	}
	if err := authorizer.Authorize(context.Background(), request); err != nil {
		t.Fatalf("Authorize exact Task tool call: %v", err)
	}

	// The evaluator can block while the permission is revoked. The final check
	// must use a fresh Job snapshot rather than the pre-evaluation snapshot.
	revokedStore := &revokedACPExecutionPermissionStore{
		ProviderExecutionStore: database,
		revokedAt:              now,
	}
	revokingEvaluator := &fakeACPToolCallEvaluator{
		effect: permission.Effect, resource: permission.Resource,
	}
	revokingAuthorizer, err := NewACPExecutionPermissionAuthorizer(revokedStore, revokingEvaluator)
	if err != nil {
		t.Fatalf("NewACPExecutionPermissionAuthorizer with revocation store: %v", err)
	}
	revokingAuthorizer.now = func() time.Time { return now }
	if err := revokingAuthorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("ACP tool call was accepted after the permission changed during evaluation")
	}

	evaluator.resource += "/outside"
	if err := authorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("ACP tool call outside the exact approved resource was accepted")
	}
	evaluator.resource = permission.Resource
	nowCalls := 0
	authorizer.now = func() time.Time {
		nowCalls++
		if nowCalls == 1 {
			return now
		}
		return permission.ExpiresAt
	}
	if err := authorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("ACP tool permission was accepted after evaluator returned beyond expiry")
	}
	authorizer.now = func() time.Time { return permission.ExpiresAt }
	if err := authorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("expired ExecutionPermission was accepted")
	}
}

func TestACPExecutionPermissionAuthorizerRejectsMismatchedProviderAndRole(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(time.Hour),
	}}
	executionController, err := NewExecutionController(database, assessor)
	if err != nil {
		t.Fatalf("NewExecutionController: %v", err)
	}
	executionController.now = func() time.Time { return now }
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_93939393939393939393939393939393",
		Role: "CODER", Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1,
		ReservedTokens:      100,
		ExpiresAt:           now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(context.Background(), permissionRequest)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	_, execution, intent, err := executionController.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a8080808080808080808080808080808", permissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	execution, intent, assignment := bindACPTestWorktree(
		t, database, executionController, jobID,
		"cmd_b2222222222222222222222222222222",
		revision.Contract.TargetRepositories[0], execution,
	)
	authorizer, err := NewACPExecutionPermissionAuthorizer(database, &fakeACPToolCallEvaluator{
		effect: permission.Effect, resource: permission.Resource,
	})
	if err != nil {
		t.Fatalf("NewACPExecutionPermissionAuthorizer: %v", err)
	}
	authorizer.now = func() time.Time { return now }
	request := provider.ACPToolPermissionRequest{
		OperationID: intent.ID, JobID: jobID, RoleExecutionID: execution.ID,
		TaskID: task.ID, ExecutionPermissionID: permission.ID,
		WorktreeAssignmentID: assignment.ID, WorktreePath: assignment.WorktreePath, SessionID: "session_2",
		RequestSHA256: permission.RequestSHA256,
		ProviderIdentity: provider.Identity{
			ProviderID: "foreign.provider", AccountID: permission.AccountID,
			SDKVersion: "copilot_cli_acp", ConfigurationSHA256: permission.ConfigurationSHA256,
		},
		ToolCall: []byte("tool-call"),
	}
	if err := authorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("foreign Provider identity was accepted")
	}
	request.ProviderIdentity.ProviderID = permission.ProviderID
	request.RoleExecutionID = "role_execution_foreign"
	if err := authorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("foreign RoleExecution was accepted")
	}
}
