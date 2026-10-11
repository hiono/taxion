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
	task, ok := state.Graph.GraphTask(taskID)
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
