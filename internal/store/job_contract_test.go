package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

func createJobContractPlanner(t *testing.T, database *Store, jobID string, principal auth.AuthenticatedPrincipal) (domain.RoleExecution, uint64) {
	t.Helper()
	ctx := context.Background()
	createCommand := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	grantCommand := "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	startCommand := "cmd_cccccccccccccccccccccccccccccccc"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Propose a bounded implementation.", createCommand, principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	now := time.Now().UTC()
	request := domain.PlanningAuthorizationRequest{
		ProviderID: "provider.test", AccountID: "account.primary", ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:     "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1", SHA256: strings.Repeat("c", 64)},
		Budget:           domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000}, Deadline: now.Add(2 * time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommand, request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommand, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	_, execution, start, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommand, principal)
	if err != nil {
		t.Fatalf("StartTasklessPlanner: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Start: %v", err)
	}
	_, _, err = database.AddProviderStartObservation(
		ctx, jobID, job.Revision, start.ID, "cmd_dddddddddddddddddddddddddddddddd",
		domain.ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("e", 64), false, principal,
	)
	if err != nil {
		t.Fatalf("AddProviderStartObservation: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after accepted Start: %v", err)
	}
	return execution, job.Revision
}

func validStoreJobContract() domain.JobContract {
	return domain.JobContract{
		Purpose:            "Deliver the approved feature",
		Constraints:        []string{"Linux only", "no network writes"},
		TargetRepositories: []string{"repo_taxion"},
		TargetResources:    []string{"resource_ci"},
		Workspaces:         []string{"workspace_taxion"},
		AcceptanceCriteria: []string{"Tests pass", "No secret leakage"},
		RequiredEvidence:   []string{"Linux CI", "Secret scan"},
		Budget:             domain.JobContractBudget{MaxTurns: 3, MaxTokens: 1000},
		ConcurrencyCeiling: 1, Deadline: time.Now().UTC().Add(time.Hour),
		ConfigurationSHA256: strings.Repeat("f", 64),
	}
}

func TestJobContractAndApprovalStoreTransactions(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	execution, revision := createJobContractPlanner(t, database, "job_contract_store", principal)
	commandID := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	contract := validStoreJobContract()
	receipt, contractRevision, err := database.AddJobContractRevision(
		ctx, "job_contract_store", revision, commandID, execution.ID, contract,
	)
	if err != nil || contractRevision.Revision != 1 || receipt.Revision != revision+1 {
		t.Fatalf("AddJobContractRevision: %#v %#v %v", receipt, contractRevision, err)
	}
	retryReceipt, retryRevision, err := database.AddJobContractRevision(
		ctx, "job_contract_store", receipt.Revision, commandID, execution.ID, contract,
	)
	if err != nil || retryReceipt.CommandID != receipt.CommandID || retryReceipt.Revision != receipt.Revision || retryRevision.ID != contractRevision.ID || retryRevision.CanonicalHash != contractRevision.CanonicalHash {
		t.Fatalf("contract revision idempotency: %#v %#v %v", retryReceipt, retryRevision, err)
	}
	job, err := database.GetJob(ctx, "job_contract_store")
	if err != nil || job.State != domain.JobDraft || job.Goal != contractRevision.Contract.Purpose {
		t.Fatalf("Job after contract proposal: state=%s goal=%q err=%v", job.State, job.Goal, err)
	}
	approvalCommand := "cmd_ffffffffffffffffffffffffffffffff"
	approvalReceipt, approval, err := database.AddJobApproval(
		ctx, job.ID, job.Revision, approvalCommand, contractRevision.Revision,
		contractRevision.CanonicalHash, contractRevision.CanonicalizationVersion,
		"Approved the exact bounded scope", principal,
	)
	if err != nil || approval.ContractRevision != contractRevision.Revision || approvalReceipt.Revision != job.Revision+1 {
		t.Fatalf("AddJobApproval: %#v %#v %v", approvalReceipt, approval, err)
	}
	job, err = database.GetJob(ctx, "job_contract_store")
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 {
		t.Fatalf("Job after approval: state=%s approvals=%d err=%v", job.State, len(job.JobApprovals), err)
	}
	retryApprovalReceipt, retryApproval, err := database.AddJobApproval(
		ctx, job.ID, job.Revision, approvalCommand, contractRevision.Revision,
		contractRevision.CanonicalHash, contractRevision.CanonicalizationVersion,
		"Approved the exact bounded scope", principal,
	)
	if err != nil || retryApprovalReceipt.CommandID != approvalReceipt.CommandID || retryApprovalReceipt.Revision != approvalReceipt.Revision || retryApproval.ID != approval.ID || retryApproval.CanonicalHash != approval.CanonicalHash {
		t.Fatalf("approval idempotency: %#v %#v %v", retryApprovalReceipt, retryApproval, err)
	}
	newContract := validStoreJobContract()
	newContract.Purpose = "Deliver the revised feature"
	if _, _, err := database.AddJobContractRevision(
		ctx, job.ID, job.Revision, "cmd_12121212121212121212121212121212", execution.ID, newContract,
	); err == nil {
		t.Fatal("ACTIVE Job accepted a contract amendment through the DRAFT Planner authority")
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil || job.State != domain.JobActive || len(job.JobApprovals) != 1 || len(job.JobContractRevisions) != 1 {
		t.Fatalf("rejected amendment changed approved Job: %#v %v", job, err)
	}
	if _, _, err := database.AddJobApproval(
		ctx, job.ID, job.Revision, "cmd_13131313131313131313131313131313",
		contractRevision.Revision, strings.Repeat("0", 64), contractRevision.CanonicalizationVersion,
		"Wrong hash", principal,
	); err == nil {
		t.Fatal("JobApproval with wrong canonical hash was accepted")
	}
	if _, _, err := database.AddJobApproval(
		ctx, job.ID, job.Revision, "cmd_14141414141414141414141414141414",
		contractRevision.Revision, contractRevision.CanonicalHash, contractRevision.CanonicalizationVersion,
		"Duplicate approval", principal,
	); err == nil {
		t.Fatal("second JobApproval for the same revision was accepted")
	}
}

func TestJobContractStoreRollbackLeavesNoFactOrReceipt(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	execution, revision := createJobContractPlanner(t, database, "job_contract_rollback", principal)
	commandID := "cmd_15151515151515151515151515151515"
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_job_contract_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_id = '`+commandID+`'
		BEGIN
			SELECT RAISE(ABORT, 'forced JobContract receipt failure');
		END;`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}
	if _, _, err := database.AddJobContractRevision(
		ctx, "job_contract_rollback", revision, commandID, execution.ID, validStoreJobContract(),
	); err == nil {
		t.Fatal("JobContract revision succeeded despite forced receipt failure")
	}
	job, err := database.GetJob(ctx, "job_contract_rollback")
	if err != nil || job.Revision != revision || len(job.JobContractRevisions) != 0 {
		t.Fatalf("failed JobContract command partially changed Job: %#v %v", job, err)
	}
	var count int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_commands WHERE command_id = ?", commandID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed command left receipt count=%d err=%v", count, err)
	}
}
