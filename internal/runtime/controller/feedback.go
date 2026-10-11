package controller

import (
	"fmt"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type FeedbackRequest struct {
	FlowID string `json:"flowId"`
	core.FlowFeedback
}

type FeedbackObservation struct {
	FlowID             string              `json:"flowId"`
	ExpectedChangeID   string              `json:"expectedChangeId"`
	Target             core.FeedbackTarget `json:"target"`
	PendingFeedbackIDs []string            `json:"pendingFeedbackIds"`
	ExpectedDraftID    string              `json:"expectedDraftId"`
	PendingDraftID     string              `json:"pendingDraftId"`
}

func ObserveFeedback(db *store.Store, flowID string) (FeedbackObservation, error) {
	if db == nil {
		return FeedbackObservation{}, fmt.Errorf("taxion: feedback observation requires a workspace")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return FeedbackObservation{}, err
	}
	flow, ok := state.Graph.ReviewFlow(flowID)
	if !state.HasGraph || !ok {
		return FeedbackObservation{}, fmt.Errorf("taxion: feedback observation requires an existing review flow")
	}
	last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	target, err := core.ObserveFeedbackTarget(last)
	if err != nil {
		return FeedbackObservation{}, err
	}
	observation := FeedbackObservation{FlowID: flowID, Target: target, PendingFeedbackIDs: flow.PendingFeedbackIDs()}
	if len(flow.InstructionDrafts) != 0 {
		observation.ExpectedDraftID = flow.InstructionDrafts[len(flow.InstructionDrafts)-1].ID
	}
	observation.PendingDraftID = flow.PendingDraftID()
	if len(flow.HandoffChanges) != 0 {
		observation.ExpectedChangeID = flow.HandoffChanges[len(flow.HandoffChanges)-1].ID
	}
	return observation, nil
}

func RecordFeedback(db *store.Store, request FeedbackRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: feedback recording requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: feedback recording requires a Task Graph")
		}
		var err error
		flow, err = state.Graph.RecordFlowFeedback(request.FlowID, request.FlowFeedback)
		return err
	})
	return flow, err
}
