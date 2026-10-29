package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// ControllerReservePercent keeps account quota for the parent controller, whose
// own usage is outside Taxion records and stops when the shared quota is gone.
const ControllerReservePercent = 5.0

type quotaAllocationBasis struct {
	StartedAt time.Time `json:"startedAt"`
	ResetAt   time.Time `json:"resetAt"`
	Remaining float64   `json:"remainingPercentage"`
}

func allocateResetDaily(observation *budgetObservation, receipts []store.RuntimeReceipt, now time.Time) error {
	observation.AccountAllocation = "reset-daily"
	quota := observation.Account
	if quota == nil || quota.State != "available" || quota.Unlimited {
		return fmt.Errorf("taxion: reset-daily allocation requires an available finite account quota")
	}
	if quota.ResetAt == nil || !quota.ResetAt.After(now) || quota.RemainingPercentage == nil {
		return fmt.Errorf("taxion: reset-daily allocation requires current remaining quota and a future reset")
	}
	basis := quotaAllocationBasis{StartedAt: now, ResetAt: *quota.ResetAt, Remaining: *quota.RemainingPercentage}
	savedBasis := false
	for _, receipt := range receipts {
		if receipt.Type != "budget-guidance" {
			continue
		}
		var saved struct {
			Observation *budgetObservation `json:"observation"`
		}
		if receipt.Hash != receiptHash(receipt.Payload) || json.Unmarshal([]byte(receipt.Payload), &saved) != nil {
			return fmt.Errorf("taxion: saved account allocation evidence is invalid")
		}
		if saved.Observation == nil || saved.Observation.AccountAllocation != "reset-daily" {
			continue
		}
		previous := saved.Observation.Account
		if previous == nil || previous.Provider != quota.Provider || previous.Account != quota.Account ||
			previous.Resource != quota.Resource || previous.Unit != quota.Unit || previous.Scope != quota.Scope ||
			previous.ResetAt == nil || !previous.ResetAt.Equal(*quota.ResetAt) {
			continue
		}
		if previous.State != "available" || previous.Unlimited {
			continue
		}
		candidate := saved.Observation.AllocationBasis
		if candidate == nil || !candidate.ResetAt.Equal(*quota.ResetAt) || candidate.StartedAt.IsZero() ||
			candidate.StartedAt.After(now) || !candidate.ResetAt.After(candidate.StartedAt) ||
			math.IsNaN(candidate.Remaining) || math.IsInf(candidate.Remaining, 0) ||
			candidate.Remaining < 0 || candidate.Remaining > 100 {
			return fmt.Errorf("taxion: saved account allocation basis is invalid")
		}
		if savedBasis && (!candidate.StartedAt.Equal(basis.StartedAt) || candidate.Remaining != basis.Remaining) {
			return fmt.Errorf("taxion: saved account allocation bases conflict")
		}
		if !savedBasis {
			basis = *candidate
			savedBasis = true
		}
	}
	days := math.Ceil(basis.ResetAt.Sub(basis.StartedAt).Hours() / 24)
	daysLeft := math.Ceil(basis.ResetAt.Sub(now).Hours() / 24)
	floor := basis.Remaining * (daysLeft - 1) / days
	observation.AllocationBasis, observation.RemainingFloor = &basis, &floor
	if quota.ExecutionDenied || *quota.RemainingPercentage <= 0 {
		quota.ExecutionDenied, quota.DenialReason = true, "entitlement-exhausted"
	} else if *quota.RemainingPercentage <= floor {
		quota.ExecutionDenied, quota.DenialReason = true, "reset-daily-floor"
	}
	return nil
}

// allocateAccountQuota denies a new Dispatch when the shared account falls to the
// controller reserve, or when today's decrease already used today's share of the
// quota above the reserve. Percentages are compared directly, so no unit conversion
// between quota requests, AI Credits and nano-AIU is needed.
func allocateAccountQuota(quota *runtime.QuotaSnapshot, dayStart *float64, now time.Time) {
	if quota == nil || quota.State != "available" || quota.Unlimited || quota.ExecutionDenied ||
		quota.RemainingPercentage == nil {
		if quota != nil && quota.ExecutionDenied && quota.DenialReason == "" {
			quota.DenialReason = "entitlement-exhausted"
		}
		return
	}
	remaining := *quota.RemainingPercentage
	if remaining <= ControllerReservePercent {
		quota.ExecutionDenied, quota.DenialReason = true, "controller-reserve"
		return
	}
	if dayStart == nil || quota.ResetAt == nil || remaining > *dayStart {
		return
	}
	today := now.UTC().Truncate(24 * time.Hour)
	days := math.Ceil(quota.ResetAt.Sub(today).Hours() / 24)
	if days < 1 {
		return
	}
	share := (*dayStart - ControllerReservePercent) / days
	if *dayStart-remaining >= share {
		quota.ExecutionDenied, quota.DenialReason = true, "daily-allocation"
	}
}

// accountDayStart returns the earliest available remaining percentage recorded today
// for the same provider account, resource and reset cycle.
func accountDayStart(events []store.RuntimeReceipt, quota *runtime.QuotaSnapshot, now time.Time) (*float64, error) {
	if quota == nil {
		return nil, nil
	}
	today := now.UTC().Truncate(24 * time.Hour)
	var start *float64
	var at time.Time
	for _, receipt := range events {
		if receipt.Type != "budget-guidance" {
			continue
		}
		if receipt.Hash != receiptHash(receipt.Payload) {
			return nil, fmt.Errorf("taxion: saved account allocation receipt is invalid")
		}
		var body struct {
			Observation struct {
				Account *runtime.QuotaSnapshot `json:"account"`
			} `json:"observation"`
		}
		if json.Unmarshal([]byte(receipt.Payload), &body) != nil {
			return nil, fmt.Errorf("taxion: saved account allocation receipt is invalid")
		}
		seen := body.Observation.Account
		if seen == nil || seen.Provider != quota.Provider || seen.Account != quota.Account ||
			seen.Resource != quota.Resource || seen.Scope != quota.Scope || seen.Unit != quota.Unit ||
			seen.State != "available" {
			continue
		}
		if seen.RemainingPercentage == nil || seen.ObservedAt.IsZero() ||
			math.IsNaN(*seen.RemainingPercentage) || math.IsInf(*seen.RemainingPercentage, 0) ||
			*seen.RemainingPercentage < 0 || *seen.RemainingPercentage > 100 {
			return nil, fmt.Errorf("taxion: saved account allocation receipt is invalid")
		}
		if (seen.ResetAt == nil) != (quota.ResetAt == nil) ||
			seen.ResetAt != nil && quota.ResetAt != nil && !seen.ResetAt.Equal(*quota.ResetAt) ||
			seen.ObservedAt.Before(today) || seen.ObservedAt.After(now) {
			continue
		}
		if start == nil || seen.ObservedAt.Before(at) {
			value := *seen.RemainingPercentage
			start, at = &value, seen.ObservedAt
		}
	}
	if start == nil && quota.RemainingPercentage != nil {
		value := *quota.RemainingPercentage
		start = &value
	}
	return start, nil
}

func accountDenial(quota *runtime.QuotaSnapshot) error {
	switch quota.DenialReason {
	case "reset-daily-floor":
		return fmt.Errorf("taxion: approved account remaining floor reached; execution is not authorized in this allocation window")
	case "controller-reserve":
		return fmt.Errorf("taxion: account quota is at the %.0f%% controller reserve; execution is not authorized", ControllerReservePercent)
	case "daily-allocation":
		return fmt.Errorf("taxion: today's account quota allocation is used; execution is not authorized until the next UTC day")
	}
	return fmt.Errorf("taxion: account entitlement exhausted; paid overage is not authorized")
}

func quotaExecutionStop(quota *runtime.QuotaSnapshot) string {
	if quota == nil {
		return "account quota observation is missing"
	}
	if quota.State == "unsupported" && quota.Provider != "copilot" {
		return ""
	}
	if quota.State != "available" {
		return "account quota is unavailable or stale; remaining allowance is unverified"
	}
	if !quota.Unlimited && quota.RemainingPercentage == nil {
		return "account quota remaining percentage is missing"
	}
	if quota.ExecutionDenied {
		return accountDenial(quota).Error()
	}
	return ""
}

func (c *Controller) monitorAccountQuota(ctx context.Context, dispatch core.GraphDispatch, initial *budgetObservation) error {
	observation, readErr := c.prepareBudgetGuidance(ctx, dispatch, "account")
	refreshQuotaState(observation, time.Now().UTC())
	reason := ""
	if readErr != nil {
		reason = "account quota monitoring failed; provider details withheld"
	} else if observation == nil {
		reason = "account quota monitoring observation is missing"
	} else {
		reason = quotaExecutionStop(observation.Account)
	}
	if observation == nil {
		observation = &budgetObservation{Mode: "account", Source: initial.Source, ObservedAt: time.Now().UTC()}
	}
	if reason == "" && initial.Account != nil && initial.Account.State == "available" &&
		(observation.Account == nil || observation.Account.State != "available") {
		reason = "account quota became unavailable during execution"
	}
	if reason == "" && initial.Account != nil && initial.Account.ResetAt != nil {
		reset := initial.Account.ResetAt
		current := observation.Account
		if !time.Now().Before(*reset) || current == nil || current.ResetAt == nil || !current.ResetAt.Equal(*reset) {
			reason = "account quota cycle changed; a running Dispatch cannot use a new cycle allowance"
		}
	}
	if reason == "" && initial.Account != nil && initial.Account.State == "available" && observation.Account != nil &&
		(initial.Account.Resource != observation.Account.Resource || initial.Account.Unit != observation.Account.Unit ||
			initial.Account.Scope != observation.Account.Scope) {
		reason = "account quota resource basis changed during execution"
	}
	recordErr := c.recordBudgetGuidance(dispatch, observation, "monitoring", readErr)
	if reason == "" {
		return recordErr
	}
	return errors.Join(recordErr, c.stopForAccountQuota(dispatch, reason))
}

func (c *Controller) stopForAccountQuota(dispatch core.GraphDispatch, reason string) error {
	budgetErr := &budgetStopError{reason: reason}
	state, loadErr := c.store.LoadWorkspace()
	if loadErr != nil {
		return errors.Join(budgetErr, loadErr)
	}
	var stopErr error
	if flow, ok := flowForDispatch(state.Graph, dispatch.ID); ok && flow.Status == "RUNNING" {
		stopErr = stopFlowForBudget(c.store, flow.ID, reason)
	}
	return errors.Join(budgetErr, stopErr)
}
