package domain

import (
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
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_exact", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = now
	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_other", "cmd_cccccccccccccccccccccccccccccccc", strings.Repeat("b", 64), true, now.Add(time.Second)); err == nil {
		t.Fatal("resume accepted a different Provider Session")
	}
	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_exact", "cmd_dddddddddddddddddddddddddddddddd", strings.Repeat("b", 64), false, now.Add(time.Second)); err == nil {
		t.Fatal("resume accepted an unsupported reattachment capability")
	}
	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_exact", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("b", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resume.RoleExecutionID != executionID || resume.ProviderSessionID != "session_exact" {
		t.Fatalf("resume changed execution/session identity: %#v", resume)
	}
}

func TestPlanningResumeUnknownRequiresSameOperationReconciliation(t *testing.T) {
	job, principal, executionID := planningLifecycleJob(t)
	now := job.UpdatedAt.Add(time.Second)
	turn, err := NewPlanningTurnIntent(job, executionID, principal, "session_exact", "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("a", 64), 100, now)
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	job.PlanningTurnIntents = append(job.PlanningTurnIntents, turn)
	job.Revision++
	job.UpdatedAt = now

	resume, err := NewPlanningResumeIntent(job, executionID, principal, "session_exact", "cmd_cccccccccccccccccccccccccccccccc", strings.Repeat("b", 64), true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	job.PlanningResumeIntents = append(job.PlanningResumeIntents, resume)
	job.Revision++
	job.UpdatedAt = resume.CreatedAt

	unknown, err := NewPlanningResumeObservation(job, resume.ID, principal, "cmd_dddddddddddddddddddddddddddddddd", PlanningResumeUnknown, "", false, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("UNKNOWN observation: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, unknown)
	job.Revision++
	job.UpdatedAt = unknown.CreatedAt

	if _, err := NewPlanningResumeIntent(job, executionID, principal, "session_exact", "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", strings.Repeat("c", 64), true, now.Add(3*time.Second)); err == nil || !strings.Contains(err.Error(), "previous_resume_unknown") {
		t.Fatalf("new resume error = %v, want previous_resume_unknown", err)
	}

	resolved, err := NewPlanningResumeObservation(job, resume.ID, principal, "cmd_ffffffffffffffffffffffffffffffff", PlanningResumeAttached, strings.Repeat("d", 64), true, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("reconcile UNKNOWN resume: %v", err)
	}
	job.PlanningResumeObservations = append(job.PlanningResumeObservations, resolved)
	job.Revision++
	job.UpdatedAt = resolved.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after resume reconciliation: %v", err)
	}
}

