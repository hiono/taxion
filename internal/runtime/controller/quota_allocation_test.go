package controller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

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
