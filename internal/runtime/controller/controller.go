package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// Controller binds one role-scoped runtime to durable Task Graph Dispatches.
type Controller struct {
	store   *store.Store
	runtime runtime.Identity
	mu      sync.Mutex
}

func New(db *store.Store, worker runtime.Identity) (*Controller, error) {
	if db == nil || worker == nil {
		return nil, fmt.Errorf("taxion: controller requires a store and runtime")
	}
	if strings.TrimSpace(worker.Role()) == "" || strings.TrimSpace(worker.ConfigHash()) == "" {
		return nil, fmt.Errorf("taxion: runtime role and configuration hash must not be empty")
	}
	return &Controller{store: db, runtime: worker}, nil
}

// Start creates and durably binds an SDK session, or resumes the existing
// binding. The session never crosses Dispatch, task, role, or config boundaries.
func (c *Controller) Start(ctx context.Context, dispatchID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dispatch, err := c.dispatch(dispatchID)
	if err != nil {
		return "", err
	}
	if dispatch.Recipient != c.runtime.Role() {
		return "", fmt.Errorf("taxion: Dispatch role %s does not match runtime role %s", dispatch.Recipient, c.runtime.Role())
	}
	if dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return "", fmt.Errorf("taxion: Dispatch status %s does not authorize runtime resume", dispatch.Status)
	}
	if !sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
		return "", fmt.Errorf("taxion: runtime worktree does not match Dispatch worktree")
	}
	if dispatch.RuntimeSessionID != "" {
		if dispatch.RuntimeConfigHash != c.runtime.ConfigHash() {
			return "", fmt.Errorf("taxion: runtime config does not match Dispatch session binding")
		}
		if err := c.runtime.Resume(ctx, dispatch.RuntimeSessionID); err != nil {
			return "", err
		}
		return dispatch.RuntimeSessionID, nil
	}

	sessionID, err := c.runtime.Start(ctx, dispatch.TaskID)
	if err != nil {
		return "", err
	}
	err = c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Task Graph is not initialized")
		}
		current, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || !active(current.Status) ||
			current.TaskID != dispatch.TaskID || current.Recipient != dispatch.Recipient {
			return fmt.Errorf("taxion: Dispatch changed while starting runtime session")
		}
		if err := state.Graph.BindRuntimeSession(dispatchID, sessionID, c.runtime.ConfigHash()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if stopErr := c.runtime.Stop(ctx, sessionID); stopErr != nil {
			return "", errors.Join(err, fmt.Errorf("taxion: stop unbound runtime session: %w", stopErr))
		}
		return "", err
	}
	return sessionID, nil
}

func (c *Controller) Send(ctx context.Context, dispatchID, prompt string) error {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return err
	}
	if dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return fmt.Errorf("taxion: Dispatch status %s does not authorize sending instructions", dispatch.Status)
	}
	if err := c.runtime.Resume(ctx, dispatch.RuntimeSessionID); err != nil {
		return err
	}
	if err := c.markRunning(dispatchID); err != nil {
		return err
	}
	if err := c.runtime.Send(ctx, dispatch.RuntimeSessionID, prompt); err != nil {
		if failErr := c.Fail(dispatchID, err.Error()); failErr != nil {
			return errors.Join(err, fmt.Errorf("taxion: record runtime failure: %w", failErr))
		}
		return err
	}
	return nil
}

func (c *Controller) Resume(ctx context.Context, dispatchID string) error {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return err
	}
	if dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return fmt.Errorf("taxion: Dispatch status %s does not authorize runtime resume", dispatch.Status)
	}
	return c.runtime.Resume(ctx, dispatch.RuntimeSessionID)
}

func (c *Controller) Stop(ctx context.Context, dispatchID string) error {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return err
	}
	if dispatch.Status == core.DispatchRunning {
		if err := c.transition(dispatchID, core.DispatchCancelRequested, "cancel requested", nil, false, false); err != nil {
			return err
		}
	}
	if err := c.runtime.Stop(ctx, dispatch.RuntimeSessionID); err != nil {
		return err
	}
	result := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
		Repository: dispatch.Repository, Status: core.DispatchCancelled,
		StartedSHA: dispatch.Instruction.BaselineSHA,
		Evidence:   []string{"runtime-stop:" + dispatch.RuntimeSessionID}, NextTransition: core.Blocked,
	}
	return c.transition(dispatchID, core.DispatchCancelled, "runtime stopped", result, false, false)
}

func (c *Controller) Fail(dispatchID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("taxion: runtime failure reason must not be empty")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !state.HasGraph || !ok ||
		dispatch.RuntimeSessionID == "" || dispatch.Recipient != c.runtime.Role() ||
		dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
		!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) ||
		dispatch.Status != core.DispatchRunning && dispatch.Status != core.DispatchCancelRequested {
		return fmt.Errorf("taxion: runtime failure requires a running Dispatch")
	}
	result := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
		Repository: dispatch.Repository, Status: core.DispatchFailed,
		StartedSHA: dispatch.Instruction.BaselineSHA, Blockers: []string{reason},
		NextTransition: core.Blocked,
	}
	return c.transition(dispatchID, core.DispatchFailed, "runtime failed: "+reason, result, false, false)
}

func (c *Controller) Events(ctx context.Context, dispatchID string) (<-chan runtime.Event, error) {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return nil, err
	}
	return c.runtime.Events(ctx, dispatch.RuntimeSessionID)
}

func (c *Controller) RecordUsage(dispatchID string, usage core.DispatchUsage) error {
	if !nonnegativeUsage(usage) {
		return fmt.Errorf("taxion: invalid runtime usage record")
	}
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Task Graph is not initialized")
		}
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.Status != core.DispatchRunning || dispatch.RuntimeSessionID == "" ||
			dispatch.Recipient != c.runtime.Role() || dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("taxion: runtime usage does not match an active bound Dispatch")
		}
		for i := range state.Graph.Dispatches {
			if state.Graph.Dispatches[i].ID == dispatchID {
				state.Graph.Dispatches[i].RuntimeUsage = append(state.Graph.Dispatches[i].RuntimeUsage, usage)
				return nil
			}
		}
		return fmt.Errorf("taxion: Dispatch %q not found", dispatchID)
	})
}

func (c *Controller) boundDispatch(dispatchID string) (core.GraphDispatch, error) {
	dispatch, err := c.dispatch(dispatchID)
	if err != nil {
		return core.GraphDispatch{}, err
	}
	if dispatch.Recipient != c.runtime.Role() {
		return core.GraphDispatch{}, fmt.Errorf("taxion: Dispatch role %s does not match runtime role %s", dispatch.Recipient, c.runtime.Role())
	}
	if !sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
		return core.GraphDispatch{}, fmt.Errorf("taxion: runtime worktree does not match Dispatch worktree")
	}
	if dispatch.RuntimeSessionID == "" {
		return core.GraphDispatch{}, fmt.Errorf("taxion: Dispatch has no runtime session binding")
	}
	if dispatch.RuntimeConfigHash != c.runtime.ConfigHash() {
		return core.GraphDispatch{}, fmt.Errorf("taxion: runtime config does not match Dispatch session binding")
	}
	return dispatch, nil
}

func (c *Controller) dispatch(id string) (core.GraphDispatch, error) {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return core.GraphDispatch{}, err
	}
	if !state.HasGraph {
		return core.GraphDispatch{}, fmt.Errorf("taxion: Task Graph is not initialized")
	}
	dispatch, ok := state.Graph.GraphDispatch(id)
	if !ok || !active(dispatch.Status) {
		return core.GraphDispatch{}, fmt.Errorf("taxion: active Dispatch %q not found", id)
	}
	return dispatch, nil
}

func (c *Controller) markRunning(dispatchID string) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !state.HasGraph || !ok || !active(dispatch.Status) || dispatch.RuntimeSessionID == "" ||
			dispatch.Recipient != c.runtime.Role() ||
			dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("taxion: runtime start does not match an active Dispatch")
		}
		switch dispatch.Status {
		case core.DispatchIssued:
			if err := state.Graph.TransitionDispatch(dispatchID, core.DispatchAcknowledged,
				"runtime", "session accepted instruction", true, false, nil); err != nil {
				return err
			}
			fallthrough
		case core.DispatchAcknowledged:
			return state.Graph.TransitionDispatch(dispatchID, core.DispatchRunning,
				"runtime", "session started execution", true, true, nil)
		case core.DispatchRunning:
			return nil
		default:
			return fmt.Errorf("taxion: Dispatch status %s does not authorize execution", dispatch.Status)
		}
	})
}

func (c *Controller) transition(dispatchID string, status core.DispatchStatus, reason string,
	result *core.DispatchResult, acknowledged, started bool) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Task Graph is not initialized")
		}
		if err := state.Graph.TransitionDispatch(dispatchID, status, "runtime", reason, acknowledged, started, result); err != nil {
			return err
		}
		active := status == core.DispatchIssued || status == core.DispatchAcknowledged ||
			status == core.DispatchRunning || status == core.DispatchCancelRequested ||
			status == core.DispatchSuspended
		for i := range state.Ledger.Dispatches {
			if state.Ledger.Dispatches[i].ID == dispatchID {
				state.Ledger.Dispatches[i].Active = active
				return nil
			}
		}
		return fmt.Errorf("taxion: Dispatch %q has no ledger projection", dispatchID)
	})
}

func active(status core.DispatchStatus) bool {
	switch status {
	case core.DispatchIssued, core.DispatchAcknowledged, core.DispatchRunning,
		core.DispatchCancelRequested, core.DispatchSuspended:
		return true
	default:
		return false
	}
}

func sameWorktree(expected, actual string) bool {
	expectedAbs, expectedErr := filepath.Abs(expected)
	actualAbs, actualErr := filepath.Abs(actual)
	return expectedErr == nil && actualErr == nil && filepath.Clean(expectedAbs) == filepath.Clean(actualAbs)
}

func nonnegativeUsage(usage core.DispatchUsage) bool {
	for _, value := range []*int64{
		usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens,
	} {
		if value != nil && *value < 0 {
			return false
		}
	}
	return strings.TrimSpace(usage.Model) != ""
}
