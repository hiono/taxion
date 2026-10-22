package controller

import (
	"context"
	"fmt"
	"slices"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type operationKey struct{}

// WithOperation propagates a claimed admission identity through the existing
// runner. It grants no Task, configuration, packet or budget authorization.
func WithOperation(ctx context.Context, operation store.Operation) context.Context {
	return context.WithValue(ctx, operationKey{}, operation)
}

func checkOperation(state *store.Workspace, ctx context.Context, taskID, flowID string) error {
	if claim, supplied := ctx.Value(operationKey{}).(store.Operation); supplied {
		current := state.Records.Operation
		if current == nil || !current.Active() || current.ID != claim.ID ||
			claim.Owner == "" || current.Owner != claim.Owner {
			return fmt.Errorf("taxion: operation claim is stale")
		}
	}
	if operation := state.Records.Operation; operation != nil && operation.Active() {
		claim, ok := ctx.Value(operationKey{}).(store.Operation)
		if !ok || claim.ID != operation.ID || claim.Owner == "" || claim.Owner != operation.Owner ||
			operation.State != "RUNNING" || operation.TaskID != taskID && operation.FlowID != flowID ||
			flowID == "" && operation.TaskID != taskID {
			return fmt.Errorf("taxion: operation %s is %s; only its authorized continuation is accepted", operation.ID, operation.State)
		}
	}
	for _, flow := range state.Graph.ReviewFlows {
		if flow.ID != flowID && (flow.Status == "RUNNING" || flow.RunnerID != "") {
			return fmt.Errorf("taxion: workflow %s is active; another operation cannot start", flow.ID)
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if !active(dispatch.Status) || !dispatch.ExecutionStarted && dispatch.RuntimeSessionID == "" {
			continue
		}
		flow, found := flowForDispatch(state.Graph, dispatch.ID)
		if !found || flow.ID != flowID {
			return fmt.Errorf("taxion: Dispatch %s is active; another operation cannot start", dispatch.ID)
		}
	}
	return nil
}

// PreflightReviewFlow batches the approved read-only lanes before runtime
// creation. No model is invoked and a passing lane grants no new approval.
func PreflightReviewFlow(ctx context.Context, db *store.Store, taskID, root string) (LaneReport, error) {
	state, err := db.LoadWorkspace()
	if err != nil {
		return LaneReport{}, err
	}
	flowID := ""
	for _, flow := range state.Graph.ReviewFlows {
		if flow.TaskID == taskID && flow.Status == "RUNNING" {
			flowID = flow.ID
		}
	}
	if err := checkOperation(&state, ctx, taskID, flowID); err != nil {
		return LaneReport{}, err
	}
	report, err := RunReadOnlyLanes(ctx, LaneRequest{Graph: &state.Graph, TaskID: taskID, RepositoryRoot: root})
	if err == nil && !report.ExecutionGateOpen {
		err = fmt.Errorf("taxion: preflight failed; execution gate remains closed")
	}
	return report, err
}

func checkDispatchOperation(state *store.Workspace, ctx context.Context, dispatch core.GraphDispatch) error {
	flow, _ := flowForDispatch(state.Graph, dispatch.ID)
	if flow.ID == "" {
		copy := *state
		copy.Graph.Dispatches = slices.DeleteFunc(slices.Clone(state.Graph.Dispatches), func(d core.GraphDispatch) bool { return d.ID == dispatch.ID })
		state = &copy
	}
	return checkOperation(state, ctx, dispatch.TaskID, flow.ID)
}
