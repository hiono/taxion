package core

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestStageReservationsKeepOriginalClockAcrossRepairs(t *testing.T) {
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	plan := &StagePlan{Version: 1, Hash: "sha256:" + strings.Repeat("a", 64),
		TimeoutSeconds: 3600, MaxRepairs: 1, ValidationSeconds: 60, ReviewSeconds: 600, RepairSeconds: 600,
		Basis: []string{"test estimate"}}
	flow := ReviewFlow{StartedAt: start, Deadline: start.Add(time.Hour), MaxRepairs: 1, StagePlan: plan}
	if err := plan.Validate(flow); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		repair int
		role   string
		offset time.Duration
	}{{0, "CODER", 1680 * time.Second}, {0, "REVIEWER", 2340 * time.Second},
		{1, "CODER", 2940 * time.Second}, {1, "REVIEWER", time.Hour}} {
		flow.Repairs = test.repair
		if got := plan.SubmissionDeadline(flow, test.role); !got.Equal(start.Add(test.offset)) {
			t.Fatalf("repair %d role %s deadline %s", test.repair, test.role, got)
		}
	}
	copy := CloneStagePlan(plan)
	copy.Basis[0] = "changed"
	if plan.Basis[0] != "test estimate" {
		t.Fatal("stage plan clone aliases approval")
	}
	if (*StagePlan)(nil).Validate(flow) != nil {
		t.Fatal("legacy flows must remain readable")
	}
}

func TestStageNativeReservationsCannotDisappear(t *testing.T) {
	plan := StagePlan{MaxRepairs: 1}
	if plan.ValidateNativeReservations(100) == nil {
		t.Fatal("missing future-role estimates were accepted")
	}
	plan.ReviewNanoAIU, plan.RepairNanoAIU = 20, 30
	if plan.ValidateNativeReservations(100) != nil || plan.ValidateNativeReservations(70) == nil ||
		plan.ValidateNativeReservations(0) == nil {
		t.Fatal("native reservation boundary or provider-only binding was ignored")
	}
	plan = StagePlan{}
	if plan.ValidateNativeReservations(0) != nil {
		t.Fatal("provider-only planning unexpectedly requires native estimates")
	}
	plan.ReviewOnly = true
	if plan.ValidateNativeReservations(100) != nil {
		t.Fatal("one final review has no later role to reserve")
	}
}

func TestStageBudgetRejectsInvalidAndNonfiniteLimits(t *testing.T) {
	for _, policy := range []StageBudget{
		{Version: 2}, {Version: 1, ValidationSeconds: -1}, {Version: 1, ReviewSeconds: math.MaxInt64},
		{Version: 1, CoderSessionCredits: 29}, {Version: 1, ReviewerSessionCredits: math.NaN()},
		{Version: 1, ReviewNanoAIU: math.Inf(1)}, {Version: 1, RepairNanoAIU: -1},
	} {
		if policy.Validate() == nil {
			t.Fatalf("invalid stage budget accepted: %+v", policy)
		}
	}
}
