package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
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
	return checkDispatchConfiguration(state.Graph, dispatchID, candidate)
}

func checkDispatchConfiguration(graph core.TaskGraph, dispatchID string, candidate config.Config) (core.TaskDecision, error) {
	dispatch, ok := graph.GraphDispatch(dispatchID)
	if !ok || !active(dispatch.Status) || dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration requires a Dispatch authorized to start or continue")
	}
	if dispatch.Repository != candidate.Repository.Path {
		return core.TaskDecision{}, fmt.Errorf("taxion: configuration repository differs from the Dispatch")
	}
	decision, err := checkTaskConfiguration(graph, dispatch.TaskID, candidate)
	if err != nil {
		return core.TaskDecision{}, err
	}
	task, _ := graph.GraphTask(dispatch.TaskID)
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
		if dispatch.Configuration != nil {
			var selected config.Config
			if err := core.DecodeGraphInput(strings.NewReader(string(dispatch.Configuration.Selection)), &selected); err != nil {
				return core.TaskDecision{}, fmt.Errorf("taxion: saved Dispatch configuration is invalid")
			}
			if selected != candidate || dispatch.Configuration.DecisionID != decision.ID {
				return core.TaskDecision{}, fmt.Errorf("taxion: configuration differs from the saved Dispatch selection; issue a newly authorized Dispatch")
			}
		}
		return recorded, nil
	}
	return core.TaskDecision{}, fmt.Errorf("taxion: configuration approval is absent from the saved Dispatch instruction; issue a newly authorized Dispatch")
}

// BindConfiguration saves a selected approved tuple before a session exists.
// It does not authenticate actors or authorize an SDK operation.
func BindConfiguration(db *store.Store, dispatchID string, resolved config.Resolution) error {
	if db == nil {
		return fmt.Errorf("taxion: configuration selection requires a workspace")
	}
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: configuration selection requires a Task Graph")
		}
		decision, err := checkDispatchConfiguration(state.Graph, dispatchID, resolved.Config)
		if err != nil {
			return err
		}
		selection, err := json.Marshal(resolved.Config)
		if err != nil {
			return fmt.Errorf("taxion: encode selected configuration: %w", err)
		}
		return state.Graph.BindDispatchConfiguration(dispatchID, core.DispatchConfiguration{
			DecisionID: decision.ID, Selection: selection, Sources: resolved.Sources, RecordedAt: time.Now().UTC(),
		})
	})
}

func (c *Controller) checkExecutionConfiguration(graph core.TaskGraph, dispatchID string) (config.Config, error) {
	dispatch, ok := graph.GraphDispatch(dispatchID)
	if !ok {
		return config.Config{}, fmt.Errorf("taxion: configuration Dispatch not found")
	}
	worker, configured := c.runtime.(runtime.ConfigurationIdentity)
	var selection json.RawMessage
	if configured {
		selection = worker.ExecutionConfiguration()
	}
	if dispatch.Configuration == nil && len(selection) == 0 {
		return config.Config{}, nil
	}
	if dispatch.Configuration == nil || len(selection) == 0 {
		return config.Config{}, fmt.Errorf("taxion: runtime requires the saved Dispatch configuration selection")
	}
	var candidate config.Config
	if err := core.DecodeGraphInput(strings.NewReader(string(selection)), &candidate); err != nil {
		return config.Config{}, fmt.Errorf("taxion: runtime configuration selection is invalid")
	}
	if _, err := checkDispatchConfiguration(graph, dispatchID, candidate); err != nil {
		return config.Config{}, err
	}
	return candidate, nil
}

func (c *Controller) authorizeExecution(ctx context.Context, dispatchID, operation string) error {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}

	if !state.HasGraph {
		return fmt.Errorf("taxion: execution configuration requires a Task Graph")
	}
	if err := state.Graph.CheckHandoffStart(dispatchID); err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok {
		return fmt.Errorf("taxion: specification gate requires a saved Dispatch")
	}
	if err := state.Graph.CheckTaskSpecificationUse(dispatch.TaskID, "copilot"); err != nil {
		return err
	}
	candidate, err := c.checkExecutionConfiguration(state.Graph, dispatchID)
	if err != nil || candidate.Version == 0 {
		return err
	}
	// checkExecutionConfiguration requires this capability for selected Dispatches.
	worker, ok := c.runtime.(runtime.ConfigurationIdentity)
	if !ok {
		return fmt.Errorf("taxion: runtime does not support configuration authentication")
	}
	startedAt := time.Now().UTC()
	if err := worker.CheckAuthentication(ctx, candidate.Runtime.Account); err != nil {
		return err
	}
	if err := worker.CheckRepositoryAccess(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	completedAt := time.Now().UTC()
	// Recheck approval and save the observation together before SDK session work.
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if _, err := c.checkExecutionConfiguration(state.Graph, dispatchID); err != nil {
			return err
		}
		return state.Graph.RecordDispatchAccessCheck(dispatchID, operation, c.runtime.ConfigHash(), startedAt, completedAt)
	})
}

func (c *Controller) checkServiceAccess(ctx context.Context, dispatchID, sessionID string) error {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	candidate, err := c.checkExecutionConfiguration(state.Graph, dispatchID)
	if err != nil || candidate.Version == 0 {
		return err
	}
	worker, ok := c.runtime.(runtime.ConfigurationIdentity)
	if !ok {
		return fmt.Errorf("taxion: runtime does not support provider service access checks")
	}
	if err := worker.CheckServiceAccess(ctx, sessionID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err = c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if err := state.Graph.CheckHandoffStart(dispatchID); err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok {
		return fmt.Errorf("taxion: specification gate requires a saved Dispatch")
	}
	if err := state.Graph.CheckTaskSpecificationUse(dispatch.TaskID, "copilot"); err != nil {
		return err
	}
	_, err = c.checkExecutionConfiguration(state.Graph, dispatchID)
	return err
}
