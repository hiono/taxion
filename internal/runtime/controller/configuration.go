package controller

import (
	"fmt"
	"strings"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// CheckConfiguration resolves a recorded approval, not the identity of the
// person recording it or the eligibility of external credentials and contracts.
func CheckConfiguration(db *store.Store, taskID string, candidate config.Config) (core.TaskDecision, error) {
	if db == nil {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval requires a workspace")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return core.TaskDecision{}, err
	}
	if !state.HasGraph {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval requires a Task Graph")
	}
	if err := state.Graph.Validate(); err != nil {
		return core.TaskDecision{}, err
	}
	return checkTaskConfiguration(state.Graph, taskID, candidate)
}

func checkTaskConfiguration(graph core.TaskGraph, taskID string, candidate config.Config) (core.TaskDecision, error) {
	task, ok := graph.GraphTask(taskID)
	if !ok {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval Task not found")
	}
	switch task.Status {
	case core.Cancelled, core.Rejected, core.Superseded, core.Completed:
		return core.TaskDecision{}, fmt.Errorf("taxion: terminal Task cannot authorize configuration")
	}
	if task.Repo != candidate.Repository.Path {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration repository path differs from the Task")
	}
	if task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "" {
		return core.TaskDecision{}, fmt.Errorf("taxion: pending Human decision requires resolution")
	}
	for _, decision := range task.Decisions {
		if decision.ID != candidate.Execution.ApprovalRef {
			continue
		}
		if strings.TrimSpace(decision.Source) == "" {
			return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval requires a source")
		}
		if err := config.CheckApproval(decision.Text, taskID, decision.ID, candidate); err != nil {
			return core.TaskDecision{}, err
		}
		return decision, nil
	}
	return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval reference is absent from the Task decisions")
}

// CheckDispatchConfiguration checks the immutable execution instruction as well
// as the current Task's approval. A later Task approval cannot widen a Dispatch.
func CheckDispatchConfiguration(db *store.Store, dispatchID string, candidate config.Config) (core.TaskDecision, error) {
	if db == nil {
		return core.TaskDecision{}, fmt.Errorf("taxion: Dispatch configuration approval requires a workspace")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return core.TaskDecision{}, err
	}
	if !state.HasGraph {
		return core.TaskDecision{}, fmt.Errorf("taxion: Dispatch configuration approval requires a Task Graph")
	}
	if err := state.Graph.Validate(); err != nil {
		return core.TaskDecision{}, err
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok || !active(dispatch.Status) || dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration requires a Dispatch authorized to start or continue")
	}
	if dispatch.Repository != candidate.Repository.Path {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration repository differs from the Dispatch")
	}
	decision, err := checkTaskConfiguration(state.Graph, dispatch.TaskID, candidate)
	if err != nil {
		return core.TaskDecision{}, err
	}
	task, _ := state.Graph.GraphTask(dispatch.TaskID)
	if task.Status != core.Active {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration requires an active Dispatch Task")
	}
	for _, recorded := range dispatch.Instruction.Decisions {
		if recorded.ID != candidate.Execution.ApprovalRef {
			continue
		}
		if recorded != decision {
			return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval changed after Dispatch issuance; issue a newly authorized Dispatch")
		}
		if err := config.CheckApproval(recorded.Text, dispatch.TaskID, recorded.ID, candidate); err != nil {
			return core.TaskDecision{}, err
		}
		return recorded, nil
	}
	return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval is absent from the saved Dispatch instruction; issue a newly authorized Dispatch")
}
