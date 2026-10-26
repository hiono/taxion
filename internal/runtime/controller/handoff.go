package controller

import (
	"errors"
	"fmt"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type HandoffRequest struct {
	FlowID string `json:"flowId"`
	core.HandoffChange
}

func ChangeHandoff(db *store.Store, request HandoffRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: handoff change requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: handoff change requires a Task Graph")
		}
		var err error
		flow, err = state.Graph.ChangeHandoff(request.FlowID, request.HandoffChange)
		return err
	})
	return flow, err
}

// ChangeObservedHandoff does not silently apply a console choice to a role or
// draft that changed while the user was reading the displayed observation.
func ChangeObservedHandoff(db *store.Store, observation FeedbackObservation, change core.HandoffChange) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: observed handoff change requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.ReviewFlow(observation.FlowID)
		if !state.HasGraph || !ok || len(current.DispatchIDs) == 0 {
			return fmt.Errorf("taxion: observed handoff requires an existing flow")
		}
		last, _ := state.Graph.GraphDispatch(current.DispatchIDs[len(current.DispatchIDs)-1])
		target, err := core.ObserveFeedbackTarget(last)
		if err != nil {
			return err
		}
		draftID := ""
		if len(current.InstructionDrafts) != 0 {
			draftID = current.InstructionDrafts[len(current.InstructionDrafts)-1].ID
		}
		hash, err := current.DraftContractHash()
		if err != nil {
			return err
		}
		if target != observation.Target || draftID != observation.ExpectedDraftID || hash != observation.DraftContractHash ||
			change.ExpectedChangeID != observation.ExpectedChangeID {
			return core.ErrFeedbackStale
		}
		flow, err = state.Graph.ChangeHandoff(observation.FlowID, change)
		return err
	})
	return flow, err
}

// A held boundary is benign only when no cleanup or storage failure is joined.
func handoffHeldOnly(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if !handoffHeldOnly(part) {
				return false
			}
		}
		return true
	}
	if wrapped := errors.Unwrap(err); wrapped != nil {
		return handoffHeldOnly(wrapped)
	}
	return err == core.ErrHandoffHeld
}
