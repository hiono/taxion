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
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Plan safely.", createCommand, principal.AuthenticatedAt(), principal); err != nil { t.Fatalf("CreateTrustedDraftJob: %v", err) }
	job, err := database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob: %v", err) }
	now := time.Now().UTC()
	request := domain.PlanningAuthorizationRequest{
		ProviderID: "provider.test", AccountID: "account.primary", ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID: "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1", SHA256: strings.Repeat("c", 64)},
		Budget: domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000}, Deadline: now.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommand, request, now)
	if err != nil { t.Fatalf("NewPlanningAuthorization: %v", err) }
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommand, grant, principal)
	if err != nil { t.Fatalf("AddPlanningAuthorization: %v", err) }
	_, _, _, err = database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommand, principal)
	if err != nil { t.Fatalf("StartTasklessPlanner: %v", err) }
	job, err = database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob after start: %v", err) }
	executionID := job.RoleExecutions[0].ID
	contextDigest := job.PlanningAuthorizations[0].ReadOnlyBoundary.SHA256

	turnCommand := "cmd_dddddddddddddddddddddddddddddddd"
	turnReceipt, turn, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, executionID, "session_exact", turnCommand, strings.Repeat("d", 64), 500, principal)
	if err != nil { t.Fatalf("AddPlanningTurnIntent: %v", err) }
	retryReceipt, retryTurn, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, executionID, "session_exact", turnCommand, strings.Repeat("d", 64), 500, principal)
	if err != nil || retryReceipt != turnReceipt || retryTurn != turn { t.Fatalf("idempotent turn retry: %#v %#v %v", retryReceipt, retryTurn, err) }

	job, err = database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob after turn: %v", err) }
	unknownCommand := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	unknownReceipt, unknown, err := database.AddPlanningTurnObservation(ctx, jobID, job.Revision, turn.ID, unknownCommand, domain.PlanningTurnUnknown, 0, "", false, principal)
	if err != nil { t.Fatalf("record UNKNOWN: %v", err) }
	if unknown.Status != domain.PlanningTurnUnknown || unknownReceipt.Revision != job.Revision+1 { t.Fatalf("UNKNOWN observation: %#v %#v", unknownReceipt, unknown) }
	job, err = database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob after UNKNOWN: %v", err) }
	if _, _, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, executionID, "session_exact", "cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("e", 64), 100, principal); err == nil { t.Fatal("new turn was allowed while prior turn remained UNKNOWN") }
	reconcileCommand := "cmd_11111111111111111111111111111111"
	_, resolved, err := database.AddPlanningTurnObservation(ctx, jobID, job.Revision, turn.ID, reconcileCommand, domain.PlanningTurnCompleted, 120, strings.Repeat("f", 64), true, principal)
	if err != nil { t.Fatalf("reconcile UNKNOWN: %v", err) }
	if !resolved.ReconcilesUnknown || resolved.TokensUsed != 120 { t.Fatalf("reconciled observation: %#v", resolved) }

	job, err = database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob before effect: %v", err) }
	_, allowed, err := database.AddPlanningEffectDecision(ctx, jobID, job.Revision, executionID, "cmd_22222222222222222222222222222222", domain.PlanningEffectRepositoryRead, contextDigest, principal)
	if err != nil || allowed.Decision != domain.PlanningEffectAllow { t.Fatalf("repository.read decision: %#v %v", allowed, err) }
	job, err = database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob before denied effect: %v", err) }
	_, denied, err := database.AddPlanningEffectDecision(ctx, jobID, job.Revision, executionID, "cmd_33333333333333333333333333333333", "repository.write", contextDigest, principal)
	if err != nil || denied.Decision != domain.PlanningEffectDeny { t.Fatalf("repository.write decision: %#v %v", denied, err) }

	job, err = database.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob before resume: %v", err) }
	if _, _, err := database.AddPlanningResumeIntent(ctx, jobID, job.Revision, executionID, "session_other", "cmd_44444444444444444444444444444444", strings.Repeat("1", 64), true, principal); err == nil { t.Fatal("resume accepted a different Provider Session") }
	_, resume, err := database.AddPlanningResumeIntent(ctx, jobID, job.Revision, executionID, "session_exact", "cmd_55555555555555555555555555555555", strings.Repeat("1", 64), true, principal)
	if err != nil { t.Fatalf("resume: %v", err) }
	if resume.RoleExecutionID != executionID || resume.ProviderSessionID != "session_exact" { t.Fatalf("resume changed identity: %#v", resume) }

	if err := database.Close(); err != nil { t.Fatalf("Close: %v", err) }
	reopened, err := Open(ctx, path)
	if err != nil { t.Fatalf("reopen: %v", err) }
	defer reopened.Close()
	job, err = reopened.GetJob(ctx, jobID)
	if err != nil { t.Fatalf("GetJob after restart: %v", err) }
	if job.State != domain.JobDraft || len(job.PlanningTurnIntents) != 1 || len(job.PlanningTurnObservations) != 2 ||
		len(job.PlanningEffectDecisions) != 2 || len(job.PlanningResumeIntents) != 1 || len(job.RoleExecutions) != 1 {
		t.Fatalf("restored Job lifecycle facts: %#v", job)
	}
}
