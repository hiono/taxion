package controller

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type questionRuntime struct {
	*configuredRuntime
	events  chan runtime.Event
	onSend  func()
	onStop  func()
	stopErr error
}

func TestQuestionContextComparisonPreservesFormattedConfigurationAndRejectsForgedContent(t *testing.T) {
	current := QuestionContext{
		Task: PlanningTask{Dispatches: []core.GraphDispatch{{
			Configuration: &core.DispatchConfiguration{
				DecisionID: "approval", Selection: json.RawMessage(`{"runtime":{"account":"approved"}}`),
			},
		}}},
		Observation: &PlanObservation{ContentHash: "unchanged-observation"},
	}
	input, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var supplied QuestionContext
	if err := json.Unmarshal(input, &supplied); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(current.Task.Dispatches, supplied.Task.Dispatches) {
		t.Fatal("fixture did not exercise formatted RawMessage")
	}
	matching, err := questionContextsEqual(current, supplied)
	if err != nil || !matching {
		t.Fatal("formatted CLI configuration became stale evidence", err)
	}
	supplied.Task.Dispatches[0].Configuration.Selection = json.RawMessage(`{"runtime":{"account":"other"}}`)
	matching, err = questionContextsEqual(current, supplied)
	if err != nil || matching {
		t.Fatal("copied observation hash concealed changed configuration", err)
	}
}

func (q *questionRuntime) Events(context.Context, string) (<-chan runtime.Event, error) {
	return q.events, nil
}
func (q *questionRuntime) Send(ctx context.Context, id, text string) error {
	if q.onSend != nil {
		q.onSend()
	}
	return q.configuredRuntime.Send(ctx, id, text)
}
func (q *questionRuntime) Stop(context.Context, string) error {
	q.stops++
	if q.onStop != nil {
		q.onStop()
	}
	return q.stopErr
}

func questionEvents(answer string) chan runtime.Event {
	return questionEventsWithUsage(answer, `{"model":"test","inputTokens":10,"outputTokens":5}`)
}

func questionEventsWithUsage(answer, usage string) chan runtime.Event {
	events := make(chan runtime.Event, 6)
	var sequence uint64
	send := func(event runtime.Event) {
		sequence++
		event.Sequence = sequence
		events <- event
	}
	send(runtime.Event{Type: runtime.EventInput, MessageID: "question-input"})
	send(runtime.Event{Type: runtime.EventTurnStarted, TurnID: "question-turn"})
	if usage != "" {
		send(runtime.Event{Type: runtime.EventUsage, Payload: usage})
	}
	send(runtime.Event{Type: runtime.EventOutput, Payload: answer, MessageID: "question-input", TurnID: "question-turn"})
	send(runtime.Event{Type: runtime.EventTurnEnded, TurnID: "question-turn"})
	send(runtime.Event{Type: runtime.EventTurnFinished})
	return events
}

const savedQuestionAnswer = `{"confirmed":[{"text":"The saved Task is Active.","sources":["/task/task/status","/observation/contentHash"]}],"assumptions":[],"unconfirmed":["Live runtime state has not been queried."]}`

func TestCompletedTurnWaitsForProviderBoundary(t *testing.T) {
	for _, scenario := range []string{"idle", "child", "execution", "closed", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			events := make(chan runtime.Event, 1)
			done := make(chan error, 1)
			go func() {
				var usage []core.DispatchUsage
				sequence := uint64(1)
				done <- drainCompletedTurn(ctx, events, &usage, &sequence, "question-session")
			}()
			select {
			case err := <-done:
				t.Fatal("transiently empty stream accepted before provider boundary", err)
			case <-time.After(20 * time.Millisecond):
			}
			switch scenario {
			case "idle":
				events <- runtime.Event{Type: runtime.EventTurnFinished, Sequence: 2}
			case "child":
				events <- runtime.Event{Type: runtime.EventContextUsage, AgentID: "child", Sequence: 2}
			case "execution":
				events <- runtime.Event{Type: runtime.EventExecution, Sequence: 2}
			case "closed":
				close(events)
			case "timeout":
				cancel()
			}
			err := <-done
			if (err == nil) != (scenario == "idle" || scenario == "closed") {
				t.Fatal("provider boundary or delayed activity incorrectly classified", scenario, err)
			}
		})
	}
}

func TestQuestionAcceptsOpenACPMetadataButNotUnknownUsage(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		db, path, base, selected := questionFixture(t)
		base.role = "ORCHESTRATOR"
		stream := make(chan runtime.Event, 9)
		metadata := []runtime.Event{
			{Type: "acp_connection", Payload: `{"transport":"stdio"}`},
			{Type: "acp_capabilities", Payload: `{"usage":"latest-assistant-message-not-total"}`},
		}
		if unavailable {
			metadata = append(metadata, runtime.Event{Type: "acp_usage_unavailable", Payload: `{"reason":"omitted usage"}`})
		}
		for i, event := range metadata {
			event.Sequence = uint64(i + 1)
			stream <- event
		}
		original := questionEvents(savedQuestionAnswer)
		for i := 0; i < 6; i++ {
			event := <-original
			event.Sequence += uint64(len(metadata))
			stream <- event
		}
		worker := &questionRuntime{configuredRuntime: base, events: stream}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		result, err := AskQuestion(ctx, db, filepath.Join(filepath.Dir(path), "ledger.json"), "task", "status?", selected.Config, worker)
		cancel()
		if unavailable && (err == nil || result.Answer != nil) ||
			!unavailable && (err != nil || result.Answer == nil) || worker.stops != 1 {
			t.Fatal("provider metadata or unknown usage incorrectly accepted", unavailable, result, err)
		}
	}
}

func questionFixture(t *testing.T, guidance ...*core.BudgetGuidancePolicy) (*store.Store, string, *configuredRuntime, config.Resolution) {
	t.Helper()
	original, _, worker, resolved := executionConfigurationFixture(t, guidance...)
	state, err := original.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.InitializeWorkspace(state); err != nil {
		t.Fatal(err)
	}
	return db, path, worker, resolved
}

func TestQuestionAnswersSavedRecordsWithoutChangingWorkspace(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	worker.role = "ORCHESTRATOR"
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(databasePath), "ledger.json")
	result, err := AskQuestion(context.Background(), db, path, "task", "今は何を確認できていますか？", resolved.Config, q)
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadWorkspace()
	if after.Records.Operation == nil || after.Records.Operation.State != "COMPLETED" || after.Records.Operation.Owner != "" {
		t.Fatal("question did not finish its admission record")
	}
	operationID := after.Records.Operation.ID
	receiptFound := false
	for _, record := range after.Records.Requests {
		if record.RequestID != operationID || record.Kind != "planning" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(record.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["answer"] != nil && (payload["terminalEvent"] == runtime.EventTurnFinished || payload["terminalEvent"] == runtime.EventTurnEnded) {
			receiptFound = true
		}
	}
	if !receiptFound {
		t.Fatal("validated terminal answer was not durably recorded")
	}
	after.Records.Operation = before.Records.Operation
	after.Records.Requests = before.Records.Requests
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("question mutated state, authorization or main-role history", err)
	}
	if result.Answer == nil || result.Changed || result.Context.Observation == nil || len(result.Usage) != 1 ||
		result.SessionID == "" || q.starts != 1 || q.stops != 1 || len(q.sends) != 1 ||
		!strings.Contains(q.sends[0], before.Graph.Dispatches[0].InstructionHash) {
		t.Fatal("question lost target, usage, answer or cleanup")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored, err := ObserveQuestion(reopened, path, "owner/repo", "task")
	if err != nil || restored.Observation.ContentHash != result.Context.Observation.ContentHash {
		t.Fatal("question observation depended on session history", err)
	}
}

func TestQuestionValidatesLiveAndDrainedCheckpointsWithoutDuplicateUsage(t *testing.T) {
	for _, mode := range []string{"live", "drain", "wrong-session", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			defer db.Close()
			worker.role = "ORCHESTRATOR"
			payload := `{"model":"test","inputTokens":10,"outputTokens":5,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":3.5,"aiCreditsStatus":"complete"}`
			var usage core.DispatchUsage
			if err := json.Unmarshal([]byte(payload), &usage); err != nil {
				t.Fatal(err)
			}
			checkpoint := runtime.UsageCheckpoint{UsageTotals: []core.DispatchUsage{usage}, TotalNanoAIU: usage.TotalNanoAIU}
			if mode == "wrong-session" {
				checkpoint.Watermarks = map[string]int64{"another-native-session": 1}
			}
			body, err := json.Marshal(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "invalid" {
				body = []byte(`{"usageTotals":"private-invalid-checkpoint"}`)
			}
			original := questionEventsWithUsage(savedQuestionAnswer, payload)
			events := make(chan runtime.Event, 7)
			sequence, at := uint64(0), 3
			if mode == "drain" || mode == "wrong-session" {
				at = 5
			}
			send := func(event runtime.Event) {
				sequence++
				event.Sequence = sequence
				events <- event
			}
			for index := 0; index < 6; index++ {
				if index == at {
					send(runtime.Event{Type: runtime.EventUsageCheckpoint, Payload: string(body)})
				}
				send(<-original)
			}
			q := &questionRuntime{configuredRuntime: worker, events: events}
			result, err := AskQuestion(t.Context(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
				"task", "Observe the saved Task.", resolved.Config, q)
			valid := mode == "live" || mode == "drain"
			if (err == nil) != valid || len(result.Usage) != 1 ||
				err != nil && strings.Contains(err.Error(), "private-invalid-checkpoint") {
				t.Fatal("checkpoint admission lost accounting, duplicated it, or disclosed private payload", result, err)
			}
			after, loadErr := db.LoadWorkspace()
			if loadErr != nil || valid && after.Records.Operation.State != "COMPLETED" ||
				!valid && after.Records.Operation.State == "COMPLETED" {
				t.Fatal("checkpoint receipt acceptance was not preserved", loadErr)
			}
		})
	}
}

func TestQuestionMainTurnEndWithoutIdleAndLaterActivity(t *testing.T) {
	for _, test := range []struct {
		name  string
		later bool
	}{
		{"no-idle", false}, {"later-activity", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			events := make(chan runtime.Event, 20)
			source := questionEvents(savedQuestionAnswer)
			close(source)
			for event := range source {
				if event.Type != runtime.EventTurnFinished {
					events <- event
				}
			}
			if test.later {
				events <- runtime.Event{Type: runtime.EventExecution, Sequence: 20}
			}
			close(events)
			q := &questionRuntime{configuredRuntime: worker, events: events}
			result, err := AskQuestion(t.Context(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
				"task", "What is saved?", resolved.Config, q)
			if test.later {
				if err == nil || result.Answer != nil {
					t.Fatal("later tool activity was accepted after the main turn end")
				}
			} else if err != nil || result.Answer == nil {
				t.Fatal("bound completed main turn depended on session.idle", err)
			}
		})
	}
}

func TestQuestionOnlyCompletesAfterDurableAnswerAndSuccessfulStop(t *testing.T) {
	for _, test := range []struct {
		name        string
		answer      string
		stopErr     error
		wantAnswer  bool
		wantReceipt bool
	}{
		{name: "invalid answer despite successful stop", answer: `{"confirmed":[],"assumptions":[],"unconfirmed":[]}`},
		{name: "valid answer retained when stop fails", answer: savedQuestionAnswer, stopErr: errors.New("stop failed"), wantAnswer: true, wantReceipt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(test.answer), stopErr: test.stopErr}
			terminalSavedBeforeCleanup := false
			q.onStop = func() {
				state, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}

				for _, record := range state.Records.Requests {
					if record.Kind != "planning" {
						continue
					}
					var payload map[string]any
					if err := json.Unmarshal([]byte(record.Payload), &payload); err != nil {
						t.Fatal(err)
					}
					terminalSavedBeforeCleanup = terminalSavedBeforeCleanup ||
						payload["answer"] != nil && (payload["terminalEvent"] == runtime.EventTurnFinished || payload["terminalEvent"] == runtime.EventTurnEnded)
				}
			}
			result, err := AskQuestion(context.Background(), db,
				filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task", "What is saved?", resolved.Config, q)
			if err == nil || (result.Answer != nil) != test.wantAnswer {
				t.Fatalf("question result = %+v, error = %v", result, err)
			}
			if terminalSavedBeforeCleanup != test.wantReceipt {
				t.Fatal("terminal answer was not persisted before runtime cleanup")
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if state.Records.Operation == nil || state.Records.Operation.State != "BLOCKED" {
				t.Fatal("question without confirmed quiescence and durable success did not retain the admission hold")
			}
			receiptFound := false
			for _, record := range state.Records.Requests {
				if record.RequestID != state.Records.Operation.ID || record.Kind != "planning" {
					continue
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(record.Payload), &payload); err != nil {
					t.Fatal(err)
				}
				receiptFound = receiptFound || payload["attemptId"] != nil && payload["answer"] != nil
			}
			if receiptFound != test.wantReceipt {
				t.Fatalf("durable terminal answer receipt=%v, want %v", receiptFound, test.wantReceipt)
			}
			followOn := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
			startsBeforeFollowOn := followOn.starts
			if _, err := AskQuestion(context.Background(), db,
				filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task", "Can I continue?", resolved.Config, followOn); err == nil ||
				followOn.starts != startsBeforeFollowOn {
				t.Fatal("follow-on question bypassed the unresolved operation", err)
			}
		})
	}
}

func TestQuestionRejectsMismatchedLiveMessageAndTurnIdentity(t *testing.T) {
	for _, mode := range []string{"missing-input", "wrong-output-message", "wrong-output-turn", "wrong-turn-end", "child-usage"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			events := questionEvents(savedQuestionAnswer)
			saved := make([]runtime.Event, 0, len(events))
			for len(events) != 0 {
				saved = append(saved, <-events)
			}
			switch mode {
			case "missing-input":
				saved[0].MessageID = ""
			case "wrong-output-message":
				saved[3].MessageID = "another-input"
			case "wrong-output-turn":
				saved[3].TurnID = "another-turn"
			case "wrong-turn-end":
				saved[4].TurnID = "another-turn"
			case "child-usage":
				saved[2].AgentID = "child-agent"
			}
			events = make(chan runtime.Event, len(saved))
			for _, event := range saved {
				events <- event
			}
			q := &questionRuntime{configuredRuntime: worker, events: events}
			result, err := AskQuestion(context.Background(), db,
				filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task",
				"Which turn is this?", resolved.Config, q)
			if err == nil || result.Answer != nil {
				t.Fatal("question accepted an unbound live answer", err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range state.Records.Requests {
				if record.Kind == "planning" && strings.Contains(record.Payload, `"answer"`) {
					t.Fatal("ambiguous turn wrote a terminal answer receipt")
				}
			}
		})
	}
}

func TestQuestionRejectsUnapprovedProviderDeniedAndChangedApproval(t *testing.T) {
	for _, mode := range []string{"selection", "approval", "authentication", "repository", "service", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
			switch mode {
			case "selection":
				q.selection = json.RawMessage(`{}`)
			case "approval":
				resolved.Config.Runtime.ContractRef = "not-approved"
				q.selection, _ = json.Marshal(resolved.Config)
			case "authentication":
				q.authErr = errors.New("authentication unavailable")
			case "repository":
				q.repositoryErr = errors.New("repository denied")
			case "service":
				q.serviceErr = errors.New("service denied")
			case "revoked":
				q.onService = func() {
					if err := db.UpdateWorkspace(func(state *store.Workspace) error {
						return state.Graph.Transition("task", core.Cancelled, "human", "revoke question authorization", core.TransitionProof{})
					}); err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := AskQuestion(context.Background(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
				"task", "status?", resolved.Config, q)
			if err == nil || result.Answer != nil || len(q.sends) != 0 {
				t.Fatal("question ignored approval or provider gate")
			}
			if q.starts != 0 && q.stops != 1 {
				t.Fatal("denied question leaked an attached session")
			}
		})
	}
}

func TestQuestionRejectsIncompleteUsageButKeepsHistoricalSoftLimitBehavior(t *testing.T) {
	for _, mode := range []string{"missing-usage", "missing-count", "over-former-limit"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
			switch mode {
			case "missing-usage":
				q.events = questionEventsWithUsage(savedQuestionAnswer, "")
			case "missing-count":
				q.events = questionEventsWithUsage(savedQuestionAnswer, `{"model":"test","inputTokens":10}`)
			case "over-former-limit":
				q.events = questionEventsWithUsage(savedQuestionAnswer, `{"model":"test","inputTokens":1001,"outputTokens":2000}`)
			}
			result, err := AskQuestion(context.Background(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
				"task", "What is saved?", resolved.Config, q)
			if mode == "over-former-limit" {
				if err != nil || result.Answer == nil {
					t.Fatalf("reported usage exceeded an obsolete token threshold: result=%+v err=%v", result, err)
				}
			} else if err == nil || result.Answer != nil {
				t.Fatalf("incomplete usage was accepted: result=%+v err=%v", result, err)
			}
			if q.starts != 1 || q.stops != 1 {
				t.Fatalf("question session lifecycle differs: starts=%d stops=%d", q.starts, q.stops)
			}
		})
	}
}

func TestQuestionRejectsInvalidSourcesAndFailures(t *testing.T) {
	for _, mode := range []string{"bad-source", "missing-source", "null-lists", "empty-answer", "runtime-failure", "unclassified-native", "stream-ended", "cancelled", "stop-failure", "oversize-input", "missing-turn-start", "missing-turn-end"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
			text := "What is saved?"
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			switch mode {
			case "bad-source":
				q.events = questionEvents(strings.ReplaceAll(savedQuestionAnswer, "/task/task/status", "/task/task/fabricated"))
			case "missing-source":
				q.events = questionEvents(`{"confirmed":[{"text":"considered","sources":[]}],"assumptions":[],"unconfirmed":[]}`)
			case "null-lists":
				q.events = questionEvents(`{"confirmed":null,"assumptions":[],"unconfirmed":[]}`)
			case "empty-answer":
				q.events = questionEvents(`{"confirmed":[],"assumptions":[],"unconfirmed":[]}`)
			case "runtime-failure":
				q.events = make(chan runtime.Event, 1)
				q.events <- runtime.Event{Type: runtime.EventFailed, Payload: "private-provider-detail"}
			case "unclassified-native":
				q.events = make(chan runtime.Event, 1)
				q.events <- runtime.Event{Type: runtime.EventUnclassified, Payload: "private-provider-detail", Sequence: 1}
			case "stream-ended":
				q.events = make(chan runtime.Event)
				close(q.events)
			case "cancelled":
				cancel()
			case "stop-failure":
				q.stopErr = errors.New("stop failed")
			case "oversize-input":
				text = strings.Repeat("x", 128*1024)
			case "missing-turn-start":
				q.events = make(chan runtime.Event, 4)
				q.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10,"outputTokens":5}`, Sequence: 1}
				q.events <- runtime.Event{Type: runtime.EventOutput, Payload: savedQuestionAnswer, Sequence: 2}
				q.events <- runtime.Event{Type: runtime.EventTurnEnded, Sequence: 3}
				q.events <- runtime.Event{Type: runtime.EventTurnFinished, Sequence: 4}
			case "missing-turn-end":
				q.events = make(chan runtime.Event, 4)
				q.events <- runtime.Event{Type: runtime.EventTurnStarted, Sequence: 1}
				q.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":10,"outputTokens":5}`, Sequence: 2}
				q.events <- runtime.Event{Type: runtime.EventOutput, Payload: savedQuestionAnswer, Sequence: 3}
				q.events <- runtime.Event{Type: runtime.EventTurnFinished, Sequence: 4}
			}
			result, err := AskQuestion(ctx, db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
				"task", text, resolved.Config, q)
			if err == nil || strings.Contains(err.Error(), "private-provider-detail") {
				t.Fatal("invalid question returned success or leaked details", mode, result, err)
			}
			if q.starts != 0 && q.stops != 1 {
				t.Fatal("failed question was not safely stopped", mode)
			}
		})
	}
}

func TestQuestionKeepsInvalidAnswerOnlyInPrivateResult(t *testing.T) {
	for _, answer := range []string{
		savedQuestionAnswer,
		"private malformed model output",
		strings.ReplaceAll(savedQuestionAnswer, "/task/task/status", "/task/task/fabricated"),
	} {
		t.Run(answer, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(answer)}
			result, err := AskQuestion(context.Background(), db,
				filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task", "status?", resolved.Config, q)
			data, encodeErr := json.Marshal(result)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if answer == savedQuestionAnswer {
				if err != nil || result.InvalidAnswer != "" || strings.Contains(string(data), `"invalidAnswer"`) {
					t.Fatal("valid answer changed its wire shape", err)
				}
			} else if err == nil || result.Answer != nil || result.InvalidAnswer != answer ||
				strings.Contains(err.Error(), answer) || !strings.Contains(string(data), `"invalidAnswer"`) {
				t.Fatal("malformed answer lost its private evidence or leaked into the error", err)
			}
			if q.stops != 1 {
				t.Fatal("question session was not stopped")
			}
		})
	}
}

func TestQuestionReportsTargetChangeWithoutRetargetingAnswer(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	worker.role = "ORCHESTRATOR"
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
	q.onSend = func() {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			return state.Graph.AddConcern(core.Concern{ID: "changed", TaskID: "task", Source: "human:question", Text: "new concern"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := AskQuestion(context.Background(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task",
		"status?", resolved.Config, q)
	if err != nil || !result.Changed || len(result.Context.Task.Concerns) != 0 {
		t.Fatal("question silently replaced its observed target", err)
	}
}
