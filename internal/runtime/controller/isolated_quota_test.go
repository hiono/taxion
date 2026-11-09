package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestSearchPreservesLiveAndDrainedUsageCheckpoints(t *testing.T) {
	for _, mode := range []string{"live", "drained", "stopped"} {
		t.Run(mode, func(t *testing.T) {
			db, _, base, _ := questionFixture(t)
			defer db.Close()
			base.role = "ORCHESTRATOR"
			payload := `{"model":"test","provider":{"id":"provider","kind":"openai","label":"Provider"},"inputTokens":10,"outputTokens":5,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":3.5,"aiCreditsStatus":"complete"}`
			var usage core.DispatchUsage
			if err := json.Unmarshal([]byte(payload), &usage); err != nil {
				t.Fatal(err)
			}
			checkpoint := runtime.UsageCheckpoint{
				UsageTotals: []core.DispatchUsage{usage}, TotalNanoAIU: usage.TotalNanoAIU,
				AICreditsStatus: usage.AICreditsStatus,
				ProviderModelMetrics: []core.ProviderModelMetric{{
					ModelID: &usage.Model, Provider: usage.Provider,
					Metrics: core.ProviderModelUsageMetrics{
						AICreditsStatus: usage.AICreditsStatus, TotalNanoAIU: usage.TotalNanoAIU,
						Usage: core.ProviderModelTokenUsage{InputTokens: 10, OutputTokens: 5},
					},
				}},
			}
			body, err := json.Marshal(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			source := managedSearchEvents(payload)
			close(source)
			events := make(chan runtime.Event, len(source)+1)
			sequence := uint64(0)
			emit := func(event runtime.Event) {
				sequence++
				event.Sequence = sequence
				events <- event
			}
			for event := range source {
				if mode == "live" && event.Type == runtime.EventTurnEnded ||
					mode == "drained" && event.Type == runtime.EventTurnFinished {
					emit(runtime.Event{Type: runtime.EventUsageCheckpoint, Payload: string(body)})
				}
				emit(event)
			}
			worker := &questionRuntime{configuredRuntime: base, events: events}
			if mode == "stopped" {
				worker.stoppedEvents = []runtime.Event{{Type: runtime.EventUsageCheckpoint, Sequence: sequence + 1, Payload: string(body)}}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			turn, err := RunSearchTurn(ctx, worker, "task", "query")
			if err != nil || len(turn.Usage) != 1 || len(turn.UsageCheckpoints) != 1 ||
				len(turn.UsageCheckpoints[0].ProviderModelMetrics) != 1 ||
				turn.UsageCheckpoints[0].ProviderModelMetrics[0].Provider == nil ||
				turn.UsageCheckpoints[0].ProviderModelMetrics[0].Provider.ID != "provider" ||
				turn.UsageCheckpoints[0].TotalNanoAIU == nil ||
				*turn.UsageCheckpoints[0].TotalNanoAIU != 3.5 {
				t.Fatal("search checkpoint was lost or counted as another receipt", turn, err)
			}
		})
	}
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

func managedSearchEvents(usage string) chan runtime.Event {
	source := questionEventsWithUsage("search receipt", usage)
	events := make(chan runtime.Event, cap(source)+2)
	var sequence uint64
	send := func(event runtime.Event) {
		sequence++
		event.Sequence = sequence
		events <- event
	}
	for len(source) != 0 {
		event := <-source
		send(event)
		if event.Type == runtime.EventTurnStarted {
			send(runtime.Event{Type: runtime.EventExecution, Payload: `{"phase":"tool-start","callId":"search-call","tool":"taxion_search"}`})
			send(runtime.Event{Type: runtime.EventExecution, Payload: `{"phase":"tool-complete","callId":"search-call","success":true}`})
		}
	}
	return events
}

func TestIsolatedQuotaFinalGateAndCleanup(t *testing.T) {
	for _, entry := range []string{"question", "planner", "search"} {
		for _, failure := range []string{"", "reserve", "stale", "missing", "cycle", "resource", "unlimited", "stop-failed"} {
			t.Run(entry+"/"+failure, func(t *testing.T) {
				policy := budgetPolicy(t, "account")
				db, path, base, selected := questionFixture(t, policy)
				base.role = "ORCHESTRATOR"
				worker := &isolatedQuotaRuntime{
					questionRuntime: &questionRuntime{configuredRuntime: base, events: questionEventsWithUsage(savedQuestionAnswer, `{"model":"fixture","inputTokens":10,"outputTokens":5}`)},
					policy:          policy, quota: observedQuota(),
				}
				if entry == "planner" {
					worker.events = questionEventsWithUsage(`{"recommendation":"HUMAN_REQUIRED","confirmed":[{"text":"Task status","sources":["/planner/task/status"]}],"assumptions":[],"unconfirmed":["Live state unknown."]}`, `{"model":"fixture","inputTokens":10,"outputTokens":5}`)
				}
				if entry == "search" {
					worker.events = managedSearchEvents(`{"model":"fixture","inputTokens":10,"outputTokens":5}`)
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
					accepted = runErr == nil && result.Answer != nil
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
							var fields map[string]json.RawMessage
							if json.Unmarshal([]byte(receipt.Payload), &fields) == nil && fields["observation"] != nil {
								count++
							}
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

func TestSearchRequiresBoundTurnEndBeforeIdle(t *testing.T) {
	for _, scenario := range []string{"idle-only", "answer-without-end", "later-output"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, base, _ := questionFixture(t)
			base.role = "ORCHESTRATOR"
			events := make(chan runtime.Event, 6)
			var sequence uint64
			send := func(event runtime.Event) {
				sequence++
				event.Sequence = sequence
				events <- event
			}
			if scenario != "idle-only" {
				send(runtime.Event{Type: runtime.EventInput, MessageID: "search-input"})
				send(runtime.Event{Type: runtime.EventTurnStarted, TurnID: "search-turn"})
				send(runtime.Event{Type: runtime.EventOutput, MessageID: "search-input", TurnID: "search-turn", Payload: "answer"})
			}
			if scenario == "later-output" {
				send(runtime.Event{Type: runtime.EventTurnEnded, TurnID: "search-turn"})
				send(runtime.Event{Type: runtime.EventOutput, MessageID: "search-input", TurnID: "search-turn", Payload: "later activity"})
			} else {
				send(runtime.Event{Type: runtime.EventTurnFinished})
			}
			close(events)
			worker := &questionRuntime{configuredRuntime: base, events: events}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := RunSearchTurn(ctx, worker, "task", "query"); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("unbound or later activity did not fail closed", err)
			}
			if worker.stops != 1 {
				t.Fatal("rejected search did not preserve cleanup")
			}
		})
	}
}

func TestSearchRejectsUnsequencedEvents(t *testing.T) {
	for _, missing := range []int{0, 1, 2, 3, 4} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			_, _, base, _ := questionFixture(t)
			base.role = "ORCHESTRATOR"
			stream := []runtime.Event{
				{Type: runtime.EventInput, MessageID: "search-input"},
				{Type: runtime.EventTurnStarted, TurnID: "search-turn"},
				{Type: runtime.EventOutput, MessageID: "search-input", TurnID: "search-turn", Payload: "answer"},
				{Type: runtime.EventUsage, Payload: "{}"},
				{Type: runtime.EventTurnEnded, TurnID: "search-turn"},
			}
			events := make(chan runtime.Event, len(stream))
			for i, event := range stream {
				if i != missing {
					event.Sequence = uint64(i + 1)
				}
				events <- event
			}
			close(events)
			worker := &questionRuntime{configuredRuntime: base, events: events}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := RunSearchTurn(ctx, worker, "task", "query"); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("missing event sequence did not fail closed", err)
			}
			if worker.stops != 1 {
				t.Fatal("rejected search did not preserve cleanup")
			}
		})
	}
}

func TestSearchAllowsOnlyBoundManagedAcquisition(t *testing.T) {
	for _, scenario := range []string{"managed", "other-tool", "unknown", "permission", "incomplete", "no-acquisition"} {
		t.Run(scenario, func(t *testing.T) {
			_, _, base, _ := questionFixture(t)
			base.role = "ORCHESTRATOR"
			activity := runtime.Event{Type: runtime.EventExecution, Payload: `{"phase":"tool-start","callId":"search-call","tool":"taxion_search"}`}
			switch scenario {
			case "other-tool":
				activity.Payload = `{"phase":"tool-start","callId":"search-call","tool":"bash"}`
			case "unknown":
				activity.Type = "unknown-provider-event"
			case "permission":
				activity.Type = runtime.EventPermission
			}
			stream := []runtime.Event{
				{Type: runtime.EventInput, MessageID: "search-input"},
				{Type: runtime.EventTurnStarted, TurnID: "search-turn"},
			}
			if scenario != "no-acquisition" {
				stream = append(stream, activity)
			}
			if scenario != "incomplete" && scenario != "no-acquisition" {
				stream = append(stream, runtime.Event{Type: runtime.EventExecution, Payload: `{"phase":"tool-complete","callId":"search-call","success":true}`})
			}
			stream = append(stream,
				runtime.Event{Type: runtime.EventOutput, MessageID: "search-input", TurnID: "search-turn", Payload: "answer"},
				runtime.Event{Type: runtime.EventTurnEnded, TurnID: "search-turn"},
				runtime.Event{Type: runtime.EventTurnFinished})
			events := make(chan runtime.Event, len(stream))
			for i, event := range stream {
				event.Sequence = uint64(i + 1)
				events <- event
			}
			worker := &questionRuntime{configuredRuntime: base, events: events}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := RunSearchTurn(ctx, worker, "task", "query")
			if (err == nil) != (scenario == "managed") || worker.stops != 1 {
				t.Fatal("managed acquisition or prohibited activity misclassified", scenario, err)
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
			worker.events = managedSearchEvents(`{"model":"test","inputTokens":10,"outputTokens":5}`)
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
