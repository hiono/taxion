package controller

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
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
			if err := validateResultReceipt(state, dispatch); err != nil {
				return err
			}
		}
		last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		if last.Status == core.DispatchSucceeded {
			if err := revalidateFlowResult(state.Graph, flow, state.Records); err != nil {
				return fmt.Errorf("taxion: recovery result revalidation: %w", err)
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

func revalidateFlowResult(graph core.TaskGraph, flow core.ReviewFlow, records ...store.Records) error {
	last, ok := graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok || last.Status != core.DispatchSucceeded || last.Result == nil {
		return fmt.Errorf("flow checkpoint requires a successful saved result")
	}
	if len(records) != 0 {
		last = records[0].RestoredDispatch(last)
	}
	if last.Recipient == "CODER" {
		return revalidateCoderResult(last)
	}
	if err := gitproof.ReverifyReleasedReview(last, last.Result); err != nil {
		return err
	}
	coderID := flow.SourceDispatchID
	if len(flow.DispatchIDs) > 1 {
		coderID = flow.DispatchIDs[len(flow.DispatchIDs)-2]
	}
	if coderID == "" {
		return fmt.Errorf("review requires its Coder source")
	}
	coder, ok := graph.GraphDispatch(coderID)
	if !ok || coder.Recipient != "CODER" || coder.Result == nil ||
		coder.Result.OutputSHA != last.Result.OutputSHA ||
		coder.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot &&
			coder.Result.OutputTreeSHA != last.Result.OutputTreeSHA {
		return fmt.Errorf("review and Coder output identity differs")
	}
	if len(records) != 0 {
		coder = records[0].RestoredDispatch(coder)
	}
	return revalidateCoderResult(coder)
}

func revalidateCoderResult(dispatch core.GraphDispatch) error {
	if err := dispatch.ValidateResult(dispatch.Status, dispatch.Result); err != nil {
		return err
	}
	if dispatch.Result == nil || dispatch.Result.GitEvidence == nil {
		return fmt.Errorf("saved Coder result requires controller-observed Git evidence")
	}
	result := *dispatch.Result
	result.Validations = slices.Clone(result.Validations)
	if err := gitproof.Verify(dispatch, &result); err != nil {
		return err
	}
	result.GitEvidence.VerifiedAt = dispatch.Result.GitEvidence.VerifiedAt
	if !reflect.DeepEqual(result.GitEvidence, dispatch.Result.GitEvidence) {
		return fmt.Errorf("current Git facts differ from saved Coder evidence")
	}
	return nil
}
