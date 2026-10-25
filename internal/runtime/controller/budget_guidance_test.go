package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

type quotaRuntime struct {
	*configuredRuntime
	quota   runtime.QuotaSnapshot
	reads   int
	timeout time.Duration
	onRead  func()
}

func (f *quotaRuntime) ReadAccountQuota(ctx context.Context, account string) (runtime.QuotaSnapshot, error) {
	f.reads++
	deadline, ok := ctx.Deadline()
	if !ok || account != "ai-user" {
		return runtime.QuotaSnapshot{}, fmt.Errorf("read requires selected account and bounded deadline")
	}
	f.timeout = time.Until(deadline)
	if f.onRead != nil {
		f.onRead()
	}
	return f.quota, nil
}

func budgetPolicy(t *testing.T, mode string) *core.BudgetGuidancePolicy {
	t.Helper()
	policy, err := core.NewBudgetGuidance("", mode)
	if err != nil {
		t.Fatal(err)
	}
	policy.AccountAllocation = ""
	return policy
}

func observedQuota() runtime.QuotaSnapshot {
	entitlement, used, remaining := int64(50000), int64(12000), 76.0
	return runtime.QuotaSnapshot{Provider: "copilot", Account: "ai-user", Resource: "premium_interactions",
		Scope: "account", Unit: "requests", Source: "sdk.account.getQuota", State: "available",
		ObservedAt: time.Now().UTC(), Entitlement: &entitlement, Used: &used, RemainingPercentage: &remaining}
}

func TestGuidedAccountSendAndDurableReceipt(t *testing.T) {
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
	if err := control.Send(t.Context(), id, "do approved work"); err != nil {
		t.Fatal(err)
	}
	if control.Send(t.Context(), id, "duplicate execution") == nil {
		t.Fatal("guidance bypassed the existing one-execution gate")
	}
	if worker.reads != 1 || len(worker.sends) != 1 || worker.timeout <= 0 || worker.timeout > 5*time.Second ||
		!strings.Contains(worker.sends[0], `"unit":"requests"`) || strings.Contains(worker.sends[0], `"unit":"credits"`) {
		t.Fatal("send did not request one fresh quota observation per actual turn", worker.reads, worker.timeout, worker.sends)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, receipt := range state.Records.Events {
		if receipt.Type != "budget-guidance" {
			continue
		}
		var body struct {
			Observation budgetObservation `json:"observation"`
			Supplement  string            `json:"supplement"`
		}
		if json.Unmarshal([]byte(receipt.Payload), &body) != nil || receipt.Hash != receiptHash(receipt.Payload) ||
			body.Observation.Account.Account != "ai-user" || "do approved work"+body.Supplement != worker.sends[0] ||
			receipt.StreamID != "budget-guidance" {
			t.Fatal("receipt differs from the actual supplemental instruction", receipt)
		}
		count++
	}
	if count != 2 {
		t.Fatal("prepared and returned phases were not retained", count)
	}
	// Budget receipts must coexist with the provider event stream.
	if err := control.recordEvent(id, "provider-stream", runtime.Event{Sequence: 1, Type: runtime.EventOutput, Payload: "hello"}); err != nil {
		t.Fatal(err)
	}
}

func TestAccountGuidanceFailuresAndApprovedOverrides(t *testing.T) {
	for _, name := range []string{"unavailable", "stale", "expired", "unsupported", "mismatch", "provider-mismatch", "exhausted", "invalid", "off", "flow", "unapproved"} {
		t.Run(name, func(t *testing.T) {
			policy := budgetPolicy(t, "account")
			if name == "unapproved" {
				policy = budgetPolicy(t, "flow")
			}
			db, id, base, _ := executionConfigurationFixture(t, policy)
			defer db.Close()
			worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
			mode := ""
			switch name {
			case "unavailable", "unsupported":
				worker.quota.State, worker.quota.Reason = name, "explicit read failure"
			case "stale":
				worker.quota.ObservedAt = time.Now().Add(-61 * time.Second)
			case "expired":
				expiry := time.Now().Add(-time.Second)
				worker.quota.ExpiresAt = &expiry
			case "mismatch":
				worker.quota.Account = "other"
			case "provider-mismatch":
				worker.quota.Provider = "other"
			case "exhausted":
				worker.quota.ExecutionDenied = true
			case "invalid":
				bad := int64(-2)
				worker.quota.Used = &bad
			case "off", "flow":
				mode = name
			case "unapproved":
				mode = "account"
			}
			control, _ := New(db, worker)
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			err := control.SendWithBudgetGuidance(t.Context(), id, "execute", mode)
			denied := name != "off" && name != "flow"
			if (err != nil) != denied || denied && len(worker.sends) != 0 {
				t.Fatalf("deny=%t, sends=%v, err=%v", denied, worker.sends, err)
			}
			if name == "off" || name == "flow" || name == "unapproved" {
				if worker.reads != 0 {
					t.Fatal("non-account or unapproved mode read external quota")
				}
			} else if worker.reads != 1 {
				t.Fatal("account read retried or was skipped", worker.reads)
			}
			if name == "off" && worker.sends[0] != "execute" {
				t.Fatal("off added a model instruction")
			}
			if denied && worker.starts != 1 {
				t.Fatal("quota denial retried session creation")
			}
			if name == "stale" || name == "expired" || name == "invalid" || name == "unavailable" {
				state, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				receipt := state.Records.Events[len(state.Records.Events)-1]
				expected := `"state":"unavailable"`
				if name == "stale" || name == "expired" {
					expected = `"state":"stale"`
				}
				if !strings.Contains(receipt.Payload, `"phase":"denied"`) || !strings.Contains(receipt.Payload, expected) {
					t.Fatal("quota denial lost the unavailable/stale observation", receipt.Type)
				}
			}
		})
	}
}

func TestQuotaUnsupportedAndLegacyRemainDistinct(t *testing.T) {
	for _, mode := range []string{"", "account"} {
		t.Run(mode, func(t *testing.T) {
			var policy *core.BudgetGuidancePolicy
			if mode != "" {
				policy = budgetPolicy(t, mode)
			}
			db, id, worker, _ := executionConfigurationFixture(t, policy)
			defer db.Close()
			control, _ := New(db, worker)
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			err := control.Send(t.Context(), id, "execute")
			if mode == "account" {
				if err == nil || len(worker.sends) != 0 {
					t.Fatal("missing Copilot quota capability authorized sending")
				}
			} else if err != nil || worker.sends[0] != "execute" {
				t.Fatal("legacy behavior changed", err)
			}
			if mode == "" && control.SendWithBudgetGuidance(t.Context(), id, "execute", "flow") == nil {
				t.Fatal("legacy Dispatch silently adopted new guidance")
			}
		})
	}
}

func TestQuotaTimeoutIsBoundedByApprovedTimeAndResetIsNotExpiry(t *testing.T) {
	for _, deadline := range []time.Duration{30 * time.Millisecond, time.Minute} {
		db, _, base, selected := executionConfigurationFixture(t, budgetPolicy(t, "account"))
		defer db.Close()
		worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
		pastReset := time.Now().Add(-time.Hour)
		worker.quota.ResetAt = &pastReset
		ctx, cancel := context.WithTimeout(t.Context(), deadline)
		observation := &budgetObservation{Mode: "account"}
		result, err := readBudgetQuota(ctx, worker, selected.Config, observation)
		cancel()
		refreshQuotaState(result, time.Now().UTC())
		if err != nil || worker.reads != 1 || worker.timeout > 5*time.Second || worker.timeout > deadline ||
			result.Account.State != "available" {
			t.Fatal("read timeout or reset freshness contract violated", worker.timeout, err)
		}
	}
}

func TestFlowGuidanceUsesObservedCreditsWithoutTokenCeilings(t *testing.T) {
	for _, mode := range []string{"flow", "off"} {
		t.Run(mode, func(t *testing.T) {
			db, path, request := reviewFlowFixtureWithPolicy(t, "", budgetPolicy(t, mode))
			flow, err := StartReviewFlow(db, path, request)
			if err != nil {
				t.Fatal(err)
			}
			id := flow.DispatchIDs[0]
			worker := &fakeRuntime{role: "CODER", hash: "test", worktree: flow.Contract.Worktree}
			control, _ := New(db, worker)
			if _, err := control.Start(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			input, output := int64(20), int64(10)
			nanoAIU, creditStatus := 12.5, "complete"
			if err := control.Send(t.Context(), id, "execute"); err != nil {
				t.Fatal(err)
			}
			if err := control.RecordUsage(id, core.DispatchUsage{
				Model: "fixture", InputTokens: &input, OutputTokens: &output,
				TotalNanoAIU: &nanoAIU, AICreditsStatus: &creditStatus,
			}); err != nil {
				t.Fatal(err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			dispatch, _ := state.Graph.GraphDispatch(id)
			observation, err := control.prepareBudgetGuidance(t.Context(), dispatch, "")
			if err != nil {
				t.Fatal(err)
			}
			text, err := budgetSupplement(observation)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "flow" && (!strings.Contains(text, `"usedTokens":30`) ||
				!strings.Contains(text, `"reportedNanoAIU":12.5`) ||
				!strings.Contains(text, `"aiCreditsStatus":"complete"`) ||
				!strings.Contains(text, `"unfinishedRoles":["CODER","REVIEWER"]`)) {
				t.Fatal("flow observation did not use durable per-call usage", worker.sends)
			}
			input = 100000
			if err := control.RecordUsage(id, core.DispatchUsage{Model: "fixture", InputTokens: &input, OutputTokens: &output}); err != nil {
				t.Fatal(err)
			}
			if _, err := control.prepareBudgetGuidance(t.Context(), dispatch, ""); err != nil || len(worker.sends) != 1 {
				t.Fatal("observed token count alone denied the approved goal", err)
			}
		})
	}
}
