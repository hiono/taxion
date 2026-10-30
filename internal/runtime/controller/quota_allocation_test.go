package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestRunningAccountQuotaStopsBeforeAcceptingTurn(t *testing.T) {
	for _, failure := range []string{"reserve", "daily-allocation", "unavailable", "stale", "identity", "cycle", "resource", "missing-basis", "quiet"} {
		t.Run(failure, func(t *testing.T) {
			db, id, base, _ := executionConfigurationFixture(t, budgetPolicy(t, "account"))
			defer db.Close()
			worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
			reset := time.Now().UTC().Add(8 * 24 * time.Hour)
			worker.quota.ResetAt = &reset
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if err := control.Send(t.Context(), id, "execute"); err != nil {
				t.Fatal(err)
			}
			events := make(chan runtime.Event, 1)
			worker.events = events
			if failure == "quiet" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					for index := range state.Records.Events {
						state.Records.Events[index].At = time.Now().Add(-time.Minute)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				events <- runtime.Event{Type: runtime.EventTurnFinished}
			}
			if failure == "missing-basis" {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					events := state.Records.Events[:0]
					for _, receipt := range state.Records.Events {
						if receipt.Type != "budget-guidance" {
							events = append(events, receipt)
						}
					}
					state.Records.Events = events
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			switch failure {
			case "reserve", "quiet":
				remaining := 5.0
				worker.quota.RemainingPercentage = &remaining
			case "daily-allocation":
				remaining := 6.0
				worker.quota.RemainingPercentage = &remaining
			case "unavailable":
				worker.quota.State, worker.quota.Reason = "unavailable", "read failed"
			case "stale":
				worker.quota.ObservedAt = time.Now().Add(-2 * time.Minute)
			case "identity":
				worker.quota.Account = "other-account"
			case "cycle":
				next := reset.AddDate(0, 1, 0)
				worker.quota.ResetAt = &next
			case "resource":
				worker.quota.Resource = "different-resource"
			}
			dispatch, err := control.boundDispatch(id)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err = control.StreamEvents(ctx, dispatch, 0, io.Discard, nil)
			var stopped *budgetStopError
			expectedReads := 2
			if failure == "missing-basis" {
				expectedReads = 1
			}
			if !errors.As(err, &stopped) || worker.reads != expectedReads || worker.stops != 1 {
				t.Fatalf("quota did not stop the running session: reads=%d stops=%d err=%v", worker.reads, worker.stops, err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			saved, _ := state.Graph.GraphDispatch(id)
			if saved.Status != core.DispatchCancelled || saved.Result == nil {
				t.Fatal("running session cancellation was not saved", saved.Status)
			}
			observed := false
			for _, receipt := range state.Records.Events {
				observed = observed || receipt.Type == "budget-guidance" && strings.Contains(receipt.Payload, `"phase":"monitoring"`)
			}
			if !observed && failure != "missing-basis" {
				t.Fatal("quota observation was not durably recorded")
			}
		})
	}
}

func TestQuotaExecutionStopKeepsUnsupportedTimeOnlyDistinct(t *testing.T) {
	for _, provider := range []string{"copilot", "opencode"} {
		quota := &runtime.QuotaSnapshot{Provider: provider, State: "unsupported"}
		if (quotaExecutionStop(quota) != "") != (provider == "copilot") {
			t.Fatal("unsupported account monitoring was misclassified", provider)
		}
	}
}

func TestRunningAccountQuotaAllowsCurrentObservationAndApprovedOverrides(t *testing.T) {
	for _, mode := range []string{"account", "off", "flow"} {
		t.Run(mode, func(t *testing.T) {
			db, id, base, _ := executionConfigurationFixture(t, budgetPolicy(t, "account"))
			defer db.Close()
			worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			if err := control.SendWithBudgetGuidance(t.Context(), id, "execute", mode); err != nil {
				t.Fatal(err)
			}
			dispatch, err := control.boundDispatch(id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "account" {
				initial := &budgetObservation{Mode: mode, Account: &worker.quota}
				if err := control.monitorAccountQuota(t.Context(), dispatch, initial); err != nil || worker.reads != 2 || worker.stops != 0 {
					t.Fatal("current available quota stopped the runtime", err)
				}
				return
			}
			events := make(chan runtime.Event, 1)
			events <- runtime.Event{Type: runtime.EventTurnFinished}
			worker.events = events
			err = control.StreamEvents(t.Context(), dispatch, 0, io.Discard, nil)
			var stopped *budgetStopError
			if err == nil || errors.As(err, &stopped) || worker.reads != 0 {
				t.Fatal("approved override read account quota or became a quota stop", err)
			}
		})
	}
}

func TestAccountDayStartDoesNotReusePriorResetCycle(t *testing.T) {
	now := time.Now().UTC()
	previousReset := now.Add(-time.Hour)
	currentReset := now.AddDate(0, 1, 0)
	current := allocationQuota(45, &currentReset)
	previous := allocationQuota(10, &previousReset)
	previous.ObservedAt = now
	payload, err := json.Marshal(map[string]any{"observation": map[string]any{"account": previous}})
	if err != nil {
		t.Fatal(err)
	}
	receipt := store.RuntimeReceipt{Type: "budget-guidance", Payload: string(payload)}
	receipt.Hash = receiptHash(receipt.Payload)
	start, err := accountDayStart([]store.RuntimeReceipt{receipt}, current, now)
	if err != nil || start == nil || *start != 45 {
		t.Fatal("new cycle reused a prior-cycle day-start observation", start)
	}
}

func allocationQuota(remaining float64, reset *time.Time) *runtime.QuotaSnapshot {
	quota := observedQuota()
	quota.RemainingPercentage, quota.ResetAt = &remaining, reset
	return &quota
}

func TestAllocateAccountQuota(t *testing.T) {
	now := time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) // 8 UTC days including today
	start := 45.0                                         // share = (45-5)/8 = 5
	for _, tc := range []struct {
		name      string
		remaining float64
		dayStart  *float64
		reset     *time.Time
		reason    string
	}{
		{"within share", 40.1, &start, &reset, ""},
		{"share used", 40, &start, &reset, "daily-allocation"},
		{"reserve", 5, &start, &reset, "controller-reserve"},
		{"reserve without reset", 4.9, nil, nil, "controller-reserve"},
		{"no reset date keeps reserve only", 10, &start, nil, ""},
		{"no day start keeps reserve only", 10, nil, &reset, ""},
		{"recovered after reset", 5.5, func() *float64 { v := 5.0; return &v }(), &reset, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quota := allocationQuota(tc.remaining, tc.reset)
			allocateAccountQuota(quota, tc.dayStart, now)
			if quota.ExecutionDenied != (tc.reason != "") || quota.DenialReason != tc.reason {
				t.Fatal("unexpected allocation", quota.ExecutionDenied, quota.DenialReason)
			}
		})
	}
	unlimited := allocationQuota(1, &reset)
	unlimited.Unlimited = true
	allocateAccountQuota(unlimited, &start, now)
	if unlimited.ExecutionDenied {
		t.Fatal("unlimited entitlement was rationed")
	}
	exhausted := allocationQuota(0, &reset)
	exhausted.ExecutionDenied = true
	allocateAccountQuota(exhausted, &start, now)
	if exhausted.DenialReason != "entitlement-exhausted" || !strings.Contains(accountDenial(exhausted).Error(), "paid overage") {
		t.Fatal("existing exhaustion denial changed")
	}
}

func TestAccountDayStartUsesEarliestMatchingObservationToday(t *testing.T) {
	now := time.Date(2026, 10, 24, 6, 0, 0, 0, time.UTC)
	receipt := func(at time.Time, remaining float64, account string) store.RuntimeReceipt {
		quota := observedQuota()
		quota.ObservedAt, quota.RemainingPercentage, quota.Account = at, &remaining, account
		body, _ := json.Marshal(map[string]any{"observation": map[string]any{"account": quota}})
		payload := string(body)
		return store.RuntimeReceipt{Type: "budget-guidance", Payload: payload, Hash: receiptHash(payload)}
	}
	events := []store.RuntimeReceipt{
		receipt(now.Add(-7*time.Hour), 60, "ai-user"), // yesterday
		receipt(now.Add(-2*time.Hour), 44, "ai-user"),
		receipt(now.Add(-5*time.Hour), 45, "ai-user"),
		receipt(now.Add(-5*time.Hour), 90, "other"),
		{Type: "provider-stream", Payload: "{}"},
	}
	current := allocationQuota(40, nil)
	if start, err := accountDayStart(events, current, now); err != nil || start == nil || *start != 45 {
		t.Fatal("day start was not the earliest same-account observation today", start)
	}
	if start, err := accountDayStart(nil, current, now); err != nil || start == nil || *start != 40 {
		t.Fatal("first observation of the day was not its own start", start)
	}
	events[1].Payload += " "
	if _, err := accountDayStart(events, current, now); err == nil {
		t.Fatal("tampered account allocation evidence was accepted")
	}
	invalid := allocationQuota(101, nil)
	if _, err := accountDayStart([]store.RuntimeReceipt{allocationReceipt(t, &budgetObservation{Account: invalid})}, current, now); err == nil {
		t.Fatal("invalid saved quota percentage was accepted")
	}
}

func TestDailyAllocationDeniesSendBeforeModelTurn(t *testing.T) {
	db, id, base, _ := executionConfigurationFixture(t, budgetPolicy(t, "account"))
	defer db.Close()
	worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
	reserve := ControllerReservePercent
	worker.quota.RemainingPercentage = &reserve
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := control.Start(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	err = control.Send(t.Context(), id, "execute")
	if err == nil || !strings.Contains(err.Error(), "controller reserve") || len(worker.sends) != 0 {
		t.Fatal("reserve did not stop the Dispatch before the model turn", err, worker.sends)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, receipt := range state.Records.Events {
		found = found || receipt.Type == "budget-guidance" && strings.Contains(receipt.Payload, `"denialReason":"controller-reserve"`)
	}
	if !found {
		t.Fatal("reserve denial was not recorded")
	}
	more := 50.0
	worker.quota.RemainingPercentage = &more
	if err := control.Send(t.Context(), id, "execute"); err != nil || len(worker.sends) != 1 {
		t.Fatal("denied Dispatch was not retryable", err, worker.sends)
	}
}

func allocationReceipt(t *testing.T, observation *budgetObservation) store.RuntimeReceipt {
	t.Helper()
	body, err := json.Marshal(map[string]any{"observation": observation})
	if err != nil {
		t.Fatal(err)
	}
	payload := string(body)
	return store.RuntimeReceipt{Type: "budget-guidance", Payload: payload, Hash: receiptHash(payload)}
}

func TestResetDailyFloorIsFrozenUntilFinal24Hours(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	reset := start.Add(30 * 24 * time.Hour)
	initial := &budgetObservation{Account: allocationQuota(90, &reset)}
	if err := allocateResetDaily(initial, nil, start); err != nil || *initial.RemainingFloor != 87 {
		t.Fatal("first daily allowance incorrect", initial, err)
	}
	receipts := []store.RuntimeReceipt{allocationReceipt(t, initial)}
	for _, tc := range []struct {
		name      string
		at        time.Time
		remaining float64
		floor     float64
		denied    bool
	}{
		{"early floor", start.Add(time.Hour), 87, 87, true},
		{"before floor", start.Add(time.Hour), 87.01, 87, false},
		{"mid cycle", start.Add(15 * 24 * time.Hour), 42, 42, true},
		{"next window", start.Add(16 * 24 * time.Hour), 42, 39, false},
		{"remaining increase", start.Add(16 * 24 * time.Hour), 95, 39, false},
		{"before final day", reset.Add(-24*time.Hour - time.Nanosecond), 3, 3, true},
		{"final24h", reset.Add(-24 * time.Hour), 0.01, 0, false},
		{"final second", reset.Add(-time.Second), 0.01, 0, false},
		{"exhausted", reset.Add(-time.Second), 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observation := &budgetObservation{Account: allocationQuota(tc.remaining, &reset)}
			if err := allocateResetDaily(observation, receipts, tc.at); err != nil {
				t.Fatal(err)
			}
			if *observation.RemainingFloor != tc.floor || observation.Account.ExecutionDenied != tc.denied ||
				*observation.AllocationBasis != *initial.AllocationBasis {
				t.Fatalf("allocation drifted: %+v floor=%v", observation, *observation.RemainingFloor)
			}
			if tc.denied && accountDenial(observation.Account) == nil {
				t.Fatal("denial not surfaced")
			}
		})
	}
	other := &budgetObservation{Account: allocationQuota(30, &reset)}
	other.Account.Account = "other"
	if err := allocateResetDaily(other, receipts, start); err != nil || other.AllocationBasis.Remaining != 30 {
		t.Fatal("another account inherited the basis", err)
	}
}

func TestResetDailyRejectsUnavailableBasisAndPreservesEvidence(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(3 * 24 * time.Hour)
	initial := &budgetObservation{Account: allocationQuota(60, &reset)}
	if err := allocateResetDaily(initial, nil, now); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"no-reset", "expired", "corrupt-receipt", "missing-basis", "future-basis", "conflicting-basis", "unsupported", "unavailable", "stale", "unlimited"} {
		t.Run(failure, func(t *testing.T) {
			current := &budgetObservation{Account: allocationQuota(50, &reset)}
			receipts := []store.RuntimeReceipt{allocationReceipt(t, initial)}
			switch failure {
			case "unsupported", "unavailable", "stale":
				current.Account.Provider = "opencode"
				current.Account.State = failure
			case "unlimited":
				current.Account.Unlimited = true
			case "no-reset":
				current.Account.ResetAt = nil
			case "expired":
				current.Account.ResetAt = &now
			case "corrupt-receipt":
				receipts[0].Hash = "invalid"
			case "missing-basis", "future-basis":
				saved := *initial
				if failure == "missing-basis" {
					saved.AllocationBasis = nil
				} else {
					basis := *initial.AllocationBasis
					basis.StartedAt = now.Add(time.Hour)
					saved.AllocationBasis = &basis
				}
				receipts[0] = allocationReceipt(t, &saved)
			case "conflicting-basis":
				saved := *initial
				basis := *initial.AllocationBasis
				basis.Remaining++
				saved.AllocationBasis = &basis
				receipts = append(receipts, allocationReceipt(t, &saved))
			}
			before, _ := json.Marshal(receipts)
			if allocateResetDaily(current, receipts, now) == nil {
				t.Fatal("invalid allocation evidence accepted")
			}
			after, _ := json.Marshal(receipts)
			if string(before) != string(after) {
				t.Fatal("saved evidence was changed")
			}
		})
	}
}

func TestResetDailySendAndMonitorUseSavedFloor(t *testing.T) {
	policy := budgetPolicy(t, "account")
	policy.AccountAllocation = "reset-daily"
	db, id, base, _ := executionConfigurationFixture(t, policy)
	defer db.Close()
	worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
	reset := time.Now().UTC().Add(8 * 24 * time.Hour)
	worker.quota.ResetAt = &reset
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	var initial *budgetObservation
	for _, receipt := range state.Records.Events {
		if receipt.Type == "budget-guidance" {
			var saved struct {
				Observation *budgetObservation `json:"observation"`
			}
			if err := json.Unmarshal([]byte(receipt.Payload), &saved); err != nil {
				t.Fatal(err)
			}
			initial = saved.Observation
			break
		}
	}

	if initial == nil || initial.RemainingFloor == nil || initial.AllocationBasis == nil {
		t.Fatal("allocation basis was not saved")
	}
	dispatch, err := control.boundDispatch(id)
	if err != nil {
		t.Fatal(err)
	}
	remaining := *initial.RemainingFloor
	worker.quota.RemainingPercentage = &remaining
	var stopped *budgetStopError
	if err := control.monitorAccountQuota(t.Context(), dispatch, initial); !errors.As(err, &stopped) ||
		!strings.Contains(err.Error(), "remaining floor") {
		t.Fatal("monitor did not enforce the persisted floor", err)
	}
}

func TestResetDailyFinalDayAndMissingResetBeforeSend(t *testing.T) {
	for _, hasReset := range []bool{true, false} {
		t.Run(fmt.Sprint(hasReset), func(t *testing.T) {
			policy := budgetPolicy(t, "account")
			policy.AccountAllocation = "reset-daily"
			db, id, base, _ := executionConfigurationFixture(t, policy)
			defer db.Close()
			worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
			remaining := 4.0
			worker.quota.RemainingPercentage = &remaining
			if hasReset {
				reset := time.Now().UTC().Add(23 * time.Hour)
				worker.quota.ResetAt = &reset
			}
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			err = control.Send(t.Context(), id, "execute")
			if hasReset && (err != nil || len(worker.sends) != 1) {
				t.Fatal("final24h retained the legacy5% reserve", err)
			}
			if !hasReset && (err == nil || len(worker.sends) != 0) {
				t.Fatal("missing reset failed open", err)
			}
		})
	}
}
