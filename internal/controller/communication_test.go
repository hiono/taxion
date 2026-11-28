package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type conflictReplayCommunicationStore struct {
	job     domain.Job
	message domain.JobMessage
	calls   int
}

func (s *conflictReplayCommunicationStore) GetJob(context.Context, string) (domain.Job, error) {
	return s.job, nil
}

func (s *conflictReplayCommunicationStore) AddJobMessage(_ context.Context, jobID string, expectedRevision uint64, commandID string, _ domain.JobMessageRequest) (store.CommandReceipt, domain.JobMessage, error) {
	s.calls++
	if s.calls == 1 {
		s.job.Revision++
		s.message = domain.JobMessage{CommandID: commandID}
		s.job.JobMessages = append(s.job.JobMessages, s.message)
		return store.CommandReceipt{}, domain.JobMessage{}, store.ErrIdempotencyConflict
	}
	if expectedRevision != s.job.Revision {
		return store.CommandReceipt{}, domain.JobMessage{}, store.ErrIdempotencyConflict
	}
	return store.CommandReceipt{CommandID: commandID, JobID: jobID, Revision: s.job.Revision}, s.message, nil
}

func TestPersistMessageReplaysConcurrentIdenticalCommandAfterRevisionConflict(t *testing.T) {
	const commandID = "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fake := &conflictReplayCommunicationStore{job: domain.Job{ID: "job_message_replay", Revision: 1}}
	controller, err := NewCommunicationController(fake)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	outcome, err := controller.persistMessage(context.Background(), fake.job.ID, commandID, domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: "role_coder"},
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: "role_planner"},
		Body:      "retry the same command",
	})
	if err != nil {
		t.Fatalf("persistMessage() error = %v", err)
	}
	if fake.calls != 2 || outcome.Message.CommandID != commandID || outcome.MessageReceipt.CommandID != commandID {
		t.Fatalf("replayed result = %#v, calls=%d", outcome, fake.calls)
	}
}

func TestCommunicationControllerPersistsCoderPlannerQuestionAndAnswer(t *testing.T) {
	database, jobID, revision, task := approvedExecutionPermissionControllerFixture(t)
	runtime := &fakePlanningProviderRuntime{identity: provider.Identity{
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		SDKVersion:          "test-sdk/1",
	}}
	now := time.Now().UTC().Round(0)
	runtime.capabilities = provider.Capabilities{
		Identity: runtime.identity, StartIdempotency: true,
		Evidence: []provider.CapabilityEvidence{{
			Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity,
			TestID: "test_start_idempotency", EvidenceSHA256: strings.Repeat("a", 64),
			EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
		}},
	}
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
		Role: domain.RoleCoder, Effect: "repository.write",
		Resource:   revision.Contract.TargetRepositories[0],
		ProviderID: runtime.identity.ProviderID, AccountID: runtime.identity.AccountID,
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	_, permission, err := executionController.Authorize(context.Background(), permissionRequest)
	if err != nil {
		t.Fatalf("Authorize Coder execution: %v", err)
	}
	_, coderExecution, _, err := executionController.StartTaskExecution(
		context.Background(), permission.ID, "cmd_a7070707070707070707070707070707", permissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution Coder: %v", err)
	}
	coderExecution, _ = bindProviderTestWorktree(
		t, database, executionController, jobID, "cmd_c3333333333333333333333333333333",
		revision.Contract.TargetRepositories[0], coderExecution,
	)

	providerController, err := NewProviderControllerWithExecution(database, runtime, executionController)
	if err != nil {
		t.Fatalf("NewProviderControllerWithExecution: %v", err)
	}
	runtime.startResult = provider.StartResult{
		OperationID: permission.OperationID, SessionID: "session_coder",
		ReceiptID: "receipt_coder", EvidenceSHA256: strings.Repeat("f", 64),
	}
	coderStart, _, err := providerController.DispatchTaskExecution(
		context.Background(), jobID, coderExecution.ID, permission.ID, permissionRequest,
	)
	if err != nil {
		t.Fatalf("DispatchTaskExecution Coder: %v", err)
	}

	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before communication: %v", err)
	}
	var plannerExecution domain.RoleExecution
	for _, execution := range job.RoleExecutions {
		if execution.Role == domain.RolePlanner {
			plannerExecution = execution
			break
		}
	}
	if plannerExecution.ID == "" {
		t.Fatal("Planner RoleExecution was not found")
	}
	var plannerSession string
	for _, observation := range job.ProviderStartObservations {
		if observation.RoleExecutionID == plannerExecution.ID &&
			observation.Status == domain.ProviderStartAccepted {
			plannerSession = observation.ProviderSessionID
		}
	}
	if plannerSession == "" {
		t.Fatal("accepted Planner Provider Session was not found")
	}

	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	question, err := communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: coderExecution.ID, ProviderSessionID: coderStart.ProviderSessionID,
		CommandID: "cmd_44444444444444444444444444444444",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: plannerExecution.ID},
		Body:      "この制約の優先順位を確認してください。\\n元のTaskとContextは維持してください。",
	})
	if err != nil {
		t.Fatalf("SendFromRole Coder QUESTION: %v", err)
	}
	if question.Message.Sender.Kind != domain.ActorCoder ||
		question.Message.Sender.ID != coderExecution.ID ||
		question.Message.Recipient.ID != plannerExecution.ID {
		t.Fatalf("Coder QUESTION identity was not preserved: %#v", question.Message)
	}

	answer, err := communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: plannerExecution.ID, ProviderSessionID: plannerSession,
		CommandID:        "cmd_55555555555555555555555555555555",
		Recipient:        domain.ActorRef{Kind: domain.ActorCoder, ID: coderExecution.ID},
		ReplyToMessageID: question.Message.ID,
		Body:             "優先順位は安全性、次に完遂性です。",
	})
	if err != nil {
		t.Fatalf("SendFromRole Planner ANSWER: %v", err)
	}
	if answer.Message.ReplyToMessageID != question.Message.ID ||
		answer.Message.Sender.ID != plannerExecution.ID ||
		answer.Message.Recipient.ID != coderExecution.ID {
		t.Fatalf("Planner ANSWER lost correlation or identity: %#v", answer.Message)
	}

	plannerInbox, err := communication.InboxForRole(
		context.Background(), jobID, plannerExecution.ID,
	)
	if err != nil || len(plannerInbox) != 1 || plannerInbox[0].ID != question.Message.ID {
		t.Fatalf("Planner inbox = %#v, %v; want the Coder question", plannerInbox, err)
	}
	coderInbox, err := communication.InboxForRole(
		context.Background(), jobID, coderExecution.ID,
	)
	if err != nil || len(coderInbox) != 1 || coderInbox[0].ID != answer.Message.ID {
		t.Fatalf("Coder inbox = %#v, %v; want the Planner answer", coderInbox, err)
	}
	persisted, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Coder / Planner communication: %v", err)
	}
	if len(persisted.JobMessages) != 2 {
		t.Fatalf("persisted communication messages=%d", len(persisted.JobMessages))
	}
}

func TestCommunicationControllerRejectsUnboundRoleSession(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := providerController.StartPlanning(
		context.Background(), jobID, grantID, "cmd_66666666666666666666666666666666", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	_, err = communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: execution.ID, ProviderSessionID: "session_forged",
		CommandID: "cmd_77777777777777777777777777777777",
		Recipient: domain.ActorRef{Kind: domain.ActorHuman, ID: principal.PrincipalID()},
		Body:      "must be rejected",
	})
	if err == nil {
		t.Fatal("CommunicationController accepted an unbound Provider Session")
	}
}

func TestCommunicationControllerSupportsControllerAndOrchestratorActors(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, planner, _, _, err := providerController.StartPlanning(
		context.Background(), jobID, grantID, "cmd_88888888888888888888888888888888", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	controllerMessage, err := communication.SendFromController(context.Background(), SystemMessageRequest{
		JobID: jobID, CommandID: "cmd_89898989898989898989898989898989",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: planner.ID},
		Body:      "Plannerの実行結果を確認してください。",
	})
	if err != nil {
		t.Fatalf("SendFromController: %v", err)
	}
	orchestratorMessage, err := communication.SendFromOrchestrator(context.Background(), SystemMessageRequest{
		JobID: jobID, CommandID: "cmd_90909090909090909090909090909090",
		Recipient: domain.ActorRef{Kind: domain.ActorHuman, ID: principal.PrincipalID()},
		Body:      "Plannerの確認が完了しました。",
	})
	if err != nil {
		t.Fatalf("SendFromOrchestrator: %v", err)
	}
	if controllerMessage.Message.Sender != (domain.ActorRef{Kind: domain.ActorController, ID: domain.ControllerActorID}) {
		t.Fatalf("Controller identity was not fixed by Taxion: %#v", controllerMessage.Message.Sender)
	}
	if orchestratorMessage.Message.Sender != (domain.ActorRef{Kind: domain.ActorOrchestrator, ID: domain.OrchestratorActorID}) {
		t.Fatalf("Orchestrator identity was not fixed by Taxion: %#v", orchestratorMessage.Message.Sender)
	}
	persisted, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after system messages: %v", err)
	}
	if len(persisted.JobMessages) != 2 {
		t.Fatalf("persisted system messages=%d, want 2", len(persisted.JobMessages))
	}
}
