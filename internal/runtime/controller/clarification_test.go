package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/store"
)

func clarificationRequest(t *testing.T, db *store.Store, task, dispatch, role string) ClarificationRequest {
	t.Helper()
	context, err := ObserveClarification(db, task, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	return ClarificationRequest{
		ID: "question", TaskID: task, DispatchID: dispatch, Role: role, Category: "specification",
		BasisHash: context.Hash, Question: "Which acceptance interpretation applies?",
		Reason: "Two materially different implementations are possible", Uncertainty: "acceptance-interpretation",
		References: []string{"taxion:clarification-task"}, AnswerFormat: "text",
		DecisionImpact: "Select the implementation within the saved acceptance",
		StopCondition:  "Do not perform work depending on the unanswered interpretation",
		Deadline:       time.Now().UTC().Add(time.Hour),
	}
}

func TestClarificationRoutesAndRecordsWithoutExecutionAuthority(t *testing.T) {
	db, _, _, _ := questionFixture(t)
	base := clarificationRequest(t, db, "task", "dispatch", "CODER")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range []struct {
		category, role, owner, route, recipient string
	}{
		{"specification", "CODER", "", "ASK", "PLANNER"},
		{"specification", "CODER", "HUMAN", "ASK", "HUMAN"},
		{"specification", "PLANNER", "", "ASK", "HUMAN"},
		{"specification", "PLANNER", "PLANNER", "ASK", "PLANNER"},
		{"authority", "PLANNER", "", "ASK", "HUMAN"},
		{"intent", "PLANNER", "", "ASK", "HUMAN"},
		{"irreversible", "PLANNER", "", "ASK", "HUMAN"},
		{"scope-change", "PLANNER", "", "ASK", "HUMAN"},
		{"budget-change", "PLANNER", "", "ASK", "HUMAN"},
		{"deadline-change", "PLANNER", "", "ASK", "HUMAN"},
		{"safety", "PLANNER", "", "STOP", ""},
		{"identity", "PLANNER", "", "STOP", ""},
		{"integrity", "PLANNER", "", "STOP", ""},
		{"budget", "PLANNER", "", "STOP", ""},
		{"deadline", "PLANNER", "", "STOP", ""},
	} {
		input := base
		input.ID, input.Uncertainty = fmt.Sprintf("question-%d", index), fmt.Sprintf("uncertainty-%d", index)
		input.Category, input.Role, input.SpecificationOwnerRole = item.category, item.role, item.owner
		if input.Role == "PLANNER" {
			input.DispatchID = ""
			context, err := ObserveClarification(db, "task", "")
			if err != nil {
				t.Fatal(err)
			}
			input.BasisHash = context.Hash
		}
		got, err := RequestClarification(db, input)
		if err != nil || got.Route != item.route || got.Recipient != item.recipient || got.State != "OBSERVED" {
			t.Fatalf("%s/%s routed incorrectly: %+v %v", item.category, item.role, got, err)
		}
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("routing changed graph, approval, HOLD, runtime ownership or ledger")
	}
}

func TestClarificationReusesOriginalsAndBoundsAssumptions(t *testing.T) {
	db, _, _, _ := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	input.Category, input.SavedSourceID = "saved", "taxion:clarification-task"
	saved, err := RequestClarification(db, input)
	if err != nil || saved.Route != "REUSE" || saved.Recipient != "" || saved.Answer != nil {
		t.Fatal("saved source caused a new question or fabricated an answer", saved, err)
	}
	if len(saved.Context.Documents) == 0 || saved.Context.Documents[len(saved.Context.Documents)-1].Text == "" {
		t.Fatal("original source content was not retained")
	}
	input.ID, input.Category, input.Uncertainty = "assumption", "minor", "minor-format"
	input.SavedSourceID = ""
	input.Assumption = &ClarificationAssumption{Text: "Use the existing file style", Impact: []string{"README.md"}, Reversible: true}
	assumed, err := RequestClarification(db, input)
	if err != nil || assumed.Route != "ASSUME" || assumed.Recipient != "" {
		t.Fatal("reversible, in-scope assumption did not remain advisory", assumed, err)
	}
	input.ID = "widened-assumption"
	input.Assumption.Impact = []string{"outside-approved-scope"}
	if _, err := RequestClarification(db, input); err == nil {
		t.Fatal("assumption widened the saved scope")
	}
	input.Assumption.Impact, input.Assumption.Reversible = []string{"README.md"}, false
	if _, err := RequestClarification(db, input); err == nil {
		t.Fatal("irreversible assumption was accepted")
	}
}

func TestClarificationConcurrentReplayAndAnswerSurviveRestart(t *testing.T) {
	db, path, _, _ := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			duplicate := input
			duplicate.ID = fmt.Sprintf("retry-%d", i)
			got, err := RequestClarification(db, duplicate)
			if err == nil && (got.Route != "ASK" || got.Recipient != "PLANNER") {
				err = fmt.Errorf("incorrect duplicate route")
			}
			errs <- err
		}(i)
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := ClarificationHistory(db)
	if err != nil || len(history) != 1 {
		t.Fatal("concurrent retries created repeated questions", len(history), err)
	}
	id := history[0].Request.ID
	answer := ClarificationAnswer{Role: "PLANNER", Actor: "spec-owner", Source: "decision:acceptance", Text: "Preserve the documented behavior."}
	observed, err := AnswerClarification(db, id, answer)
	if err != nil || observed.State != "OBSERVED" || observed.Answer == nil || observed.Answer.At.IsZero() {
		t.Fatal("answer was not durably observed, or was implicitly accepted", observed, err)
	}
	if _, err := AnswerClarification(db, id, answer); err != nil {
		t.Fatal("identical answer retry failed", err)
	}
	answer.Text = "Contradict the original answer."
	if _, err := AnswerClarification(db, id, answer); err == nil {
		t.Fatal("contradictory answer replaced the original")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	input.ID = "new-session-retry"
	again, err := RequestClarification(reopened, input)
	if err != nil || again.Request.ID != id || !reflect.DeepEqual(again.Answer, observed.Answer) {
		t.Fatal("new session reissued a question or lost the observed answer", again, err)
	}
	after, err := reopened.LoadWorkspace()
	if err != nil || len(after.Records.Requests) != len(before.Records.Requests)+2 {
		t.Fatal("retry appended duplicate question or answer receipts", err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("observing an answer changed authority, Dispatch, HOLD or operation ownership")
	}
}

func TestReviewerClarificationRoutesEvidenceToCoderWithoutChangingReviewer(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	for _, category := range []string{"implementation", "evidence", "specification"} {
		input := clarificationRequest(t, db, dispatch.TaskID, dispatch.ID, "REVIEWER")
		input.ID, input.Category, input.Uncertainty = category, category, category
		got, err := RequestClarification(db, input)
		expected := "CODER"
		if category == "specification" {
			expected = "PLANNER"
		}
		if err != nil || got.Recipient != expected {
			t.Fatalf("Reviewer %s clarification was misrouted: %+v %v", category, got, err)
		}
		if _, err := AnswerClarification(db, got.Request.ID, ClarificationAnswer{
			Role: "REVIEWER", Actor: "reviewer", Source: "self", Text: "I will implement the fix myself.",
		}); err == nil {
			t.Fatal("Reviewer answered its own implementation clarification")
		}
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(state.Graph, after.Graph) || !reflect.DeepEqual(state.Ledger, after.Ledger) {
		t.Fatal("clarification replaced Reviewer independence, instructions or execution state", err)
	}
}

func TestClarificationRejectsStaleSourcesAndMismatchedSender(t *testing.T) {
	db, _, _, _ := questionFixture(t)
	for _, mutate := range []func(*ClarificationRequest){
		func(q *ClarificationRequest) { q.BasisHash = "stale" },
		func(q *ClarificationRequest) { q.Role = "REVIEWER" },
		func(q *ClarificationRequest) { q.DispatchID = "" },
		func(q *ClarificationRequest) { q.AnswerFormat = "shell" },
		func(q *ClarificationRequest) { q.References = nil },
		func(q *ClarificationRequest) { q.StopCondition = "" },
		func(q *ClarificationRequest) { q.Category, q.SavedSourceID = "saved", "foreign-source" },
	} {
		input := clarificationRequest(t, db, "task", "dispatch", "CODER")
		mutate(&input)
		if _, err := RequestClarification(db, input); err == nil {
			t.Fatal("invalid clarification accepted", input)
		}
	}
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	input.Deadline = time.Now().Add(-time.Second)
	got, err := RequestClarification(db, input)
	if err != nil || got.Route != "STOP" || got.Recipient != "" {
		t.Fatal("expired question was sent to another role", got, err)
	}
}

func TestClarificationReplayPreservesTheAnswerContract(t *testing.T) {
	db, _, _, _ := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	original, err := RequestClarification(db, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*ClarificationRequest)
	}{
		{"owner", func(q *ClarificationRequest) { q.SpecificationOwnerRole = "HUMAN" }},
		{"format", func(q *ClarificationRequest) { q.AnswerFormat = "json" }},
	} {
		changed := input
		change.mutate(&changed)
		if _, err := RequestClarification(db, changed); err == nil {
			t.Fatalf("%s changed the contract bound to the existing ID", change.name)
		}
		changed.ID = change.name
		got, err := RequestClarification(db, changed)
		if err != nil || got.Request.ID != changed.ID || got.Signature == original.Signature {
			t.Fatalf("%s reused a different answer contract: %+v %v", change.name, got, err)
		}
	}
	replayed := input
	replayed.ID = "retry"
	replayed.Deadline = input.Deadline.Add(time.Hour)
	got, err := RequestClarification(db, replayed)
	if err != nil || got.Request.ID != input.ID || !got.Request.Deadline.Equal(input.Deadline) {
		t.Fatal("retry replaced the original question or extended its deadline", got, err)
	}
}

func TestClarificationUsesPlannerLifecycleAndReferencesItsCanonicalAnswer(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	input.AnswerFormat = "json"
	requested, err := RequestClarification(db, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.Dispatches[0].UpdatedAt = time.Now().UTC().Add(time.Second)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	current, err := ObserveClarification(db, "task", "dispatch")
	if err != nil || current.Hash == requested.Context.Hash || current.ScopeHash != requested.Context.ScopeHash {
		t.Fatal("progress changed the contract basis or did not change the observation", err)
	}
	replayed := input
	replayed.ID, replayed.BasisHash = "progress-retry", current.Hash
	if again, err := RequestClarification(db, replayed); err != nil || again.Request.ID != input.ID {
		t.Fatal("runtime progress reissued an unchanged specification question", again, err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	worker.role = "ORCHESTRATOR"
	answer := `{"recommendation":"HUMAN_REQUIRED","confirmed":[{"text":"The saved Task is active.","sources":["/planner/task/status"]}],"assumptions":[],"unconfirmed":["Confirm the unresolved interpretation with its owner."]}`
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(answer)}
	result, err := AskPlannerClarification(context.Background(), db,
		filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task", requested.Request.ID, resolved.Config, q)
	if err != nil || result.Answer == nil || q.stops != 1 {
		t.Fatal("routed question did not use the bounded Planner lifecycle", result, err)
	}
	history, err := ClarificationHistory(db)
	if err != nil || len(history) != 2 || history[1].Answer == nil {
		t.Fatal("Planner answer was not linked to the saved question", history, err)
	}
	observed := history[1]
	if observed.State != "OBSERVED" || observed.Answer.Receipt == nil ||
		observed.Answer.Actor != "session:"+result.SessionID || !json.Valid([]byte(observed.Answer.Text)) {
		t.Fatal("Planner provenance or observation/acceptance boundary was lost", observed)
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if after.Records.Operation == nil || after.Records.Operation.State != "COMPLETED" || after.Records.Operation.Owner != "" {
		t.Fatal("Planner cleanup or ownership release was bypassed")
	}
	ref := observed.Answer.Receipt
	found := false
	for _, record := range after.Records.Requests {
		if record.RequestID == ref.RequestID && record.Sequence == ref.Sequence {
			found = record.Hash == ref.Hash && record.RequestID != "clarification:"+input.ID
		}
		if record.RequestID == "clarification:"+input.ID && record.Sequence == 2 {
			var saved Clarification
			if err := json.Unmarshal([]byte(record.Payload), &saved); err != nil || saved.Answer.Text != "" {
				t.Fatal("adapter duplicated the canonical Planner answer", err)
			}
		}
	}
	if !found || !reflect.DeepEqual(before.Graph, after.Graph) || !reflect.DeepEqual(before.Ledger, after.Ledger) {
		t.Fatal("Planner routing changed authority or lost the canonical receipt")
	}
	retry := &questionRuntime{configuredRuntime: worker, events: questionEvents(answer)}
	starts := worker.starts
	if _, err := AskPlannerClarification(context.Background(), db, databasePath, "task", input.ID, resolved.Config, retry); err == nil {
		t.Fatal("observed answer was re-requested")
	}
	if retry.starts != starts {
		t.Fatal("replay started another Planner session", retry.starts)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := ClarificationHistory(reopened)
	if err != nil || len(recovered) != 2 || !reflect.DeepEqual(recovered[1].Answer, observed.Answer) {
		t.Fatal("new session did not resolve the original Planner receipt", recovered, err)
	}
}

func TestClarificationRetainsObservedAnswerWhenPlannerStopFails(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	if _, err := RequestClarification(db, input); err != nil {
		t.Fatal(err)
	}
	worker.role = "ORCHESTRATOR"
	answer := `{"recommendation":"HOLD","confirmed":[{"text":"The saved Task is active.","sources":["/planner/task/status"]}],"assumptions":[],"unconfirmed":["The specification owner must confirm."]}`
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(answer), stopErr: errors.New("stop not confirmed")}
	result, askErr := AskPlannerClarification(context.Background(), db,
		filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task", input.ID, resolved.Config, q)
	if askErr == nil || !strings.Contains(askErr.Error(), "session cleanup") || q.stops != 1 {
		t.Fatal("failed runtime Stop was treated as successful completion")
	}
	history, err := ClarificationHistory(db)
	if err != nil || len(history) != 2 || history[1].Answer == nil || history[1].State != "OBSERVED" {
		t.Fatal("Stop failure lost the canonical answer or implicitly accepted it", history, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil || state.Records.Operation == nil || state.Records.Operation.State != "BLOCKED" {
		t.Fatal("Stop failure bypassed the existing pending operation recovery", err)
	}
	unconfirmedStop := false
	for _, record := range state.Records.Requests {
		var cleanup struct {
			AttemptID string `json:"attemptId"`
			Stopped   *bool  `json:"stopped"`
		}
		if json.Unmarshal([]byte(record.Payload), &cleanup) == nil &&
			cleanup.AttemptID == result.AttemptID && cleanup.Stopped != nil {
			unconfirmedStop = !*cleanup.Stopped
		}
	}
	if !unconfirmedStop {
		t.Fatal("cleanup failed to preserve the absence of a confirmed runtime stop")
	}
}

func TestClarificationBindingConflictDoesNotRollbackCanonicalPlannerObservation(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	input := clarificationRequest(t, db, "task", "dispatch", "CODER")
	if _, err := RequestClarification(db, input); err != nil {
		t.Fatal(err)
	}
	worker.role = "ORCHESTRATOR"
	answer := `{"recommendation":"HOLD","confirmed":[{"text":"The saved Task is active.","sources":["/planner/task/status"]}],"assumptions":[],"unconfirmed":["The specification owner must confirm."]}`
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(answer)}
	q.onSend = func() {
		if _, err := AnswerClarification(db, input.ID, ClarificationAnswer{
			Role: "HUMAN", Actor: "user", Source: "human:concurrent-answer", Text: "Preserve the current behavior.",
		}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := AskPlannerClarification(context.Background(), db,
		filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task", input.ID, resolved.Config, q)
	if err == nil || !strings.Contains(err.Error(), "binding changed") {
		t.Fatal("conflicting concurrent respondent was silently replaced")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range state.Records.Requests {
		var terminal struct {
			AttemptID string          `json:"attemptId"`
			Answer    json.RawMessage `json:"answer"`
		}
		if json.Unmarshal([]byte(record.Payload), &terminal) == nil &&
			terminal.AttemptID == result.AttemptID && len(terminal.Answer) != 0 {
			found = true
		}
	}
	history, err := ClarificationHistory(db)
	if !found || err != nil || len(history) != 2 || history[1].Answer.Role != "HUMAN" {
		t.Fatal("binding failure lost the Planner receipt or replaced the Human answer", history, err)
	}
}
