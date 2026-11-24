package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func planningLifecycleJob(t *testing.T) (Job, string, string) {
	t.Helper()
	job := newPlanningTestJob(t)
	principal := job.IntakeProvenance.PrincipalID
	now := job.CreatedAt.Add(time.Minute)
	grant := newValidPlanningAuthorization(t, job, now)
	job.PlanningAuthorizations = append(job.PlanningAuthorizations, grant)
	job.Revision++
	job.UpdatedAt = now
	commandID := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	execution, start, err := NewTasklessPlannerStart(job, grant.ID, principal, commandID, strings.Repeat("d", 64), now.Add(time.Second))
	if err != nil {
		t.Fatalf("NewTasklessPlannerStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, execution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start)
	job.Revision++
	job.UpdatedAt = now.Add(time.Second)
	startObservation, err := NewProviderStartObservation(
		job, start.ID, principal, "cmd_99999999999999999999999999999999",
		ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("e", 64), false,
		now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("NewProviderStartObservation: %v", err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, startObservation)
	job.ProviderLifecycleEnforcedFromRevision = startObservation.CreatedRevision
	job.Revision++
	job.UpdatedAt = startObservation.CreatedAt
	return job, principal, execution.ID
}

func TestPlanningTurnLifecycleRevalidatesBudgetAndUnknown(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("e", 64), 600, now)
	if err != nil {
		t.Fatalf("NewPlanningTurnIntent: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = now
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after turn intent: %v", err)
	}

	unknown, err := NewPlanningTurnObservation(job, turn.ID, principal, "cmd_cccccccccccccccccccccccccccccccc", PlanningTurnUnknown, 0, "", false, now.Add(time.Second))
	if err != nil {
		t.Fatalf("UNKNOWN observation: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after UNKNOWN: %v", err)
	}

	if _, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("f", 64), 100, now.Add(2*time.Second)); err == nil {
		t.Fatal("new turn was allowed while previous OperationID is UNKNOWN")
	}
	reconciled, err := NewPlanningTurnObservation(job, turn.ID, principal, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PlanningTurnCompleted, 120, strings.Repeat("1", 64), true, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("reconcile UNKNOWN: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, reconciled)
	job.Revision++
	job.UpdatedAt = reconciled.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after reconciliation: %v", err)
	}

	second, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("2", 64), 99880, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if second.TurnNumber != 2 {
		t.Fatalf("turn number = %d, want 2", second.TurnNumber)
	}

	job.PlanningTurnIntents = append(job.PlanningTurnIntents, second)
	job.Revision++
	job.UpdatedAt = second.CreatedAt
	secondObservation, err := NewPlanningTurnObservation(job, second.ID, principal, "cmd_77777777777777777777777777777777", PlanningTurnFailed, 99880, strings.Repeat("3", 64), false, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("second turn observation: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, secondObservation)
	job.Revision++
	job.UpdatedAt = secondObservation.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after token usage: %v", err)
	}
	if _, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_66666666666666666666666666666666", strings.Repeat("4", 64), 1, now.Add(5*time.Second)); err == nil {
		t.Fatal("turn exceeded the remaining token budget after actual usage was applied")
	}
}

func TestPlanningEffectDecisionFailsClosed(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	allowed, err := NewPlanningEffectDecision(job, executionID, principal, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PlanningEffectRepositoryRead, job.PlanningAuthorizations[0].ReadOnlyBoundary.SHA256, now)
	if err != nil {
		t.Fatalf("repository.read decision: %v", err)
	}
	if allowed.Decision != PlanningEffectAllow {
		t.Fatalf("decision = %s, want ALLOW", allowed.Decision)
	}
	job.PlanningEffectDecisions = append(job.PlanningEffectDecisions, allowed)
	job.Revision++
	job.UpdatedAt = now
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate: %v", err)
	}

	denied, err := NewPlanningEffectDecision(job, executionID, principal, "cmd_cccccccccccccccccccccccccccccccc", "repository.write", job.PlanningAuthorizations[0].ReadOnlyBoundary.SHA256, now.Add(time.Second))
	if err != nil {
		t.Fatalf("repository.write decision: %v", err)
	}
	if denied.Decision != PlanningEffectDeny {
		t.Fatalf("decision = %s, want DENY", denied.Decision)
	}
}

func TestPlanningResumeRequiresExactSessionAndCapabilityEvidence(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = now
	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_other", "cmd_cccccccccccccccccccccccccccccccc", strings.Repeat("b", 64), true, now.Add(time.Second)); err == nil {
		t.Fatal("resume accepted a different Provider Session")
	}
	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("b", 64), false, now.Add(time.Second)); err == nil {
		t.Fatal("resume accepted an unsupported reattachment capability")
	}
	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("b", 64), true, now.Add(-time.Second)); err == nil {
		t.Fatal("resume was accepted before the latest Provider turn")
	} else if boundary, ok := err.(*PlanningBoundaryError); !ok || boundary.ReasonCode != "resume_before_latest_turn" {
		t.Fatalf("resume-before-turn error = %v, want typed resume_before_latest_turn boundary", err)
	}
	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("b", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resume.RoleExecutionID != executionID || resume.ProviderSessionID != "session_1" {
		t.Fatalf("resume changed execution/session identity: %#v", resume)
	}
}

func TestPlanningResumeUnknownRequiresSameOperationReconciliation(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = now
	turnObservation, err := NewPlanningTurnObservation(job, turn.ID, principal, "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PlanningTurnCompleted, 10, strings.Repeat("e", 64), false, now.Add(time.Second))
	if err != nil {
		t.Fatalf("turn observation: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, turnObservation)
	job.Revision++
	job.UpdatedAt = turnObservation.CreatedAt

	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_cccccccccccccccccccccccccccccccc", strings.Repeat("b", 64), true, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt

	unknown, err := NewPlanningResumeObservation(job, resume.ID, principal, "cmd_dddddddddddddddddddddddddddddddd", PlanningResumeUnknown, "", false, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("UNKNOWN observation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt

	if _, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("c", 64), 100, now.Add(4*time.Second)); err == nil || !strings.Contains(err.Error(), "previous_resume_unknown") {
		t.Fatalf("new turn error = %v, want previous_resume_unknown", err)
	}
	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("d", 64), true, now.Add(4*time.Second)); err == nil || !strings.Contains(err.Error(), "previous_resume_unknown") {
		t.Fatalf("new resume error = %v, want previous_resume_unknown", err)
	}

	resolved, err := NewPlanningResumeObservation(job, resume.ID, principal, "cmd_11111111111111111111111111111111", PlanningResumeAttached, strings.Repeat("f", 64), true, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("reconcile UNKNOWN resume: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, resolved)
	job.Revision++
	job.UpdatedAt = resolved.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after resume reconciliation: %v", err)
	}
	nextTurn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_22222222222222222222222222222222", strings.Repeat("2", 64), 100, now.Add(6*time.Second))
	if err != nil {
		t.Fatalf("turn after successful resume: %v", err)
	}
	if nextTurn.TurnNumber != 2 {
		t.Fatalf("turn number after resume = %d, want 2", nextTurn.TurnNumber)
	}
}
func TestPlanningResumeFactsRejectNonAppendOrderedHistory(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt

	first, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("b", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("first resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, first)
	job.Revision++
	job.UpdatedAt = first.CreatedAt
	attached, err := NewPlanningResumeObservation(job, first.ID, principal, "cmd_cccccccccccccccccccccccccccccccc", PlanningResumeAttached, strings.Repeat("c", 64), false, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("first resume observation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, attached)
	job.Revision++
	job.UpdatedAt = attached.CreatedAt

	second, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("d", 64), true, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("second resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, second)
	job.Revision++
	job.UpdatedAt = second.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("valid resume history: %v", err)
	}

	invalidResumeTime := second
	invalidResumeTime.CreatedAt = turn.CreatedAt.Add(-time.Second)
	if err := invalidResumeTime.Validate(job); err == nil {
		t.Fatal("PlanningResumeIntent accepted a timestamp before its latest turn")
	}
	job.PlanningResumeIntents[1].CreatedRevision = first.CreatedRevision
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted non-monotonic resume intent revisions")
	}
	job.PlanningResumeIntents[1] = second

	unknown, err := NewPlanningResumeObservation(job, second.ID, principal, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PlanningResumeUnknown, "", false, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("second resume UNKNOWN: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	resolved, err := NewPlanningResumeObservation(job, second.ID, principal, "cmd_ffffffffffffffffffffffffffffffff", PlanningResumeAttached, strings.Repeat("e", 64), true, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("second resume reconciliation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, resolved)
	job.Revision++
	job.UpdatedAt = resolved.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("valid resume observation history: %v", err)
	}

	job.PlanningResumeObservations[len(job.PlanningResumeObservations)-1].CreatedAt = unknown.CreatedAt.Add(-time.Second)
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted out-of-order resume observations")
	}
}

func TestPlanningLifecycleRejectsObservationAfterNextIntent(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	firstTurn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, firstTurn)
	job.Revision++
	job.UpdatedAt = firstTurn.CreatedAt
	firstTurnResult, err := NewPlanningTurnObservation(job, firstTurn.ID, principal, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PlanningTurnCompleted, 10, strings.Repeat("b", 64), false, now.Add(time.Second))
	if err != nil {
		t.Fatalf("first turn result: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, firstTurnResult)
	job.Revision++
	job.UpdatedAt = firstTurnResult.CreatedAt
	secondTurn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_cccccccccccccccccccccccccccccccc", strings.Repeat("c", 64), 90, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, secondTurn)
	job.Revision++
	job.UpdatedAt = secondTurn.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("valid turn history: %v", err)
	}

	job.PlanningTurnIntents[1].ProviderSessionID = "session_other"
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted a changed Provider Session within one RoleExecution")
	}
	job.PlanningTurnIntents[1] = secondTurn

	job.PlanningTurnObservations[0].CreatedRevision = secondTurn.CreatedRevision + 1
	job.PlanningTurnObservations[0].CreatedAt = secondTurn.CreatedAt.Add(time.Second)
	job.Revision++
	job.UpdatedAt = job.PlanningTurnObservations[0].CreatedAt
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted a turn result appended after the next turn intent")
	}

	job, principal, executionID = planningLifecycleJob(t)
	now = job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("d", 64), 100, now)
	if err != nil {
		t.Fatalf("resume prerequisite turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	firstResume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("e", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("first resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, firstResume)
	job.Revision++
	job.UpdatedAt = firstResume.CreatedAt
	firstResumeResult, err := NewPlanningResumeObservation(job, firstResume.ID, principal, "cmd_ffffffffffffffffffffffffffffffff", PlanningResumeAttached, strings.Repeat("f", 64), false, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("first resume result: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, firstResumeResult)
	job.Revision++
	job.UpdatedAt = firstResumeResult.CreatedAt
	secondResume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_11111111111111111111111111111111", strings.Repeat("1", 64), true, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("second resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, secondResume)
	job.Revision++
	job.UpdatedAt = secondResume.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("valid resume history: %v", err)
	}

	job.PlanningResumeObservations[0].CreatedRevision = secondResume.CreatedRevision + 1
	job.PlanningResumeObservations[0].CreatedAt = secondResume.CreatedAt.Add(time.Second)
	job.Revision++
	job.UpdatedAt = job.PlanningResumeObservations[0].CreatedAt
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted a resume result appended after the next resume intent")
	}
}

func TestPlanningLifecycleFactsFollowTheirPredecessorRevisions(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	var execution RoleExecution
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == executionID {
			execution = candidate
			break
		}
	}

	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	invalidTurn := turn
	invalidTurn.CreatedRevision = execution.CreatedRevision
	if err := invalidTurn.Validate(job); err == nil {
		t.Fatal("PlanningTurnIntent accepted a revision before Provider Start")
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt

	effect, err := NewPlanningEffectDecision(job, executionID, principal, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PlanningEffectRepositoryRead, job.PlanningAuthorizations[0].ReadOnlyBoundary.SHA256, now.Add(time.Second))
	if err != nil {
		t.Fatalf("effect: %v", err)
	}
	invalidEffect := effect
	invalidEffect.CreatedRevision = execution.CreatedRevision
	if err := invalidEffect.Validate(job); err == nil {
		t.Fatal("PlanningEffectDecision accepted a revision before RoleExecution")
	}
	job.PlanningEffectDecisions = append(job.PlanningEffectDecisions, effect)
	job.Revision++
	job.UpdatedAt = effect.CreatedAt

	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_cccccccccccccccccccccccccccccccc", strings.Repeat("c", 64), true, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	invalidResume := resume
	invalidResume.CreatedRevision = turn.CreatedRevision
	if err := invalidResume.Validate(job); err == nil {
		t.Fatal("PlanningResumeIntent accepted a revision before its turn")
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt

	boundary, err := NewPlanningBoundaryDecision(job, executionID, principal, "cmd_dddddddddddddddddddddddddddddddd", PlanningBoundaryTurn, "token_budget_exhausted", now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("boundary: %v", err)
	}
	invalidBoundary := boundary
	invalidBoundary.CreatedRevision = execution.CreatedRevision
	if err := invalidBoundary.Validate(job); err == nil {
		t.Fatal("PlanningBoundaryDecision accepted a revision before RoleExecution")
	}
}

func TestPlanningTurnRequiresAttachedSessionAfterFailedResume(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	turnObservation, err := NewPlanningTurnObservation(job, turn.ID, principal, "cmd_cccccccccccccccccccccccccccccccc", PlanningTurnCompleted, 10, strings.Repeat("b", 64), false, now.Add(time.Second))
	if err != nil {
		t.Fatalf("turn observation: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, turnObservation)
	job.Revision++
	job.UpdatedAt = turnObservation.CreatedAt

	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("c", 64), true, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt
	failed, err := NewPlanningResumeObservation(job, resume.ID, principal, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PlanningResumeFailed, strings.Repeat("d", 64), false, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("failed resume observation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, failed)
	job.Revision++
	job.UpdatedAt = failed.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after failed resume: %v", err)
	}

	sourceCommandID := "cmd_ffffffffffffffffffffffffffffffff"
	_, err = NewPlanningTurnIntent(job, executionID, principal, "session_1", sourceCommandID, strings.Repeat("e", 64), 100, now.Add(4*time.Second))
	var boundary *PlanningBoundaryError
	if !errors.As(err, &boundary) || boundary.ReasonCode != "provider_session_not_attached" {
		t.Fatalf("turn after FAILED resume = %v, want provider_session_not_attached", err)
	}
	decision, err := NewPlanningBoundaryDecision(job, executionID, principal, sourceCommandID, PlanningBoundaryTurn, boundary.ReasonCode, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("persist provider_session_not_attached decision: %v", err)
	}
	job.PlanningBoundaryDecisions = append(job.PlanningBoundaryDecisions, decision)
	job.Revision++
	job.UpdatedAt = decision.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after boundary denial: %v", err)
	}
}

func TestPlanningTurnValidationUsesOnlyPriorResumeObservations(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	firstTurn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, firstTurn)
	job.Revision++
	job.UpdatedAt = firstTurn.CreatedAt
	firstObservation, err := NewPlanningTurnObservation(job, firstTurn.ID, principal, "cmd_cccccccccccccccccccccccccccccccc", PlanningTurnCompleted, 10, strings.Repeat("b", 64), false, now.Add(time.Second))
	if err != nil {
		t.Fatalf("first turn observation: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, firstObservation)
	job.Revision++
	job.UpdatedAt = firstObservation.CreatedAt
	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("c", 64), true, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt
	attached, err := NewPlanningResumeObservation(job, resume.ID, principal, "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PlanningResumeAttached, strings.Repeat("d", 64), false, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("attached observation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, attached)
	job.Revision++
	job.UpdatedAt = attached.CreatedAt
	secondTurn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1", "cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("e", 64), 100, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("second turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, secondTurn)
	job.Revision++
	job.UpdatedAt = secondTurn.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("valid history: %v", err)
	}

	job.PlanningTurnIntents[1].CreatedRevision = attached.CreatedRevision
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate accepted a turn whose resume attachment observation was appended after that turn")
	}
}

func TestPlanningTurnRequiresAcceptedStartObservationAndExactSession(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	job.ProviderStartObservations = nil
	job.ProviderLifecycleEnforcedFromRevision = 0
	_, err := NewPlanningTurnIntent(job, executionID, principal, "session_1",
		"cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("a", 64), 100, job.UpdatedAt.Add(time.Second))
	var boundary *PlanningBoundaryError
	if !errors.As(err, &boundary) || boundary.ReasonCode != "provider_start_unconfirmed" {
		t.Fatalf("turn without Start observation error = %v, want provider_start_unconfirmed", err)
	}

	job, principal, executionID = planningLifecycleJob(t)
	_, err = NewPlanningTurnIntent(job, executionID, principal, "session_other",
		"cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("b", 64), 100, job.UpdatedAt.Add(time.Second))
	if !errors.As(err, &boundary) || boundary.ReasonCode != "provider_session_mismatch" {
		t.Fatalf("turn with unbound Session error = %v, want provider_session_mismatch", err)
	}
}

func TestLegacyPlanningTurnFactsRemainReadableWithoutStartObservation(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1",
		"cmd_ffffffffffffffffffffffffffffffff", strings.Repeat("f", 64), 100, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewPlanningTurnIntent: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	job.ProviderStartObservations = nil
	job.ProviderLifecycleEnforcedFromRevision = 0
	if err := job.Validate(); err != nil {
		t.Fatalf("legacy Job with historical Turn intent must remain readable: %v", err)
	}
}

func TestNewPlanningResumeRequiresAcceptedStartObservation(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1",
		"cmd_11111111111111111111111111111111", strings.Repeat("1", 64), 100, job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("NewPlanningTurnIntent: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt
	job.ProviderStartObservations = nil
	job.ProviderLifecycleEnforcedFromRevision = 0
	_, err = NewPlanningResumeIntent(job, executionID, principal, "session_1",
		"cmd_22222222222222222222222222222222", strings.Repeat("2", 64), true, job.UpdatedAt.Add(time.Second))
	var boundary *PlanningBoundaryError
	if !errors.As(err, &boundary) || boundary.ReasonCode != "provider_start_unconfirmed" {
		t.Fatalf("Resume without accepted Start observation error = %v, want provider_start_unconfirmed", err)
	}
}

func TestProviderStartUnknownEvidenceIsAppendOnlyUntilTerminalResolution(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	startID := job.ProviderStartIntents[0].ID
	job.ProviderStartObservations = nil
	job.ProviderLifecycleEnforcedFromRevision = 0

	first, err := NewProviderStartObservation(job, startID, principal,
		"cmd_33333333333333333333333333333333", ProviderStartUnknown, "", "", "", false,
		job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("first UNKNOWN observation: %v", err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, first)
	job.ProviderLifecycleEnforcedFromRevision = first.CreatedRevision
	job.Revision++
	job.UpdatedAt = first.CreatedAt

	second, err := NewProviderStartObservation(job, startID, principal,
		"cmd_44444444444444444444444444444444", ProviderStartUnknown, "session_1", "receipt_1", strings.Repeat("4", 64), false,
		job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("second UNKNOWN observation: %v", err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, second)
	job.Revision++
	job.UpdatedAt = second.CreatedAt

	terminal, err := NewProviderStartObservation(job, startID, principal,
		"cmd_55555555555555555555555555555555", ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("5", 64), true,
		job.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("terminal reconciliation: %v", err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, terminal)
	job.Revision++
	job.UpdatedAt = terminal.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("append-only UNKNOWN reconciliation Job.Validate: %v", err)
	}
	if len(job.ProviderStartObservations) != 3 ||
		job.ProviderStartObservations[0].Status != ProviderStartUnknown ||
		job.ProviderStartObservations[1].EvidenceSHA256 != strings.Repeat("4", 64) ||
		!job.ProviderStartObservations[2].ReconcilesUnknown {
		t.Fatalf("unexpected Start observation history: %#v", job.ProviderStartObservations)
	}
	if _, err := NewPlanningTurnIntent(job, executionID, principal, "session_1",
		"cmd_66666666666666666666666666666666", strings.Repeat("6", 64), 100, job.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatalf("accepted Start Session did not unlock Turn: %v", err)
	}
}

func TestPlanningResumeAfterStopRecordsBoundaryDenial(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(
		job, executionID, principal, "session_1",
		"cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now,
	)
	if err != nil {
		t.Fatalf("Planning Turn intent: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt

	turnObservation, err := NewPlanningTurnObservation(
		job, turn.ID, principal, "cmd_cccccccccccccccccccccccccccccccc",
		PlanningTurnCompleted, 10, strings.Repeat("b", 64), false, now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("Planning Turn observation: %v", err)
	}
	job.PlanningTurnObservations = append(job.PlanningTurnObservations, turnObservation)
	job.Revision++
	job.UpdatedAt = turnObservation.CreatedAt

	stop, err := NewProviderStopIntent(
		job, executionID, principal, "session_1",
		"cmd_dddddddddddddddddddddddddddddddd", ProviderStopReasonCancelled, now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("Provider Stop intent: %v", err)
	}
	job.ProviderStopIntents = append(job.ProviderStopIntents, stop)
	job.Revision++
	job.UpdatedAt = stop.CreatedAt

	sourceCommandID := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	_, err = NewPlanningResumeIntent(
		job, executionID, principal, "session_1", sourceCommandID,
		strings.Repeat("c", 64), true, now.Add(3*time.Second),
	)
	var boundary *PlanningBoundaryError
	if !errors.As(err, &boundary) || boundary.ReasonCode != "provider_session_stopped" {
		t.Fatalf("resume after Stop = %v, want provider_session_stopped boundary", err)
	}
	decision, err := NewPlanningBoundaryDecision(
		job, executionID, principal, sourceCommandID,
		PlanningBoundaryResume, boundary.ReasonCode, now.Add(3*time.Second),
	)
	if err != nil {
		t.Fatalf("persist stopped-session boundary decision: %v", err)
	}
	job.PlanningBoundaryDecisions = append(job.PlanningBoundaryDecisions, decision)
	job.Revision++
	job.UpdatedAt = decision.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after stopped-session denial: %v", err)
	}
}

func TestProviderStopPreservesLegacyUnknownResumeHistory(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_1",
		"cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("Planning Turn intent: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = turn.CreatedAt

	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_1",
		"cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("b", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Planning Resume intent: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt
	unknown, err := NewPlanningResumeObservation(job, resume.ID, principal,
		"cmd_cccccccccccccccccccccccccccccccc", PlanningResumeUnknown, "", false, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("Planning Resume UNKNOWN observation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("legacy Job before Stop: %v", err)
	}

	if _, err := NewProviderStopIntent(job, executionID, principal, "session_1",
		"cmd_dddddddddddddddddddddddddddddddd", ProviderStopReasonCancelled, now.Add(3*time.Second)); err == nil {
		t.Fatal("new Provider Stop intent was accepted while the latest Resume is UNKNOWN")
	}

	start := job.ProviderStartIntents[0]
	commandID := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	legacyStop := ProviderStopIntent{
		ID: ProviderStopIntentID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: executionID, OperationID: ProviderStopOperationID(commandID),
		SessionID: "session_1", ProviderID: start.ProviderID, AccountID: start.AccountID,
		ConfigurationSHA256: start.ConfigurationSHA256, Reason: ProviderStopReasonCancelled,
		CreatedAt: now.Add(3 * time.Second), CreatedRevision: job.Revision + 1,
	}
	job.ProviderStopIntents = append(job.ProviderStopIntents, legacyStop)
	job.Revision++
	job.UpdatedAt = legacyStop.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("legacy Provider Stop after UNKNOWN Resume must remain readable: %v", err)
	}
}
