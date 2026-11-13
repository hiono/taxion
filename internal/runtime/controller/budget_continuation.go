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
	Time            *TimeContinuationEstimate `json:"time,omitempty"`
	Unit            string                    `json:"unit"`
	Source          string                    `json:"source"`
	Scope           string                    `json:"scope"`
	ObservedAt      time.Time                 `json:"observedAt"`
	Used            *float64                  `json:"used"`
	ApprovedCeiling *float64                  `json:"approvedCeiling"`
	RemainingWork   *float64                  `json:"remainingWork"`
	Repair          *float64                  `json:"repair"`
	Review          *float64                  `json:"review"`
	Preservation    *float64                  `json:"preservation"`
}

type BudgetContinuationAdvice struct {
	Time                *TimeContinuationAdvice    `json:"time,omitempty"`
	Estimate            BudgetContinuationEstimate `json:"estimate"`
	Remaining           float64                    `json:"remaining"`
	Needed              float64                    `json:"needed"`
	AdditionalRequested float64                    `json:"additionalRequested"`
	Action              string                     `json:"action"`
	ExecutionAuthorized bool                       `json:"executionAuthorized"`
}

type TimeContinuationEstimate struct {
	Deadline            time.Time `json:"deadline"`
	RemainingSeconds    *int64    `json:"remainingSeconds"`
	RepairSeconds       *int64    `json:"repairSeconds"`
	ReviewSeconds       *int64    `json:"reviewSeconds"`
	PreservationSeconds *int64    `json:"preservationSeconds"`
	ResponseWaitSeconds *int64    `json:"responseWaitSeconds"`
}

type TimeContinuationAdvice struct {
	PreviousDeadline  time.Time `json:"previousDeadline"`
	ProposedDeadline  time.Time `json:"proposedDeadline"`
	NeededSeconds     int64     `json:"neededSeconds"`
	AdditionalSeconds int64     `json:"additionalSeconds"`
}

func estimateTimeContinuation(estimate TimeContinuationEstimate, now time.Time) (*TimeContinuationAdvice, error) {
	var needed int64
	for _, seconds := range []*int64{estimate.RemainingSeconds, estimate.RepairSeconds, estimate.ReviewSeconds, estimate.PreservationSeconds, estimate.ResponseWaitSeconds} {
		if seconds == nil || *seconds < 0 || *seconds > 365*24*3600 {
			return nil, fmt.Errorf("taxion: time continuation requires explicit nonnegative estimates within one year; unknown is not zero")
		}
		needed += *seconds
	}
	if estimate.Deadline.IsZero() || needed > 365*24*3600 {
		return nil, fmt.Errorf("taxion: time continuation requires a saved deadline and bounded remaining stages")
	}
	proposed := now.Add(time.Duration(needed) * time.Second)
	if proposed.Before(estimate.Deadline) {
		proposed = estimate.Deadline
	}
	return &TimeContinuationAdvice{PreviousDeadline: estimate.Deadline, ProposedDeadline: proposed, NeededSeconds: needed,
		AdditionalSeconds: int64(math.Ceil(proposed.Sub(estimate.Deadline).Seconds()))}, nil
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
	if estimate.Time != nil {
		var err error
		report.Time, err = estimateTimeContinuation(*estimate.Time, time.Now().UTC())
		if err != nil {
			return BudgetContinuationAdvice{}, err
		}
	}
	report.AdditionalRequested = math.Max(*estimate.Used+needed-*estimate.ApprovedCeiling, 0)
	if math.IsInf(report.AdditionalRequested, 0) {
		return BudgetContinuationAdvice{}, fmt.Errorf("taxion: continuation estimate overflow")
	}
	report.Action = "continue only already-authorized work; reuse saved evidence and preserve a stop reserve"
	if report.AdditionalRequested > 0 || report.Time != nil && report.Time.AdditionalSeconds > 0 {
		report.Action = "request one combined bounded time/budget extension before the ceiling; keep authorized execution running while awaiting consent; preserve results and cumulative usage, not a baseline restart"
	}
	if *estimate.Used >= *estimate.ApprovedCeiling {
		report.Action = "BUDGET_STOP: no new chargeable work; preserve results and request the displayed bounded top-up; approval must not reset cumulative usage or deadlines"
	}
	return report, nil
}
