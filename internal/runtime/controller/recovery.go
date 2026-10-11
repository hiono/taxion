package controller

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type RecoveryRequest struct {
	FlowID                 string   `json:"flowId"`
	ExpectedRunnerID       string   `json:"expectedRunnerId"`
	PreviousProcessStopped bool     `json:"previousProcessStopped"`
	Actor                  string   `json:"actor"`
	Reason                 string   `json:"reason"`
	Evidence               []string `json:"evidence"`
}

// RecoverReviewFlow releases only the observed owner after all its Dispatches
// have ended. A stale request cannot clear a newer runner's claim.
func RecoverReviewFlow(db *store.Store, request RecoveryRequest) (core.ReviewFlow, error) {
	if db == nil || strings.TrimSpace(request.FlowID) == "" ||
		strings.TrimSpace(request.ExpectedRunnerID) == "" || !request.PreviousProcessStopped ||
		strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" ||
		len(request.Evidence) == 0 {
		return core.ReviewFlow{}, fmt.Errorf("taxion: recovery requires the observed owner, process-stop confirmation, actor, reason and evidence")
	}
	seen := map[string]bool{}
	for _, reference := range request.Evidence {
		if strings.TrimSpace(reference) == "" || seen[reference] {
			return core.ReviewFlow{}, fmt.Errorf("taxion: recovery evidence must contain unique nonempty references")
		}
		seen[reference] = true
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var ok bool
		flow, ok = state.Graph.ReviewFlow(request.FlowID)
		if !state.HasGraph || !ok || flow.RunnerID != request.ExpectedRunnerID {
			return fmt.Errorf("taxion: recovery owner differs from the saved runner claim")
		}
		for _, id := range flow.DispatchIDs {
			dispatch, ok := state.Graph.GraphDispatch(id)
			if !ok || active(dispatch.Status) {
				return fmt.Errorf("taxion: reconcile active Dispatch %s through runtime stop or recorded result before recovering ownership", id)
			}
		}
		flow.Recoveries = append(flow.Recoveries, core.FlowRecovery{
			RunnerID: flow.RunnerID, Actor: request.Actor, Reason: request.Reason,
			Evidence: slices.Clone(request.Evidence), At: time.Now().UTC(),
		})
		flow.RunnerID = ""
		storeFlow(state, flow)
		return nil
	})
	return flow, err
}
