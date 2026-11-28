package controller

import (
	"context"
	"errors"
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

func TestExecutionControllerAuthorizesAndRevalidatesExactPermission(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	controller, err := NewExecutionController(database, assessor)
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
		ExpiresAt:           now.Add(30 * time.Minute),
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
				ExpiresAt:           now.Add(30 * time.Minute),
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
		ExpiresAt:           now.Add(30 * time.Minute),
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
	changed.Effect = "repository.read"
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
