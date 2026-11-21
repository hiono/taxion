package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

func validControllerJobContract(deadline time.Time) domain.JobContract {
	return domain.JobContract{
		Purpose:                 "Deliver the approved feature",
		Constraints:             []string{"Preserve authentication boundaries"},
		TargetRepositories:      []string{"https://github.com/example/project"},
		Workspaces:              []string{"workspace/main"},
		AcceptanceCriteria:      []string{"All required Linux checks pass"},
		RequiredEvidence:        []string{"Linux CI result"},
		Budget:                  domain.JobContractBudget{MaxTurns: 5, MaxTokens: 5000},
		ConcurrencyCeiling:      1,
		Deadline:                deadline,
		ProhibitedEffects:       []string{"publish"},
		ApprovalRequiredEffects: []string{"external.delete"},
		ConfigurationSHA256:     strings.Repeat("a", 64),
	}
}

func startContractPlanner(t *testing.T) (*JobContractController, *store.Store, auth.AuthenticatedPrincipal, string, string, domain.ProviderResultObservation) {
	t.Helper()
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, _, _, _, err := providerController.StartPlanning(
		context.Background(), jobID, grantID,
		"cmd_cccccccccccccccccccccccccccccccc", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Turn: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(context.Background(), jobID, job.Revision,
		domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"), "session_1",
		"cmd_11111111111111111111111111111111", strings.Repeat("a", 64), 100, principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Turn observation: %v", err)
	}
	if _, _, err := database.AddPlanningTurnObservation(context.Background(), jobID, job.Revision,
		turn.ID, "cmd_22222222222222222222222222222222", domain.PlanningTurnCompleted, 10,
		strings.Repeat("b", 64), false, principal); err != nil {
		t.Fatalf("AddPlanningTurnObservation: %v", err)
	}
	job, err = database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before Planner Result: %v", err)
	}
	content := "Final Planner result"
	_, sourceResult, err := database.AddProviderResultObservation(context.Background(), jobID, job.Revision,
		domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"), turn.OperationID, turn.ID,
		"session_1", content, sha256String(content), true, strings.Repeat("c", 64), principal)
	if err != nil {
		t.Fatalf("AddProviderResultObservation: %v", err)
	}
	contractController, err := NewJobContractController(database)
	if err != nil {
		t.Fatalf("NewJobContractController: %v", err)
	}
	return contractController, database, principal, jobID, grantID, sourceResult
}

func TestJobContractControllerProposesForAuthorizedPlanner(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult := startContractPlanner(t)
	ctx := context.Background()
	commandID := "cmd_dddddddddddddddddddddddddddddddd"
	contract := validControllerJobContract(time.Now().UTC().Add(2 * time.Hour))
	receipt, revision, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, sourceResult.ContentSHA256, commandID, contract, principal,
	)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if receipt.Revision != job.Revision || revision.Revision != 1 ||
		job.State != domain.JobDraft || len(job.JobContractRevisions) != 1 ||
		len(job.JobApprovals) != 0 || job.Goal != revision.Contract.Purpose {
		t.Fatalf("unexpected proposal state: receipt=%#v revision=%#v job=%#v", receipt, revision, job)
	}
}

func TestJobContractControllerRejectsPrincipalMismatchAndExpiredGrant(t *testing.T) {
	controller, database, principal, jobID, grantID, sourceResult := startContractPlanner(t)
	ctx := context.Background()
	otherPrincipal := newProviderTestPrincipal(t)
	contract := validControllerJobContract(time.Now().UTC().Add(3 * time.Hour))
	if _, _, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, sourceResult.ContentSHA256, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", contract, otherPrincipal,
	); err == nil {
		t.Fatal("proposal with a different HUMAN Principal was accepted")
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	var grant domain.PlanningAuthorization
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == grantID {
			grant = candidate
			break
		}
	}
	if grant.ID == "" || grant.PrincipalID != principal.PrincipalID() {
		t.Fatalf("expected PlanningAuthorization was not found: %#v", grant)
	}
	controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	if _, _, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, sourceResult.ContentSHA256, "cmd_ffffffffffffffffffffffffffffffff", contract, principal,
	); err == nil {
		t.Fatal("proposal after PlanningAuthorization deadline was accepted")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.JobContractRevisions) != 0 {
		t.Fatalf("rejected proposal changed Job: revisions=%d err=%v", len(job.JobContractRevisions), err)
	}
}

func TestJobContractControllerAllowsExactReplayAfterGrantExpiry(t *testing.T) {
	controller, database, principal, jobID, grantID, sourceResult := startContractPlanner(t)
	ctx := context.Background()
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	var grant domain.PlanningAuthorization
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == grantID {
			grant = candidate
			break
		}
	}
	commandID := "cmd_12121212121212121212121212121212"
	contract := validControllerJobContract(time.Now().UTC().Add(3 * time.Hour))
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	firstReceipt, firstRevision, err := controller.Propose(ctx, jobID, roleExecutionID, sourceResult.ID, sourceResult.ContentSHA256, commandID, contract, principal)
	if err != nil {
		t.Fatalf("initial Propose: %v", err)
	}
	controller.now = func() time.Time { return grant.Deadline.Add(time.Second) }
	retryReceipt, retryRevision, err := controller.Propose(ctx, jobID, roleExecutionID, sourceResult.ID, sourceResult.ContentSHA256, commandID, contract, principal)
	if err != nil {
		t.Fatalf("idempotent replay after expiry: %v", err)
	}
	if retryReceipt.CommandID != firstReceipt.CommandID ||
		retryReceipt.Revision != firstReceipt.Revision ||
		retryRevision.ID != firstRevision.ID ||
		retryRevision.CanonicalHash != firstRevision.CanonicalHash {
		t.Fatalf("retry did not return original proposal: first=%#v/%#v retry=%#v/%#v",
			firstReceipt, firstRevision, retryReceipt, retryRevision)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || len(job.JobContractRevisions) != 1 {
		t.Fatalf("idempotent replay appended another revision: revisions=%d err=%v", len(job.JobContractRevisions), err)
	}
}

func TestJobContractControllerRejectsNewProposalAfterApproval(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult := startContractPlanner(t)
	ctx := context.Background()
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	contract := validControllerJobContract(time.Now().UTC().Add(2 * time.Hour))
	_, revision, err := controller.Propose(
		ctx, jobID, roleExecutionID, sourceResult.ID, sourceResult.ContentSHA256, "cmd_23232323232323232323232323232323", contract, principal,
	)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if _, _, err := database.AddJobApproval(
		ctx, jobID, job.Revision, "cmd_24242424242424242424242424242424",
		revision.Revision, revision.CanonicalHash, revision.CanonicalizationVersion,
		"Approved the exact proposal", principal,
	); err != nil {
		t.Fatalf("AddJobApproval: %v", err)
	}
	if _, _, err := controller.Propose(
		ctx, jobID, roleExecutionID, sourceResult.ID, sourceResult.ContentSHA256, "cmd_25252525252525252525252525252525",
		contract, principal,
	); err == nil {
		t.Fatal("new proposal through the DRAFT Planner authority was accepted after approval")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobContractRevisions) != 1 || len(job.JobApprovals) != 1 {
		t.Fatalf("rejected ACTIVE amendment changed Job: %#v err=%v", job, err)
	}
}

func TestJobContractControllerRejectsCommandIDReuseByDifferentPlanner(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult := startContractPlanner(t)
	ctx := context.Background()
	commandID := "cmd_34343434343434343434343434343434"
	contract := validControllerJobContract(time.Now().UTC().Add(2 * time.Hour))
	roleExecutionID := domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc")
	if _, _, err := controller.Propose(ctx, jobID, roleExecutionID, sourceResult.ID, sourceResult.ContentSHA256, commandID, contract, principal); err != nil {
		t.Fatalf("initial Propose: %v", err)
	}
	if _, _, err := controller.Propose(
		ctx, jobID, "role_exec_00000000000000000000000000000000",
		sourceResult.ID, sourceResult.ContentSHA256, commandID, contract, principal,
	); err == nil {
		t.Fatal("same CommandID with a different proposer was accepted")
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || len(job.JobContractRevisions) != 1 {
		t.Fatalf("CommandID conflict changed Job: revisions=%d err=%v", len(job.JobContractRevisions), err)
	}
}

func TestJobContractControllerApprovesExactRevisionAndReplaysAfterActivation(t *testing.T) {
	controller, database, principal, jobID, _, sourceResult := startContractPlanner(t)
	ctx := context.Background()
	contract := validControllerJobContract(time.Now().UTC().Add(2 * time.Hour))
	_, revision, err := controller.Propose(
		ctx, jobID, domain.RoleExecutionID("cmd_cccccccccccccccccccccccccccccccc"),
		sourceResult.ID, sourceResult.ContentSHA256, "cmd_45454545454545454545454545454545", contract, principal,
	)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	approvalCommandID := "cmd_56565656565656565656565656565656"
	firstReceipt, firstApproval, err := controller.Approve(
		ctx, jobID, approvalCommandID, revision.Revision,
		revision.CanonicalHash, revision.CanonicalizationVersion,
		"Reviewed and approved the exact bounded scope", principal,
	)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 ||
		firstApproval.ContractRevision != revision.Revision ||
		firstApproval.CanonicalHash != revision.CanonicalHash ||
		firstApproval.ApproverPrincipalID != principal.PrincipalID() {
		t.Fatalf("approval did not activate exact revision: job=%#v approval=%#v err=%v", job, firstApproval, err)
	}
	retryReceipt, retryApproval, err := controller.Approve(
		ctx, jobID, approvalCommandID, revision.Revision,
		revision.CanonicalHash, revision.CanonicalizationVersion,
		"Reviewed and approved the exact bounded scope", principal,
	)
	if err != nil {
		t.Fatalf("exact approval replay: %v", err)
	}
	if retryReceipt.CommandID != firstReceipt.CommandID ||
		retryReceipt.Revision != firstReceipt.Revision ||
		retryApproval.ID != firstApproval.ID {
		t.Fatalf("approval replay changed receipt or fact: first=%#v/%#v retry=%#v/%#v",
			firstReceipt, firstApproval, retryReceipt, retryApproval)
	}
	if _, _, err := controller.Approve(
		ctx, jobID, "cmd_67676767676767676767676767676767",
		revision.Revision, strings.Repeat("0", 64),
		revision.CanonicalizationVersion, "Wrong hash", principal,
	); err == nil {
		t.Fatal("approval with a different canonical hash was accepted")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 {
		t.Fatalf("rejected approval changed Job: %#v err=%v", job, err)
	}
}
