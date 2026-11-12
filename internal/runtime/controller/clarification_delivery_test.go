package controller

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestCoderClarificationResolutionPersistsOnlyCanonicalHeldReference(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _ := before.Graph.GraphDispatch(flow.LastDispatchID())
	input := clarificationRequest(t, db, reviewer.TaskID, reviewer.ID, "REVIEWER")
	input.Category = "evidence"
	observed, err := RequestClarification(db, input)
	if err != nil || observed.Delivery == nil {
		t.Fatal("saved Reviewer question did not enter delivery control", observed, err)
	}
	delivery := *observed.Delivery
	if delivery.Status != "HELD" || delivery.Reason != "coder-ended-or-stopping" || delivery.Target == nil ||
		delivery.Target.DispatchID != coder.ID || delivery.Target.TaskID != coder.TaskID ||
		delivery.Target.Role != "CODER" || delivery.Target.SessionID != coder.RuntimeSessionID ||
		delivery.Target.SessionID == reviewer.RuntimeSessionID || delivery.Target.Owner != "" ||
		observed.State != "OBSERVED" || observed.Answer != nil {
		t.Fatal("RelatedID resolution guessed a destination, owner or authorization", delivery)
	}
	var workers sync.WaitGroup
	errs := make(chan error, 8)
	for range cap(errs) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := ResolveCoderClarification(db, input.ID)
			if err == nil && !reflect.DeepEqual(got, delivery) {
				t.Error("parallel retry rebound the saved destination", got)
			}
			errs <- err
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	replayed, err := RequestClarification(db, input)
	if err != nil || !reflect.DeepEqual(replayed.Delivery, observed.Delivery) {
		t.Fatal("question replay changed delivery identity", replayed, err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || len(after.Records.Requests) != len(before.Records.Requests)+2 {
		t.Fatal("question replay or parallel resolution appended duplicate receipts", err)
	}
	var question, resolution store.RequestRecord
	for _, record := range after.Records.Requests {
		switch record.RequestID {
		case "clarification:" + input.ID:
			question = record
		case "clarification-delivery:" + input.ID:
			resolution = record
		}
	}
	if delivery.Question != (ClarificationReceipt{question.RequestID, question.Sequence, question.Hash}) ||
		strings.Contains(resolution.Payload, input.Question) {
		t.Fatal("delivery lost its canonical reference or duplicated question text")
	}
	history, err := ClarificationHistory(db)
	if err != nil || len(history) != 1 || !reflect.DeepEqual(history[0].Delivery, observed.Delivery) {
		t.Fatal("history did not expose the durable HELD destination", history, err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("resolution changed Graph, approval, HOLD, runtime ownership or ledger")
	}
	// Import a pre-delivery snapshot through the existing store, then resolve it.
	legacy := before
	legacy.Records.Requests = append(legacy.Records.Requests, question)
	path := filepath.Join(t.TempDir(), "legacy.db")
	old, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.InitializeWorkspace(legacy); err != nil {
		t.Fatal(err)
	}
	fromOld, err := ResolveCoderClarification(old, input.ID)
	if err != nil || fromOld.Target == nil || fromOld.Target.DispatchID != coder.ID {
		t.Fatal("pre-existing canonical question could not be resolved", fromOld, err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := ResolveCoderClarification(reopened, input.ID)
	if err != nil || !reflect.DeepEqual(fromOld, recovered) {
		t.Fatal("store reopen reissued delivery or lost the HELD reference", recovered, err)
	}

	// Destination changes must never turn a retry into a retargeted request.
	snapshot, _ := db.LoadWorkspace()
	for index := range snapshot.Graph.Dispatches {
		if snapshot.Graph.Dispatches[index].ID == coder.ID {
			snapshot.Graph.Dispatches[index].RuntimeSessionID = "different-session"
		}
	}
	changed, err := resolveCoderClarification(&snapshot, input.ID)
	if err != nil || changed.Status != "HELD" || changed.Reason != "target-identity-changed" ||
		changed.Target.SessionID != delivery.Target.SessionID {
		t.Fatal("saved destination was rebound to a different session", changed, err)
	}
	var corrupt ClarificationDelivery
	if err := json.Unmarshal([]byte(resolution.Payload), &corrupt); err != nil {
		t.Fatal(err)
	}
	corrupt.Question.Hash = "wrong-evidence"
	payload, _ := json.Marshal(corrupt)
	for index := range snapshot.Records.Requests {
		if snapshot.Records.Requests[index].RequestID == resolution.RequestID {
			snapshot.Records.Requests[index].Payload = string(payload)
		}
	}
	if _, err := resolveCoderClarification(&snapshot, input.ID); err == nil {
		t.Fatal("delivery accepted a mismatched canonical question receipt")
	}
}

func TestCoderClarificationResolutionHoldsUntrustedAndLiveDestinations(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	source, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
	input := clarificationRequest(t, db, source.TaskID, source.ID, "REVIEWER")
	input.Category = "implementation"
	question, err := RequestClarification(db, input)
	if err != nil {
		t.Fatal(err)
	}
	// Untrusted in-memory metadata probes do not create an executable Flow.
	for _, name := range []string{"related", "missing", "ambiguous", "role", "task", "dispatch", "session", "source-context", "expired", "live", "owner", "owner-released", "deadline", "continuation-source"} {
		t.Run(name, func(t *testing.T) {
			body, _ := json.Marshal(state)
			var snapshot store.Workspace
			if err := json.Unmarshal(body, &snapshot); err != nil {
				t.Fatal(err)
			}
			body, _ = json.Marshal(question)
			var q Clarification
			if err := json.Unmarshal(body, &q); err != nil {
				t.Fatal(err)
			}
			expected := "no-safe-input-path"
			for index := range snapshot.Graph.Dispatches {
				d := &snapshot.Graph.Dispatches[index]
				if d.ID == source.ID {
					d.Status, d.RuntimeSessionID, d.RuntimeConfigHash = core.DispatchRunning, "review-session", "review-config"
					q.Context.Dispatch.RuntimeSessionID, q.Context.Dispatch.RuntimeConfigHash = d.RuntimeSessionID, d.RuntimeConfigHash
					if name == "related" {
						d.RelatedID, expected = "different", "source-identity-unverified"
					}
					if name == "session" {
						d.RuntimeSessionID, expected = "different", "source-identity-unverified"
					}
				}
				if d.ID != coder.ID {
					continue
				}
				d.Status = core.DispatchRunning
				switch name {
				case "role":
					d.Recipient, expected = "REVIEWER", "coder-not-unique"
				case "task":
					d.TaskID, expected = source.TaskID, "task-or-dispatch-mismatch"
				case "dispatch":
					d.Instruction.DispatchID, expected = "different", "task-or-dispatch-mismatch"
				}
			}
			snapshot.Graph.ReviewFlows[0].RunnerID = "target-runner"
			snapshot.Records.Operation = &store.Operation{
				ID: "target-operation", Kind: "request", TaskID: flow.TaskID, FlowID: flow.ID,
				Owner: "target-owner", State: "RUNNING", Phase: "EXECUTION", Deadline: time.Now().Add(time.Hour),
			}
			q.Origin = &ClarificationOrigin{
				SessionID: "review-session", OperationID: "target-operation", Owner: "target-owner", RunnerID: "target-runner",
			}
			if observed, err := clarificationContext(snapshot, source.TaskID, source.ID); err == nil && name != "source-context" {
				q.Context.ScopeHash = observed.ScopeHash
			}
			switch name {
			case "continuation-source":
				f := &snapshot.Graph.ReviewFlows[0]
				f.SourceDispatchID, f.TaskID, f.Contract.Supersedes = coder.ID, "successor-task", coder.TaskID
				f.DispatchIDs = []string{source.ID}
				for index := range snapshot.Graph.Tasks {
					if snapshot.Graph.Tasks[index].ID == source.TaskID {
						snapshot.Graph.Tasks[index].CreatedFrom = "flow:" + f.ID
					}
				}
				for index := range snapshot.Graph.Dispatches {
					if snapshot.Graph.Dispatches[index].ID == coder.ID {
						snapshot.Graph.Dispatches[index].Status = core.DispatchSucceeded
					}
				}
				observed, err := clarificationContext(snapshot, source.TaskID, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				q.Context.ScopeHash = observed.ScopeHash
				expected = "coder-ended-or-stopping"
			case "source-context":
				q.Context.ScopeHash = "changed-evidence"
				expected = "source-context-changed"
			case "missing":
				for index, d := range snapshot.Graph.Dispatches {
					if d.ID == coder.ID {
						snapshot.Graph.Dispatches = append(snapshot.Graph.Dispatches[:index], snapshot.Graph.Dispatches[index+1:]...)
						break
					}
				}
				expected = "coder-not-unique"
			case "ambiguous":
				snapshot.Graph.Dispatches = append(snapshot.Graph.Dispatches, coder)
				expected = "coder-not-unique"
			case "expired":
				q.Request.Deadline, expected = time.Now().Add(-time.Hour), "question-expired"
			case "owner":
				q.Origin.Owner, expected = "reviewer-owner-not-target-owner", "source-owner-changed"
			case "owner-released":
				snapshot.Records.Operation.Owner, expected = "", "coder-owner-unverified"
			case "deadline":
				snapshot.Records.Operation.Deadline, expected = time.Now().Add(-time.Hour), "coder-owner-unverified"
			}
			target, reason := coderClarificationTarget(snapshot, q)
			if reason != expected {
				t.Fatal("destination was not safely held", reason, expected, target)
			}
			if name == "live" && (target == nil || target.DispatchID != coder.ID ||
				target.SessionID != coder.RuntimeSessionID || target.Owner != "target-owner" ||
				target.SessionID == q.Origin.SessionID) {
				t.Fatal("target identity was copied from the Reviewer", target)
			}
		})
	}
}
