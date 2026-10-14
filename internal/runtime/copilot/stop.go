package copilot

import (
	"encoding/hex"
	"fmt"
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
	config := Config{
		Role: dispatch.Recipient, WorkingDirectory: dispatch.Instruction.Worktree,
		SystemPrompt: "Cancel the saved session only. No execution is authorized.",
		CLIPath:      cli, BaseDirectory: base,
	}
	if dispatch.Configuration != nil {
		config.Configuration = dispatch.Configuration.Selection
	}
	adapter, err := New(config)
	if err != nil {
		return nil, err
	}
	adapter.stopSessionID, adapter.stopConfigHash = dispatch.RuntimeSessionID, dispatch.RuntimeConfigHash
	return adapter, nil
}
