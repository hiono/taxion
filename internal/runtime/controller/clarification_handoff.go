package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// Handoffs name a new, explicitly selected successor; the original delivery is immutable.
type ClarificationHandoff struct {
	Version       int                  `json:"version"`
	Phase         string               `json:"phase"`
	Question      ClarificationReceipt `json:"question"`
	Original      ClarificationReceipt `json:"original"`
	Target        ClarificationTarget  `json:"target"`
	Reason        string               `json:"reason"`
	Status        string               `json:"status"`
	CallID        string               `json:"callId,omitempty"`
	RequestHash   string               `json:"requestHash,omitempty"`
	EventSequence uint64               `json:"eventSequence,omitempty"`
	At            time.Time            `json:"at"`
}

type clarificationInboxResponse struct {
	ID       string                `json:"id"`
	Handoff  ClarificationHandoff  `json:"handoff"`
	Receipt  ClarificationReceipt  `json:"receipt"`
	Question *ClarificationRequest `json:"question,omitempty"`
}

func savedClarificationHandoff(state store.Workspace, id string) (*ClarificationHandoff, ClarificationReceipt, error) {
	var last *ClarificationHandoff
	var ref ClarificationReceipt
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || record.RequestID != "clarification-handoff:"+id {
			continue
		}
		var next ClarificationHandoff
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &next); err != nil {
			return nil, ref, err
		}
		if next.Version != 1 || next.Phase != "clarification.handoff" || next.Question.RequestID != "clarification:"+id ||
			next.Target.DispatchID == "" || next.Target.Role != "CODER" || next.Reason == "" {
			return nil, ref, fmt.Errorf("taxion: invalid clarification handoff")
		}
		if last == nil {
			original, err := savedClarificationDelivery(state, id)
			if err != nil || original == nil || original.Question != next.Question || next.Status != "HELD" ||
				next.CallID != "" || next.RequestHash != "" || next.EventSequence != 0 {
				return nil, ref, fmt.Errorf("taxion: handoff requires its canonical original delivery")
			}
			found := false
			for _, r := range state.Records.Requests {
				found = found || r.Kind == "planning" && next.Original == (ClarificationReceipt{r.RequestID, r.Sequence, r.Hash}) &&
					r.RequestID == "clarification-delivery:"+id
			}
			if !found {
				return nil, ref, fmt.Errorf("taxion: handoff original receipt missing")
			}
		} else {
			valid := last.Status == "HELD" && next.Status == "REQUESTED" && next.CallID != "" &&
				next.EventSequence == 0 && next.RequestHash == "" ||
				last.Status == "REQUESTED" && next.Status == "DELIVERED" && next.CallID == last.CallID &&
					next.EventSequence > 0 && next.RequestHash == ref.Hash ||
				last.Status == "DELIVERED" && next.Status == "ACKNOWLEDGED" && next.CallID == last.CallID &&
					next.EventSequence == last.EventSequence && next.RequestHash == last.RequestHash
			if !valid || next.Question != last.Question || next.Original != last.Original ||
				!reflect.DeepEqual(next.Target, last.Target) || next.Reason != last.Reason {
				return nil, ref, fmt.Errorf("taxion: invalid clarification handoff transition or rebind")
			}
		}
		last = &next
		ref = ClarificationReceipt{record.RequestID, record.Sequence, record.Hash}
	}
	return last, ref, nil
}

func appendClarificationHandoff(state *store.Workspace, id string, value *ClarificationHandoff) (ClarificationReceipt, error) {
	value.At = time.Now().UTC()
	body, err := json.Marshal(value)
	if err != nil || core.Redact(string(body)) != string(body) {
		return ClarificationReceipt{}, fmt.Errorf("taxion: invalid or credential-bearing handoff")
	}
	if err := state.AppendRequestRecord("clarification-handoff:"+id, "planning", string(body)); err != nil {
		return ClarificationReceipt{}, err
	}
	r := state.Records.Requests[len(state.Records.Requests)-1]
	return ClarificationReceipt{r.RequestID, r.Sequence, r.Hash}, nil
}

func HandoffCoderClarification(db *store.Store, id, dispatchID, reason string) (ClarificationHandoff, error) {
	var result ClarificationHandoff
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if strings.TrimSpace(reason) == "" || dispatchID == "" {
			return fmt.Errorf("taxion: handoff requires an explicit successor Dispatch and reason")
		}
		prior, _, err := savedClarificationHandoff(*state, id)
		if err != nil {
			return err
		}
		if prior != nil {
			if prior.Target.DispatchID != dispatchID || prior.Reason != reason {
				return fmt.Errorf("taxion: an existing handoff cannot be retargeted")
			}
			result = *prior
			return nil
		}
		delivery, err := resolveCoderClarification(state, id)
		if err != nil || delivery.Target == nil {
			return fmt.Errorf("taxion: handoff requires a resolved original Coder")
		}
		target, ok := state.Graph.GraphDispatch(dispatchID)
		flow, found := state.Graph.ReviewFlow(delivery.Target.FlowID)
		history, err := clarifications(*state)
		if err != nil {
			return err
		}
		var question Clarification
		for _, q := range history {
			if q.Request.ID == id {
				question = q
			}
		}
		source, sourceFound := state.Graph.GraphDispatch(question.Request.DispatchID)
		index := slices.Index(flow.DispatchIDs, source.ID)
		if !ok || !found || !sourceFound || source.RelatedID != delivery.Target.DispatchID ||
			source.Status != core.DispatchSucceeded || source.Result == nil || source.Result.ReviewVerdict != "REQUEST_CHANGES" ||
			index < 0 || index+1 >= len(flow.DispatchIDs) || flow.DispatchIDs[index+1] != dispatchID ||
			flow.LastDispatchID() != dispatchID || target.TaskID != flow.TaskID ||
			target.Relationship != core.RelationshipAmends || target.RelatedID != delivery.Target.DispatchID {
			return fmt.Errorf("taxion: handoff requires the formally issued immediate successor Coder")
		}
		observed, err := clarificationContext(*state, target.TaskID, target.ID)
		if err != nil {
			return err
		}
		op := state.Records.Operation
		if op == nil {
			return fmt.Errorf("taxion: handoff requires the current execution owner")
		}
		result = ClarificationHandoff{
			Version: 1, Phase: "clarification.handoff", Question: delivery.Question, Status: "HELD", Reason: reason,
			Target: ClarificationTarget{
				TaskID: target.TaskID, DispatchID: target.ID, Role: target.Recipient, SessionID: target.RuntimeSessionID,
				ConfigHash: target.RuntimeConfigHash, ScopeHash: observed.ScopeHash, Status: target.Status, FlowID: flow.ID,
				OperationID: op.ID, Owner: op.Owner, RunnerID: flow.RunnerID,
			},
		}
		for _, r := range state.Records.Requests {
			if r.RequestID == "clarification-delivery:"+id && r.Kind == "planning" {
				result.Original = ClarificationReceipt{r.RequestID, r.Sequence, r.Hash}
			}
		}
		if err := validateClarificationHandoff(*state, result); err != nil {
			return err
		}
		_, err = appendClarificationHandoff(state, id, &result)
		return err
	})
	return result, err
}

func validateClarificationHandoff(state store.Workspace, handoff ClarificationHandoff) error {
	t := handoff.Target
	d, ok := state.Graph.GraphDispatch(t.DispatchID)
	flow, found := state.Graph.ReviewFlow(t.FlowID)
	op := state.Records.Operation
	now := time.Now().UTC()
	if !ok || !found || d.TaskID != t.TaskID || d.Recipient != "CODER" || t.Role != "CODER" ||
		d.Status != core.DispatchRunning || d.RuntimeSessionID == "" || d.RuntimeSessionID != t.SessionID ||
		d.RuntimeConfigHash == "" || d.RuntimeConfigHash != t.ConfigHash ||
		flow.Status != "RUNNING" || flow.LastDispatchID() != d.ID || flow.RunnerID == "" || flow.RunnerID != t.RunnerID ||
		flow.HandoffHeld() || !now.Before(flow.ExecutionDeadline()) ||
		op == nil || op.ID != t.OperationID || op.Owner == "" || op.Owner != t.Owner ||
		op.Kind != "request" || op.State != "RUNNING" || op.Phase != "EXECUTION" || op.FlowID != flow.ID || op.TaskID != flow.TaskID ||
		!now.Before(op.Deadline) {
		return fmt.Errorf("taxion: clarification successor session or execution lease is no longer current")
	}
	context, err := clarificationContext(state, t.TaskID, t.DispatchID)
	if err != nil || context.ScopeHash != t.ScopeHash {
		return fmt.Errorf("taxion: clarification successor scope changed")
	}
	questions, err := clarifications(state)
	if err != nil {
		return err
	}
	for _, q := range questions {
		if "clarification:"+q.Request.ID == handoff.Question.RequestID {
			if q.Answer != nil || q.State != "OBSERVED" || !now.Before(q.Request.Deadline) {
				return fmt.Errorf("taxion: clarification is answered, expired or no longer observational")
			}
			return nil
		}
	}
	return fmt.Errorf("taxion: clarification question missing")
}

func (c *Controller) ReceiveCoderClarification(ctx context.Context, binding runtime.ManagedClarification, sessionID, callID string, body json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Action  string `json:"action"`
		ID      string `json:"id,omitempty"`
		Receipt string `json:"receipt,omitempty"`
	}
	if len(body) > 4096 || core.DecodeGraphInput(strings.NewReader(string(body)), &input) != nil ||
		(input.Action != "receive" && input.Action != "ack") || callID == "" ||
		input.Action == "ack" && (input.ID == "" || input.Receipt == "") {
		return nil, fmt.Errorf("taxion: invalid clarification inbox request")
	}
	var result any = map[string]string{"status": "HELD", "reason": "no-explicit-handoff"}
	err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, record := range state.Records.Requests {
			if record.Kind != "planning" || !strings.HasPrefix(record.RequestID, "clarification-handoff:") {
				continue
			}
			id := strings.TrimPrefix(record.RequestID, "clarification-handoff:")
			if seen[id] || input.ID != "" && input.ID != id {
				continue
			}
			seen[id] = true
			h, ref, err := savedClarificationHandoff(*state, id)
			if err != nil {
				return err
			}
			t := h.Target
			if t.DispatchID != binding.DispatchID {
				continue
			}
			if err := validateClarificationHandoff(*state, *h); err != nil {
				return err
			}
			d, _ := state.Graph.GraphDispatch(t.DispatchID)
			if sessionID != t.SessionID || binding.ScopeHash != t.ScopeHash || binding.OperationID != t.OperationID ||
				binding.Owner != t.Owner || binding.RunnerID != t.RunnerID || binding.FlowID != t.FlowID ||
				c.runtime.Role() != "CODER" || c.runtime.ConfigHash() != t.ConfigHash ||
				!sameWorktree(c.runtime.Worktree(), d.Instruction.Worktree) {
				return fmt.Errorf("taxion: clarification inbox binding changed")
			}
			if input.Action == "receive" && input.ID == "" && h.Status != "HELD" {
				result = clarificationInboxResponse{id, *h, ref, nil}
				continue
			}
			var question *ClarificationRequest
			if input.Action == "ack" {
				if (h.Status != "DELIVERED" && h.Status != "ACKNOWLEDGED") || h.RequestHash != input.Receipt {
					return fmt.Errorf("taxion: acknowledgement requires the delivered request receipt")
				}
				if h.Status == "DELIVERED" {
					h.Status = "ACKNOWLEDGED"
					ref, err = appendClarificationHandoff(state, id, h)
				}
			} else if h.Status == "HELD" {
				for _, r := range state.Records.Requests {
					if r.Kind == "planning" && strings.HasPrefix(r.RequestID, "clarification-handoff:") {
						var previous ClarificationHandoff
						if json.Unmarshal([]byte(r.Payload), &previous) != nil {
							return fmt.Errorf("taxion: invalid handoff evidence")
						}
						if previous.CallID == callID && previous.Target.SessionID == sessionID {
							return fmt.Errorf("taxion: inbox call ID already consumed")
						}
					}
				}
				// Persist before returning any payload. Uncertain outcomes are never resent.
				h.Status, h.CallID = "REQUESTED", callID
				ref, err = appendClarificationHandoff(state, id, h)
				questions, readErr := clarifications(*state)
				if readErr != nil {
					return readErr
				}
				for _, q := range questions {
					if q.Request.ID == id {
						value := q.Request
						question = &value
					}
				}
			}
			if err != nil {
				return err
			}
			result = clarificationInboxResponse{id, *h, ref, question}
			return nil
		}
		if input.Action == "ack" {
			return fmt.Errorf("taxion: acknowledgement has no matching handoff")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func recordClarificationArrival(state *store.Workspace, dispatch core.GraphDispatch, event runtime.Event) error {
	if event.Type != runtime.EventExecution || event.AgentID != "" || event.Sequence == 0 {
		return nil
	}
	var observation core.ExecutionObservation
	if core.DecodeGraphInput(strings.NewReader(event.Payload), &observation) != nil ||
		observation.Phase != "tool-complete" || observation.Success == nil || !*observation.Success {
		return nil
	}
	seen := map[string]bool{}
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || !strings.HasPrefix(record.RequestID, "clarification-handoff:") {
			continue
		}
		id := strings.TrimPrefix(record.RequestID, "clarification-handoff:")
		if seen[id] {
			continue
		}
		seen[id] = true
		h, ref, err := savedClarificationHandoff(*state, id)
		if err != nil {
			return err
		}
		if h.Status != "REQUESTED" || h.CallID != observation.CallID || h.Target.DispatchID != dispatch.ID ||
			h.Target.SessionID != dispatch.RuntimeSessionID || validateClarificationHandoff(*state, *h) != nil {
			continue
		}
		questions, err := clarifications(*state)
		if err != nil {
			return err
		}
		var question *ClarificationRequest
		for _, q := range questions {
			if q.Request.ID == id {
				value := q.Request
				question = &value
			}
		}
		expected, err := json.Marshal(clarificationInboxResponse{id, *h, ref, question})
		if err != nil || question == nil || observation.ContentHash != receiptHash(string(expected)) {
			continue
		}
		h.Status, h.RequestHash, h.EventSequence = "DELIVERED", ref.Hash, event.Sequence
		if _, err := appendClarificationHandoff(state, id, h); err != nil {
			return err
		}
	}
	return nil
}
