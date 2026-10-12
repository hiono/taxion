package controller

import (
	"context"
	"fmt"
	"slices"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/specsearch"
	"github.com/hiono/taxion/internal/store"
)

// SearchSpecifications is the Orchestrator's local acquisition path. A search
// supplies quoted inputs; the Task's normal approval and dispatch rules apply.
func SearchSpecifications(ctx context.Context, db *store.Store, taskID string, scope specsearch.Scope, query string) (specsearch.Result, error) {
	check := func(state *store.Workspace) error {
		task, ok := state.Graph.GraphTask(taskID)
		if !state.HasGraph || !ok ||
			!slices.Contains([]core.TaskStatus{core.Draft, core.Blocked, core.HumanDecisionRequired}, task.Status) {
			return fmt.Errorf("taxion: local search attaches inputs to a DRAFT, BLOCKED or HUMAN_DECISION_REQUIRED Task")
		}
		return nil
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return specsearch.Result{}, err
	}
	if err := check(&state); err != nil {
		return specsearch.Result{}, err
	}
	result, err := specsearch.Search(ctx, scope, query)
	if err != nil {
		return specsearch.Result{}, err
	}
	if len(result.Specifications) == 0 {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return specsearch.Result{}, err
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := check(state); err != nil {
			return err
		}
		task, _ := state.Graph.GraphTask(taskID)
		for _, found := range result.Specifications {
			replaced := false
			for i, current := range task.Specifications {
				if current.ID == found.ID {
					task.Specifications[i] = found
					replaced = true
					break
				}
			}
			if !replaced {
				task.Specifications = append(task.Specifications, found)
			}
		}
		return state.Graph.Track(task, "ORCHESTRATOR", "approved local search scope:"+result.ScopeHash)
	})
	return result, err
}
