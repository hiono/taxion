package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type replayQuestionAuthorizationStore struct {
	receipt       store.CommandReceipt
	authorization domain.JobQuestionAuthorization
	found         bool
	getCalls      int
	addCalls      int
}

func (s *replayQuestionAuthorizationStore) GetJob(context.Context, string) (domain.Job, error) {
	s.getCalls++
	return domain.Job{}, errors.New("GetJob must not be called for a committed command replay")
}

func (s *replayQuestionAuthorizationStore) AddJobQuestionAuthorization(
	context.Context, string, uint64, domain.JobQuestionAuthorizationRequest,
) (store.CommandReceipt, domain.JobQuestionAuthorization, error) {
	s.addCalls++
	return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("AddJobQuestionAuthorization must not be called for a replay")
}

func (s *replayQuestionAuthorizationStore) GetJobQuestionAuthorizationCommand(
	context.Context, string, string,
) (store.CommandReceipt, domain.JobQuestionAuthorization, bool, error) {
	return s.receipt, s.authorization, s.found, nil
}

func TestJobQuestionAuthorizationControllerReplaysBeforeResolvingBoundary(t *testing.T) {
	deadline := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	budget := domain.PlanningBudget{MaxTurns: 1, MaxTokens: 100}
	receipt := store.CommandReceipt{
		CommandID:     "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		JobID:         "job_replay",
		Revision:      8,
		PayloadSHA256: "payload",
		CreatedAt:     deadline.Add(-time.Hour),
	}
	authorization := domain.JobQuestionAuthorization{
		JobID:             "job_replay",
		CommandID:         receipt.CommandID,
		QuestionMessageID: "message_question",
		Budget:            budget,
		Deadline:          deadline,
	}
	authorizationStore := &replayQuestionAuthorizationStore{
		receipt: receipt, authorization: authorization, found: true,
	}
	resolver := &fakeQuestionReadOnlyContextResolver{err: errors.New("read-only context resolver unavailable")}
	controller, err := NewJobQuestionAuthorizationController(authorizationStore, resolver)
	if err != nil {
		t.Fatalf("NewJobQuestionAuthorizationController: %v", err)
	}

	replayedReceipt, replayedAuthorization, err := controller.Authorize(
		context.Background(), authorization.JobID, authorization.QuestionMessageID,
		authorization.CommandID, budget, deadline,
	)
	if err != nil {
		t.Fatalf("Authorize committed command replay: %v", err)
	}
	if replayedReceipt != receipt || replayedAuthorization != authorization {
		t.Fatalf("replay changed persisted receipt or grant: %#v %#v", replayedReceipt, replayedAuthorization)
	}
	if resolver.calls != 0 || authorizationStore.getCalls != 0 || authorizationStore.addCalls != 0 {
		t.Fatalf("replay performed new work: resolver=%d GetJob=%d Add=%d",
			resolver.calls, authorizationStore.getCalls, authorizationStore.addCalls)
	}

	changedBudget := budget
	changedBudget.MaxTokens++
	if _, _, err := controller.Authorize(
		context.Background(), authorization.JobID, authorization.QuestionMessageID,
		authorization.CommandID, changedBudget, deadline,
	); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed stable caller inputs returned %v, want ErrIdempotencyConflict", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("changed-input replay invoked resolver %d times", resolver.calls)
	}
}
