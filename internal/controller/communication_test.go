package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
)

func TestCommunicationControllerPersistsRoleQuestionAndAnswer(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, firstExecution, _, firstStart, err := providerController.StartPlanning(
		context.Background(), jobID, grantID, "cmd_11111111111111111111111111111111", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning first Role: %v", err)
	}

	job, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob before second PlanningAuthorization: %v", err)
	}
	now := time.Now().UTC()
	secondGrantCommand := "cmd_22222222222222222222222222222222"
	secondGrant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), secondGrantCommand,
		domain.PlanningAuthorizationRequest{
			ProviderID: runtime.identity.ProviderID, AccountID: runtime.identity.AccountID,
			ConfigurationSHA256: runtime.identity.ConfigurationSHA256,
			RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
			ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
				Mode: domain.PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1",
				SHA256: sha256String(documents[0].Content),
			},
			Budget:    domain.PlanningBudget{MaxTurns: 3, MaxTokens: 1000},
			Deadline: now.Add(time.Hour),
		}, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization second Role: %v", err)
	}
	if _, err := database.AddPlanningAuthorization(
		context.Background(), jobID, job.Revision, secondGrantCommand, secondGrant, principal,
	); err != nil {
		t.Fatalf("AddPlanningAuthorization second Role: %v", err)
	}
	secondStartCommand := "cmd_33333333333333333333333333333333"
	runtime.startResult = provider.StartResult{
		OperationID: domain.ProviderStartOperationID(secondStartCommand),
		SessionID:    "session_2", ReceiptID: "receipt_2", EvidenceSHA256: strings.Repeat("e", 64),
	}
	_, secondExecution, _, secondStart, err := providerController.StartPlanning(
		context.Background(), jobID, secondGrant.ID, secondStartCommand, documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning second Role: %v", err)
	}

	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	question, err := communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: firstExecution.ID, ProviderSessionID: firstStart.SessionID,
		CommandID: "cmd_44444444444444444444444444444444",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: secondExecution.ID},
		Kind:       domain.MessageKindQuestion,
		Body:       "この制約の優先順位を確認してください。\n既存のContextは維持してください。",
	})
	if err != nil {
		t.Fatalf("SendFromRole QUESTION: %v", err)
	}
	if question.Message.Sender.ID != firstExecution.ID || question.Message.Recipient.ID != secondExecution.ID ||
		question.Message.Body == "" || question.Delivery == nil ||
		question.Delivery.Status != domain.DeliveryRequested || question.Delivery.Recipient.ID != secondExecution.ID {
		t.Fatalf("Role QUESTION was not persisted with its recipient delivery request: %#v", question)
	}

	answer, err := communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: secondExecution.ID, ProviderSessionID: secondStart.SessionID,
		CommandID: "cmd_55555555555555555555555555555555",
		Recipient: domain.ActorRef{Kind: domain.ActorPlanner, ID: firstExecution.ID},
		Kind:       domain.MessageKindAnswer, ReplyToMessageID: question.Message.ID,
		Body: "優先順位は安全性、次に完遂性です。",
	})
	if err != nil {
		t.Fatalf("SendFromRole ANSWER: %v", err)
	}
	if answer.Message.ThreadID != question.Message.ThreadID ||
		answer.Message.ReplyToMessageID != question.Message.ID ||
		answer.Message.Sender.ID != secondExecution.ID ||
		answer.Message.Recipient.ID != firstExecution.ID {
		t.Fatalf("Role ANSWER lost correlation or identity: %#v", answer.Message)
	}

	persisted, err := database.GetJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetJob after Role communication: %v", err)
	}
	if len(persisted.JobMessages) != 2 || len(persisted.DeliveryObservations) != 2 {
		t.Fatalf("persisted communication facts: messages=%d deliveries=%d", len(persisted.JobMessages), len(persisted.DeliveryObservations))
	}
}

func TestCommunicationControllerRejectsUnboundRoleSession(t *testing.T) {
	runtime := &fakePlanningProviderRuntime{}
	providerController, database, principal, jobID, grantID, documents := newProviderControllerFixture(t, runtime)
	_, execution, _, _, err := providerController.StartPlanning(
		context.Background(), jobID, grantID, "cmd_66666666666666666666666666666666", documents, principal,
	)
	if err != nil {
		t.Fatalf("StartPlanning: %v", err)
	}
	communication, err := NewCommunicationController(database)
	if err != nil {
		t.Fatalf("NewCommunicationController: %v", err)
	}
	_, err = communication.SendFromRole(context.Background(), RoleMessageRequest{
		JobID: jobID, RoleExecutionID: execution.ID, ProviderSessionID: "session_forged",
		CommandID: "cmd_77777777777777777777777777777777",
		Recipient: domain.ActorRef{Kind: domain.ActorHuman, ID: principal.PrincipalID()},
		Kind:       domain.MessageKindMessage, Body: "must be rejected",
	})
	if err == nil {
		t.Fatal("CommunicationController accepted an unbound Provider Session")
	}
}
