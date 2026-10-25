package controller

import (
	"context"
	"encoding/json"
	"errors"
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
	start := accountDayStart([]store.RuntimeReceipt{{Type: "budget-guidance", Payload: string(payload)}}, current, now)
	if start == nil || *start != 45 {
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
		return store.RuntimeReceipt{Type: "budget-guidance", Payload: string(body)}
	}
	events := []store.RuntimeReceipt{
		receipt(now.Add(-7*time.Hour), 60, "ai-user"), // yesterday
		receipt(now.Add(-2*time.Hour), 44, "ai-user"),
		receipt(now.Add(-5*time.Hour), 45, "ai-user"),
		receipt(now.Add(-5*time.Hour), 90, "other"),
		{Type: "provider-stream", Payload: "{}"},
	}
	current := allocationQuota(40, nil)
	if start := accountDayStart(events, current, now); start == nil || *start != 45 {
		t.Fatal("day start was not the earliest same-account observation today", start)
	}
	if start := accountDayStart(nil, current, now); start == nil || *start != 40 {
		t.Fatal("first observation of the day was not its own start", start)
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
