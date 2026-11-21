package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

func newProviderResultUsageFixture(t *testing.T, database *Store, ctx context.Context) (string, string, string, domain.PlanningTurnIntent, auth.AuthenticatedPrincipal) {
	t.Helper()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_provider_result_usage"
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
		Budget: domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000}, Deadline: now.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grant.CommandID, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	_, execution, startIntent, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, "cmd_cccccccccccccccccccccccccccccccc", principal)
	if err != nil {
		t.Fatalf("StartTasklessPlanner: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Start observation: %v", err)
	}
	if _, _, err := database.AddProviderStartObservation(ctx, jobID, job.Revision, startIntent.ID,
		"cmd_dddddddddddddddddddddddddddddddd", domain.ProviderStartAccepted, "session_1", "receipt_1",
		strings.Repeat("e", 64), false, principal); err != nil {
		t.Fatalf("AddProviderStartObservation: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Turn intent: %v", err)
	}
	_, turn, err := database.AddPlanningTurnIntent(ctx, jobID, job.Revision, execution.ID, "session_1",
		"cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("f", 64), 100, principal)
	if err != nil {
		t.Fatalf("AddPlanningTurnIntent: %v", err)
	}
	return jobID, execution.ID, "session_1", turn, principal
}

func resultDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func TestProviderResultAndUsageFactsPersistAtomicallyAndRestore(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	jobID, roleExecutionID, sessionID, turn, principal := newProviderResultUsageFixture(t, database, ctx)
	content := "Final planning result"
	contentSHA := resultDigest(content)
	evidence := strings.Repeat("1", 64)

	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Result: %v", err)
	}
	resultReceipt, result, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, content, contentSHA, true, evidence, principal)
	if err != nil || !result.Final || result.ContentSHA256 != contentSHA || result.CreatedRevision != resultReceipt.Revision {
		t.Fatalf("AddProviderResultObservation: %#v %#v %v", resultReceipt, result, err)
	}
	retryReceipt, retry, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, content, contentSHA, true, evidence, principal)
	if err != nil || retryReceipt != resultReceipt || retry != result {
		t.Fatalf("idempotent Result retry: %#v %#v %v", retryReceipt, retry, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before partial Usage: %v", err)
	}
	partialReceipt, partial, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "usage_turn_1", 12, false, evidence, principal)
	if err != nil || partial.Complete || partial.CreatedRevision != partialReceipt.Revision {
		t.Fatalf("Add partial Provider Usage: %#v %#v %v", partialReceipt, partial, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before complete Usage: %v", err)
	}
	completeReceipt, complete, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "usage_turn_1", 20, true, strings.Repeat("2", 64), principal)
	if err != nil || !complete.Complete || complete.CreatedRevision != completeReceipt.Revision {
		t.Fatalf("Add complete Provider Usage: %#v %#v %v", completeReceipt, complete, err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before invalid Usage: %v", err)
	}
	if _, _, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "usage_turn_1", 21, false, evidence, principal); err == nil {
		t.Fatal("Provider Usage accepted an observation after Complete=true")
	}
	if _, _, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "different final", resultDigest("different final"), true, evidence, principal); err == nil {
		t.Fatal("Provider Result accepted a second terminal result for the same Turn")
	}
	if _, _, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, content, contentSHA, false, evidence, principal); err == nil {
		t.Fatal("Provider Result accepted Final=false")
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before restart: %v", err)
	}
	if len(job.ProviderResultObservations) != 1 || len(job.ProviderUsageObservations) != 2 {
		t.Fatalf("unexpected persisted Result / Usage facts: %d / %d", len(job.ProviderResultObservations), len(job.ProviderUsageObservations))
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	restored, err := reopened.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after restart: %v", err)
	}
	if len(restored.ProviderResultObservations) != 1 || len(restored.ProviderUsageObservations) != 2 ||
		!restored.ProviderUsageObservations[1].Complete || restored.ProviderUsageObservations[1].Tokens != 20 {
		t.Fatalf("restored Provider Result / Usage facts: %#v %#v", restored.ProviderResultObservations, restored.ProviderUsageObservations)
	}
}

func TestProviderResultAndUsageReceiptFailuresRollbackAggregate(t *testing.T) {
	database, _ := newTestStore(t)
	defer database.Close()
	ctx := context.Background()
	jobID, roleExecutionID, sessionID, turn, principal := newProviderResultUsageFixture(t, database, ctx)
	content := "Receipt rollback"
	contentSHA := resultDigest(content)
	evidence := strings.Repeat("3", 64)
	resultCommandID := domain.ProviderResultObservationCommandID(turn.OperationID, turn.ID, contentSHA, evidence)
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_provider_result_receipt
		BEFORE INSERT ON job_commands WHEN NEW.command_id = '`+resultCommandID+`'
		BEGIN SELECT RAISE(ABORT, 'forced Provider Result receipt failure'); END;`); err != nil {
		t.Fatalf("create Result rollback trigger: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Result rollback: %v", err)
	}
	if _, _, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, content, contentSHA, true, evidence, principal); err == nil {
		t.Fatal("Provider Result succeeded despite forced receipt failure")
	}
	afterResultRollback, err := database.GetJob(ctx, jobID)
	if err != nil || afterResultRollback.Revision != job.Revision || len(afterResultRollback.ProviderResultObservations) != 0 {
		t.Fatalf("failed Result partially changed Job: %#v %v", afterResultRollback, err)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_provider_result_receipt"); err != nil {
		t.Fatalf("drop Result rollback trigger: %v", err)
	}
	if _, _, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, content, contentSHA, true, evidence, principal); err != nil {
		t.Fatalf("retry Result after rollback: %v", err)
	}
	usageEvidence := strings.Repeat("4", 64)
	usageCommandID := domain.ProviderUsageObservationCommandID(turn.OperationID, turn.ID, sessionID, "usage_rollback", 7, true, usageEvidence)
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_provider_usage_receipt
		BEFORE INSERT ON job_commands WHEN NEW.command_id = '`+usageCommandID+`'
		BEGIN SELECT RAISE(ABORT, 'forced Provider Usage receipt failure'); END;`); err != nil {
		t.Fatalf("create Usage rollback trigger: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Usage rollback: %v", err)
	}
	if _, _, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "usage_rollback", 7, true, usageEvidence, principal); err == nil {
		t.Fatal("Provider Usage succeeded despite forced receipt failure")
	}
	afterUsageRollback, err := database.GetJob(ctx, jobID)
	if err != nil || afterUsageRollback.Revision != job.Revision || len(afterUsageRollback.ProviderUsageObservations) != 0 {
		t.Fatalf("failed Usage partially changed Job: %#v %v", afterUsageRollback, err)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER fail_provider_usage_receipt"); err != nil {
		t.Fatalf("drop Usage rollback trigger: %v", err)
	}
	if _, _, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "usage_rollback", 7, true, usageEvidence, principal); err != nil {
		t.Fatalf("retry Usage after rollback: %v", err)
	}
}

func TestProviderResultUsageRejectsInvalidPayloadAndIdentity(t *testing.T) {
	database, _ := newTestStore(t)
	defer database.Close()
	ctx := context.Background()
	jobID, roleExecutionID, sessionID, turn, principal := newProviderResultUsageFixture(t, database, ctx)
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	startOperationID := job.ProviderStartIntents[0].ID
	evidence := strings.Repeat("7", 64)
	content := "valid result"
	contentSHA := resultDigest(content)
	invalidResults := []struct {
		name, operationID, turnID, sessionID, content, contentSHA string
		final                                                     bool
	}{
		{name: "non-final", operationID: startOperationID, sessionID: sessionID, content: content, contentSHA: contentSHA, final: false},
		{name: "digest mismatch", operationID: startOperationID, sessionID: sessionID, content: content, contentSHA: strings.Repeat("8", 64), final: true},
		{name: "invalid UTF-8", operationID: startOperationID, sessionID: sessionID, content: string([]byte{0xff}), contentSHA: resultDigest(string([]byte{0xff})), final: true},
		{name: "content too large", operationID: startOperationID, sessionID: sessionID,
			content:    strings.Repeat("x", domain.MaxProviderResultContentBytes+1),
			contentSHA: resultDigest(strings.Repeat("x", domain.MaxProviderResultContentBytes+1)), final: true},
		{name: "wrong Session", operationID: startOperationID, sessionID: "session_other", content: content, contentSHA: contentSHA, final: true},
		{name: "Start operation with Turn identity", operationID: startOperationID, turnID: turn.ID, sessionID: sessionID, content: content, contentSHA: contentSHA, final: true},
	}
	for _, tc := range invalidResults {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := database.AddProviderResultObservation(ctx, jobID, job.Revision,
				roleExecutionID, tc.operationID, tc.turnID, tc.sessionID, tc.content, tc.contentSHA,
				tc.final, evidence, principal); err == nil {
				t.Fatalf("invalid Provider Result was accepted: %s", tc.name)
			}
		})
	}
	invalidUsages := []struct {
		name, operationID, turnID, sessionID, usageID string
	}{
		{name: "wrong operation", operationID: "provider_turn_unknown", turnID: turn.ID, sessionID: sessionID, usageID: "usage_invalid_1"},
		{name: "wrong Session", operationID: turn.OperationID, turnID: turn.ID, sessionID: "session_other", usageID: "usage_invalid_2"},
		{name: "invalid UsageID", operationID: turn.OperationID, turnID: turn.ID, sessionID: sessionID, usageID: "usage invalid"},
	}
	for _, tc := range invalidUsages {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
				roleExecutionID, tc.operationID, tc.turnID, tc.sessionID, tc.usageID, 1, false, evidence, principal); err == nil {
				t.Fatalf("invalid Provider Usage was accepted: %s", tc.name)
			}
		})
	}
	if _, _, err := database.AddProviderUsageObservation(ctx, jobID, job.Revision,
		roleExecutionID, turn.OperationID, turn.ID, sessionID, "usage_rebind", 5, false, evidence, principal); err != nil {
		t.Fatalf("persist initial Usage binding: %v", err)
	}
	current, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob before Usage rebind: %v", err)
	}
	if _, _, err := database.AddProviderUsageObservation(ctx, jobID, current.Revision,
		roleExecutionID, startOperationID, "", sessionID, "usage_rebind", 6, true, evidence, principal); err == nil {
		t.Fatal("Provider UsageID was rebound from a Turn to the Start operation")
	}
	after, err := database.GetJob(ctx, jobID)
	if err != nil || len(after.ProviderResultObservations) != 0 || len(after.ProviderUsageObservations) != 1 {
		t.Fatalf("invalid Result / Usage input changed Job facts: %#v %v", after, err)
	}
}
