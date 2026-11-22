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
	_, database, principal, jobID, _, sourceResult, contract := startContractPlanner(t)
	ctx := context.Background()
	jobContractController, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	executionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	_, revision, err := jobContractController.Propose(
		ctx, jobID, executionID, sourceResult.ID,
		"cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", principal,
	)
	if err != nil {
		t.Fatalf("Propose JobContract: %v", err)
	}
	if _, _, err := jobContractController.Approve(
		ctx, jobID, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve the bounded implementation", principal,
	); err != nil {
		t.Fatalf("Approve JobContract: %v", err)
	}
	return database, jobID, revision
}

func TestExecutionControllerAuthorizesAndRevalidatesExactPermission(t *testing.T) {
	database, jobID, revision := approvedExecutionControllerFixture(t)
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
		JobID: jobID, CommandID: "cmd_cccccccccccccccccccccccccccccccc",
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
		context.Background(), jobID, permission.ID, permission.OperationID, permission.RequestSHA256,
	)
	if err != nil {
		t.Fatalf("ValidatePermission: %v", err)
	}
	if validated.ID != permission.ID {
		t.Fatalf("validated permission = %q, want %q", validated.ID, permission.ID)
	}
	if _, err := controller.ValidatePermission(
		context.Background(), jobID, permission.ID, "provider_op_foreign", permission.RequestSHA256,
	); err == nil {
		t.Fatal("permission was reused for another operation")
	}
	controller.now = func() time.Time { return request.ExpiresAt }
	if _, err := controller.ValidatePermission(
		context.Background(), jobID, permission.ID, permission.OperationID, permission.RequestSHA256,
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
			database, jobID, revision := approvedExecutionControllerFixture(t)
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
				JobID: jobID, CommandID: "cmd_dddddddddddddddddddddddddddddddd",
				Role: "CODER", Effect: "repository.write",
				Resource:   revision.Contract.TargetRepositories[0],
				ProviderID: "provider.test", AccountID: "account.primary",
				ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
				ReservedTokens:      100, ExpiresAt: now.Add(30 * time.Minute),
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
	database, jobID, revision := approvedExecutionControllerFixture(t)
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
		JobID: jobID, CommandID: "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Role: "CODER", Effect: "repository.write",
		Resource:   revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTokens:      100, ExpiresAt: now.Add(30 * time.Minute),
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
