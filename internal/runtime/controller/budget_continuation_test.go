package controller

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestBudgetContinuationRequestsTopUpBeforeStopWithoutResettingUsage(t *testing.T) {
	number := func(n float64) *float64 { return &n }
	estimate := BudgetContinuationEstimate{
		Unit: "requests", Source: "read-only provider quota", Scope: "whole approved goal including predecessor flows",
		ObservedAt: time.Now().Add(-time.Second), Used: number(900), ApprovedCeiling: number(1050),
		RemainingWork: number(300), Repair: number(100), Review: number(150), Preservation: number(50),
	}
	report, err := EstimateBudgetContinuation(estimate)
	if err != nil || report.Remaining != 150 || report.Needed != 600 ||
		report.AdditionalRequested != 450 || report.ExecutionAuthorized || !strings.Contains(report.Action, "before the ceiling") {
		t.Fatal("late or incorrect top-up advice", report, err)
	}
	estimate.ApprovedCeiling = number(1500)
	report, err = EstimateBudgetContinuation(estimate)
	if err != nil || report.AdditionalRequested != 0 || *report.Estimate.Used != 900 ||
		report.Remaining != 600 || report.ExecutionAuthorized {
		t.Fatal("additional approval reset consumption or authorized execution", report, err)
	}
	estimate.Used = number(1500)
	report, err = EstimateBudgetContinuation(estimate)
	if err != nil || !strings.HasPrefix(report.Action, "BUDGET_STOP") {
		t.Fatal("exhausted ceiling ignored", report, err)
	}
	for _, invalid := range []*float64{nil, number(-1), number(math.Inf(1)), number(math.NaN())} {
		estimate.Review = invalid
		if _, err := EstimateBudgetContinuation(estimate); err == nil {
			t.Fatal("unknown or invalid estimate treated as free work")
		}
	}
}
