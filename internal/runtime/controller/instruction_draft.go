package controller

import (
	"fmt"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type InstructionDraftRequest struct {
	FlowID string `json:"flowId"`
	core.InstructionDraft
}

type DraftDiscardRequest struct {
	FlowID string `json:"flowId"`
	core.DraftDiscard
}

func RecordInstructionDraft(db *store.Store, request InstructionDraftRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: instruction draft requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: instruction draft requires a Task Graph")
		}
		var err error
		flow, err = state.Graph.RecordInstructionDraft(request.FlowID, request.InstructionDraft)
		return err
	})
	return flow, err
}

func DiscardInstructionDraft(db *store.Store, request DraftDiscardRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: draft discard requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: draft discard requires a Task Graph")
		}
		var err error
		flow, err = state.Graph.DiscardInstructionDraft(request.FlowID, request.DraftDiscard)
		return err
	})
	return flow, err
}
