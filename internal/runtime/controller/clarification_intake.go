package controller

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func (c *Controller) ObserveRoleClarification(ctx context.Context, binding runtime.ManagedClarification, sessionID string, body json.RawMessage) (json.RawMessage, error) {
	var envelope struct {
		Action string `json:"action,omitempty"`
		ClarificationRequest
	}
	if len(body) > 64*1024 || core.DecodeGraphInput(strings.NewReader(string(body)), &envelope) != nil {
		return nil, fmt.Errorf("taxion: invalid bounded clarification input")
	}
	input := envelope.ClarificationRequest
	if envelope.Action == "read" {
		other := input
		other.ID = ""
		if binding.ProtocolVersion < 3 || input.ID == "" || !reflect.DeepEqual(other, ClarificationRequest{}) {
			return nil, fmt.Errorf("taxion: clarification read requires protocol 3 and only the original question ID")
		}
		return c.readRoleClarification(ctx, binding, sessionID, input.ID)
	}
	if envelope.Action != "" && envelope.Action != "request" {
		return nil, fmt.Errorf("taxion: unknown clarification action")
	}
	var result Clarification
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		observed, err := c.roleClarificationContext(ctx, *state, binding, sessionID)
		if err != nil {
			return err
		}
		dispatch := observed.Dispatch
		if input.TaskID != "" && input.TaskID != dispatch.TaskID ||
			input.DispatchID != "" && input.DispatchID != dispatch.ID ||
			input.Role != "" && input.Role != dispatch.Recipient ||
			input.BasisHash != "" && input.BasisHash != observed.Hash {
			return fmt.Errorf("taxion: clarification target or source does not match the running session")
		}
		operation := state.Records.Operation
		input.TaskID, input.DispatchID, input.Role, input.BasisHash = dispatch.TaskID, dispatch.ID, dispatch.Recipient, observed.Hash
		if input.ID == "" {
			input.ID = "clarification-" + rand.Text()
		}
		if input.Category == "" {
			switch dispatch.Recipient {
			case "REVIEWER":
				input.Category = "implementation"
			case "CODER":
				input.Category = "specification"
				if input.SpecificationOwnerRole == "" {
					input.SpecificationOwnerRole = "PLANNER"
				}
			default:
				return fmt.Errorf("taxion: unsupported clarification sender role")
			}
		}
		if input.Reason == "" {
			input.Reason = "The current Role cannot resolve this question from its bound Task and Dispatch context."
		}
		if input.Uncertainty == "" {
			input.Uncertainty = "The required information is not established by the current execution context."
		}
		if len(input.References) == 0 {
			for _, document := range observed.Documents {
				if document.ID != "" {
					input.References = append(input.References, document.ID)
				}
			}
		}
		if input.AnswerFormat == "" {
			input.AnswerFormat = "text"
		}
		if input.DecisionImpact == "" {
			input.DecisionImpact = "The answer may affect the current implementation or review decision within the saved scope."
		}
		if input.StopCondition == "" {
			input.StopCondition = "Do not proceed with work that depends on this answer until it has been assessed."
		}
		if input.Deadline.IsZero() || input.Deadline.After(operation.Deadline) {
			input.Deadline = operation.Deadline
		}
		result, err = requestClarification(state, input, &ClarificationOrigin{
			SessionID: sessionID, OperationID: operation.ID, Owner: operation.Owner, RunnerID: binding.RunnerID,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	// The receipt remains canonical; the tool returns a reference, not another
	// copy of the question or an authorization to continue dependent work.
	return json.Marshal(struct {
		ID        string                 `json:"id"`
		State     string                 `json:"state"`
		Route     string                 `json:"route"`
		Recipient string                 `json:"recipient"`
		RequestID string                 `json:"requestId"`
		Delivery  *ClarificationDelivery `json:"delivery,omitempty"`
	}{result.Request.ID, result.State, result.Route, result.Recipient, "clarification:" + result.Request.ID, result.Delivery})
}

func (c *Controller) roleClarificationContext(ctx context.Context, state store.Workspace, binding runtime.ManagedClarification, sessionID string) (ClarificationContext, error) {
	if err := ctx.Err(); err != nil {
		return ClarificationContext{}, err
	}
	dispatch, ok := state.Graph.GraphDispatch(binding.DispatchID)
	if !state.HasGraph || !ok || dispatch.Status != core.DispatchRunning ||
		sessionID == "" || dispatch.RuntimeSessionID != sessionID ||
		dispatch.Recipient != c.runtime.Role() || dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
		!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
		return ClarificationContext{}, fmt.Errorf("taxion: clarification requires its running bound Role session")
	}
	operation := state.Records.Operation
	flow, found := flowForDispatch(state.Graph, dispatch.ID)
	now := time.Now().UTC()
	if operation == nil || binding.OperationID == "" || binding.Owner == "" ||
		operation.ID != binding.OperationID || operation.Owner != binding.Owner ||
		operation.State != "RUNNING" || operation.Phase != "EXECUTION" ||
		operation.FlowID != binding.FlowID || !now.Before(operation.Deadline) ||
		!found || flow.ID != binding.FlowID || flow.TaskID != operation.TaskID ||
		flow.Status != "RUNNING" || binding.RunnerID == "" || flow.RunnerID != binding.RunnerID ||
		!now.Before(flow.ExecutionDeadline()) {
		return ClarificationContext{}, fmt.Errorf("taxion: clarification operation owner, runner or deadline changed")
	}
	observed, err := clarificationContext(state, dispatch.TaskID, dispatch.ID)
	if err != nil {
		return observed, err
	}
	if binding.ScopeHash == "" || observed.ScopeHash != binding.ScopeHash {
		return observed, fmt.Errorf("taxion: clarification source does not match the running session")
	}
	return observed, nil
}

func (c *Controller) readRoleClarification(ctx context.Context, binding runtime.ManagedClarification, sessionID, id string) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	observed, err := c.roleClarificationContext(ctx, state, binding, sessionID)
	if err != nil {
		return nil, err
	}
	history, err := clarifications(state)
	if err != nil {
		return nil, err
	}
	var question Clarification
	for _, entry := range history {
		if entry.Request.ID == id {
			question = entry
		}
	}
	origin := question.Origin
	if origin == nil || origin.SessionID != sessionID || origin.OperationID != binding.OperationID ||
		origin.Owner != binding.Owner || origin.RunnerID != binding.RunnerID ||
		question.Request.TaskID != observed.Task.ID || question.Request.DispatchID != binding.DispatchID ||
		question.Request.Role != observed.Dispatch.Recipient || question.Context.ScopeHash != observed.ScopeHash {
		return nil, fmt.Errorf("taxion: clarification read requires this execution's original question")
	}
	report, err := assessClarification(state, id)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(report)
	if err != nil || len(body) > 64*1024 {
		return nil, fmt.Errorf("taxion: clarification assessment exceeds the bounded response")
	}
	return body, nil
}
