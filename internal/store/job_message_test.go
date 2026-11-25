package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func createMessageTestPlanner(t *testing.T, database *Store, jobID string) (domain.Job, domain.RoleExecution) {
	t.Helper()
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	createCommand := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	grantCommand := "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	startCommand := "cmd_cccccccccccccccccccccccccccccccc"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Enable role communication.", createCommand, principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob: %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	now := time.Now().UTC()
	request := domain.PlanningAuthorizationRequest{
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1", SHA256: strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000},
		Deadline: now.Add(2 * time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommand, request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, jobID, job.Revision, grantCommand, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization: %v", err)
	}
	_, execution, _, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommand, principal)
	if err != nil {
		t.Fatalf("StartTasklessPlanner: %v", err)
	}
	job, err = database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob after Planner Start: %v", err)
	}
	return job, execution
}

func TestJobMessageAndDeliveryStoreCommandsAreIdempotent(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job, planner := createMessageTestPlanner(t, database, "job_message_store")
	request := domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: domain.ActorPlanner, ID: planner.ID},
		Recipient: domain.ActorRef{Kind: domain.ActorHuman, ID: job.IntakeProvenance.PrincipalID},
		Kind:      domain.MessageKindQuestion,
		Body:      "この要件の意図を確認してください。",
	}
	commandID := "cmd_dddddddddddddddddddddddddddddddd"
	receipt, message, err := database.AddJobMessage(ctx, job.ID, job.Revision, commandID, request)
	if err != nil {
		t.Fatalf("AddJobMessage: %v", err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob after AddJobMessage: %v", err)
	}
	retryReceipt, retryMessage, err := database.AddJobMessage(ctx, job.ID, job.Revision, commandID, request)
	if err != nil || retryReceipt != receipt || retryMessage.ID != message.ID {
		t.Fatalf("AddJobMessage idempotency: %#v %#v %v", retryReceipt, retryMessage, err)
	}
	changed := request
	changed.Body = "異なる本文"
	if _, _, err := database.AddJobMessage(ctx, job.ID, job.Revision, commandID, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed JobMessage payload returned %v, want ErrIdempotencyConflict", err)
	}

	requestCommand := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	requestReceipt, requested, err := database.AddDeliveryObservation(
		ctx, job.ID, job.Revision, message.ID, requestCommand, domain.DeliveryRequested, "", false,
	)
	if err != nil || requested.Status != domain.DeliveryRequested {
		t.Fatalf("AddDeliveryObservation REQUESTED: %#v %v", requestReceipt, err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob after REQUESTED: %v", err)
	}
	unknownReceipt, unknown, err := database.AddDeliveryObservation(
		ctx, job.ID, job.Revision, message.ID, "cmd_ffffffffffffffffffffffffffffffff", domain.DeliveryUnknown, "", false,
	)
	if err != nil || unknown.Status != domain.DeliveryUnknown || unknown.Attempt != 1 {
		t.Fatalf("AddDeliveryObservation UNKNOWN: %#v %#v %v", unknownReceipt, unknown, err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob after UNKNOWN: %v", err)
	}
	_, accepted, err := database.AddDeliveryObservation(
		ctx, job.ID, job.Revision, message.ID, "cmd_11111111111111111111111111111111",
		domain.DeliveryAccepted, strings.Repeat("1", 64), true,
	)
	if err != nil || accepted.Status != domain.DeliveryAccepted || !accepted.ReconcilesUnknown {
		t.Fatalf("AddDeliveryObservation reconciliation: %#v %v", accepted, err)
	}
	job, err = database.GetJob(ctx, job.ID)
	if err != nil || len(job.JobMessages) != 1 || len(job.DeliveryObservations) != 3 {
		t.Fatalf("persisted Job communication: messages=%d deliveries=%d err=%v", len(job.JobMessages), len(job.DeliveryObservations), err)
	}
}
