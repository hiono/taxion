package controller

import (
	"encoding/json"
	"fmt"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type InterruptRequest struct {
	FlowID           string              `json:"flowId"`
	ID               string              `json:"id"`
	ExpectedChangeID string              `json:"expectedChangeId"`
	Target           core.FeedbackTarget `json:"target"`
	Actor            string              `json:"actor"`
	Source           string              `json:"source"`
	Reason           string              `json:"reason"`
}

// RequestFlowInterrupt holds the boundary and records a targeted request, not
// stop confirmation or Task cancellation. The owning runner performs safe stop.
func RequestFlowInterrupt(db *store.Store, request InterruptRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: interruption requires a workspace")
	}
	text, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Reason    string `json:"reason"`
	}{"INTERRUPT_CURRENT_EXECUTION", request.Reason})
	if err != nil {
		return core.ReviewFlow{}, err
	}
	var flow core.ReviewFlow
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.ReviewFlow(request.FlowID)
		if !state.HasGraph || !ok {
			return fmt.Errorf("taxion: interruption requires an existing review flow")
		}
		replay := false
		for _, feedback := range current.Feedback {
			if feedback.ID == request.ID {
				replay = true
			}
		}
		var err error
		flow, err = state.Graph.RecordFlowFeedback(request.FlowID, core.FlowFeedback{
			ID: request.ID, ExpectedChangeID: request.ExpectedChangeID, Target: request.Target,
			Actor: request.Actor, Source: request.Source, Reason: request.Reason,
			Text: string(text), Evidence: []string{},
		})
		if err != nil || replay {
			return err
		}
		dispatch, _ := state.Graph.GraphDispatch(request.Target.DispatchID)
		switch dispatch.Status {
		case core.DispatchRunning:
			return state.Graph.TransitionDispatch(dispatch.ID, core.DispatchCancelRequested, request.Actor,
				"targeted user interruption requested: "+request.ID, false, false, nil)
		case core.DispatchIssued, core.DispatchAcknowledged:
			// The persisted hold prevents a concurrent start. An unstarted
			// session is not falsely reported as stopped.
			return nil
		default:
			return fmt.Errorf("taxion: current execution already ended or has a stop request; observe its result instead")
		}
	})
	return flow, err
}
