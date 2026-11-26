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
		SendIdempotency: true, ReadOnlyContextEnforcement: true,
		Evidence: []provider.CapabilityEvidence{
			{
				Capability: provider.CapabilityStartIdempotency, Identity: runtime.identity,
				TestID: "test_start_idempotency", EvidenceSHA256: strings.Repeat("a", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
			},
			{
				Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity,
				TestID: "test_send_idempotency", EvidenceSHA256: strings.Repeat("b", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
			},
			{
				Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: runtime.identity,
				TestID: "test_read_only_context", EvidenceSHA256: strings.Repeat("c", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
			},
		},
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
	questionAuthorization, err := NewJobQuestionAuthorizationController(database, fixedQuestionTurnContextResolver{
		context: QuestionReadOnlyContext{
			RepositoryID: revision.Contract.TargetRepositories[0],
			Boundary: domain.PlanningReadOnlyBoundary{
				Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "communication_question_snapshot",
				SHA256: sha256String("immutable communication question snapshot"),
			},
		},
	})
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	questionTurn, err := newJobQuestionTurnController(database, runtime, provider.EvidenceSynthetic)
	if err != nil {
		t.Fatalf("NewJobQuestionTurnController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		database, communication, questionAuthorization, questionTurn,
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	route, err := orchestrator.SendRoleMessage(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: coderExecution.ID, ProviderSessionID: coderStart.ProviderSessionID,
		CommandID: "cmd_44444444444444444444444444444444",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: plannerExecution.ID},
		Body:      "この制約の優先順位を確認してください。\\n元のTaskとContextは維持してください。",
	})
	if err != nil {
		t.Fatalf("SendRoleMessage Coder QUESTION: %v", err)
	}
	question := route.Message
	if len(route.QuestionTurns) != 1 || len(route.ResumedContexts) != 1 {
		t.Fatalf("automatic Planner route did not return one turn and one resumed context: %#v", route)
	}
	answer := route.QuestionTurns[0].Reply
	resumedCoder := route.ResumedContexts[0]
	if resumedCoder.RoleExecution.ID != coderExecution.ID ||
		resumedCoder.ProviderSessionID != coderStart.ProviderSessionID ||
		len(resumedCoder.Messages) != 2 ||
		resumedCoder.Messages[0].ID != question.Message.ID ||
		resumedCoder.Messages[1].ID != answer.Message.ID {
		t.Fatalf("Planner answer did not restore the original Coder RoleExecution / Session / conversation: %#v", resumedCoder)
	}
	if question.Message.Sender.Kind != domain.ActorCoder ||
		question.Message.Sender.ID != coderExecution.ID ||
		question.Message.Recipient.ID != plannerExecution.ID {
		t.Fatalf("Coder QUESTION identity was not preserved: %#v", question.Message)
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

	// Reviewer feedback, Coder correction, and re-review return to each original accepted Session.
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Reviewer permission: %v", err)
	}
	reviewerPermissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: task.ID, CommandID: "cmd_66666666666666666666666666666666",
		Role: domain.RoleReviewer, Effect: "repository.read",
		Resource:   revision.Contract.TargetRepositories[0],
		ProviderID: runtime.identity.ProviderID, AccountID: runtime.identity.AccountID,
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(30 * time.Minute),
	}
	reviewerRequestSHA, err := domain.ExecutionPermissionRequestSHA256(reviewerPermissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256 Reviewer: %v", err)
	}
	reviewerAssessment := domain.ExecutionEffectAssessment{
		Role: reviewerPermissionRequest.Role, Effect: reviewerPermissionRequest.Effect,
		Resource: reviewerPermissionRequest.Resource, ProviderID: reviewerPermissionRequest.ProviderID,
		AccountID:           reviewerPermissionRequest.AccountID,
		ConfigurationSHA256: reviewerPermissionRequest.ConfigurationSHA256,
		RequestSHA256:       reviewerRequestSHA, Reversible: true, Contained: true,
		Mutating: false, ValidUntil: reviewerPermissionRequest.ExpiresAt.Add(time.Minute),
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	_, reviewerPermission, err := database.AddExecutionPermission(
		context.Background(), job.Revision, reviewerPermissionRequest, reviewerRequestSHA, reviewerAssessment,
	)
	if err != nil {
		t.Fatalf("Add Reviewer ExecutionPermission: %v", err)
	}
	_, reviewerExecution, _, err := executionController.StartTaskExecution(
		context.Background(), reviewerPermission.ID, "cmd_67676767676767676767676767676767",
		reviewerPermissionRequest,
	)
	if err != nil {
		t.Fatalf("StartTaskExecution Reviewer: %v", err)
	}
	// The mock Provider accepts a Reviewer Session; this test exercises message routing,
	// while production Reviewer dispatch remains guarded by the Worktree boundary.
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before mock Reviewer acceptance: %v", err)
	}
	reviewerSessionID := "session_reviewer"
	_, reviewerObservation, err := database.AddTaskProviderStartObservation(
		context.Background(), jobID, job.Revision, reviewerExecution.ProviderStartOperationID,
		"cmd_72727272727272727272727272727272", domain.ProviderStartAccepted,
		reviewerSessionID, "receipt_reviewer", strings.Repeat("f", 64), false,
	)
	if err != nil || reviewerObservation.Status != domain.ProviderStartAccepted {
		t.Fatalf("record mock Reviewer Provider acceptance: %#v %v", reviewerObservation, err)
	}

	coderOutput, err := orchestrator.SendRoleMessage(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: coderExecution.ID, ProviderSessionID: coderStart.ProviderSessionID,
		CommandID:        "cmd_68686868686868686868686868686868",
		Recipient:        domain.ActorRef{Kind: domain.ActorReviewer, ID: reviewerExecution.ID},
		ReplyToMessageID: answer.Message.ID,
		Body:             "実装結果を確認してください。固定された成果を提示します。",
	})
	if err != nil {
		t.Fatalf("SendRoleMessage Coder output: %v", err)
	}
	if len(coderOutput.ResumedContexts) != 1 ||
		coderOutput.ResumedContexts[0].RoleExecution.ID != reviewerExecution.ID ||
		coderOutput.ResumedContexts[0].ProviderSessionID != reviewerSessionID {
		t.Fatalf("Coder output did not route to the independent Reviewer Session: %#v", coderOutput.ResumedContexts)
	}

	review, err := orchestrator.SendRoleMessage(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: reviewerExecution.ID, ProviderSessionID: reviewerSessionID,
		CommandID:        "cmd_69696969696969696969696969696969",
		Recipient:        domain.ActorRef{Kind: domain.ActorCoder, ID: coderExecution.ID},
		ReplyToMessageID: coderOutput.Message.Message.ID,
		Body:             "指摘: 境界条件の検証を追加してください。対象は同じTaskの変更範囲です。",
	})
	if err != nil {
		t.Fatalf("SendRoleMessage Reviewer feedback: %v", err)
	}
	if len(review.ResumedContexts) != 1 ||
		review.ResumedContexts[0].RoleExecution.ID != coderExecution.ID ||
		review.ResumedContexts[0].ProviderSessionID != coderStart.ProviderSessionID {
		t.Fatalf("Reviewer feedback did not restore the original Coder Session: %#v", review.ResumedContexts)
	}

	correction, err := orchestrator.SendRoleMessage(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: coderExecution.ID, ProviderSessionID: coderStart.ProviderSessionID,
		CommandID:        "cmd_70707070707070707070707070707070",
		Recipient:        domain.ActorRef{Kind: domain.ActorReviewer, ID: reviewerExecution.ID},
		ReplyToMessageID: review.Message.Message.ID,
		Body:             "修正しました。境界条件のテストを追加しました。",
	})
	if err != nil {
		t.Fatalf("SendRoleMessage Coder correction: %v", err)
	}
	if len(correction.ResumedContexts) != 1 ||
		correction.ResumedContexts[0].RoleExecution.ID != reviewerExecution.ID ||
		correction.ResumedContexts[0].ProviderSessionID != reviewerSessionID {
		t.Fatalf("Coder correction did not restore the independent Reviewer Session: %#v", correction.ResumedContexts)
	}

	rereview, err := orchestrator.SendRoleMessage(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: reviewerExecution.ID, ProviderSessionID: reviewerSessionID,
		CommandID:        "cmd_71717171717171717171717171717171",
		Recipient:        domain.ActorRef{Kind: domain.ActorCoder, ID: coderExecution.ID},
		ReplyToMessageID: correction.Message.Message.ID,
		Body:             "再レビュー結果: 指摘を解消し、対象成果をPASSと判定します。",
	})
	if err != nil {
		t.Fatalf("SendRoleMessage Reviewer re-review: %v", err)
	}
	if len(rereview.ResumedContexts) != 1 ||
		rereview.ResumedContexts[0].RoleExecution.ID != coderExecution.ID ||
		rereview.ResumedContexts[0].ProviderSessionID != coderStart.ProviderSessionID ||
		len(rereview.ResumedContexts[0].Messages) != 6 {
		t.Fatalf("Reviewer re-review did not restore the complete original Coder context: %#v", rereview.ResumedContexts)
	}
	persisted, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Coder / Reviewer correction cycle: %v", err)
	}
	if len(persisted.JobMessages) != 6 {
		t.Fatalf("persisted Role conversation messages=%d, want 6", len(persisted.JobMessages))
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
