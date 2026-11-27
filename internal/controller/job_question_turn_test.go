package controller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type fixedQuestionTurnContextResolver struct {
	context QuestionReadOnlyContext
}

func (r fixedQuestionTurnContextResolver) ResolveReadOnlyBoundary(
	context.Context, domain.Job, domain.JobMessage, domain.RoleExecution,
) (QuestionReadOnlyContext, error) {
	return r.context, nil
}

type jobQuestionTurnFixture struct {
	controller                                                                      *JobQuestionTurnController
	runtime                                                                         *fakePlanningProviderRuntime
	database                                                                        *store.Store
	jobID, authorizationID, questionID, coderID, plannerID, sessionID, databasePath string
}

func newJobQuestionTurnFixture(t *testing.T) jobQuestionTurnFixture {
	t.Helper()
	return newJobQuestionTurnFixtureAtPath(t, filepath.Join(t.TempDir(), "job-question-turn.sqlite"))
}

func newJobQuestionTurnFixtureAtPath(t *testing.T, databasePath string) jobQuestionTurnFixture {
	t.Helper()
	ctx := context.Background()
	planning, database, principal, jobID, _, sourceResult, contract := startContractPlannerWithResultContentAtPath(t, "", databasePath)
	_, revision, err := planning.ProposeJobContract(ctx, jobID,
		domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, "cmd_91919191919191919191919191919191", principal)
	if err != nil {
		t.Fatalf("ProposeJobContract: %v", err)
	}
	contracts, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	if _, _, err := contracts.Approve(ctx, jobID, "cmd_92929292929292929292929292929292",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approve bounded question response", principal); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after approval: %v", err)
	}
	taskRequest := domain.TaskRequest{
		JobID: jobID, CommandID: "cmd_93939393939393939393939393939393",
		Title: "Question response task", Purpose: "Exercise bounded Planner question response",
		TargetRepositories: []string{contract.TargetRepositories[0]},
		Workspaces:         []string{contract.Workspaces[0]},
		AcceptanceCriteria: []string{"Planner answer returns to the original Role"},
		Budget:             contract.Budget, ConcurrencyCeiling: 1, Deadline: contract.Deadline,
		ProhibitedEffects:       contract.ProhibitedEffects,
		ApprovalRequiredEffects: contract.ApprovalRequiredEffects,
	}
	if _, _, err := database.AddTask(ctx, job.Revision, taskRequest); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before ExecutionPermission: %v", err)
	}
	permissionRequest := domain.ExecutionPermissionRequest{
		JobID: jobID, TaskID: job.Tasks[len(job.Tasks)-1].ID,
		CommandID: "cmd_94949494949494949494949494949494",
		Role:      domain.RoleCoder, Effect: "repository.write", Resource: contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: contract.Deadline.Add(-time.Minute),
	}
	permissionSHA, err := domain.ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := domain.ExecutionEffectAssessment{
		Role: permissionRequest.Role, Effect: permissionRequest.Effect, Resource: permissionRequest.Resource,
		ProviderID: permissionRequest.ProviderID, AccountID: permissionRequest.AccountID,
		ConfigurationSHA256: permissionRequest.ConfigurationSHA256, RequestSHA256: permissionSHA,
		Contained: true, Mutating: true, ValidUntil: permissionRequest.ExpiresAt.Add(time.Minute),
		EvidenceSHA256: strings.Repeat("d", 64),
	}
	_, permission, err := database.AddExecutionPermission(ctx, job.Revision, permissionRequest, permissionSHA, assessment)
	if err != nil {
		t.Fatalf("AddExecutionPermission: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Coder execution: %v", err)
	}
	_, coderStart, err := database.StartTaskExecution(ctx, job.Revision, jobID,
		permissionRequest.TaskID, permission.ID, "cmd_95959595959595959595959595959595", permission.RequestSHA256)
	if err != nil {
		t.Fatalf("StartTaskExecution: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before question: %v", err)
	}
	plannerID := ""
	for _, execution := range job.RoleExecutions {
		if execution.Role == string(domain.RolePlanner) {
			plannerID = execution.ID
			break
		}
	}
	if plannerID == "" {
		t.Fatal("Taskless Planner RoleExecution not found")
	}
	_, question, err := database.AddJobMessage(ctx, jobID, job.Revision,
		"cmd_96969696969696969696969696969696", domain.JobMessageRequest{
			Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: coderStart.RoleExecution.ID},
			Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: plannerID},
			Body:      "Please clarify the approved implementation constraint.",
		})
	if err != nil {
		t.Fatalf("AddJobMessage: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before question authorization: %v", err)
	}
	plannerExecution, found := findRoleExecution(job, plannerID)
	if !found {
		t.Fatal("Planner RoleExecution not found")
	}
	var planningGrant domain.PlanningAuthorization
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID == plannerExecution.PlanningAuthorizationID {
			planningGrant = grant
			break
		}
	}
	var sessionID string
	for _, observation := range job.ProviderStartObservations {
		if observation.OperationID == plannerExecution.ProviderStartOperationID &&
			observation.Status == domain.ProviderStartAccepted {
			sessionID = observation.ProviderSessionID
		}
	}
	if planningGrant.ID == "" || sessionID == "" {
		t.Fatal("Planner grant or accepted Provider Session not found")
	}
	boundary := domain.PlanningReadOnlyBoundary{
		Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
		SHA256: sha256String("immutable question snapshot"),
	}
	authorizer, err := NewJobQuestionAuthorizationController(database, fixedQuestionTurnContextResolver{
		context: QuestionReadOnlyContext{RepositoryID: contract.TargetRepositories[0], Boundary: boundary},
	})
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	_, authorization, err := authorizer.Authorize(ctx, jobID, question.ID,
		"cmd_97979797979797979797979797979797",
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100}, planningGrant.Deadline.Add(-time.Minute))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	runtime := &fakePlanningProviderRuntime{identity: provider.Identity{
		ProviderID: authorization.ProviderID, AccountID: authorization.AccountID,
		SDKVersion: "test-sdk/1", ConfigurationSHA256: authorization.ConfigurationSHA256,
	}}
	runtime.capabilities = provider.Capabilities{
		Identity: runtime.identity, SendIdempotency: true, ReadOnlyContextEnforcement: true,
		Evidence: []provider.CapabilityEvidence{
			{
				Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity,
				TestID: "question-turn", EvidenceSHA256: strings.Repeat("a", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: time.Now().UTC(),
			},
			{
				Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: runtime.identity,
				TestID: "read-only-context", EvidenceSHA256: strings.Repeat("b", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: time.Now().UTC(),
			},
		},
	}
	controller, err := newJobQuestionTurnController(database, runtime, provider.EvidenceSynthetic)
	if err != nil {
		t.Fatalf("NewJobQuestionTurnController: %v", err)
	}
	return jobQuestionTurnFixture{
		controller: controller, runtime: runtime, database: database, jobID: jobID,
		authorizationID: authorization.ID, questionID: question.ID,
		coderID: coderStart.RoleExecution.ID, databasePath: databasePath,
		plannerID: plannerID, sessionID: sessionID,
	}
}

func TestJobQuestionTurnControllerSendsReadOnlyTurnAndRepliesToOriginalRole(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	f.runtime.capabilities.SendIdempotency = false
	filteredEvidence := f.runtime.capabilities.Evidence[:0]
	for _, evidence := range f.runtime.capabilities.Evidence {
		if evidence.Capability != provider.CapabilitySendIdempotency {
			filteredEvidence = append(filteredEvidence, evidence)
		}
	}
	f.runtime.capabilities.Evidence = filteredEvidence
	persisted := false
	f.runtime.sendHook = func(ctx context.Context, request provider.TurnRequest) error {
		job, err := f.database.GetJob(ctx, f.jobID)
		if err != nil {
			return err
		}
		persisted = len(job.JobQuestionTurnIntents) == 1 && job.JobQuestionTurnIntents[0].ID == request.TurnID
		if !persisted || len(job.JobQuestionTurnObservations) != 0 {
			return errors.New("question intent was not persisted before Provider.Send")
		}
		if request.ReadOnlyBoundary.Reference != "question_snapshot_1" ||
			request.ReadOnlyBoundary.SHA256 != sha256String("immutable question snapshot") ||
			!strings.Contains(request.Prompt, "Please clarify the approved implementation constraint.") {
			return errors.New("Provider Turn lost its read-only boundary or natural-language question")
		}
		return nil
	}
	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID,
		"cmd_a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", 100)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !persisted || f.runtime.sendCalls != 1 || outcome.Observation.Status != domain.JobQuestionTurnCompleted {
		t.Fatalf("Planner turn did not complete: %#v calls=%d", outcome, f.runtime.sendCalls)
	}
	reply := outcome.Reply.Message
	if reply.Sender != (domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID}) ||
		reply.Recipient != (domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID}) ||
		reply.ReplyToMessageID != f.questionID || reply.Body != outcome.Observation.Content {
		t.Fatalf("Planner answer did not return to original Coder: %#v", reply)
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 1 || len(job.JobQuestionTurnObservations) != 1 || len(job.JobMessages) != 2 {
		t.Fatalf("question turn was not persisted exactly once: intents=%d observations=%d messages=%d",
			len(job.JobQuestionTurnIntents), len(job.JobQuestionTurnObservations), len(job.JobMessages))
	}
}

func TestJobQuestionTurnControllerReconcilesUnknownWithoutBlindResend(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	now := time.Now().UTC()
	f.runtime.capabilities = provider.Capabilities{
		Identity: f.runtime.identity, SendIdempotency: true, SendLookup: true,
		ReadOnlyContextEnforcement: true, FinalOutputRetrieval: true,
		UsageIdentity: true, UsageCompleteness: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity, TestID: "send-idempotency", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: f.runtime.identity, TestID: "read-only-context", EvidenceSHA256: strings.Repeat("6", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "lookup", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "result", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "usage-id", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "usage-complete", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		},
	}
	f.runtime.sendErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown}
	commandID := "cmd_a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2a2"
	first, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err == nil || first.Observation.Status != domain.JobQuestionTurnUnknown || f.runtime.sendCalls != 1 {
		t.Fatalf("ambiguous Send did not persist UNKNOWN: %#v calls=%d err=%v", first, f.runtime.sendCalls, err)
	}
	content := "Planner response recovered from the same Provider operation."
	f.runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: first.Intent.OperationID, Status: domain.JobQuestionTurnCompleted,
		ReceiptID: "receipt_question_recovered", SessionID: f.sessionID, TurnID: first.Intent.ID,
		ResultSHA256: sha256String(content), UsageID: "usage_question_recovered",
		EvidenceSHA256: strings.Repeat("e", 64),
	}
	f.runtime.resultResult = provider.Result{
		OperationID: first.Intent.OperationID, SessionID: f.sessionID, TurnID: first.Intent.ID,
		Content: content, ResultSHA256: sha256String(content), Final: true, EvidenceSHA256: strings.Repeat("f", 64),
	}
	f.runtime.usageResult = provider.Usage{
		OperationID: first.Intent.OperationID, SessionID: f.sessionID, TurnID: first.Intent.ID,
		UsageID: "usage_question_recovered", Tokens: 18, Complete: true, EvidenceSHA256: strings.Repeat("1", 64),
	}
	f.runtime.sendErr = nil
	recovered, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("reconcile UNKNOWN: %v", err)
	}
	if recovered.Observation.Status != domain.JobQuestionTurnCompleted ||
		!recovered.Observation.ReconcilesUnknown || recovered.Observation.Content != content ||
		recovered.Observation.TokensUsed != 18 || recovered.Reply.Message.ReplyToMessageID != f.questionID ||
		f.runtime.sendCalls != 1 || f.runtime.lookupCalls != 1 || f.runtime.resultCalls != 1 || f.runtime.usageCalls != 1 {
		t.Fatalf("UNKNOWN was not reconciled without resend: %#v calls(send=%d lookup=%d result=%d usage=%d)",
			recovered, f.runtime.sendCalls, f.runtime.lookupCalls, f.runtime.resultCalls, f.runtime.usageCalls)
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob after reconciliation: %v", err)
	}
	if len(job.JobQuestionTurnObservations) != 2 ||
		job.JobQuestionTurnObservations[0].Status != domain.JobQuestionTurnUnknown ||
		!job.JobQuestionTurnObservations[1].ReconcilesUnknown {
		t.Fatalf("UNKNOWN history was not retained: %#v", job.JobQuestionTurnObservations)
	}
}

func TestJobQuestionTurnControllerReconcilesFailedLookupWithoutResult(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	now := time.Now().UTC()
	f.runtime.capabilities = provider.Capabilities{
		Identity: f.runtime.identity, SendIdempotency: true, SendLookup: true,
		ReadOnlyContextEnforcement: true, UsageIdentity: true, UsageCompleteness: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity, TestID: "send-idempotency", EvidenceSHA256: strings.Repeat("5", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: f.runtime.identity, TestID: "read-only-context", EvidenceSHA256: strings.Repeat("6", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "lookup", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "usage-id", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
			{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "usage-complete", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		},
	}
	f.runtime.sendErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown}
	commandID := "cmd_a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5"
	first, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err == nil || first.Observation.Status != domain.JobQuestionTurnUnknown || f.runtime.sendCalls != 1 {
		t.Fatalf("ambiguous Send did not persist UNKNOWN: %#v calls=%d err=%v", first, f.runtime.sendCalls, err)
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: first.Intent.OperationID, Status: domain.JobQuestionTurnFailed,
		ReceiptID: "receipt_question_failed", SessionID: f.sessionID, TurnID: first.Intent.ID,
		UsageID: "usage_question_failed", EvidenceSHA256: strings.Repeat("e", 64),
	}
	f.runtime.usageResult = provider.Usage{
		OperationID: first.Intent.OperationID, SessionID: f.sessionID, TurnID: first.Intent.ID,
		UsageID: "usage_question_failed", Tokens: 9, Complete: true, EvidenceSHA256: strings.Repeat("f", 64),
	}
	f.runtime.sendErr = nil
	failed, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err == nil {
		t.Fatal("FAILED Provider lookup did not report the terminal operation failure")
	}
	if failed.Observation.Status != domain.JobQuestionTurnFailed || !failed.Observation.ReconcilesUnknown ||
		failed.Observation.Content != "" || failed.Observation.TokensUsed != 9 ||
		failed.Reply.Message.ID != "" || f.runtime.sendCalls != 1 ||
		f.runtime.lookupCalls != 1 || f.runtime.resultCalls != 0 || f.runtime.usageCalls != 1 {
		t.Fatalf("FAILED lookup was not persisted without Result retrieval: %#v calls(send=%d lookup=%d result=%d usage=%d)",
			failed, f.runtime.sendCalls, f.runtime.lookupCalls, f.runtime.resultCalls, f.runtime.usageCalls)
	}
}

func TestJobQuestionTurnControllerRequiresLiveEvidenceOutsideSyntheticHarness(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	productionController, err := NewJobQuestionTurnController(f.database, f.runtime)
	if err != nil {
		t.Fatalf("NewJobQuestionTurnController: %v", err)
	}
	now := time.Now().UTC()
	f.runtime.capabilities = provider.Capabilities{
		Identity: f.runtime.identity, SendIdempotency: true, ReadOnlyContextEnforcement: true,
		Evidence: []provider.CapabilityEvidence{
			{
				Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity,
				TestID: "synthetic-send-idempotency", EvidenceSHA256: strings.Repeat("a", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
			},
			{
				Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: f.runtime.identity,
				TestID: "synthetic-read-only-context", EvidenceSHA256: strings.Repeat("b", 64),
				EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
			},
		},
	}
	if _, err := productionController.Send(context.Background(), f.jobID, f.authorizationID,
		"cmd_b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", 100); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("production Send with synthetic evidence = %v, want ErrUnsupportedCapability", err)
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 0 || f.runtime.sendCalls != 0 {
		t.Fatalf("synthetic evidence crossed the production gate: intents=%d sends=%d",
			len(job.JobQuestionTurnIntents), f.runtime.sendCalls)
	}
}

func TestJobQuestionTurnControllerAllowsOneShotSendWithoutRecoveryCapability(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	now := time.Now().UTC()
	f.runtime.capabilities = provider.Capabilities{
		Identity: f.runtime.identity, ReadOnlyContextEnforcement: true,
		Evidence: []provider.CapabilityEvidence{{
			Capability: provider.CapabilityReadOnlyContextEnforcement, Identity: f.runtime.identity,
			TestID: "read-only-context", EvidenceSHA256: strings.Repeat("b", 64),
			EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
		}},
	}
	f.runtime.sendErr = &provider.OperationError{Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown}
	const commandID = "cmd_a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3"
	first, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err == nil || first.Observation.Status != domain.JobQuestionTurnUnknown || f.runtime.sendCalls != 1 {
		t.Fatalf("initial one-shot Send was not retained as UNKNOWN: outcome=%#v sends=%d err=%v",
			first, f.runtime.sendCalls, err)
	}
	second, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if !errors.Is(err, provider.ErrUnsupportedCapability) || f.runtime.sendCalls != 1 {
		t.Fatalf("UNKNOWN Send was retried without Lookup or idempotency evidence: outcome=%#v sends=%d err=%v",
			second, f.runtime.sendCalls, err)
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 1 || len(job.JobQuestionTurnObservations) != 1 ||
		job.JobQuestionTurnObservations[0].Status != domain.JobQuestionTurnUnknown {
		t.Fatalf("one-shot Send did not preserve a single UNKNOWN operation: intents=%d observations=%#v",
			len(job.JobQuestionTurnIntents), job.JobQuestionTurnObservations)
	}
}

func TestJobQuestionTurnControllerRejectsProviderWithoutReadOnlyContextEnforcement(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	now := time.Now().UTC()
	f.runtime.capabilities = provider.Capabilities{
		Identity: f.runtime.identity, SendIdempotency: true,
		Evidence: []provider.CapabilityEvidence{{
			Capability: provider.CapabilitySendIdempotency, Identity: f.runtime.identity,
			TestID: "send-idempotency", EvidenceSHA256: strings.Repeat("a", 64),
			EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now,
		}},
	}
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID,
		"cmd_a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4a4", 100); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("Send without read-only enforcement = %v, want ErrUnsupportedCapability", err)
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 0 || f.runtime.sendCalls != 0 {
		t.Fatalf("Provider without read-only enforcement created intent or ran Provider: %#v", job.JobQuestionTurnIntents)
	}
}

func TestOrchestratorRoutesPlannerInboxAndReturnsAnswerToOriginalCoder(t *testing.T) {
	fixture := newJobQuestionTurnFixture(t)
	communication, err := NewCommunicationController(fixture.database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	boundary := domain.PlanningReadOnlyBoundary{
		Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
		SHA256: sha256String("immutable question snapshot"),
	}
	authorization, err := NewJobQuestionAuthorizationController(
		fixture.database, fixedQuestionTurnContextResolver{
			context: QuestionReadOnlyContext{
				RepositoryID: "https://github.com/example/project",
				Boundary:     boundary,
			},
		},
	)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		fixture.database, communication, authorization, fixture.controller,
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	outcomes, err := orchestrator.ProcessPlannerQuestions(
		context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID,
	)
	if err != nil {
		t.Fatalf("ProcessPlannerQuestions: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].Observation.Status != domain.JobQuestionTurnCompleted ||
		fixture.runtime.sendCalls != 1 {
		t.Fatalf("Orchestrator did not process the persisted Planner inbox: %#v calls=%d",
			outcomes, fixture.runtime.sendCalls)
	}
	inbox, err := communication.InboxForRole(context.Background(), fixture.jobID, fixture.coderID)
	if err != nil {
		t.Fatalf("Coder InboxForRole: %v", err)
	}
	var answer *domain.JobMessage
	for i := range inbox {
		if inbox[i].ReplyToMessageID == fixture.questionID {
			answer = &inbox[i]
			break
		}
	}
	if answer == nil || answer.Sender != (domain.ActorRef{Kind: domain.ActorPlanner, ID: fixture.plannerID}) ||
		answer.Recipient != (domain.ActorRef{Kind: domain.ActorCoder, ID: fixture.coderID}) {
		t.Fatalf("original Coder Inbox does not contain the correlated Planner answer: %#v", inbox)
	}
	// Reprocessing the same inbox is idempotent and must not create another Provider Turn or reply.
	if _, err := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID); err != nil {
		t.Fatalf("replay ProcessPlannerQuestions: %v", err)
	}
	job, err := fixture.database.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob after inbox replay: %v", err)
	}
	if fixture.runtime.sendCalls != 1 || len(job.JobQuestionTurnIntents) != 1 ||
		len(job.JobQuestionTurnObservations) != 1 || len(job.JobMessages) != 2 {
		t.Fatalf("Planner inbox replay was not idempotent: sends=%d intents=%d observations=%d messages=%d",
			fixture.runtime.sendCalls, len(job.JobQuestionTurnIntents),
			len(job.JobQuestionTurnObservations), len(job.JobMessages))
	}
}
func TestOrchestratorRestoresQuestionTurnAndReplyAfterSQLiteRestart(t *testing.T) {
	fixture := newJobQuestionTurnFixture(t)
	communication, err := NewCommunicationController(fixture.database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	authorization, err := NewJobQuestionAuthorizationController(
		fixture.database, fixedQuestionTurnContextResolver{
			context: QuestionReadOnlyContext{
				RepositoryID: "https://github.com/example/project",
				Boundary: domain.PlanningReadOnlyBoundary{
					Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
					SHA256: sha256String("immutable question snapshot"),
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		fixture.database, communication, authorization, fixture.controller,
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	first, err := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID)
	if err != nil || len(first) != 1 || first[0].Observation.Status != domain.JobQuestionTurnCompleted ||
		fixture.runtime.sendCalls != 1 {
		t.Fatalf("initial Planner inbox processing failed: outcomes=%#v sends=%d err=%v",
			first, fixture.runtime.sendCalls, err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatalf("close SQLite before restart: %v", err)
	}
	reopened, err := store.Open(context.Background(), fixture.databasePath)
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	fixture.database = reopened

	communication, err = NewCommunicationController(reopened)
	if err != nil {
		t.Fatalf("NewCommunicationController after restart: %v", err)
	}
	authorization, err = NewJobQuestionAuthorizationController(
		reopened, fixedQuestionTurnContextResolver{
			context: QuestionReadOnlyContext{
				RepositoryID: "https://github.com/example/project",
				Boundary: domain.PlanningReadOnlyBoundary{
					Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
					SHA256: sha256String("immutable question snapshot"),
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController after restart: %v", err)
	}
	turnController, err := newJobQuestionTurnController(reopened, fixture.runtime, provider.EvidenceSynthetic)
	if err != nil {
		t.Fatalf("newJobQuestionTurnController after restart: %v", err)
	}
	orchestrator, err = NewOrchestratorController(
		reopened, communication, authorization, turnController,
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController after restart: %v", err)
	}
	replayed, err := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID)
	if err != nil || len(replayed) != 1 || replayed[0].Observation.Status != domain.JobQuestionTurnCompleted ||
		replayed[0].Reply.Message.ReplyToMessageID != fixture.questionID || fixture.runtime.sendCalls != 1 {
		t.Fatalf("SQLite restart replay changed the completed turn or resent Provider work: outcomes=%#v sends=%d err=%v",
			replayed, fixture.runtime.sendCalls, err)
	}
	job, err := reopened.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob after SQLite restart: %v", err)
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after SQLite restart: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 1 || len(job.JobQuestionTurnObservations) != 1 || len(job.JobMessages) != 2 {
		t.Fatalf("SQLite restart changed persisted question facts: intents=%d observations=%d messages=%d",
			len(job.JobQuestionTurnIntents), len(job.JobQuestionTurnObservations), len(job.JobMessages))
	}
}

func TestOrchestratorContinuesAfterFailedQuestionAndCollectsErrors(t *testing.T) {
	fixture := newJobQuestionTurnFixture(t)
	communication, err := NewCommunicationController(fixture.database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	job, err := fixture.database.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob before second question: %v", err)
	}
	_, secondQuestion, err := fixture.database.AddJobMessage(
		context.Background(), fixture.jobID, job.Revision, "cmd_85858585858585858585858585858585",
		domain.JobMessageRequest{
			Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: fixture.coderID},
			Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: fixture.plannerID},
			Body:      "Second independent question",
		},
	)
	if err != nil {
		t.Fatalf("AddJobMessage second question: %v", err)
	}
	authorization, err := NewJobQuestionAuthorizationController(
		fixture.database, fixedQuestionTurnContextResolver{
			context: QuestionReadOnlyContext{
				RepositoryID: "https://github.com/example/project",
				Boundary: domain.PlanningReadOnlyBoundary{
					Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
					SHA256: sha256String("immutable question snapshot"),
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		fixture.database, communication, authorization, fixture.controller,
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	fixture.runtime.sendHook = func(ctx context.Context, request provider.TurnRequest) error {
		job, err := fixture.database.GetJob(ctx, fixture.jobID)
		if err != nil {
			return err
		}
		failedQuestion := false
		for _, intent := range job.JobQuestionTurnIntents {
			if intent.ID == request.TurnID && intent.JobQuestionAuthorizationID == fixture.authorizationID {
				failedQuestion = true
				break
			}
		}
		status := domain.JobQuestionTurnCompleted
		output := "answer to second question"
		tokens := uint64(8)
		receiptID := "receipt_second_question"
		evidence := strings.Repeat("d", 64)
		if failedQuestion {
			status = domain.JobQuestionTurnFailed
			output = ""
			tokens = 5
			receiptID = "receipt_first_question_failed"
			evidence = strings.Repeat("c", 64)
		}
		fixture.runtime.sendResult = provider.TurnResult{
			OperationID: request.OperationID, SessionID: request.SessionID, TurnID: request.TurnID,
			Status: status, Output: output, TokensUsed: tokens, ReceiptID: receiptID,
			EvidenceSHA256: evidence,
		}
		return nil
	}
	outcomes, err := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID, secondQuestion.ID)
	if err == nil || !strings.Contains(err.Error(), fixture.questionID) {
		t.Fatalf("ProcessPlannerQuestions error = %v, want first question failure", err)
	}
	if len(outcomes) != 2 || outcomes[0].Observation.Status != domain.JobQuestionTurnFailed ||
		outcomes[1].Observation.Status != domain.JobQuestionTurnCompleted || fixture.runtime.sendCalls != 2 {
		t.Fatalf("failed question blocked independent inbox processing: outcomes=%#v sends=%d err=%v",
			outcomes, fixture.runtime.sendCalls, err)
	}
	if outcomes[1].Reply.Message.ReplyToMessageID != secondQuestion.ID {
		t.Fatalf("second question reply correlation = %q, want %q", outcomes[1].Reply.Message.ReplyToMessageID, secondQuestion.ID)
	}
	job, err = fixture.database.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob after inbox processing: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 2 || len(job.JobQuestionTurnObservations) != 2 || len(job.JobMessages) != 3 {
		t.Fatalf("independent inbox processing persisted unexpected state: intents=%d observations=%d messages=%d",
			len(job.JobQuestionTurnIntents), len(job.JobQuestionTurnObservations), len(job.JobMessages))
	}
	for _, message := range job.JobMessages {
		if message.ReplyToMessageID == fixture.questionID {
			t.Fatalf("FAILED question unexpectedly received a reply: %#v", message)
		}
	}
	// Replay must retain the completed turn and its correlated reply without another Provider Send.
	replayed, replayErr := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID, secondQuestion.ID)
	if replayErr != nil || len(replayed) != 1 ||
		replayed[0].Observation.Status != domain.JobQuestionTurnCompleted || fixture.runtime.sendCalls != 2 {
		t.Fatalf("inbox replay did not skip the prior terminal failure or preserve the completed turn: outcomes=%#v sends=%d err=%v",
			replayed, fixture.runtime.sendCalls, replayErr)
	}
	job, err = fixture.database.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob after replay: %v", err)
	}
	if len(job.JobMessages) != 3 {
		t.Fatalf("completed question reply was duplicated during replay: messages=%d", len(job.JobMessages))
	}
}

type failOnceQuestionReplyStore struct {
	*store.Store
	failed bool
}

func (s *failOnceQuestionReplyStore) AddJobMessage(
	ctx context.Context, jobID string, expectedRevision uint64, commandID string, request domain.JobMessageRequest,
) (store.CommandReceipt, domain.JobMessage, error) {
	if request.ReplyToMessageID != "" && !s.failed {
		s.failed = true
		return store.CommandReceipt{}, domain.JobMessage{}, errors.New("injected reply persistence failure")
	}
	return s.Store.AddJobMessage(ctx, jobID, expectedRevision, commandID, request)
}

func TestOrchestratorReplaysCompletedTurnAfterReplyPersistenceFailure(t *testing.T) {
	fixture := newJobQuestionTurnFixture(t)
	communication, err := NewCommunicationController(fixture.database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	authorization, err := NewJobQuestionAuthorizationController(
		fixture.database, fixedQuestionTurnContextResolver{
			context: QuestionReadOnlyContext{
				RepositoryID: "https://github.com/example/project",
				Boundary: domain.PlanningReadOnlyBoundary{
					Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
					SHA256: sha256String("immutable question snapshot"),
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	replyStore := &failOnceQuestionReplyStore{Store: fixture.database}
	turnController, err := newJobQuestionTurnController(replyStore, fixture.runtime, provider.EvidenceSynthetic)
	if err != nil {
		t.Fatalf("NewJobQuestionTurnController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		fixture.database, communication, authorization, turnController,
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	first, err := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID)
	if err == nil || len(first) != 1 || first[0].Observation.Status != domain.JobQuestionTurnCompleted ||
		fixture.runtime.sendCalls != 1 {
		t.Fatalf("reply persistence failure did not retain completed turn: outcomes=%#v sends=%d err=%v",
			first, fixture.runtime.sendCalls, err)
	}
	job, err := fixture.database.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob after reply persistence failure: %v", err)
	}
	if len(job.JobQuestionTurnObservations) != 1 || len(job.JobMessages) != 1 {
		t.Fatalf("failed reply persistence changed terminal turn or stored a partial reply: observations=%d messages=%d",
			len(job.JobQuestionTurnObservations), len(job.JobMessages))
	}
	second, err := orchestrator.ProcessPlannerQuestions(context.Background(), fixture.jobID, fixture.plannerID, fixture.questionID)
	if err != nil || len(second) != 1 || second[0].Observation.Status != domain.JobQuestionTurnCompleted ||
		second[0].Reply.Message.ReplyToMessageID != fixture.questionID || fixture.runtime.sendCalls != 1 {
		t.Fatalf("completed-turn replay did not persist the missing reply: outcomes=%#v sends=%d err=%v",
			second, fixture.runtime.sendCalls, err)
	}
}

func TestJobQuestionTurnControllerRecoversExistingIntentAfterGrantDeadline(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	grant, found := findJobQuestionAuthorization(job, f.authorizationID)
	if !found {
		t.Fatal("JobQuestionAuthorization not found")
	}
	const commandID = "cmd_b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"
	f.runtime.sendErr = errors.New("simulated failure before Provider acceptance")
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("initial Provider failure was not returned")
	}
	job, err = f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob after initial failure: %v", err)
	}
	intent, found := findJobQuestionTurnIntentByCommand(job, commandID)
	if !found || len(job.JobQuestionTurnObservations) != 0 {
		t.Fatalf("expected persisted intent without observation: intent=%#v observations=%d", intent, len(job.JobQuestionTurnObservations))
	}
	now := time.Now().UTC()
	capabilities := f.runtime.capabilities
	capabilities.SendLookup, capabilities.UsageIdentity = true, true
	capabilities.UsageCompleteness, capabilities.FinalOutputRetrieval = true, true
	capabilities.ReadOnlyContextEnforcement = false
	retainedEvidence := capabilities.Evidence[:0]
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability != provider.CapabilityReadOnlyContextEnforcement {
			retainedEvidence = append(retainedEvidence, evidence)
		}
	}
	capabilities.Evidence = retainedEvidence
	capabilities.Evidence = append(capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "question-lookup", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "question-usage-id", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "question-usage-complete", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "question-result", EvidenceSHA256: strings.Repeat("f", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
	)
	f.runtime.capabilities = capabilities
	content := "recovered answer after the authorization deadline"
	f.runtime.lookupResult = provider.LookupResult{
		Found: true, OperationID: intent.OperationID, Status: domain.JobQuestionTurnCompleted,
		ReceiptID: "receipt with spaces", SessionID: f.sessionID, TurnID: intent.ID,
		ResultSHA256: sha256String(content), UsageID: "usage_question_recovered", EvidenceSHA256: strings.Repeat("a", 64),
	}
	f.runtime.usageResult = provider.Usage{
		OperationID: intent.OperationID, SessionID: f.sessionID, TurnID: intent.ID,
		UsageID: "usage_question_recovered", Tokens: 12, Complete: true, EvidenceSHA256: strings.Repeat("b", 64),
	}
	f.runtime.resultResult = provider.Result{
		OperationID: intent.OperationID, SessionID: f.sessionID, TurnID: intent.ID,
		Content: content, ResultSHA256: sha256String(content), Final: true, EvidenceSHA256: strings.Repeat("c", 64),
	}
	f.runtime.sendErr = nil
	f.controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("recover existing Provider result after grant deadline: %v", err)
	}
	if outcome.Observation.Status != domain.JobQuestionTurnCompleted || outcome.Observation.Content != content ||
		outcome.Reply.Message.ReplyToMessageID != f.questionID || f.runtime.sendCalls != 1 ||
		f.runtime.lookupCalls != 1 || f.runtime.resultCalls != 1 || f.runtime.usageCalls != 1 {
		t.Fatalf("expired intent was not recovered exclusively through Lookup: outcome=%#v calls(send=%d lookup=%d result=%d usage=%d)",
			outcome, f.runtime.sendCalls, f.runtime.lookupCalls, f.runtime.resultCalls, f.runtime.usageCalls)
	}
}

func TestJobQuestionTurnControllerDoesNotDispatchAfterDeadlineWhenLookupReportsNotFound(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	grant, found := findJobQuestionAuthorization(job, f.authorizationID)
	if !found {
		t.Fatal("JobQuestionAuthorization not found")
	}
	const commandID = "cmd_b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
	f.runtime.sendErr = errors.New("simulated failure before Provider acceptance")
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("initial Provider failure was not returned")
	}
	job, err = f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob after initial failure: %v", err)
	}
	intent, found := findJobQuestionTurnIntentByCommand(job, commandID)
	if !found {
		t.Fatal("persisted intent not found")
	}
	now := time.Now().UTC()
	capabilities := f.runtime.capabilities
	capabilities.SendLookup, capabilities.UsageIdentity = true, true
	capabilities.UsageCompleteness, capabilities.FinalOutputRetrieval = true, true
	capabilities.Evidence = append(capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "question-lookup", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "question-usage-id", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "question-usage-complete", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "question-result", EvidenceSHA256: strings.Repeat("f", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
	)
	f.runtime.capabilities = capabilities
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, Status: provider.LookupStatusNotFound,
		SessionID: f.sessionID, TurnID: intent.ID, EvidenceSHA256: strings.Repeat("a", 64),
	}
	f.controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	f.runtime.sendErr = nil
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("expired intent with NOT_FOUND lookup was allowed to start a new Provider turn")
	}
	if f.runtime.sendCalls != 1 || f.runtime.lookupCalls != 1 {
		t.Fatalf("expired recovery dispatched a new Provider turn: send=%d lookup=%d", f.runtime.sendCalls, f.runtime.lookupCalls)
	}
}

func TestJobQuestionTurnControllerRetriesVerifiedNotFoundWithoutSendIdempotency(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	const commandID = "cmd_fedcba9876543210fedcba9876543216"
	f.runtime.sendErr = errors.New("simulated preflight failure before Provider acceptance")
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("initial preflight failure was not returned")
	}

	now := time.Now().UTC()
	capabilities := f.runtime.capabilities
	capabilities.SendIdempotency = false
	filtered := capabilities.Evidence[:0]
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability != provider.CapabilitySendIdempotency {
			filtered = append(filtered, evidence)
		}
	}
	capabilities.Evidence = filtered
	capabilities.SendLookup, capabilities.UsageIdentity, capabilities.UsageCompleteness = true, true, true
	capabilities.Evidence = append(capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "question-lookup", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "question-usage-id", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "question-usage-complete", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
	)
	f.runtime.capabilities = capabilities
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	intent, found := findJobQuestionTurnIntentByCommand(job, commandID)
	if !found {
		t.Fatal("persisted intent not found")
	}
	f.runtime.lookupResult = provider.LookupResult{
		Found: false, OperationID: intent.OperationID, Status: provider.LookupStatusNotFound,
		SessionID: f.sessionID, TurnID: intent.ID, EvidenceSHA256: strings.Repeat("a", 64),
	}
	f.runtime.sendErr = nil

	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("retry after authoritative NOT_FOUND: %v", err)
	}
	if outcome.Observation.Status != domain.JobQuestionTurnCompleted ||
		f.runtime.sendCalls != 2 || f.runtime.lookupCalls != 1 {
		t.Fatalf("verified NOT_FOUND did not release the unused first Send: outcome=%#v sends=%d lookups=%d",
			outcome, f.runtime.sendCalls, f.runtime.lookupCalls)
	}
}

func TestJobQuestionTurnControllerUsesIdempotentSendWhenLookupRecoveryEvidenceIsIncomplete(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	const commandID = "cmd_b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3b3"
	f.runtime.sendErr = errors.New("simulated failure before Provider acceptance")
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("initial Provider failure was not returned")
	}
	capabilities := f.runtime.capabilities
	capabilities.SendLookup = true
	capabilities.Evidence = append(capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "question-lookup-only", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: time.Now().UTC()},
	)
	f.runtime.capabilities = capabilities
	f.runtime.sendErr = nil
	f.controller.now = func() time.Time { return time.Now().UTC() }
	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("idempotent Send fallback: %v", err)
	}
	if outcome.Observation.Status != domain.JobQuestionTurnCompleted || f.runtime.sendCalls != 2 || f.runtime.lookupCalls != 0 {
		t.Fatalf("Lookup-only capabilities blocked idempotent Send recovery: outcome=%#v sends=%d lookups=%d",
			outcome, f.runtime.sendCalls, f.runtime.lookupCalls)
	}
}

func TestJobQuestionTurnControllerKeepsExistingTerminalObservationEvidenceOnMatchingReplay(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	const commandID = "cmd_b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4"
	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	originalEvidence := outcome.Observation.EvidenceSHA256
	_, replayed, err := f.controller.persistObservation(
		context.Background(), f.jobID, outcome.Intent.ID, domain.JobQuestionTurnCompleted,
		outcome.Observation.TokensUsed, outcome.Observation.Content, outcome.Observation.ReceiptID, strings.Repeat("9", 64),
	)
	if err != nil {
		t.Fatalf("matching terminal observation replay: %v", err)
	}
	if replayed.ID != outcome.Observation.ID || replayed.EvidenceSHA256 != originalEvidence {
		t.Fatalf("matching terminal observation replaced its original evidence: first=%#v replay=%#v", outcome.Observation, replayed)
	}
}

type selectiveQuestionTurnContextResolver struct {
	failQuestionID string
	context        QuestionReadOnlyContext
}

func (r selectiveQuestionTurnContextResolver) ResolveReadOnlyBoundary(
	_ context.Context, _ domain.Job, question domain.JobMessage, _ domain.RoleExecution,
) (QuestionReadOnlyContext, error) {
	if question.ID == r.failQuestionID {
		return QuestionReadOnlyContext{}, errors.New("injected question-specific context resolution failure")
	}
	return r.context, nil
}

func TestOrchestratorContinuesAfterQuestionAuthorizationFailure(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	_, secondQuestion, err := f.database.AddJobMessage(context.Background(), f.jobID, job.Revision,
		"cmd_b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5", domain.JobMessageRequest{
			Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID},
			Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID},
			Body:      "Second question intentionally fails authorization",
		})
	if err != nil {
		t.Fatalf("Add second question: %v", err)
	}
	job, err = f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob before third question: %v", err)
	}
	_, thirdQuestion, err := f.database.AddJobMessage(context.Background(), f.jobID, job.Revision,
		"cmd_b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7", domain.JobMessageRequest{
			Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID},
			Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID},
			Body:      "Third question must still be processed",
		})
	if err != nil {
		t.Fatalf("Add third question: %v", err)
	}
	communication, err := NewCommunicationController(f.database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	resolver := selectiveQuestionTurnContextResolver{
		failQuestionID: secondQuestion.ID,
		context: QuestionReadOnlyContext{
			RepositoryID: "https://github.com/example/project",
			Boundary: domain.PlanningReadOnlyBoundary{
				Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
				SHA256: sha256String("immutable question snapshot"),
			},
		},
	}
	authorization, err := NewJobQuestionAuthorizationController(f.database, resolver)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		f.database, communication, authorization, f.controller, domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	outcomes, err := orchestrator.ProcessPlannerQuestions(context.Background(), f.jobID, f.plannerID)
	if err == nil || !strings.Contains(err.Error(), secondQuestion.ID) {
		t.Fatalf("ProcessPlannerQuestions error = %v, want second question authorization failure", err)
	}
	if len(outcomes) != 2 || outcomes[0].Reply.Message.ReplyToMessageID != f.questionID ||
		outcomes[1].Reply.Message.ReplyToMessageID != thirdQuestion.ID || f.runtime.sendCalls != 2 {
		t.Fatalf("question authorization failure blocked a later valid question: outcomes=%#v sends=%d err=%v",
			outcomes, f.runtime.sendCalls, err)
	}
}

func TestOrchestratorStopsAfterPerTurnDeadlineWithUnknownSession(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	_, _, err = f.database.AddJobMessage(context.Background(), f.jobID, job.Revision,
		"cmd_b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6b6", domain.JobMessageRequest{
			Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID},
			Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID},
			Body:      "Second question should proceed after the first request deadline",
		})
	if err != nil {
		t.Fatalf("Add second question: %v", err)
	}
	communication, err := NewCommunicationController(f.database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	authorization, err := NewJobQuestionAuthorizationController(f.database, fixedQuestionTurnContextResolver{
		context: QuestionReadOnlyContext{
			RepositoryID: "https://github.com/example/project",
			Boundary: domain.PlanningReadOnlyBoundary{
				Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "question_snapshot_1",
				SHA256: sha256String("immutable question snapshot"),
			},
		},
	})
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	orchestrator, err := NewOrchestratorController(
		f.database, communication, authorization, f.controller, domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100},
	)
	if err != nil {
		t.Fatalf("NewOrchestratorController: %v", err)
	}
	f.runtime.sendHook = func(ctx context.Context, request provider.TurnRequest) error {
		job, err := f.database.GetJob(ctx, f.jobID)
		if err != nil {
			return err
		}
		for _, intent := range job.JobQuestionTurnIntents {
			if intent.ID == request.TurnID && intent.QuestionMessageID == f.questionID {
				return context.DeadlineExceeded
			}
		}
		return nil
	}
	outcomes, err := orchestrator.ProcessPlannerQuestions(context.Background(), f.jobID, f.plannerID)
	if err == nil || !strings.Contains(err.Error(), f.questionID) {
		t.Fatalf("ProcessPlannerQuestions error = %v, want first request deadline error", err)
	}
	if len(outcomes) != 1 || outcomes[0].Observation.Status != domain.JobQuestionTurnUnknown ||
		f.runtime.sendCalls != 1 {
		t.Fatalf("unresolved Planner Session allowed another question dispatch: outcomes=%#v sends=%d err=%v",
			outcomes, f.runtime.sendCalls, err)
	}
}

func TestJobQuestionTurnReturnsExistingPlannerReplyBeforeExpiredGrantRecovery(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	const commandID = "cmd_b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4b4"
	f.runtime.sendErr = errors.New("simulated failure before Provider acceptance")
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("initial Provider failure was not returned")
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	_, _, err = f.database.AddJobMessage(context.Background(), f.jobID, job.Revision,
		"cmd_fedcba9876543210fedcba9876543213", domain.JobMessageRequest{
			Sender:           domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID},
			Recipient:        domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID},
			ReplyToMessageID: f.questionID,
			Body:             "ordinary Planner reply",
		})
	if err != nil {
		t.Fatalf("AddJobMessage: %v", err)
	}
	job, err = f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob after Planner reply: %v", err)
	}
	grant, found := findJobQuestionAuthorization(job, f.authorizationID)
	if !found {
		t.Fatal("JobQuestionAuthorization not found")
	}
	f.controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("return existing Planner reply after grant deadline: %v", err)
	}
	if outcome.Reply.Message.Body != "ordinary Planner reply" || f.runtime.sendCalls != 1 ||
		f.runtime.lookupCalls != 0 || f.runtime.resultCalls != 0 || f.runtime.usageCalls != 0 {
		t.Fatalf("existing reply did not bypass Provider recovery: outcome=%#v calls(send=%d lookup=%d result=%d usage=%d)",
			outcome, f.runtime.sendCalls, f.runtime.lookupCalls, f.runtime.resultCalls, f.runtime.usageCalls)
	}
}

func TestJobQuestionTurnDoesNotDuplicateAnOrdinaryPlannerReply(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	question, _ := findJobMessage(job, f.questionID)
	_, _, err = f.database.AddJobMessage(context.Background(), f.jobID, job.Revision,
		"cmd_b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7", domain.JobMessageRequest{
			Sender:           domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID},
			Recipient:        domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID},
			ReplyToMessageID: f.questionID,
			Body:             "ordinary Planner reply",
		})
	if err != nil {
		t.Fatalf("AddJobMessage: %v", err)
	}
	outcome, err := f.controller.persistQuestionReply(
		context.Background(), f.jobID, domain.JobQuestionAuthorization{}, question,
		domain.JobQuestionTurnIntent{}, domain.JobQuestionTurnObservation{Content: "automatic reply"},
	)
	if err != nil {
		t.Fatalf("persistQuestionReply: %v", err)
	}
	job, err = f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob after reply replay: %v", err)
	}
	if outcome.Message.Body != "ordinary Planner reply" || len(job.JobMessages) != 2 {
		t.Fatalf("ordinary reply was duplicated or lost: reply=%#v messages=%d", outcome.Message, len(job.JobMessages))
	}
}

func TestJobQuestionTurnControllerBoundsUnknownLookupRecovery(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	const commandID = "cmd_b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7b7"
	f.runtime.sendErr = &provider.OperationError{
		Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown,
	}
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100); err == nil {
		t.Fatal("ambiguous initial Provider Send was not returned")
	}

	now := time.Now().UTC()
	capabilities := f.runtime.capabilities
	capabilities.SendLookup = true
	capabilities.UsageIdentity = true
	capabilities.UsageCompleteness = true
	capabilities.Evidence = append(capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "question-lookup-timeout", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "question-usage-timeout", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "question-usage-complete-timeout", EvidenceSHA256: strings.Repeat("e", 64), EvidenceLevel: provider.EvidenceSynthetic, VerifiedAt: now},
	)
	f.runtime.capabilities = capabilities
	f.runtime.lookupHook = func(ctx context.Context, _ provider.LookupRequest) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("Lookup context has no deadline")
		}
		if remaining := time.Until(deadline); remaining <= 0 || remaining > 5*time.Second {
			return errors.New("Lookup deadline is outside the independent five-second bound")
		}
		<-ctx.Done()
		return ctx.Err()
	}

	parentCtx, cancelParent := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancelParent()
	started := time.Now()
	_, err := f.controller.Send(parentCtx, f.jobID, f.authorizationID, commandID, 100)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled Provider Lookup error = %v, want bounded deadline", err)
	}
	if elapsed := time.Since(started); elapsed > plannerQuestionRecoveryCallTimeout+time.Second {
		t.Fatalf("Provider Lookup exceeded its recovery bound: elapsed=%s", elapsed)
	}
	if f.runtime.lookupCalls != 1 || f.runtime.sendCalls != 1 {
		t.Fatalf("timed-out recovery dispatched another Provider operation: lookup=%d send=%d", f.runtime.lookupCalls, f.runtime.sendCalls)
	}
}

func TestJobQuestionTurnControllerSerializesUnresolvedTurnsAcrossQuestionGrants(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	ctx := context.Background()
	f.runtime.sendErr = &provider.OperationError{
		Class: provider.FailureAmbiguous,
		Code:  provider.OperationCodeUnknown,
	}
	if _, err := f.controller.Send(ctx, f.jobID, f.authorizationID,
		"cmd_b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", 100); err == nil {
		t.Fatal("ambiguous first Send unexpectedly succeeded")
	}
	if f.runtime.sendCalls != 1 {
		t.Fatalf("initial Provider Send calls=%d, want 1", f.runtime.sendCalls)
	}

	job, err := f.database.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob after UNKNOWN turn: %v", err)
	}
	var firstGrant domain.JobQuestionAuthorization
	for _, grant := range job.JobQuestionAuthorizations {
		if grant.ID == f.authorizationID {
			firstGrant = grant
			break
		}
	}
	if firstGrant.ID == "" {
		t.Fatal("first JobQuestionAuthorization was not persisted")
	}
	_, question, err := f.database.AddJobMessage(ctx, f.jobID, job.Revision,
		"cmd_fedcba9876543210fedcba9876543210", domain.JobMessageRequest{
			Sender:    domain.ActorRef{Kind: domain.ActorCoder, ID: f.coderID},
			Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: f.plannerID},
			Body:      "A second question for the same Planner Session.",
		})
	if err != nil {
		t.Fatalf("AddJobMessage second question: %v", err)
	}
	authorizations, err := NewJobQuestionAuthorizationController(f.database,
		fixedQuestionTurnContextResolver{context: QuestionReadOnlyContext{
			RepositoryID: firstGrant.RepositoryID,
			Boundary:     firstGrant.ReadOnlyBoundary,
		}},
	)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}
	_, secondGrant, err := authorizations.Authorize(ctx, f.jobID, question.ID,
		"cmd_fedcba9876543210fedcba9876543211",
		domain.PlanningBudget{MaxTurns: 1, MaxTokens: 10}, firstGrant.Deadline)
	if err != nil {
		t.Fatalf("Authorize second question: %v", err)
	}

	if _, err := f.controller.Send(ctx, f.jobID, secondGrant.ID,
		"cmd_fedcba9876543210fedcba9876543212", 10); err == nil ||
		!strings.Contains(err.Error(), "Provider Session is unresolved") {
		t.Fatalf("second question on unresolved Planner Session = %v, want unresolved-session rejection", err)
	}
	if f.runtime.sendCalls != 1 {
		t.Fatalf("second question was dispatched while the Planner Session had an UNKNOWN turn: sends=%d", f.runtime.sendCalls)
	}
}

func TestJobQuestionTurnIntentReceiptDistinguishesCreationFromReplay(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	ctx := context.Background()
	job, err := f.database.GetJob(ctx, f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	grant, found := findJobQuestionAuthorization(job, f.authorizationID)
	if !found {
		t.Fatal("JobQuestionAuthorization was not found")
	}
	question, found := findJobMessage(job, f.questionID)
	if !found {
		t.Fatal("source question was not found")
	}
	request := domain.JobQuestionTurnIntentRequest{
		JobQuestionAuthorizationID: grant.ID,
		CommandID:                  "cmd_fedcba9876543210fedcba9876543214",
		PromptSHA256:               sha256String(jobQuestionTurnPrompt(question.Body)),
		ReservedTokens:             100,
	}
	first, _, err := f.database.AddJobQuestionTurnIntent(ctx, f.jobID, job.Revision, request)
	if err != nil {
		t.Fatalf("first AddJobQuestionTurnIntent: %v", err)
	}
	if !first.Created {
		t.Fatal("first intent receipt did not identify the creation")
	}
	replayed, _, err := f.database.AddJobQuestionTurnIntent(ctx, f.jobID, job.Revision, request)
	if err != nil {
		t.Fatalf("replayed AddJobQuestionTurnIntent: %v", err)
	}
	if replayed.Created {
		t.Fatal("replayed intent receipt incorrectly claimed creation")
	}
}

func TestJobQuestionTurnControllerReturnsConcurrentTerminalObservationAfterLateUnknownSend(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	commandID := "cmd_fedcba9876543210fedcba9876543215"
	f.runtime.sendHook = func(ctx context.Context, request provider.TurnRequest) error {
		if request.TurnID == "" {
			return errors.New("Provider Send omitted the persisted Turn identity")
		}
		_, _, err := f.controller.persistObservation(
			ctx, f.jobID, request.TurnID, domain.JobQuestionTurnCompleted,
			5, "Recovered Planner answer", "receipt_turn_late_1",
			sha256String("late terminal observation evidence"),
		)
		if err != nil {
			return err
		}
		return &provider.OperationError{
			Class: provider.FailureAmbiguous,
			Code:  provider.OperationCodeUnknown,
		}
	}

	outcome, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID, commandID, 100)
	if err != nil {
		t.Fatalf("Send after another caller committed the terminal observation: %v", err)
	}
	if outcome.Observation.Status != domain.JobQuestionTurnCompleted ||
		outcome.Reply.Message.Body != "Recovered Planner answer" || f.runtime.sendCalls != 1 {
		t.Fatalf("late UNKNOWN overwrote or hid the committed terminal result: outcome=%#v sends=%d",
			outcome, f.runtime.sendCalls)
	}
}
