package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitguard"
	"github.com/hiono/taxion/internal/store"
)

// Commit requires the same bound runtime as other Dispatch operations and holds
// the workspace transaction through the Git authorization and branch update.
func (c *Controller) Commit(ctx context.Context, dispatchID, message, scanner string) (string, error) {
	return c.commit(ctx, dispatchID, "", message, scanner)
}

// CommitForSession also checks the session that issued a runtime tool call.
func (c *Controller) CommitForSession(ctx context.Context, dispatchID, sessionID, message, scanner string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("managed commit tool requires an invoking session")
	}
	return c.commit(ctx, dispatchID, sessionID, message, scanner)
}

func (c *Controller) commit(ctx context.Context, dispatchID, sessionID, message, scanner string) (string, error) {
	return c.managedGit(ctx, dispatchID, sessionID, "Git commit", func(ctx context.Context, dispatch core.GraphDispatch) (string, error) {
		return gitguard.Commit(ctx, dispatch, message, scanner)
	})
}

// StageForSession shares the same authorization transaction as managed commit.
func (c *Controller) StageForSession(ctx context.Context, dispatchID, sessionID string, paths []string, scanner string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("managed stage tool requires an invoking session")
	}
	return c.managedGit(ctx, dispatchID, sessionID, "Git index tree", func(ctx context.Context, dispatch core.GraphDispatch) (string, error) {
		return gitguard.Stage(ctx, dispatch, paths, scanner)
	})
}

func (c *Controller) managedGit(ctx context.Context, dispatchID, sessionID, publication string, operation func(context.Context, core.GraphDispatch) (string, error)) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var sha string
	err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("managed commit requires a Task Graph")
		}
		if err := state.Graph.Validate(); err != nil {
			return err
		}
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.Status != core.DispatchRunning || dispatch.Recipient != "CODER" ||
			c.runtime.Role() != "CODER" || dispatch.RuntimeSessionID == "" ||
			dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("managed commit requires the running Coder's bound Dispatch")
		}
		if sessionID != "" && sessionID != dispatch.RuntimeSessionID {
			return fmt.Errorf("managed commit tool session does not match the bound Dispatch")
		}
		if _, err := c.checkExecutionConfiguration(state.Graph, dispatchID); err != nil {
			return err
		}
		task, ok := state.Graph.GraphTask(dispatch.TaskID)
		if !ok || task.Status != core.Active {
			return fmt.Errorf("managed commit requires an active Task without a pending stop or user decision")
		}
		for _, flow := range state.Graph.ReviewFlows {
			if !slices.Contains(flow.DispatchIDs, dispatchID) {
				continue
			}
			if flow.Status != "RUNNING" || !time.Now().Before(flow.Deadline) {
				return fmt.Errorf("managed commit requires an active review flow before its deadline")
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, flow.Deadline)
			defer cancel()
		}
		var err error
		sha, err = operation(ctx, dispatch)
		return err
	})
	if err != nil {
		if sha != "" {
			return sha, fmt.Errorf("%s %s was published but workspace transaction failed: %w", publication, sha, err)
		}
		return "", err
	}
	return sha, nil
}
