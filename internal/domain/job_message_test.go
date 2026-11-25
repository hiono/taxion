package domain

import (
	"strings"
	"testing"
	"time"
)

func jobWithPlannerForMessageTest(t *testing.T) (Job, RoleExecution) {
	t.Helper()
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	grant := newValidPlanningAuthorization(t, job, issuedAt)
	job.PlanningAuthorizations = append(job.PlanningAuthorizations, grant)
	job.Revision++
	job.UpdatedAt = issuedAt

	execution, intent, err := NewTasklessPlannerStart(
		job, grant.ID, grant.PrincipalID, "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		strings.Repeat("d", 64), issuedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewTasklessPlannerStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, execution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, intent)
	job.Revision++
	job.UpdatedAt = execution.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after Planner Start: %v", err)
	}
	return job, execution
}

func TestJobMessagePreservesNaturalLanguageAndQuestionCorrelation(t *testing.T) {
	job, planner := jobWithPlannerForMessageTest(t)
	principalID := job.IntakeProvenance.PrincipalID
	question, err := NewJobMessage(job, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: ActorRef{Kind: ActorHuman, ID: principalID},
		Kind:      MessageKindQuestion,
		Body:      "この仕様の優先順位について確認してください。\n制約は維持します。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage QUESTION: %v", err)
	}
	job.JobMessages = append(job.JobMessages, question)
	job.Revision++
	job.UpdatedAt = question.CreatedAt

	answer, err := NewJobMessage(job, "cmd_cccccccccccccccccccccccccccccccc", JobMessageRequest{
		Sender:           ActorRef{Kind: ActorHuman, ID: principalID},
		Recipient:        ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Kind:             MessageKindAnswer,
		ReplyToMessageID: question.ID,
		Body:             "優先順位は安全性、次に完遂性です。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage ANSWER: %v", err)
	}
	if answer.ThreadID != question.ThreadID || answer.ReplyToMessageID != question.ID ||
		answer.Body != "優先順位は安全性、次に完遂性です。" {
		t.Fatalf("ANSWER lost thread, reply, or natural-language body: %#v", answer)
	}
	job.JobMessages = append(job.JobMessages, answer)
	job.Revision++
	job.UpdatedAt = answer.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with correlated messages: %v", err)
	}
}

func TestJobMessageRejectsForgedActorsAndInvalidReplies(t *testing.T) {
	job, planner := jobWithPlannerForMessageTest(t)
	recipient := ActorRef{Kind: ActorHuman, ID: job.IntakeProvenance.PrincipalID}
	if _, err := NewJobMessage(job, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorHuman, ID: "human_forged"},
		Recipient: recipient, Kind: MessageKindMessage, Body: "forged",
	}, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("JobMessage accepted a forged HUMAN Principal")
	}
	if _, err := NewJobMessage(job, "cmd_cccccccccccccccccccccccccccccccc", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: recipient, Kind: MessageKindAnswer, Body: "unrelated answer",
	}, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("JobMessage accepted an ANSWER without a QUESTION")
	}
	if _, err := NewJobMessage(job, "cmd_dddddddddddddddddddddddddddddddd", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: recipient, Kind: MessageKindHandoff, Body: "handoff",
	}, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("generic JobMessage command accepted HANDOFF")
	}
}

func TestDeliveryObservationRequiresExplicitUnknownReconciliation(t *testing.T) {
	job, planner := jobWithPlannerForMessageTest(t)
	message, err := NewJobMessage(job, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: ActorRef{Kind: ActorHuman, ID: job.IntakeProvenance.PrincipalID},
		Kind:      MessageKindQuestion, Body: "確認してください。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, message)
	job.Revision++
	job.UpdatedAt = message.CreatedAt

	requested, err := NewDeliveryObservation(job, message.ID, "", "cmd_cccccccccccccccccccccccccccccccc", DeliveryRequested, "", false, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewDeliveryObservation REQUESTED: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, requested)
	job.Revision++
	job.UpdatedAt = requested.CreatedAt

	unknown, err := NewDeliveryObservation(job, message.ID, requested.ID, "cmd_dddddddddddddddddddddddddddddddd", DeliveryUnknown, "", false, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewDeliveryObservation UNKNOWN: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt

	if _, err := NewDeliveryObservation(job, message.ID, requested.ID, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", DeliveryAccepted, strings.Repeat("a", 64), false, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("UNKNOWN delivery was accepted without reconciliation")
	}
	accepted, err := NewDeliveryObservation(job, message.ID, requested.ID, "cmd_ffffffffffffffffffffffffffffffff", DeliveryAccepted, strings.Repeat("a", 64), true, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewDeliveryObservation reconciliation: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, accepted)
	job.Revision++
	job.UpdatedAt = accepted.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with delivery history: %v", err)
	}
}

func TestDeliveryObservationBindsResultToLatestRequestedAttempt(t *testing.T) {
	job, planner := jobWithPlannerForMessageTest(t)
	message, err := NewJobMessage(job, "cmd_10101010101010101010101010101010", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: ActorRef{Kind: ActorHuman, ID: job.IntakeProvenance.PrincipalID},
		Kind:      MessageKindQuestion, Body: "再送結果の対象attemptを確認してください。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, message)
	job.Revision++
	job.UpdatedAt = message.CreatedAt

	requested1, err := NewDeliveryObservation(job, message.ID, "", "cmd_20202020202020202020202020202020", DeliveryRequested, "", false, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("REQUESTED attempt 1: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, requested1)
	job.Revision++
	job.UpdatedAt = requested1.CreatedAt

	failed1, err := NewDeliveryObservation(job, message.ID, requested1.ID, "cmd_30303030303030303030303030303030", DeliveryFailed, strings.Repeat("a", 64), false, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("FAILED attempt 1: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, failed1)
	job.Revision++
	job.UpdatedAt = failed1.CreatedAt

	requested2, err := NewDeliveryObservation(job, message.ID, "", "cmd_40404040404040404040404040404040", DeliveryRequested, "", false, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("REQUESTED attempt 2: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, requested2)
	job.Revision++
	job.UpdatedAt = requested2.CreatedAt

	if _, err := NewDeliveryObservation(job, message.ID, requested1.ID, "cmd_50505050505050505050505050505050", DeliveryAccepted, strings.Repeat("b", 64), false, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("late result for attempt 1 was attributed to the latest REQUESTED attempt 2")
	}
	accepted2, err := NewDeliveryObservation(job, message.ID, requested2.ID, "cmd_60606060606060606060606060606060", DeliveryAccepted, strings.Repeat("c", 64), false, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("ACCEPTED attempt 2: %v", err)
	}
	job.DeliveryObservations = append(job.DeliveryObservations, accepted2)
	job.Revision++
	job.UpdatedAt = accepted2.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with two delivery attempts: %v", err)
	}
}
