package controller

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// BudgetContinuationEstimate is an explicitly scoped observation plus estimates,
// never a currency conversion, new approval or permission to replay saved work.
type BudgetContinuationEstimate struct {
	Unit            string    `json:"unit"`
	Source          string    `json:"source"`
	Scope           string    `json:"scope"`
	ObservedAt      time.Time `json:"observedAt"`
	Used            *float64  `json:"used"`
	ApprovedCeiling *float64  `json:"approvedCeiling"`
	RemainingWork   *float64  `json:"remainingWork"`
	Repair          *float64  `json:"repair"`
	Review          *float64  `json:"review"`
	Preservation    *float64  `json:"preservation"`
}

type BudgetContinuationAdvice struct {
	Estimate            BudgetContinuationEstimate `json:"estimate"`
	Remaining           float64                    `json:"remaining"`
	Needed              float64                    `json:"needed"`
	AdditionalRequested float64                    `json:"additionalRequested"`
	Action              string                     `json:"action"`
	ExecutionAuthorized bool                       `json:"executionAuthorized"`
}

func EstimateBudgetContinuation(estimate BudgetContinuationEstimate) (BudgetContinuationAdvice, error) {
	if estimate.Unit != "requests" && estimate.Unit != "tokens" && estimate.Unit != "credits" && estimate.Unit != "nanoAIU" ||
		strings.TrimSpace(estimate.Source) == "" || strings.TrimSpace(estimate.Scope) == "" ||
		core.Redact(estimate.Source) != estimate.Source || core.Redact(estimate.Scope) != estimate.Scope ||
		estimate.ObservedAt.IsZero() ||
		estimate.ObservedAt.After(time.Now()) {
		return BudgetContinuationAdvice{}, fmt.Errorf("taxion: continuation estimate requires an explicit unit, observation source and nonfuture timestamp")
	}
	for _, value := range []*float64{estimate.Used, estimate.ApprovedCeiling, estimate.RemainingWork, estimate.Repair, estimate.Review, estimate.Preservation} {
		if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return BudgetContinuationAdvice{}, fmt.Errorf("taxion: missing, negative or nonfinite continuation observations/estimates; unknown is not zero")
		}
	}
	needed := *estimate.RemainingWork + *estimate.Repair + *estimate.Review + *estimate.Preservation
	if math.IsInf(needed, 0) {
		return BudgetContinuationAdvice{}, fmt.Errorf("taxion: continuation estimate overflow")
	}
	report := BudgetContinuationAdvice{Estimate: estimate, Remaining: math.Max(*estimate.ApprovedCeiling-*estimate.Used, 0), Needed: needed}
	report.AdditionalRequested = math.Max(*estimate.Used+needed-*estimate.ApprovedCeiling, 0)
	if math.IsInf(report.AdditionalRequested, 0) {
		return BudgetContinuationAdvice{}, fmt.Errorf("taxion: continuation estimate overflow")
	}
	report.Action = "continue only already-authorized work; reuse saved evidence and preserve a stop reserve"
	if report.AdditionalRequested > 0 {
		report.Action = "request one bounded top-up before the ceiling; preserve current results while awaiting consent; resume unfinished work, not a baseline restart"
	}
	if *estimate.Used >= *estimate.ApprovedCeiling {
		report.Action = "BUDGET_STOP: no new chargeable work; preserve results and request the displayed bounded top-up; approval must not reset cumulative usage or deadlines"
	}
	return report, nil
}
