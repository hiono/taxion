package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func TestProviderEventObservationsPersistAtomicallyAndPreserveCursor(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_provider_events"
	createCommand := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	grantCommand := "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	startCommand := "cmd_cccccccccccccccccccccccccccccccc"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Inspect the repository safely.", createCommand, principal.AuthenticatedAt(), principal); err != nil {
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
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommand, request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommand, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	_, _, startIntent, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommand, principal)
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
		t.Fatalf("GetJob before first event: %v", err)
	}
	observedAt := time.Now().UTC().Round(0)
	firstReceipt, first, err := database.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, startIntent.ID, "", "session_1",
		"event_1", string(domain.ProviderEventOperationAccepted), "", "cursor_1",
		strings.Repeat("f", 64), observedAt, principal,
	)
	if err != nil || first.Kind != domain.ProviderEventOperationAccepted || first.CreatedRevision != firstReceipt.Revision {
		t.Fatalf("AddProviderEventObservation first event: %#v %#v %v", firstReceipt, first, err)
	}
	retryReceipt, retry, err := database.AddProviderEventObservation(
		ctx, jobID, firstReceipt.Revision, startIntent.RoleExecutionID, startIntent.ID, "", "session_1",
		"event_1", string(domain.ProviderEventOperationAccepted), "", "cursor_1",
		strings.Repeat("f", 64), observedAt, principal,
	)
	if err != nil || retryReceipt != firstReceipt || retry != first {
		t.Fatalf("idempotent Provider event retry: %#v %#v %v", retryReceipt, retry, err)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before invalid cursor: %v", err)
	}
	if _, _, err := database.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, startIntent.ID, "", "session_1",
		"event_bad", string(domain.ProviderEventSessionDetached), "wrong_cursor", "cursor_bad",
		strings.Repeat("f", 64), observedAt.Add(time.Second), principal,
	); err == nil {
		t.Fatal("Provider event with a discontinuous cursor was accepted")
	}
	afterInvalid, err := database.GetJob(ctx, jobID)
	if err != nil || afterInvalid.Revision != job.Revision || len(afterInvalid.ProviderEventObservations) != 1 {
		t.Fatalf("invalid Provider event changed Job: %#v %v", afterInvalid, err)
	}

	turnCommand := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	_, turn, err := database.AddPlanningTurnIntent(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, "session_1",
		turnCommand, strings.Repeat("d", 64), principal,
	)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before turn event: %v", err)
	}
	secondReceipt, second, err := database.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, turn.OperationID, turn.ID, "session_1",
		"event_2", string(domain.ProviderEventTurnStarted), "cursor_1", "cursor_2",
		strings.Repeat("1", 64), observedAt.Add(time.Second), principal,
	)
	if err != nil || second.TurnID != turn.ID || second.OperationID != turn.OperationID {
		t.Fatalf("AddProviderEventObservation turn event: %#v %#v %v", secondReceipt, second, err)
	}

	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before forced rollback: %v", err)
	}
	rollbackEventID := "event_3"
	rollbackCommandID := domain.ProviderEventObservationCommandID("session_1", rollbackEventID)
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_provider_event_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_id = '`+rollbackCommandID+`'
		BEGIN
			SELECT RAISE(ABORT, 'forced Provider event receipt failure');
		END;`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}
	if _, _, err := database.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, startIntent.ID, "", "session_1",
		rollbackEventID, string(domain.ProviderEventSessionDetached), "cursor_2", "cursor_3",
		strings.Repeat("2", 64), observedAt.Add(2*time.Second), principal,
	); err == nil {
		t.Fatal("Provider event succeeded despite forced receipt insertion failure")
	}
	afterRollback, err := database.GetJob(ctx, jobID)
	if err != nil || afterRollback.Revision != job.Revision || len(afterRollback.ProviderEventObservations) != 2 {
		t.Fatalf("failed Provider event partially changed Job: %#v %v", afterRollback, err)
	}
	var receiptCount int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_commands WHERE command_id = ?", rollbackCommandID).Scan(&receiptCount); err != nil {
		t.Fatalf("count failed Provider event receipts: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("failed Provider event left %d receipts", receiptCount)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_provider_event_receipt"); err != nil {
		t.Fatalf("drop rollback trigger: %v", err)
	}
	_, third, err := database.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, startIntent.ID, "", "session_1",
		rollbackEventID, string(domain.ProviderEventSessionDetached), "cursor_2", "cursor_3",
		strings.Repeat("2", 64), observedAt.Add(2*time.Second), principal,
	)
	if err != nil || third.Cursor != "cursor_3" {
		t.Fatalf("retry Provider event after rollback: %#v %v", third, err)
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
	if len(job.ProviderEventObservations) != 3 ||
		job.ProviderEventObservations[0].Cursor != "cursor_1" ||
		job.ProviderEventObservations[1].Cursor != "cursor_2" ||
		job.ProviderEventObservations[2].Cursor != "cursor_3" {
		t.Fatalf("restored Provider event facts: %#v", job.ProviderEventObservations)
	}

	if _, _, err := reopened.AddProviderEventObservation(
		ctx, jobID, job.Revision, startIntent.RoleExecutionID, startIntent.ID, "", "session_1",
		"event_1", string(domain.ProviderEventOperationAccepted), "", "cursor_changed",
		strings.Repeat("f", 64), observedAt, principal,
	); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused Provider EventID with different payload error = %v, want ErrIdempotencyConflict", err)
	}
}
