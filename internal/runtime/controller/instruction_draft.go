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

type DraftApprovalRequest struct {
	FlowID string `json:"flowId"`
	core.DraftApproval
}

func ApproveInstructionDraft(db *store.Store, request DraftApprovalRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: draft approval requires a workspace")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.ReviewFlow(request.FlowID)
		if !state.HasGraph || !ok {
			return fmt.Errorf("taxion: draft approval requires an existing flow")
		}
		for _, draft := range current.InstructionDrafts {
			if draft.Approval != nil && draft.Approval.ID == request.ID {
				var err error
				flow, err = state.Graph.ApproveInstructionDraft(request.FlowID, request.DraftApproval)
				return err
			}
		}
		task, _ := state.Graph.GraphTask(current.TaskID)
		if !flowContractMatches(state.Graph, current, task) {
			return fmt.Errorf("taxion: contract changed; supplementary approval cannot reauthorize it")
		}
		var err error
		flow, err = state.Graph.ApproveInstructionDraft(request.FlowID, request.DraftApproval)
		return err
	})
	return flow, err
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
