package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type fakeQuestionReadOnlyContextResolver struct {
	result QuestionReadOnlyContext
	err    error
	calls  int
}

func (r *fakeQuestionReadOnlyContextResolver) ResolveReadOnlyBoundary(
	_ context.Context,
	_ domain.Job,
	_ domain.JobMessage,
	_ domain.RoleExecution,
) (QuestionReadOnlyContext, error) {
	r.calls++
	return r.result, r.err
}

type lateQuestionAuthorizationCommitStore struct {
	JobQuestionAuthorizationStore
	receipt       store.CommandReceipt
	authorization domain.JobQuestionAuthorization
	lookupCalls   int
	addCalls      int
	addErr        error
}

func (s *lateQuestionAuthorizationCommitStore) GetJobQuestionAuthorizationCommand(
	ctx context.Context, jobID, commandID string,
) (store.CommandReceipt, domain.JobQuestionAuthorization, bool, error) {
	s.lookupCalls++
	if s.lookupCalls == 1 {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, false, nil
	}
	return s.receipt, s.authorization, true, nil
}

func (s *lateQuestionAuthorizationCommitStore) AddJobQuestionAuthorization(
	context.Context, string, uint64, domain.JobQuestionAuthorizationRequest,
) (store.CommandReceipt, domain.JobQuestionAuthorization, error) {
	s.addCalls++
	return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, s.addErr
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
	var plannerStart domain.ProviderStartIntent
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == plannerExecution.ID {
			plannerStart = candidate
			break
		}
	}
	if plannerStart.ID == "" {
		t.Fatal("Planner Provider Start intent was not found")
	}

	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	question, err := communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: coderExecution.ID, ProviderSessionID: coderStart.ProviderSessionID,
		CommandID: "cmd_44444444444444444444444444444444",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: plannerExecution.ID},
		Kind:      domain.MessageKindQuestion,
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

	resolver := &fakeQuestionReadOnlyContextResolver{
		result: QuestionReadOnlyContext{
			RepositoryID: revision.Contract.TargetRepositories[0],
			Boundary: domain.PlanningReadOnlyBoundary{
				Mode:      domain.PlanningReadOnlyBoundarySnapshot,
				Reference: "question_snapshot_controller",
				SHA256:    strings.Repeat("c", 64),
			},
		},
	}
	questionAuthorizer, err := NewJobQuestionAuthorizationController(database, resolver)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	questionCommandID := "cmd_56565656565656565656565656565656"
	questionBudget := domain.PlanningBudget{MaxTurns: 1, MaxTokens: 50}
	questionDeadline := plannerStart.Deadline.Add(-time.Minute)
	if revision.Contract.Deadline.Before(questionDeadline) {
		questionDeadline = revision.Contract.Deadline.Add(-time.Minute)
	}
	questionReceipt, questionGrant, err := questionAuthorizer.Authorize(
		context.Background(), jobID, question.Message.ID, questionCommandID,
		questionBudget, questionDeadline,
	)
	if err != nil {
		t.Fatalf("Authorize JobQuestionAuthorization: %v", err)
	}
	if questionGrant.RepositoryID != revision.Contract.TargetRepositories[0] ||
		questionGrant.QuestionMessageID != question.Message.ID || resolver.calls != 1 {
		t.Fatalf("JobQuestionAuthorization lost its scope or resolver was not called once: %#v calls=%d", questionGrant, resolver.calls)
	}

	for _, test := range []struct {
		name        string
		resolverErr error
		addErr      error
	}{
		{name: "resolver failure after concurrent commit", resolverErr: errors.New("transient resolver failure")},
		{name: "store conflict after concurrent commit", addErr: store.ErrIdempotencyConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			lateStore := &lateQuestionAuthorizationCommitStore{
				JobQuestionAuthorizationStore: database,
				receipt: questionReceipt, authorization: questionGrant, addErr: test.addErr,
			}
			lateResolver := &fakeQuestionReadOnlyContextResolver{
				result: resolver.result, err: test.resolverErr,
			}
			lateController, err := NewJobQuestionAuthorizationController(lateStore, lateResolver)
			if err != nil {
				t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
			}
			replayedReceipt, replayedGrant, err := lateController.Authorize(
				context.Background(), jobID, question.Message.ID, questionCommandID,
				questionBudget, questionDeadline,
			)
			if err != nil || replayedReceipt != questionReceipt || replayedGrant.ID != questionGrant.ID ||
				lateStore.lookupCalls != 2 || lateStore.addCalls != boolToInt(test.resolverErr == nil) {
				t.Fatalf("concurrent committed command was not replayed: receipt=%#v grant=%#v lookups=%d adds=%d err=%v",
					replayedReceipt, replayedGrant, lateStore.lookupCalls, lateStore.addCalls, err)
			}
		})
	}
	resolver.err = errors.New("read-only context resolver unavailable")
	retryReceipt, retryGrant, err := questionAuthorizer.Authorize(
		context.Background(), jobID, question.Message.ID, questionCommandID,
		questionBudget, questionDeadline,
	)
	if err != nil || retryReceipt != questionReceipt || retryGrant.ID != questionGrant.ID || resolver.calls != 1 {
		t.Fatalf("JobQuestionAuthorization retry did not replay without resolving context: receipt=%#v grant=%#v calls=%d err=%v",
			retryReceipt, retryGrant, resolver.calls, err)
	}
	changedBudget := questionBudget
	changedBudget.MaxTokens++
	if _, _, err := questionAuthorizer.Authorize(
		context.Background(), jobID, question.Message.ID, questionCommandID,
		changedBudget, questionDeadline,
	); err == nil {
		t.Fatal("JobQuestionAuthorization accepted a reused CommandID with changed caller inputs")
	}

	answer, err := communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: plannerExecution.ID, ProviderSessionID: plannerSession,
		CommandID: "cmd_55555555555555555555555555555555",
		Recipient: domain.ActorRef{Kind: domain.ActorCoder, ID: coderExecution.ID},
		Kind:      domain.MessageKindAnswer, ReplyToMessageID: question.Message.ID,
		Body: "優先順位は安全性、次に完遂性です。",
	})
	if err != nil {
		t.Fatalf("SendFromRole Planner ANSWER: %v", err)
	}
	if answer.Message.ThreadID != question.Message.ThreadID ||
		answer.Message.ReplyToMessageID != question.Message.ID ||
		answer.Message.Sender.ID != plannerExecution.ID ||
		answer.Message.Recipient.ID != coderExecution.ID {
		t.Fatalf("Planner ANSWER lost correlation or identity: %#v", answer.Message)
	}

	persisted, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Coder / Planner communication: %v", err)
	}
	if len(persisted.JobMessages) != 2 || len(persisted.DeliveryObservations) != 2 {
		t.Fatalf("persisted communication facts: messages=%d deliveries=%d", len(persisted.JobMessages), len(persisted.DeliveryObservations))
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
		Kind:      domain.MessageKindMessage, Body: "must be rejected",
	})
	if err == nil {
		t.Fatal("CommunicationController accepted an unbound Provider Session")
	}
}

func TestCommunicationControllerRecordsAndReconcilesDeliveryObservation(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, plannerExecution, _, _, err := providerController.StartPlanning(
		context.Background(), jobID, grantID, "cmd_81818181818181818181818181818181", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}

	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	message, err := communication.SendFromHuman(context.Background(), HumanMessageRequest{
		JobID: jobID, CommandID: "cmd_82828282828282828282828282828282",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: plannerExecution.ID},
		Kind:      domain.MessageKindQuestion, Body: "追加の読み取り情報を確認してください。",
	}, principal)
	if err != nil {
		t.Fatalf("SendFromHuman: %v", err)
	}
	if message.Delivery == nil || message.Delivery.Status != domain.DeliveryRequested {
		t.Fatalf("initial delivery observation = %#v, want REQUESTED", message.Delivery)
	}

	_, unknown, err := communication.RecordDeliveryObservation(
		context.Background(), jobID, message.Message.ID,
		message.Delivery.ID, "cmd_83838383838383838383838383838383",
		domain.DeliveryUnknown, "", false,
	)
	if err != nil {
		t.Fatalf("RecordDeliveryObservation UNKNOWN: %v", err)
	}
	if unknown.Status != domain.DeliveryUnknown || unknown.Attempt != 1 {
		t.Fatalf("UNKNOWN delivery observation = %#v, want attempt 1", unknown)
	}

	_, _, err = communication.RecordDeliveryObservation(
		context.Background(), jobID, message.Message.ID,
		message.Delivery.ID, "cmd_84848484848484848484848484848484",
		domain.DeliveryAccepted, strings.Repeat("a", 64), false,
	)
	if err == nil {
		t.Fatal("UNKNOWN delivery was accepted without explicit reconciliation")
	}

	_, accepted, err := communication.RecordDeliveryObservation(
		context.Background(), jobID, message.Message.ID,
		message.Delivery.ID, "cmd_85858585858585858585858585858585",
		domain.DeliveryAccepted, strings.Repeat("b", 64), true,
	)
	if err != nil {
		t.Fatalf("RecordDeliveryObservation reconciliation: %v", err)
	}
	if accepted.Status != domain.DeliveryAccepted || !accepted.ReconcilesUnknown || accepted.Attempt != 1 {
		t.Fatalf("reconciled delivery observation = %#v", accepted)
	}

	persisted, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after delivery reconciliation: %v", err)
	}
	latest, found := latestDeliveryObservation(persisted, message.Message.ID)
	if !found || latest.ID != accepted.ID || latest.ObservedBy.Kind != domain.ActorOrchestrator {
		t.Fatalf("persisted latest delivery observation = %#v, found=%v", latest, found)
	}
}
