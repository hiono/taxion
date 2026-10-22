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
	return q.stopErr
}

func questionEvents(answer string) chan runtime.Event {
	return questionEventsWithUsage(answer, `{"model":"test","inputTokens":10,"outputTokens":5}`)
}

func questionEventsWithUsage(answer, usage string) chan runtime.Event {
	events := make(chan runtime.Event, 3)
	if usage != "" {
		events <- runtime.Event{Type: runtime.EventUsage, Payload: usage}
	}
	events <- runtime.Event{Type: runtime.EventOutput, Payload: answer}
	events <- runtime.Event{Type: runtime.EventTurnFinished}
	return events
}

const savedQuestionAnswer = `{"confirmed":[{"text":"The saved Task is Active.","sources":["/task/task/status","/observation/contentHash"]}],"assumptions":[],"unconfirmed":["Live runtime state has not been queried."]}`

func questionFixture(t *testing.T) (*store.Store, string, *configuredRuntime, config.Resolution) {
	t.Helper()
	original, _, worker, resolved := executionConfigurationFixture(t)
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
	after.Records.Operation = before.Records.Operation
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

func TestQuestionAcceptsMissingOrOverLimitTokenUsage(t *testing.T) {
	for _, mode := range []string{"missing-usage", "missing-count", "over-former-limit"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(savedQuestionAnswer)}
			switch mode {
			case "missing-usage":
				q.events = questionEventsWithUsage(savedQuestionAnswer, "")
			case "missing-count":
				q.events = questionEventsWithUsage(savedQuestionAnswer, `{"inputTokens":10}`)
			case "over-former-limit":
				q.events = questionEventsWithUsage(savedQuestionAnswer, `{"inputTokens":1001,"outputTokens":2000}`)
			}
			result, err := AskQuestion(context.Background(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
				"task", "What is saved?", resolved.Config, q)
			if err != nil || result.Answer == nil || q.starts != 1 || q.stops != 1 {
				t.Fatalf("token accounting prevented a completed question: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestQuestionRejectsInvalidSourcesAndFailures(t *testing.T) {
	for _, mode := range []string{"bad-source", "missing-source", "null-lists", "empty-answer", "runtime-failure", "stream-ended", "cancelled", "stop-failure", "oversize-input"} {
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
			case "stream-ended":
				q.events = make(chan runtime.Event)
				close(q.events)
			case "cancelled":
				cancel()
			case "stop-failure":
				q.stopErr = errors.New("stop failed")
			case "oversize-input":
				text = strings.Repeat("x", 128*1024)
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
