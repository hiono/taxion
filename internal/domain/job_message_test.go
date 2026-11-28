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

func TestJobMessagePreservesNaturalLanguageAndReplyCorrelation(t *testing.T) {
	job, planner := jobWithPlannerForMessageTest(t)
	principalID := job.IntakeProvenance.PrincipalID
	question, err := NewJobMessage(job, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: ActorRef{Kind: ActorHuman, ID: principalID},
		Body:      "この仕様の優先順位について確認してください。\n制約は維持します。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage question: %v", err)
	}
	job.JobMessages = append(job.JobMessages, question)
	job.Revision++
	job.UpdatedAt = question.CreatedAt

	answer, err := NewJobMessage(job, "cmd_cccccccccccccccccccccccccccccccc", JobMessageRequest{
		Sender:           ActorRef{Kind: ActorHuman, ID: principalID},
		Recipient:        ActorRef{Kind: ActorPlanner, ID: planner.ID},
		ReplyToMessageID: question.ID,
		Body:             "優先順位は安全性、次に完遂性です。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage answer: %v", err)
	}
	if answer.ReplyToMessageID != question.ID || answer.Body != "優先順位は安全性、次に完遂性です。" {
		t.Fatalf("reply lost correlation or natural-language body: %#v", answer)
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
		Recipient: recipient, Body: "forged",
	}, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("JobMessage accepted a forged HUMAN Principal")
	}

	question, err := NewJobMessage(job, "cmd_cccccccccccccccccccccccccccccccc", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: recipient, Body: "確認してください。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage: %v", err)
	}
	job.JobMessages = append(job.JobMessages, question)
	job.Revision++
	job.UpdatedAt = question.CreatedAt

	if _, err := NewJobMessage(job, "cmd_dddddddddddddddddddddddddddddddd", JobMessageRequest{
		Sender:    ActorRef{Kind: ActorPlanner, ID: planner.ID},
		Recipient: recipient, ReplyToMessageID: "message_missing", Body: "reply",
	}, job.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("JobMessage accepted a reply to a missing message")
	}
	forward, err := NewJobMessage(job, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", JobMessageRequest{
		Sender:           ActorRef{Kind: ActorHuman, ID: recipient.ID},
		Recipient:        ActorRef{Kind: ActorController, ID: ControllerActorID},
		ReplyToMessageID: question.ID, Body: "この質問の背景を補足します。",
	}, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewJobMessage forwarding a question: %v", err)
	}
	job.JobMessages = append(job.JobMessages, forward)
	job.Revision++
	job.UpdatedAt = forward.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with forwarded conversation: %v", err)
	}
}
