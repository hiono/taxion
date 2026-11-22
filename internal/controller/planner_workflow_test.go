package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type plannerWorkflowFixture struct {
	controller *ProviderController
	database   interface {
		GetJob(context.Context, string) (domain.Job, error)
		AddPlanningTurnIntent(context.Context, string, uint64, string, string, string, string, uint64, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnIntent, error)
		AddPlanningTurnObservation(context.Context, string, uint64, string, string, string, uint64, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnObservation, error)
	}
	runtime     *fakePlanningProviderRuntime
	principal   auth.AuthenticatedPrincipal
	jobID       string
	executionID string
	sessionID   string
	turnID      string
}


type failingJobContractProposalStore struct {
	JobContractProposalStore
	failure error
}

func (s *failingJobContractProposalStore) AddJobContractRevision(
	context.Context, string, uint64, string, string, string, string, domain.JobContract,
) (store.CommandReceipt, domain.JobContractRevision, error) {
	return store.CommandReceipt{}, domain.JobContractRevision{}, s.failure
}

func newPlannerWorkflowFixture(t *testing.T, turnStatus string, addUsage, usageComplete bool) plannerWorkflowFixture {
	t.Helper()
	runtime := &fakePlanningProviderRuntime{}
	controller, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	now := time.Now().UTC()
	runtime.capabilities.FinalOutputRetrieval = true
	runtime.capabilities.UsageIdentity = true
	runtime.capabilities.UsageCompleteness = true
	runtime.capabilities.Evidence = append(runtime.capabilities.Evidence,
		provider.CapabilityEvidence{Capability: provider.CapabilityFinalOutput, Identity: runtime.identity, TestID: "planner-workflow-test", EvidenceSHA256: strings.Repeat("a", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageIdentity, Identity: runtime.identity, TestID: "planner-workflow-test", EvidenceSHA256: strings.Repeat("b", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
		provider.CapabilityEvidence{Capability: provider.CapabilityUsageCompleteness, Identity: runtime.identity, TestID: "planner-workflow-test", EvidenceSHA256: strings.Repeat("c", 64), EvidenceLevel: provider.EvidenceLive, VerifiedAt: now},
	)
	_, execution, _, _, err := controller.StartPlanning(
		context.Background(), jobID, grantID, "cmd_31313131313131313131313131313131", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(
		context.Background(), jobID, job.Revision, execution.ID, "session_1",
		"cmd_32323232323232323232323232323232", strings.Repeat("d", 64), 100, principal,
	)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	if turnStatus != "" {
		job, err = database.GetJob(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetJob before Turn observation: %v", err)
		}
		tokensUsed := uint64(10)
		evidence := strings.Repeat("e", 64)
		if turnStatus == domain.PlanningTurnUnknown {
			tokensUsed, evidence = 0, ""
		}
		if _, _, err := database.AddPlanningTurnObservation(
			context.Background(), jobID, job.Revision, turn.ID,
			"cmd_33333333333333333333333333333333", turnStatus, tokensUsed, evidence, false, principal,
		); err != nil {
			t.Fatalf("AddPlanningTurnObservation: %v", err)
		}
	}
	if addUsage {
		runtime.usageResult = provider.Usage{
			UsageID: "usage_planner_1", Tokens: 12, Complete: usageComplete,
			EvidenceSHA256: strings.Repeat("f", 64),
		}
		if _, _, err := controller.RetrieveProviderUsage(
			context.Background(), jobID, execution.ID, "session_1", turn.ID, principal,
		); err != nil {
			t.Fatalf("RetrieveProviderUsage: %v", err)
		}
	}
	contract := domain.JobContract{
		Purpose:             "Complete the requested planning task",
		TargetRepositories:  []string{"https://github.com/example/project"},
		Workspaces:          []string{"workspace-main"},
		AcceptanceCriteria:  []string{"The requested outcome is produced"},
		RequiredEvidence:    []string{"A final verified result is available"},
		Budget:              domain.JobContractBudget{MaxTurns: 3, MaxTokens: 1000},
		ConcurrencyCeiling:  1,
		Deadline:            time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second),
		ConfigurationSHA256: strings.Repeat("a", 64),
	}
	content, err := json.Marshal(contract)
	if err != nil {
		t.Fatalf("marshal Planner JobContract Result: %v", err)
	}
	runtime.resultResult = provider.Result{
		OperationID: turn.OperationID, SessionID: "session_1", TurnID: turn.ID,
		Content: string(content), ResultSHA256: sha256String(string(content)),
		Final: true, EvidenceSHA256: strings.Repeat("9", 64),
	}
	return plannerWorkflowFixture{
		controller: controller, database: database, runtime: runtime, principal: principal,
		jobID: jobID, executionID: execution.ID, sessionID: "session_1", turnID: turn.ID,
	}
}

func (f plannerWorkflowFixture) workflow(t *testing.T) *PlannerWorkflowController {
	t.Helper()
	workflow, err := NewPlannerWorkflowController(f.controller)
	if err != nil {
		t.Fatalf("NewPlannerWorkflowController: %v", err)
	}
	return workflow
}

func TestPlannerWorkflowPersistsFinalResultBeforeProposingJobContract(t *testing.T) {
	fixture := newPlannerWorkflowFixture(t, domain.PlanningTurnCompleted, true, true)
	workflow := fixture.workflow(t)
	outcome, err := workflow.ProposeJobContract(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID,
		"cmd_34343434343434343434343434343434", fixture.principal,
	)
	if err != nil {
		t.Fatalf("ProposeJobContract: %v", err)
	}
	if outcome.ResultReceipt == nil || outcome.ResultReused || outcome.Result.ID == "" ||
		!outcome.Result.Final || outcome.Revision.SourceResultObservationID != outcome.Result.ID ||
		outcome.Revision.SourceResultContentSHA256 != outcome.Result.ContentSHA256 ||
		outcome.Revision.ProposedByRoleExecutionID != fixture.executionID {
		t.Fatalf("workflow did not preserve final Result provenance: %#v", outcome)
	}
	job, err := fixture.database.GetJob(context.Background(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob after proposal: %v", err)
	}
	if len(job.ProviderResultObservations) != 1 || len(job.JobContractRevisions) != 1 ||
		job.ProviderResultObservations[0].CreatedRevision >= job.JobContractRevisions[0].CreatedRevision {
		t.Fatalf("Result was not persisted before the JobContract proposal: results=%#v revisions=%#v",
			job.ProviderResultObservations, job.JobContractRevisions)
	}
	if fixture.runtime.resultCalls != 1 {
		t.Fatalf("Provider Result calls = %d, want 1", fixture.runtime.resultCalls)
	}

	replayed, err := workflow.ProposeJobContract(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID,
		"cmd_34343434343434343434343434343434", fixture.principal,
	)
	if err != nil {
		t.Fatalf("exact proposal replay: %v", err)
	}
	if !replayed.ResultReused || replayed.ResultReceipt != nil ||
		replayed.Result.ID != outcome.Result.ID || replayed.ProposalReceipt != outcome.ProposalReceipt ||
		replayed.Revision.ID != outcome.Revision.ID || fixture.runtime.resultCalls != 1 {
		t.Fatalf("exact replay did not reuse the persisted Result and proposal: first=%#v replay=%#v resultCalls=%d",
			outcome, replayed, fixture.runtime.resultCalls)
	}
}

func TestPlannerWorkflowReusesExistingFinalResult(t *testing.T) {
	fixture := newPlannerWorkflowFixture(t, domain.PlanningTurnCompleted, true, true)
	_, existing, err := fixture.controller.RetrieveProviderResult(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID, fixture.principal,
	)
	if err != nil {
		t.Fatalf("RetrieveProviderResult: %v", err)
	}
	if fixture.runtime.resultCalls != 1 {
		t.Fatalf("initial Provider Result calls = %d, want 1", fixture.runtime.resultCalls)
	}
	outcome, err := fixture.workflow(t).ProposeJobContract(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID,
		"cmd_35353535353535353535353535353535", fixture.principal,
	)
	if err != nil {
		t.Fatalf("ProposeJobContract with existing Result: %v", err)
	}
	if !outcome.ResultReused || outcome.ResultReceipt != nil || outcome.Result.ID != existing.ID ||
		fixture.runtime.resultCalls != 1 {
		t.Fatalf("existing final Result was not reused: %#v calls=%d", outcome, fixture.runtime.resultCalls)
	}
}

func TestPlannerWorkflowRejectsIncompleteTurnOrUsageBeforeResultRetrieval(t *testing.T) {
	tests := []struct {
		name          string
		turnStatus    string
		addUsage      bool
		usageComplete bool
	}{
		{name: "missing turn observation", turnStatus: "", addUsage: true, usageComplete: true},
		{name: "unknown turn", turnStatus: domain.PlanningTurnUnknown, addUsage: true, usageComplete: true},
		{name: "failed turn", turnStatus: domain.PlanningTurnFailed, addUsage: true, usageComplete: true},
		{name: "missing usage", turnStatus: domain.PlanningTurnCompleted, addUsage: false},
		{name: "partial usage", turnStatus: domain.PlanningTurnCompleted, addUsage: true, usageComplete: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerWorkflowFixture(t, test.turnStatus, test.addUsage, test.usageComplete)
			_, err := fixture.workflow(t).ProposeJobContract(
				context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID,
				"cmd_36363636363636363636363636363636", fixture.principal,
			)
			if err == nil {
				t.Fatal("incomplete Planner workflow was accepted")
			}
			if fixture.runtime.resultCalls != 0 {
				t.Fatalf("Provider Result was retrieved before prerequisites were satisfied: %d calls", fixture.runtime.resultCalls)
			}
			job, readErr := fixture.database.GetJob(context.Background(), fixture.jobID)
			if readErr != nil {
				t.Fatalf("GetJob after rejection: %v", readErr)
			}
			if len(job.ProviderResultObservations) != 0 || len(job.JobContractRevisions) != 0 {
				t.Fatalf("rejected workflow mutated Result / JobContract facts: results=%#v revisions=%#v",
					job.ProviderResultObservations, job.JobContractRevisions)
			}
		})
	}
}


func TestPlannerWorkflowRetainsPersistedResultWhenProposalStoreFails(t *testing.T) {
	fixture := newPlannerWorkflowFixture(t, domain.PlanningTurnCompleted, true, true)
	workflow := fixture.workflow(t)
	originalStore := fixture.controller.planning.jobContract.store
	fixture.controller.planning.jobContract.store = &failingJobContractProposalStore{
		JobContractProposalStore: originalStore,
		failure: errors.New("injected JobContract persistence failure"),
	}
	outcome, err := workflow.ProposeJobContract(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID,
		"cmd_38383838383838383838383838383838", fixture.principal,
	)
	if err == nil || outcome.ResultReceipt == nil || outcome.Result.ID == "" {
		t.Fatalf("proposal failure did not preserve the persisted Result outcome: %#v err=%v", outcome, err)
	}
	job, readErr := fixture.database.GetJob(context.Background(), fixture.jobID)
	if readErr != nil {
		t.Fatalf("GetJob after injected proposal failure: %v", readErr)
	}
	if len(job.ProviderResultObservations) != 1 || len(job.JobContractRevisions) != 0 {
		t.Fatalf("proposal failure left inconsistent facts: results=%#v revisions=%#v",
			job.ProviderResultObservations, job.JobContractRevisions)
	}

	fixture.controller.planning.jobContract.store = originalStore
	retried, err := workflow.ProposeJobContract(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, fixture.turnID,
		"cmd_38383838383838383838383838383838", fixture.principal,
	)
	if err != nil {
		t.Fatalf("retry after proposal persistence failure: %v", err)
	}
	if !retried.ResultReused || retried.ResultReceipt != nil ||
		retried.Result.ID != outcome.Result.ID || fixture.runtime.resultCalls != 1 {
		t.Fatalf("retry did not reuse the original persisted Result: first=%#v retry=%#v calls=%d",
			outcome, retried, fixture.runtime.resultCalls)
	}
	job, readErr = fixture.database.GetJob(context.Background(), fixture.jobID)
	if readErr != nil {
		t.Fatalf("GetJob after successful retry: %v", readErr)
	}
	if len(job.ProviderResultObservations) != 1 || len(job.JobContractRevisions) != 1 ||
		job.JobContractRevisions[0].SourceResultObservationID != outcome.Result.ID {
		t.Fatalf("retry did not append exactly one provenance-bound revision: results=%#v revisions=%#v",
			job.ProviderResultObservations, job.JobContractRevisions)
	}
}

func TestPlannerWorkflowRejectsMismatchedTurnIdentityBeforeProviderCall(t *testing.T) {
	fixture := newPlannerWorkflowFixture(t, domain.PlanningTurnCompleted, true, true)
	_, err := fixture.workflow(t).ProposeJobContract(
		context.Background(), fixture.jobID, fixture.executionID, fixture.sessionID, "turn_foreign",
		"cmd_37373737373737373737373737373737", fixture.principal,
	)
	if err == nil {
		t.Fatal("foreign Turn identity was accepted")
	}
	if fixture.runtime.resultCalls != 0 {
		t.Fatalf("mismatched Turn caused Provider Result call: %d", fixture.runtime.resultCalls)
	}
	job, readErr := fixture.database.GetJob(context.Background(), fixture.jobID)
	if readErr != nil {
		t.Fatalf("GetJob after rejection: %v", readErr)
	}
	if len(job.JobContractRevisions) != 0 {
		t.Fatalf("mismatched Turn created a JobContract revision: %#v", job.JobContractRevisions)
	}
}
