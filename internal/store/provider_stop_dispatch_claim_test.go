package store

import (
	"context"
	"testing"
)

func TestProviderStopDispatchClaimIsExclusiveAndOwnerScoped(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_provider_stop_dispatch_claim"
	if _, err := database.CreateTrustedDraftJob(
		ctx, jobID, "Stop safely.", "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		principal.AuthenticatedAt(), principal,
	); err != nil {
		t.Fatalf("CreateTrustedDraftJob: %v", err)
	}

	const executionID = "role_execution_1"
	const sessionID = "session_1"
	const operationID = "provider_stop_operation_1"
	acquired, err := database.ClaimProviderStopDispatch(
		ctx, jobID, executionID, sessionID, operationID, "claim_first",
	)
	if err != nil || !acquired {
		t.Fatalf("first claim was not acquired: acquired=%v err=%v", acquired, err)
	}
	acquired, err = database.ClaimProviderStopDispatch(
		ctx, jobID, executionID, sessionID, operationID, "claim_second",
	)
	if err != nil || acquired {
		t.Fatalf("duplicate claim was not rejected: acquired=%v err=%v", acquired, err)
	}
	claimID, found, err := database.GetProviderStopDispatchClaim(ctx, jobID, executionID, sessionID, operationID)
	if err != nil || !found || claimID != "claim_first" {
		t.Fatalf("stored claim changed after duplicate attempt: claim=%q found=%v err=%v", claimID, found, err)
	}
	if err := database.ReleaseProviderStopDispatchClaim(
		ctx, jobID, executionID, sessionID, operationID, "claim_second",
	); err != nil {
		t.Fatalf("release with stale claim ID: %v", err)
	}
	claimID, found, err = database.GetProviderStopDispatchClaim(ctx, jobID, executionID, sessionID, operationID)
	if err != nil || !found || claimID != "claim_first" {
		t.Fatalf("stale release removed the active claim: claim=%q found=%v err=%v", claimID, found, err)
	}
	if err := database.ReleaseProviderStopDispatchClaim(
		ctx, jobID, executionID, sessionID, operationID, "claim_first",
	); err != nil {
		t.Fatalf("release owned claim: %v", err)
	}
	if _, found, err := database.GetProviderStopDispatchClaim(ctx, jobID, executionID, sessionID, operationID); err != nil || found {
		t.Fatalf("owned claim was not released: found=%v err=%v", found, err)
	}
}
