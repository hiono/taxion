package controller

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// ControllerReservePercent keeps account quota for the parent controller, whose
// own usage is outside Taxion records and stops when the shared quota is gone.
const ControllerReservePercent = 5.0

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
// for the same provider account and resource.
func accountDayStart(events []store.RuntimeReceipt, quota *runtime.QuotaSnapshot, now time.Time) *float64 {
	if quota == nil {
		return nil
	}
	today := now.UTC().Truncate(24 * time.Hour)
	var start *float64
	var at time.Time
	for _, receipt := range events {
		if receipt.Type != "budget-guidance" {
			continue
		}
		var body struct {
			Observation struct {
				Account *runtime.QuotaSnapshot `json:"account"`
			} `json:"observation"`
		}
		if json.Unmarshal([]byte(receipt.Payload), &body) != nil {
			continue
		}
		seen := body.Observation.Account
		if seen == nil || seen.State != "available" || seen.RemainingPercentage == nil ||
			seen.Provider != quota.Provider || seen.Account != quota.Account || seen.Resource != quota.Resource ||
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
	return start
}

func accountDenial(quota *runtime.QuotaSnapshot) error {
	switch quota.DenialReason {
	case "controller-reserve":
		return fmt.Errorf("taxion: account quota is at the %.0f%% controller reserve; no new Dispatch is started", ControllerReservePercent)
	case "daily-allocation":
		return fmt.Errorf("taxion: today's account quota allocation is used; no new Dispatch is started until the next UTC day")
	}
	return fmt.Errorf("taxion: account entitlement exhausted; paid overage is not authorized")
}
