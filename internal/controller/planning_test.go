package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type fakePlanningLifecycleStore struct {
	job domain.Job
	turnErr error
	effect domain.PlanningEffectDecision
	resumeErr error
	boundaryDecisions []domain.PlanningBoundaryDecision
}

func (f *fakePlanningLifecycleStore) GetJob(context.Context, string) (domain.Job, error) {
	return f.job, nil
}

func (f *fakePlanningLifecycleStore) AddPlanningTurnIntent(context.Context, string, uint64, string, string, string, string, uint64, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnIntent, error) {
	return store.CommandReceipt{}, domain.PlanningTurnIntent{}, f.turnErr
}

func (f *fakePlanningLifecycleStore) AddPlanningTurnObservation(context.Context, string, uint64, string, string, string, uint64, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnObservation, error) {
	return store.CommandReceipt{}, domain.PlanningTurnObservation{}, nil
}

func (f *fakePlanningLifecycleStore) AddPlanningEffectDecision(context.Context, string, uint64, string, string, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningEffectDecision, error) {
	return store.CommandReceipt{}, f.effect, nil
}

func (f *fakePlanningLifecycleStore) AddPlanningResumeIntent(context.Context, string, uint64, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeIntent, error) {
	return store.CommandReceipt{}, domain.PlanningResumeIntent{}, f.resumeErr
}

func (f *fakePlanningLifecycleStore) AddPlanningBoundaryDecision(_ context.Context, _ string, _ uint64, _ string, sourceCommandID, boundary, reasonCode string, _ auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningBoundaryDecision, error) {
	decision := domain.PlanningBoundaryDecision{SourceCommandID: sourceCommandID, Boundary: boundary, ReasonCode: reasonCode, CreatedAt: time.Now().UTC()}
	f.boundaryDecisions = append(f.boundaryDecisions, decision)
	return store.CommandReceipt{}, decision, nil
}

func TestBeginTurnPersistsTypedBoundaryDenial(t *testing.T) {
	fake := &fakePlanningLifecycleStore{
		job: domain.Job{ID: "job_1", Revision: 5},
		turnErr: &domain.PlanningBoundaryError{
			Boundary: domain.PlanningBoundaryTurn,
			ReasonCode: "token_budget_exhausted",
			Cause: errors.New("token reservation exceeds remaining budget"),
		},
	}
	controller, err := NewPlanningController(fake)
	if err != nil {
		t.Fatalf("NewPlanningController: %v", err)
	}
	_, _, err = controller.BeginTurn(context.Background(), "job_1", "role_1", "session_1", "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "prompt-digest", 100, auth.AuthenticatedPrincipal{})
	if err == nil {
		t.Fatal("BeginTurn did not return the boundary rejection")
	}
	if len(fake.boundaryDecisions) != 1 {
		t.Fatalf("boundary decisions = %d, want 1", len(fake.boundaryDecisions))
	}
	if fake.boundaryDecisions[0].Boundary != domain.PlanningBoundaryTurn || fake.boundaryDecisions[0].ReasonCode != "token_budget_exhausted" {
		t.Fatalf("unexpected denial fact: %#v", fake.boundaryDecisions[0])
	}
}

func TestBeginTurnDoesNotRecordStaleRevisionAsAuthorizationDenial(t *testing.T) {
	fake := &fakePlanningLifecycleStore{
		job: domain.Job{ID: "job_1", Revision: 5},
		turnErr: store.ErrIdempotencyConflict,
	}
	controller, err := NewPlanningController(fake)
	if err != nil {
		t.Fatalf("NewPlanningController: %v", err)
	}
	_, _, err = controller.BeginTurn(context.Background(), "job_1", "role_1", "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "prompt-digest", 100, auth.AuthenticatedPrincipal{})
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
	}
	if len(fake.boundaryDecisions) != 0 {
		t.Fatalf("stale revision recorded as denial: %#v", fake.boundaryDecisions)
	}
}

func TestAuthorizeEffectFailsClosedOnDeniedDecision(t *testing.T) {
	fake := &fakePlanningLifecycleStore{
		job: domain.Job{ID: "job_1", Revision: 5},
		effect: domain.PlanningEffectDecision{Decision: domain.PlanningEffectDeny, Reason: "outside_planning_authorization_scope"},
	}
	controller, err := NewPlanningController(fake)
	if err != nil {
		t.Fatalf("NewPlanningController: %v", err)
	}
	_, decision, err := controller.AuthorizeEffect(context.Background(), "job_1", "role_1", "cmd_cccccccccccccccccccccccccccccccc", "repository.write", "context-digest", auth.AuthenticatedPrincipal{})
	var denied *PlanningEffectDeniedError
	if !errors.As(err, &denied) || decision.Decision != domain.PlanningEffectDeny {
		t.Fatalf("effect decision was not rejected: %#v %v", decision, err)
	}
}
