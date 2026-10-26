package controller

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type isolatedQuotaRuntime struct {
	*questionRuntime
	policy *core.BudgetGuidancePolicy
	quota  runtime.QuotaSnapshot
	reads  int
	onRead func(int, *runtime.QuotaSnapshot) error
}

func (w *isolatedQuotaRuntime) BudgetGuidancePolicy() *core.BudgetGuidancePolicy { return w.policy }

func (w *isolatedQuotaRuntime) ReadAccountQuota(ctx context.Context, account string) (runtime.QuotaSnapshot, error) {
	w.reads++
	if _, ok := ctx.Deadline(); !ok || account != w.quota.Account {
		return runtime.QuotaSnapshot{}, errors.New("quota read is not account-bound and timed")
	}
	quota := w.quota
	quota.ObservedAt = time.Now().UTC()
	if w.onRead != nil {
		if err := w.onRead(w.reads, &quota); err != nil {
			return quota, err
		}
	}
	return quota, nil
}

func TestIsolatedQuotaFinalGateAndCleanup(t *testing.T) {
	for _, entry := range []string{"question", "planner", "search"} {
		for _, failure := range []string{"", "reserve", "stale", "missing", "cycle", "resource", "unlimited", "stop-failed"} {
			t.Run(entry+"/"+failure, func(t *testing.T) {
				policy := budgetPolicy(t, "account")
				db, path, base, selected := questionFixture(t, policy)
				base.role = "ORCHESTRATOR"
				worker := &isolatedQuotaRuntime{
					questionRuntime: &questionRuntime{configuredRuntime: base, events: questionEventsWithUsage(savedQuestionAnswer, "")},
					policy:          policy, quota: observedQuota(),
				}
				if entry == "planner" {
					worker.events = questionEventsWithUsage(`{"recommendation":"HUMAN_REQUIRED","confirmed":[{"text":"Task status","sources":["/planner/task/status"]}],"assumptions":[],"unconfirmed":["Live state unknown."]}`, "")
				}
				reset := time.Now().Add(8 * 24 * time.Hour)
				worker.quota.ResetAt = &reset
				worker.onRead = func(n int, quota *runtime.QuotaSnapshot) error {
					if n == 1 {
						return nil
					}
					switch failure {
					case "reserve", "stop-failed":
						v := 5.0
						quota.RemainingPercentage = &v
					case "stale":
						quota.ObservedAt = time.Now().Add(-2 * time.Minute)
					case "missing":
						return errors.New("private provider error")
					case "cycle":
						v := reset.Add(24 * time.Hour)
						quota.ResetAt = &v
					case "resource":
						quota.Resource = "other"
					case "unlimited":
						quota.Unlimited = true
					}
					return nil
				}
				if failure == "stop-failed" {
					worker.stopErr = errors.New("stop unavailable")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				var err error
				var observations []*budgetObservation
				var initial *budgetObservation
				accepted := false
				if entry == "search" {
					result, runErr := RunSearchTurn(ctx, worker, "task", "query")
					err, observations, initial = runErr, result.QuotaObservations, result.BudgetGuidance
					accepted = runErr == nil
				} else {
					ask := AskQuestion
					if entry == "planner" {
						ask = AskPlanner
					}
					result, runErr := ask(ctx, db, filepath.Join(filepath.Dir(path), "ledger.json"), "task", "status?", selected.Config, worker)
					err, observations, initial = runErr, result.QuotaObservations, result.BudgetGuidance
					accepted = result.Answer != nil
				}
				if worker.stops != 1 || worker.reads != 2 || len(observations) != 1 || len(worker.sends) != 1 ||
					initial == nil || *initial.Account.RemainingPercentage != 76 {
					t.Fatal("quota observations or initial basis/cleanup lost", worker.reads, worker.stops, observations)
				}
				if entry != "search" {
					state, err := db.LoadWorkspace()
					if err != nil {
						t.Fatal(err)
					}
					count := 0
					for _, receipt := range state.Records.Requests {
						if receipt.Kind == "planning" {
							count++
							if receipt.RequestID == "" || receipt.Hash != receiptHash(receipt.Payload) {
								t.Fatal("isolated quota evidence not bound to session or contents")
							}
						}
					}
					if count != 2 {
						t.Fatal("initial and final observations not persisted", count)
					}
				}

				if failure == "" {
					if err != nil || !accepted {
						t.Fatal("valid final quota refused", err)
					}
					return
				}
				var stopped *budgetStopError
				if !errors.As(err, &stopped) || accepted || strings.Contains(err.Error(), "private provider error") {
					t.Fatal("unsafe quota accepted or provider details leaked", err)
				}
				if failure == "stop-failed" && !strings.Contains(err.Error(), "cleanup") && !strings.Contains(err.Error(), "stop isolated") {
					t.Fatal("stop failure hidden", err)
				}
			})
		}
	}
}

func TestIsolatedQuotaUnavailableBeforeSend(t *testing.T) {
	for _, entry := range []string{"question", "search"} {
		t.Run(entry, func(t *testing.T) {
			policy := budgetPolicy(t, "account")
			db, path, base, selected := questionFixture(t, policy)
			base.role = "ORCHESTRATOR"
			worker := &isolatedQuotaRuntime{
				questionRuntime: &questionRuntime{configuredRuntime: base, events: questionEvents(savedQuestionAnswer)},
				policy:          policy, quota: observedQuota(),
			}
			worker.quota.State, worker.quota.Reason = "unavailable", "quota unavailable"
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var err error
			if entry == "search" {
				_, err = RunSearchTurn(ctx, worker, "task", "query")
			} else {
				_, err = AskQuestion(ctx, db, filepath.Join(filepath.Dir(path), "ledger.json"), "task", "status?", selected.Config, worker)
			}
			var stopped *budgetStopError
			if !errors.As(err, &stopped) || worker.reads != 1 || len(worker.sends) != 0 || worker.stops != 1 {
				t.Fatal("unknown quota did not prevent initial send", err, worker.reads, len(worker.sends), worker.stops)
			}
		})
	}
}

func TestIsolatedNonAccountModesDoNotReadQuota(t *testing.T) {
	for _, mode := range []string{"off", "flow", "provider"} {
		t.Run(mode, func(t *testing.T) {
			policy := budgetPolicy(t, mode)
			db, path, base, selected := questionFixture(t, policy)
			base.role = "ORCHESTRATOR"
			worker := &isolatedQuotaRuntime{
				questionRuntime: &questionRuntime{configuredRuntime: base, events: questionEvents(savedQuestionAnswer)},
				policy:          policy, quota: observedQuota(),
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := AskQuestion(ctx, db, filepath.Join(filepath.Dir(path), "ledger.json"), "task", "status?", selected.Config, worker)
			if err != nil || result.Answer == nil || worker.reads != 0 || len(result.QuotaObservations) != 0 {
				t.Fatal("non-account question queried account quota", err)
			}
			worker.events = questionEvents(savedQuestionAnswer)
			turn, err := RunSearchTurn(ctx, worker, "task", "query")
			if err != nil || worker.reads != 0 || len(turn.QuotaObservations) != 0 {
				t.Fatal("non-account search queried account quota", err)
			}
		})
	}
}

func TestIsolatedQuotaQuietTurnStopsWithoutUsage(t *testing.T) {
	for _, entry := range []string{"question", "search"} {
		t.Run(entry, func(t *testing.T) {
			policy := budgetPolicy(t, "account")
			db, path, base, selected := questionFixture(t, policy)
			base.role = "ORCHESTRATOR"
			worker := &isolatedQuotaRuntime{
				questionRuntime: &questionRuntime{configuredRuntime: base, events: make(chan runtime.Event)},
				policy:          policy, quota: observedQuota(),
			}
			worker.onRead = func(n int, quota *runtime.QuotaSnapshot) error {
				if n == 1 {
					quota.ObservedAt = time.Now().Add(-59 * time.Second)
				} else {
					v := 5.0
					quota.RemainingPercentage = &v
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var err error
			if entry == "search" {
				_, err = RunSearchTurn(ctx, worker, "task", "query")
			} else {
				_, err = AskQuestion(ctx, db, filepath.Join(filepath.Dir(path), "ledger.json"), "task", "status?", selected.Config, worker)
			}
			var stopped *budgetStopError
			if !errors.As(err, &stopped) || worker.reads != 2 || worker.stops != 1 {
				t.Fatal("quiet isolated turn did not stop on quota", err, worker.reads, worker.stops)
			}
		})
	}
}

func TestIsolatedQuotaCannotLoseAvailableCapability(t *testing.T) {
	db, _, base, selected := executionConfigurationFixture(t)
	defer db.Close()
	selected.Config.Runtime.Provider = "opencode"
	worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
	worker.quota.Provider = "opencode"
	initial := &budgetObservation{Mode: "account", Account: &worker.quota}
	snapshot := worker.quota
	initial.Account = &snapshot
	worker.quota.State, worker.quota.Reason = "unsupported", "quota no longer exposed"
	current, err := monitorIsolatedQuota(t.Context(), worker, selected.Config, initial, time.Now().UTC())
	var stopped *budgetStopError
	if !errors.As(err, &stopped) || current.Account.State != "unsupported" {
		t.Fatal("available quota silently degraded to time-only execution", current, err)
	}
	snapshot.State, snapshot.Reason = "unsupported", "quota never exposed"
	if _, err := monitorIsolatedQuota(t.Context(), worker, selected.Config, initial, time.Now().UTC()); err != nil {
		t.Fatal("initially unsupported non-Copilot legacy execution was not preserved", err)
	}
}

func TestIsolatedPlanningBasisIsReusedByDispatch(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(8 * 24 * time.Hour)
	initial := &budgetObservation{Mode: "account", Account: allocationQuota(76, &reset)}
	if err := allocateResetDaily(initial, nil, now); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Observation *budgetObservation `json:"observation"`
	}{initial})
	if err != nil {
		t.Fatal(err)
	}
	records := store.Records{Requests: []store.RequestRecord{{
		Kind: "planning", Payload: string(payload), Hash: receiptHash(string(payload)),
	}}}
	lower := &budgetObservation{Mode: "account", Account: allocationQuota(50, &reset)}
	if err := allocateResetDaily(lower, accountAllocationReceipts(records), now); err != nil ||
		lower.AllocationBasis.Remaining != 76 || !lower.Account.ExecutionDenied {
		t.Fatal("later Dispatch rebased the saved planning allowance", lower, err)
	}
}

func TestIsolatedResetDailyKeepsInitialBasis(t *testing.T) {
	db, _, base, selected := executionConfigurationFixture(t)
	defer db.Close()
	worker := &quotaRuntime{configuredRuntime: base, quota: observedQuota()}
	now := time.Now().UTC()
	reset := now.Add(8 * 24 * time.Hour)
	worker.quota.ResetAt = &reset
	initial := &budgetObservation{Mode: "account", Source: "request", Account: &worker.quota}
	if err := allocateResetDaily(initial, nil, now); err != nil {
		t.Fatal(err)
	}
	remaining := *initial.RemainingFloor
	worker.quota.RemainingPercentage = &remaining
	// Preserve the original snapshot instead of aliasing the mutable fake runtime.
	old := 76.0
	snapshot := worker.quota
	snapshot.RemainingPercentage = &old
	initial.Account = &snapshot
	current, err := monitorIsolatedQuota(t.Context(), worker, selected.Config, initial, now)
	var stopped *budgetStopError
	if !errors.As(err, &stopped) || current.AllocationBasis.Remaining != 76 ||
		*current.RemainingFloor != *initial.RemainingFloor {
		t.Fatal("isolated monitor rebased the approved allowance", current, err)
	}
}
