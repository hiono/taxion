package copilot

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/hiono/taxion/internal/core"
)

// NewForStop binds only cancellation to a saved session. Its identity is not
// recomputed from prompts or permissions that may have changed since execution.
func NewForStop(dispatch core.GraphDispatch, cli, base string) (*Adapter, error) {
	hash, err := hex.DecodeString(dispatch.RuntimeConfigHash)
	if err != nil || len(hash) != 32 || dispatch.RuntimeSessionID == "" ||
		!slices.Contains([]core.DispatchStatus{
			core.DispatchIssued, core.DispatchAcknowledged, core.DispatchRunning,
			core.DispatchCancelRequested, core.DispatchSuspended,
		}, dispatch.Status) {
		return nil, fmt.Errorf("taxion: stop-only adapter requires a saved execution session and configuration hash")
	}
	return newForSavedSession(dispatch, cli, base)
}

// NewForFailedReview permits only nonexecuting observation of the saved session.
// The ordinary cancellation constructor deliberately still rejects FAILED.
func NewForFailedReview(dispatch core.GraphDispatch, cli, base string) (*Adapter, error) {
	hash, err := hex.DecodeString(dispatch.RuntimeConfigHash)
	if err != nil || len(hash) != 32 || dispatch.RuntimeSessionID == "" ||
		dispatch.Status != core.DispatchFailed || dispatch.Recipient != "REVIEWER" {
		return nil, fmt.Errorf("taxion: failed review observation requires a failed Reviewer and saved session identity")
	}
	return newForSavedSession(dispatch, cli, base)
}

func newForSavedSession(dispatch core.GraphDispatch, cli, base string) (*Adapter, error) {
	config := Config{
		Role: dispatch.Recipient, WorkingDirectory: dispatch.Instruction.Worktree,
		SystemPrompt: "Cancel the saved session only. No execution is authorized.",
		CLIPath:      cli, BaseDirectory: base,
		ResponseSchema: map[string]any{"$id": "urn:taxion:dispatch-result:bound:v1"},
	}
	if dispatch.Configuration != nil {
		config.Configuration = dispatch.Configuration.Selection
	}
	// A crash can occur after releasing the managed worktree but before saving
	// the result. Only the nonexecuting client may start from its source then.
	releasedWorktree := false
	if _, err := os.Lstat(config.WorkingDirectory); errors.Is(err, os.ErrNotExist) &&
		dispatch.Recipient == "REVIEWER" && dispatch.ReviewWorktreeSource != "" {
		config.WorkingDirectory = dispatch.ReviewWorktreeSource
		releasedWorktree = true
	}
	adapter, err := New(config)
	if err != nil {
		return nil, err
	}
	adapter.stopSessionID, adapter.stopConfigHash = dispatch.RuntimeSessionID, dispatch.RuntimeConfigHash
	if releasedWorktree {
		adapter.config.WorkingDirectory = dispatch.Instruction.Worktree
	}
	return adapter, nil
}
