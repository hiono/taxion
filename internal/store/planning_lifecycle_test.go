package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func TestPlanningLifecycleFactsPersistAtomicallyAndReconcileUnknown(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_planning_lifecycle"
	createCommand := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	grantCommand := "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	startCommand := "cmd_cccccccccccccccccccccccccccccccc"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Plan safely.", createCommand, principal.AuthenticatedAt(), principal); err != nil {
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
		Budget:           domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000}, Deadline: now.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommand, request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommand, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	_, _, _, err = database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommand, principal)
	if err != nil {
		t.Fatalf("StartTasklessPlanner: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after start: %v", err)
	}
	executionID := job.RoleExecutions[0].ID
	contextDigest := job.PlanningAuthorizations[0].ReadOnlyBoundary.SHA256

	turnCommand := "cmd_dddddddddddddddddddddddddddddddd"
	_, err = database.db.ExecContext(ctx, `CREATE TRIGGER fail_planning_lifecycle_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_id = '`+turnCommand+`'
		BEGIN
			SELECT RAISE(ABORT, 'forced planning lifecycle receipt failure');
		END;`)
	if err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}
	if _, _, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, executionID, "session_exact", turnCommand, strings.Repeat("d", 64), 500, principal); err == nil {
		t.Fatal("planning turn succeeded despite forced receipt insertion failure")
	}
	afterRollback, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after forced rollback: %v", err)
	}
	if afterRollback.Revision != job.Revision || len(afterRollback.PlanningTurnIntents) != 0 {
		t.Fatalf("failed command partially changed Job: revision=%d turns=%d", afterRollback.Revision, len(afterRollback.PlanningTurnIntents))
	}
	var receiptCount int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_commands WHERE command_id = ?", turnCommand).Scan(&receiptCount); err != nil {
		t.Fatalf("count failed command receipts: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("failed command left %d receipts", receiptCount)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_planning_lifecycle_receipt"); err != nil {
		t.Fatalf("drop rollback trigger: %v", err)
	}
	turnReceipt, turn, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, executionID, "session_exact", turnCommand, strings.Repeat("d", 64), 500, principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	retryReceipt, retryTurn, err := database.AddPlanningTurnIntent(ctx, jobID, turnReceipt.Revision, executionID, "session_exact", turnCommand, strings.Repeat("d", 64), 500, principal)
	if err != nil || retryReceipt != turnReceipt || retryTurn != turn {
		t.Fatalf("idempotent turn retry: %#v %#v %v", retryReceipt, retryTurn, err)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after turn: %v", err)
	}
	unknownCommand := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	unknownReceipt, unknown, err := database.AddPlanningTurnObservation(ctx, jobID, job.Revision, turn.ID, unknownCommand, domain.PlanningTurnUnknown, 0, "", false, principal)
	if err != nil {
		t.Fatalf("record UNKNOWN: %v", err)
	}
	if unknown.Status != domain.PlanningTurnUnknown || unknownReceipt.Revision != job.Revision+1 {
		t.Fatalf("UNKNOWN observation: %#v %#v", unknownReceipt, unknown)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after UNKNOWN: %v", err)
	}
	if _, _, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, executionID, "session_exact", "cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("e", 64), 100, principal); err == nil {
		t.Fatal("new turn was allowed while prior turn remained UNKNOWN")
	}
	reconcileCommand := "cmd_11111111111111111111111111111111"
	_, resolved, err := database.AddPlanningTurnObservation(ctx, jobID, job.Revision, turn.ID, reconcileCommand, domain.PlanningTurnCompleted, 120, strings.Repeat("f", 64), true, principal)
	if err != nil {
		t.Fatalf("reconcile UNKNOWN: %v", err)
	}
	if !resolved.ReconcilesUnknown || resolved.TokensUsed != 120 {
		t.Fatalf("reconciled observation: %#v", resolved)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before effect: %v", err)
	}
	_, allowed, err := database.AddPlanningEffectDecision(ctx, jobID, job.Revision, executionID, "cmd_22222222222222222222222222222222", domain.PlanningEffectRepositoryRead, contextDigest, principal)
	if err != nil || allowed.Decision != domain.PlanningEffectAllow {
		t.Fatalf("repository.read decision: %#v %v", allowed, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before denied effect: %v", err)
	}
	_, denied, err := database.AddPlanningEffectDecision(ctx, jobID, job.Revision, executionID, "cmd_33333333333333333333333333333333", "repository.write", contextDigest, principal)
	if err != nil || denied.Decision != domain.PlanningEffectDeny {
		t.Fatalf("repository.write decision: %#v %v", denied, err)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before boundary denial: %v", err)
	}
	boundaryReceipt, boundaryDecision, err := database.AddPlanningBoundaryDecision(ctx, jobID, job.Revision, executionID, "cmd_66666666666666666666666666666666", domain.PlanningBoundaryTurn, "token_budget_exhausted", principal)
	if err != nil {
		t.Fatalf("AddPlanningBoundaryDecision: %v", err)
	}
	if boundaryDecision.Decision != domain.PlanningEffectDeny || boundaryDecision.ReasonCode != "token_budget_exhausted" {
		t.Fatalf("boundary denial fact: %#v", boundaryDecision)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before boundary retry: %v", err)
	}
	retryBoundaryReceipt, retryBoundaryDecision, err := database.AddPlanningBoundaryDecision(ctx, jobID, job.Revision, executionID, "cmd_66666666666666666666666666666666", domain.PlanningBoundaryTurn, "token_budget_exhausted", principal)
	if err != nil || retryBoundaryReceipt != boundaryReceipt || retryBoundaryDecision != boundaryDecision {
		t.Fatalf("idempotent boundary decision retry: %#v %#v %v", retryBoundaryReceipt, retryBoundaryDecision, err)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before resume: %v", err)
	}
	if _, _, err := database.AddPlanningResumeIntent(ctx, jobID, job.Revision, executionID, "session_other", "cmd_44444444444444444444444444444444", strings.Repeat("1", 64), true, principal); err == nil {
		t.Fatal("resume accepted a different Provider Session")
	}
	_, resume, err := database.AddPlanningResumeIntent(ctx, jobID, job.Revision, executionID, "session_exact", "cmd_55555555555555555555555555555555", strings.Repeat("1", 64), true, principal)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before resume UNKNOWN: %v", err)
	}
	unknownResumeRevision := job.Revision
	if _, _, err := database.AddPlanningResumeObservation(ctx, jobID, unknownResumeRevision, resume.ID, "cmd_77777777777777777777777777777777", "INVALID", "", false, principal); err == nil {
		t.Fatal("invalid resume observation was persisted")
	}
	afterRejectedObservation, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after rejected resume observation: %v", err)
	}
	if afterRejectedObservation.Revision != unknownResumeRevision {
		t.Fatalf("rejected resume observation changed Job revision: got %d, want %d", afterRejectedObservation.Revision, unknownResumeRevision)
	}
	unknownReceipt, resumeUnknown, err := database.AddPlanningResumeObservation(ctx, jobID, unknownResumeRevision, resume.ID, "cmd_77777777777777777777777777777777", domain.PlanningResumeUnknown, "", false, principal)
	if err != nil || resumeUnknown.Status != domain.PlanningResumeUnknown {
		t.Fatalf("record resume UNKNOWN: %#v %v", resumeUnknown, err)
	}
	retryReceipt, retryResumeUnknown, err := database.AddPlanningResumeObservation(ctx, jobID, unknownReceipt.Revision, resume.ID, "cmd_77777777777777777777777777777777", domain.PlanningResumeUnknown, "", false, principal)
	if err != nil || retryReceipt != unknownReceipt || retryResumeUnknown != resumeUnknown {
		t.Fatalf("idempotent resume observation retry: %#v %#v %v", retryReceipt, retryResumeUnknown, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before resume reconciliation: %v", err)
	}
	if _, _, err := database.AddPlanningResumeIntent(ctx, jobID, job.Revision, executionID, "session_exact", "cmd_88888888888888888888888888888888", strings.Repeat("2", 64), true, principal); err == nil {
		t.Fatal("new resume was allowed while previous resume remained UNKNOWN")
	}
	_, resumeResolved, err := database.AddPlanningResumeObservation(ctx, jobID, job.Revision, resume.ID, "cmd_99999999999999999999999999999999", domain.PlanningResumeAttached, strings.Repeat("3", 64), true, principal)
	if err != nil || !resumeResolved.ReconcilesUnknown {
		t.Fatalf("reconcile resume UNKNOWN: %#v %v", resumeResolved, err)
	}
	if resume.RoleExecutionID != executionID || resume.ProviderSessionID != "session_exact" {
		t.Fatalf("resume changed identity: %#v", resume)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	job, err = reopened.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after restart: %v", err)
	}
	if job.State != domain.JobDraft || len(job.PlanningTurnIntents) != 1 || len(job.PlanningTurnObservations) != 2 ||
		len(job.PlanningEffectDecisions) != 2 || len(job.PlanningResumeIntents) != 1 || len(job.PlanningResumeObservations) != 2 || len(job.PlanningBoundaryDecisions) != 1 || len(job.RoleExecutions) != 1 {
		t.Fatalf("restored Job lifecycle facts: %#v", job)
	}
}
