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

type fixedQuestionTurnContextResolver struct {
	context QuestionReadOnlyContext
}

func (r fixedQuestionTurnContextResolver) ResolveReadOnlyBoundary(
	context.Context, domain.Job, domain.JobMessage, domain.RoleExecution,
) (QuestionReadOnlyContext, error) {
	return r.context, nil
}

type jobQuestionTurnFixture struct {
	controller                                                        *JobQuestionTurnController
	runtime                                                           *fakePlanningProviderRuntime
	database                                                          *store.Store
	jobID, authorizationID, questionID, coderID, plannerID, sessionID string
}

func newJobQuestionTurnFixture(t *testing.T) jobQuestionTurnFixture {
	t.Helper()
	ctx := context.Background()
	planning, database, principal, jobID, _, sourceResult, contract := startContractPlanner(t)
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
		Identity: runtime.identity, SendIdempotency: true,
		Evidence: []provider.CapabilityEvidence{{
			Capability: provider.CapabilitySendIdempotency, Identity: runtime.identity,
			TestID: "question-turn", EvidenceSHA256: strings.Repeat("a", 64),
			EvidenceLevel: provider.EvidenceLive, VerifiedAt: time.Now().UTC(),
		}},
	}
	controller, err := NewJobQuestionTurnController(database, runtime)
	if err != nil {
		t.Fatalf("NewJobQuestionTurnController: %v", err)
	}
	return jobQuestionTurnFixture{
		controller: controller, runtime: runtime, database: database, jobID: jobID,
		authorizationID: authorization.ID, questionID: question.ID,
		coderID: coderStart.RoleExecution.ID, plannerID: plannerID, sessionID: sessionID,
	}
}

func TestJobQuestionTurnControllerSendsReadOnlyTurnAndRepliesToOriginalRole(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
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
		Identity: f.runtime.identity, SendLookup: true, FinalOutputRetrieval: true,
		UsageIdentity: true, UsageCompleteness: true,
		Evidence: []provider.CapabilityEvidence{
			{Capability: provider.CapabilitySendLookup, Identity: f.runtime.identity, TestID: "lookup", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityFinalOutput, Identity: f.runtime.identity, TestID: "result", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityUsageIdentity, Identity: f.runtime.identity, TestID: "usage-id", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
			{Capability: provider.CapabilityUsageCompleteness, Identity: f.runtime.identity, TestID: "usage-complete", EvidenceSHA256: strings.Repeat("d", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
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

func TestJobQuestionTurnControllerRejectsProviderWithoutSafeSend(t *testing.T) {
	f := newJobQuestionTurnFixture(t)
	f.runtime.capabilities = provider.Capabilities{Identity: f.runtime.identity}
	if _, err := f.controller.Send(context.Background(), f.jobID, f.authorizationID,
		"cmd_a3a3a3a3a3a3a3a3a3a3a3a3a3a3a3", 100); !errors.Is(err, provider.ErrUnsupportedCapability) {
		t.Fatalf("Send without safe capability = %v, want ErrUnsupportedCapability", err)
	}
	job, err := f.database.GetJob(context.Background(), f.jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if len(job.JobQuestionTurnIntents) != 0 || f.runtime.sendCalls != 0 {
		t.Fatalf("unsupported Provider Send created intent or ran Provider: %#v", job.JobQuestionTurnIntents)
	}
}
