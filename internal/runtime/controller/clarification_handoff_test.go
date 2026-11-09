package controller

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func clarificationSuccessorFixture(t *testing.T) (*store.Store, *Controller, runtime.ManagedClarification, string, string) {
	t.Helper()
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	original := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := db.LoadWorkspace()
	reviewer, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
	input := clarificationRequest(t, db, reviewer.TaskID, reviewer.ID, "REVIEWER")
	input.Category, input.Deadline = "evidence", time.Now().Add(5*time.Minute)
	if _, err := RequestClarification(db, input); err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	d, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
	worker := &fakeRuntime{role: "CODER", worktree: d.Instruction.Worktree, hash: "successor-config", sessionID: "successor-session"}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	prompt, _ := core.RenderDispatchInstruction(d)
	if err := control.Send(t.Context(), d.ID, prompt); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(s *store.Workspace) error {
		s.Records.Operation = &store.Operation{
			Version: 1, ID: "handoff-operation", Kind: "request", State: "RUNNING", Phase: "EXECUTION",
			TaskID: flow.TaskID, FlowID: flow.ID, Owner: "handoff-owner", Deadline: flow.ExecutionDeadline(), PlanningUsageState: "complete",
		}
		for i := range s.Graph.ReviewFlows {
			if s.Graph.ReviewFlows[i].ID == flow.ID {
				s.Graph.ReviewFlows[i].RunnerID = "handoff-runner"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	observed, err := ObserveClarification(db, d.TaskID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding := runtime.ManagedClarification{
		ProtocolVersion: 2, DispatchID: d.ID, ScopeHash: observed.ScopeHash,
		OperationID: "handoff-operation", Owner: "handoff-owner", FlowID: flow.ID, RunnerID: "handoff-runner",
	}
	return db, control, binding, input.ID, original.ID
}

func TestClarificationSuccessorDeliveryAndAcknowledgement(t *testing.T) {
	db, control, binding, id, original := clarificationSuccessorFixture(t)
	before, _ := db.LoadWorkspace()
	if _, err := HandoffCoderClarification(db, id, original, "explicit transfer"); err == nil {
		t.Fatal("ended original Coder accepted as a successor")
	}
	if _, err := HandoffCoderClarification(db, id, "missing", "explicit transfer"); err == nil {
		t.Fatal("missing successor accepted")
	}
	held, err := HandoffCoderClarification(db, id, binding.DispatchID, "explicit transfer")
	if err != nil || held.Status != "HELD" {
		t.Fatal(held, err)
	}
	snapshot, _ := db.LoadWorkspace()
	for _, kind := range []string{"task", "dispatch", "session", "owner", "role", "scope"} {
		bad := held
		switch kind {
		case "task":
			bad.Target.TaskID = "other"
		case "dispatch":
			bad.Target.DispatchID = original
		case "session":
			bad.Target.SessionID = "other"
		case "owner":
			bad.Target.Owner = "other"
		case "role":
			bad.Target.Role = "REVIEWER"
		case "scope":
			bad.Target.ScopeHash = "other"
		}
		if err := validateClarificationHandoff(snapshot, bad); err == nil {
			t.Fatal("changed target identity accepted", kind)
		}
	}
	replayed, err := HandoffCoderClarification(db, id, binding.DispatchID, "explicit transfer")
	if err != nil || !reflect.DeepEqual(replayed, held) {
		t.Fatal("handoff replay changed", replayed, err)
	}
	if _, err := HandoffCoderClarification(db, id, original, "retarget"); err == nil {
		t.Fatal("implicit rebind allowed")
	}

	receive := func(call string) []byte {
		t.Helper()
		body, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", call,
			json.RawMessage(`{"action":"receive"}`))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	var workers sync.WaitGroup
	payloads := make(chan []byte, 8)
	for i := range cap(payloads) {
		workers.Add(1)
		go func() { defer workers.Done(); payloads <- receive(fmt.Sprintf("receive-%d", i)) }()
	}
	workers.Wait()
	close(payloads)
	var delivered struct {
		ID       string                `json:"id"`
		Handoff  ClarificationHandoff  `json:"handoff"`
		Receipt  ClarificationReceipt  `json:"receipt"`
		Question *ClarificationRequest `json:"question"`
	}
	count := 0
	var deliveredBody []byte
	for payload := range payloads {
		var got struct {
			Question *ClarificationRequest `json:"question"`
		}
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		if got.Question != nil {
			count++
			deliveredBody = payload
			if err := json.Unmarshal(payload, &delivered); err != nil {
				t.Fatal(err)
			}
		}
	}
	if count != 1 || delivered.Handoff.Status != "REQUESTED" || delivered.Question.ID != id {
		t.Fatal("parallel requests did not return exactly one observational payload", count, delivered)
	}
	ack := json.RawMessage(fmt.Sprintf(`{"action":"ack","id":%q,"receipt":%q}`, id, delivered.Receipt.Hash))
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "ack-early", ack); err == nil {
		t.Fatal("host tool success alone was treated as arrival")
	}
	for _, mutation := range []string{"task", "session", "owner", "runner", "scope", "dispatch"} {
		bad := binding
		session := "successor-session"
		switch mutation {
		case "task":
			bad.FlowID = "other"
		case "session":
			session = "other"
		case "owner":
			bad.Owner = "other"
		case "runner":
			bad.RunnerID = "other"
		case "scope":
			bad.ScopeHash = "other"
		case "dispatch":
			bad.DispatchID = original
		}
		if _, err := control.ReceiveCoderClarification(t.Context(), bad, session, "bad-ack", ack); err == nil {
			t.Fatal("mismatched acknowledgement accepted", mutation)
		}
	}
	success := true
	payload, _ := json.Marshal(core.ExecutionObservation{Phase: "tool-complete", CallID: delivered.Handoff.CallID, Success: &success, ContentHash: receiptHash(string(deliveredBody))})
	event := runtime.Event{Type: runtime.EventExecution, Payload: string(payload), Sequence: 1, AgentID: "nested"}
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	history, _ := ClarificationHistory(db)
	if history[0].Handoff.Status != "REQUESTED" {
		t.Fatal("nested event proved root arrival")
	}
	event.AgentID, event.Sequence, event.Payload = "", 2, `{"phase":"tool-complete","callId":"unrelated","success":true}`
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	event.Sequence, event.Payload = 3, fmt.Sprintf(`{"phase":"tool-complete","callId":%q,"success":false}`, delivered.Handoff.CallID)
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	history, _ = ClarificationHistory(db)
	if history[0].Handoff.Status != "REQUESTED" {
		t.Fatal("failed or unrelated tool proved delivery")
	}
	event.Sequence, event.Payload = 4, fmt.Sprintf(`{"phase":"tool-complete","callId":%q,"success":true,"contentHash":"wrong"}`, delivered.Handoff.CallID)
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	history, _ = ClarificationHistory(db)
	if history[0].Handoff.Status != "REQUESTED" {
		t.Fatal("altered model-facing content proved delivery")
	}
	event.Sequence, event.Payload = 5, string(payload)
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	history, err = ClarificationHistory(db)
	if err != nil || history[0].Handoff.Status != "DELIVERED" || history[0].State != "OBSERVED" {
		t.Fatal("bound provider completion did not record delivery", history, err)
	}
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "ack", ack); err != nil {
		t.Fatal(err)
	}
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "ack-retry", ack); err != nil {
		t.Fatal(err)
	}
	history, err = ClarificationHistory(db)
	if err != nil || history[0].Handoff.Status != "ACKNOWLEDGED" || history[0].Answer != nil ||
		history[0].State != "OBSERVED" || history[0].Delivery.Target.DispatchID != original {
		t.Fatal("acknowledgement changed the question, answer or original destination", history, err)
	}
	after, _ := db.LoadWorkspace()
	if len(after.Records.Requests) != len(before.Records.Requests)+4 {
		t.Fatal("duplicate handoff, send, event or ACK records", len(after.Records.Requests), len(before.Records.Requests))
	}
	for _, record := range after.Records.Requests[len(before.Records.Requests):] {
		if strings.Contains(record.Payload, delivered.Question.Question) {
			t.Fatal("question body duplicated")
		}
	}
	after.Records.Requests, after.Records.Events = before.Records.Requests, before.Records.Events
	if !reflect.DeepEqual(before, after) {
		t.Fatal("delivery changed Graph, authorization, HOLD, runtime owner or ledger")
	}
}

func TestClarificationSuccessorUncertainOutcomeAndRevokedOwner(t *testing.T) {
	db, control, binding, id, _ := clarificationSuccessorFixture(t)
	if _, err := HandoffCoderClarification(db, id, binding.DispatchID, "explicit transfer"); err != nil {
		t.Fatal(err)
	}
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "first",
		json.RawMessage(`{"action":"receive"}`)); err != nil {
		t.Fatal(err)
	}
	// No provider completion was observed; retrying cannot justify resending.
	retry, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "retry",
		json.RawMessage(`{"action":"receive"}`))
	var response clarificationInboxResponse
	if err != nil || json.Unmarshal(retry, &response) != nil || response.Question != nil || response.Handoff.Status != "REQUESTED" {
		t.Fatal("uncertain payload resent", string(retry), err)
	}
	snapshot, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "reopened.db")
	copy, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := copy.InitializeWorkspace(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := copy.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	resumed, err := New(reopened, control.runtime)
	if err != nil {
		t.Fatal(err)
	}
	retry, err = resumed.ReceiveCoderClarification(t.Context(), binding, "successor-session", "after-reopen",
		json.RawMessage(`{"action":"receive"}`))
	if err != nil || json.Unmarshal(retry, &response) != nil || response.Question != nil || response.Handoff.Status != "REQUESTED" {
		t.Fatal("store reopen resent an uncertain request", string(retry), err)
	}
	if err := db.UpdateWorkspace(func(s *store.Workspace) error { s.Records.Operation.Owner = "new-owner"; return nil }); err != nil {
		t.Fatal(err)
	}

	event := runtime.Event{Type: runtime.EventExecution, Sequence: 1, Payload: `{"phase":"tool-complete","callId":"first","success":true}`}
	if err := control.recordEvent(binding.DispatchID, "stream", event); err != nil {
		t.Fatal(err)
	}
	history, err := ClarificationHistory(db)
	if err != nil || history[0].Handoff.Status != "REQUESTED" {
		t.Fatal("late event with revoked owner accepted", history, err)
	}
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "late",
		json.RawMessage(fmt.Sprintf(`{"action":"receive","id":%q}`, id))); err == nil {
		t.Fatal("revoked owner received data")
	}
}

func TestClarificationSuccessorStoppedBeforeReceive(t *testing.T) {
	db, control, binding, id, _ := clarificationSuccessorFixture(t)
	if _, err := HandoffCoderClarification(db, id, binding.DispatchID, "explicit transfer"); err != nil {
		t.Fatal(err)
	}
	if err := control.Stop(t.Context(), binding.DispatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "late",
		json.RawMessage(fmt.Sprintf(`{"action":"receive","id":%q}`, id))); err == nil {
		t.Fatal("stopped successor received the question")
	}
	history, err := ClarificationHistory(db)
	if err != nil || history[0].Handoff.Status != "HELD" {
		t.Fatal("stopped handoff was marked delivered", history, err)
	}
}
