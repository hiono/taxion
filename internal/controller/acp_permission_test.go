package controller

import (
	"context"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
)

type fakeACPToolCallEvaluator struct {
	effect string
	resource string
	err error
}

func (e *fakeACPToolCallEvaluator) Evaluate(_ context.Context, _ provider.ACPToolPermissionRequest) (string, string, error) {
	return e.effect, e.resource, e.err
}

func TestACPExecutionPermissionAuthorizerRevalidatesExactTaskAuthority(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	now := time.Now().UTC().Round(0)
	assessor := &fakeExecutionEffectAssessor{assessment: domain.ExecutionEffectAssessment{
		Reversible: true, Mutating: true, ValidUntil: now.Add(45 * time.Minute),
	}}
	executionController, err := NewExecutionController(database, assessor)
	if err != nil { t.Fatalf("NewExecutionController: %v", err) }
	executionController.now = func() time.Time { return now }
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_92929292929292929292929292929292",
		Role: "CODER", Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns: 1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(context.Background(), permissionRequest)
	if err != nil { t.Fatalf("Authorize: %v", err) }
	_, execution, intent, err := executionController.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a7070707070707070707070707070707", permissionRequest,
	)
	if err != nil { t.Fatalf("StartTaskExecution: %v", err) }
	evaluator := &fakeACPToolCallEvaluator{effect: permission.Effect, resource: permission.Resource}
	authorizer, err := NewACPExecutionPermissionAuthorizer(database, evaluator)
	if err != nil { t.Fatalf("NewACPExecutionPermissionAuthorizer: %v", err) }
	authorizer.now = func() time.Time { return now }
	request := provider.ACPToolPermissionRequest{
		OperationID: intent.ID, JobID: jobID, RoleExecutionID: execution.ID,
		TaskID: task.ID, ExecutionPermissionID: permission.ID, SessionID: "session_1",
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
	evaluator.resource += "/outside"
	if err := authorizer.Authorize(context.Background(), request); err == nil {
		t.Fatal("ACP tool call outside the exact approved resource was accepted")
	}
	evaluator.resource = permission.Resource
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
	if err != nil { t.Fatalf("NewExecutionController: %v", err) }
	executionController.now = func() time.Time { return now }
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_93939393939393939393939393939393",
		Role: "CODER", Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns: 1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(context.Background(), permissionRequest)
	if err != nil { t.Fatalf("Authorize: %v", err) }
	_, execution, intent, err := executionController.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a8080808080808080808080808080808", permissionRequest,
	)
	if err != nil { t.Fatalf("StartTaskExecution: %v", err) }
	authorizer, err := NewACPExecutionPermissionAuthorizer(database, &fakeACPToolCallEvaluator{
		effect: permission.Effect, resource: permission.Resource,
	})
	if err != nil { t.Fatalf("NewACPExecutionPermissionAuthorizer: %v", err) }
	authorizer.now = func() time.Time { return now }
	request := provider.ACPToolPermissionRequest{
		OperationID: intent.ID, JobID: jobID, RoleExecutionID: execution.ID,
		TaskID: task.ID, ExecutionPermissionID: permission.ID, SessionID: "session_2",
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
