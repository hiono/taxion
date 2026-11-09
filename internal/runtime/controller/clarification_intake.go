package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func (c *Controller) ObserveRoleClarification(ctx context.Context, binding runtime.ManagedClarification, sessionID string, body json.RawMessage) (json.RawMessage, error) {
	var input ClarificationRequest
	if len(body) > 64*1024 || core.DecodeGraphInput(strings.NewReader(string(body)), &input) != nil {
		return nil, fmt.Errorf("taxion: invalid bounded clarification input")
	}
	var result Clarification
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dispatch, ok := state.Graph.GraphDispatch(binding.DispatchID)
		if !state.HasGraph || !ok || dispatch.Status != core.DispatchRunning ||
			sessionID == "" || dispatch.RuntimeSessionID != sessionID ||
			dispatch.Recipient != c.runtime.Role() || dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("taxion: clarification requires its running bound Role session")
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
			return fmt.Errorf("taxion: clarification operation owner, runner or deadline changed")
		}
		observed, err := clarificationContext(*state, dispatch.TaskID, dispatch.ID)
		if err != nil {
			return err
		}
		if binding.ScopeHash == "" || observed.ScopeHash != binding.ScopeHash ||
			input.TaskID != "" && input.TaskID != dispatch.TaskID ||
			input.DispatchID != "" && input.DispatchID != dispatch.ID ||
			input.Role != "" && input.Role != dispatch.Recipient ||
			input.BasisHash != "" && input.BasisHash != observed.Hash {
			return fmt.Errorf("taxion: clarification target or source does not match the running session")
		}
		input.TaskID, input.DispatchID, input.Role, input.BasisHash = dispatch.TaskID, dispatch.ID, dispatch.Recipient, observed.Hash
		if input.Deadline.After(operation.Deadline) {
			input.Deadline = operation.Deadline
		}
		result, err = requestClarification(state, input, &ClarificationOrigin{
			SessionID: sessionID, OperationID: operation.ID, Owner: operation.Owner, RunnerID: flow.RunnerID,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	// The receipt remains canonical; the tool returns a reference, not another
	// copy of the question or an authorization to continue dependent work.
	return json.Marshal(struct {
		ID        string `json:"id"`
		State     string `json:"state"`
		Route     string `json:"route"`
		Recipient string `json:"recipient"`
		RequestID string `json:"requestId"`
	}{result.Request.ID, result.State, result.Route, result.Recipient, "clarification:" + result.Request.ID})
}
