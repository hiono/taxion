package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func TestProviderStopIntentAndObservationPersistAtomically(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_provider_stop"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Inspect the repository safely.", "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	now := time.Now().UTC().Round(0)
	request := domain.PlanningAuthorizationRequest{
		ProviderID: "provider.test", AccountID: "account.primary", ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID: "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1", SHA256: strings.Repeat("c", 64),
		},
		Deadline: now.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	_, _, startIntent, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, "cmd_cccccccccccccccccccccccccccccccc", principal)
	if err != nil {
		t.Fatalf("StartTasklessPlanner: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Start intent: %v", err)
	}
	_, startObservation, err := database.AddProviderStartObservation(
		ctx, jobID, job.Revision, startIntent.ID, "cmd_dddddddddddddddddddddddddddddddd",
		domain.ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("e", 64), false, principal,
	)
	if err != nil || startObservation.Status != domain.ProviderStartAccepted {
		t.Fatalf("AddProviderStartObservation: %#v %v", startObservation, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, "session_1",
		"cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("f", 64), principal,
	)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop intent: %v", err)
	}
	stopCommandID := "cmd_ffffffffffffffffffffffffffffffff"
	stopReceipt, stopIntent, err := database.AddProviderStopIntent(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, "session_1",
		stopCommandID, domain.ProviderStopReasonPlanningComplete, principal,
	)
	if err != nil || stopIntent.SessionID != "session_1" || stopIntent.CreatedRevision != stopReceipt.Revision {
		t.Fatalf("AddProviderStopIntent: %#v %#v %v", stopReceipt, stopIntent, err)
	}
	retryReceipt, retryIntent, err := database.AddProviderStopIntent(
		ctx, jobID, stopReceipt.Revision, startIntent.RoleExecutionID, "session_1",
		stopCommandID, domain.ProviderStopReasonPlanningComplete, principal,
	)
	if err != nil || retryReceipt != stopReceipt || retryIntent != stopIntent {
		t.Fatalf("idempotent Stop intent retry: %#v %#v %v", retryReceipt, retryIntent, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Stop intent: %v", err)
	}
	if _, _, err := database.AddPlanningTurnIntent(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, "session_1",
		"cmd_12121212121212121212121212121212", strings.Repeat("f", 64), principal,
	); err == nil {
		t.Fatal("Planning Turn intent was accepted after Provider Stop intent")
	}
	if _, _, err := database.AddProviderStopIntent(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, "session_1",
		"cmd_13131313131313131313131313131313", domain.ProviderStopReasonCancelled, principal,
	); err == nil {
		t.Fatal("second Provider Stop intent for the same Session was accepted")
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before UNKNOWN Stop observation: %v", err)
	}
	unknownCommandID := domain.ProviderStopObservationCommandID(stopIntent.OperationID, false, false, "", false)
	unknownReceipt, unknown, err := database.AddProviderStopObservation(
		ctx, jobID, job.Revision, stopIntent.ID, unknownCommandID, false, false, "", false, principal,
	)
	if err != nil || unknown.Stopped || unknown.Quiescent || unknown.EvidenceSHA256 != "" {
		t.Fatalf("record UNKNOWN Stop observation: %#v %#v %v", unknownReceipt, unknown, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Stop observation rollback: %v", err)
	}
	finalEvidence := strings.Repeat("1", 64)
	finalCommandID := domain.ProviderStopObservationCommandID(stopIntent.OperationID, true, true, finalEvidence, true)
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_provider_stop_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_id = '`+finalCommandID+`'
		BEGIN
			SELECT RAISE(ABORT, 'forced Provider Stop receipt failure');
		END;`); err != nil {
		t.Fatalf("create Stop rollback trigger: %v", err)
	}
	if _, _, err := database.AddProviderStopObservation(
		ctx, jobID, job.Revision, stopIntent.ID, finalCommandID,
		true, true, finalEvidence, true, principal,
	); err == nil {
		t.Fatal("Stop observation succeeded despite forced receipt failure")
	}
	afterRollback, err := database.GetJob(ctx, jobID)
	if err != nil || afterRollback.Revision != job.Revision || len(afterRollback.ProviderStopObservations) != 1 {
		t.Fatalf("failed Stop observation partially changed Job: %#v %v", afterRollback, err)
	}
	var receiptCount int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_commands WHERE command_id = ?", finalCommandID).Scan(&receiptCount); err != nil {
		t.Fatalf("count failed Stop observation receipts: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("failed Stop observation left %d receipts", receiptCount)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_provider_stop_receipt"); err != nil {
		t.Fatalf("drop Stop rollback trigger: %v", err)
	}
	finalReceipt, finalObservation, err := database.AddProviderStopObservation(
		ctx, jobID, job.Revision, stopIntent.ID, finalCommandID,
		true, true, finalEvidence, true, principal,
	)
	if err != nil || !finalObservation.Stopped || !finalObservation.Quiescent ||
		!finalObservation.ReconcilesUnknown || finalObservation.CreatedRevision != finalReceipt.Revision {
		t.Fatalf("reconcile UNKNOWN Stop observation: %#v %#v %v", finalReceipt, finalObservation, err)
	}
	if domain.ProviderStopObservationCommandID(stopIntent.OperationID, true, true, finalEvidence, true) != finalCommandID {
		t.Fatal("Provider Stop observation command identity is not deterministic")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before stopped event: %v", err)
	}
	_, stoppedEvent, err := database.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, stopIntent.OperationID, "", "session_1",
		"stop_event_1", string(domain.ProviderEventSessionStopped), "", "stop_cursor_1",
		strings.Repeat("2", 64), time.Now().UTC(), principal,
	)
	if err != nil || stoppedEvent.Kind != domain.ProviderEventSessionStopped {
		t.Fatalf("persist session.stopped Provider event: %#v %v", stoppedEvent, err)
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
	if len(job.ProviderStopIntents) != 1 || len(job.ProviderStopObservations) != 2 ||
		len(job.ProviderEventObservations) != 1 || !job.ProviderStopObservations[1].Quiescent {
		t.Fatalf("restored Provider Stop facts: %#v %#v %#v", job.ProviderStopIntents, job.ProviderStopObservations, job.ProviderEventObservations)
	}
	if turn.ProviderSessionID != stopIntent.SessionID {
		t.Fatalf("Stop intent Session differs from existing Turn: %#v", turn)
	}
}
