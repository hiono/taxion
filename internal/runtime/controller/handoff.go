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
