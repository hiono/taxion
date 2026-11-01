package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type RecoveryRequest struct {
	FlowID                 string    `json:"flowId"`
	ExpectedRunnerID       string    `json:"expectedRunnerId"`
	PreviousProcessStopped bool      `json:"previousProcessStopped"`
	Actor                  string    `json:"actor"`
	Reason                 string    `json:"reason"`
	Evidence               []string  `json:"evidence"`
	FinalizeSavedResult    bool      `json:"finalizeSavedResult,omitempty"`
	ValidationDeadline     time.Time `json:"validationDeadline,omitempty"`
}

// RecoverReviewFlow releases only the observed owner after all its Dispatches
// have ended. A stale request cannot clear a newer runner's claim.
func RecoverReviewFlow(db *store.Store, request RecoveryRequest) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: recovery requires a workspace")
	}
	if err := validateRecoveryRequest(request); err != nil {
		return core.ReviewFlow{}, err
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
		last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
		if flow.Artifact != nil && flow.Artifact.Validation.Status == "RUNNING" {
			if err := holdReviewFlowForStop(&state.Graph, &flow, "input validation execution outcome is unknown; do not replay commands"); err != nil {
				return err
			}
			flow.Status, flow.StopCode, flow.StopCause, flow.Reason = "STOPPED", "SAFETY_STOP", "other",
				"input validation execution outcome is unknown; preserve its receipt and use a newly approved successor"
		}
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

func validateRecoveryRequest(request RecoveryRequest) error {
	if strings.TrimSpace(request.FlowID) == "" ||
		strings.TrimSpace(request.ExpectedRunnerID) == "" || !request.PreviousProcessStopped ||
		strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Reason) == "" ||
		len(request.Evidence) == 0 {
		return fmt.Errorf("taxion: recovery requires the observed owner, process-stop confirmation, actor, reason and evidence")
	}
	seen := map[string]bool{}
	for _, reference := range request.Evidence {
		if strings.TrimSpace(reference) == "" || seen[reference] {
			return fmt.Errorf("taxion: recovery evidence must contain unique nonempty references")
		}
		seen[reference] = true
	}
	return nil
}

// RecoverSavedReview observes an ended provider turn and rejoins the same result
// finalizer as live delivery. It neither resends work nor relaxes Git validation.
func (c *Controller) RecoverSavedReview(ctx context.Context, request RecoveryRequest) (core.ReviewFlow, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateRecoveryRequest(request); err != nil {
		return core.ReviewFlow{}, err
	}
	if !request.FinalizeSavedResult || !request.ValidationDeadline.After(time.Now()) ||
		request.ValidationDeadline.After(time.Now().Add(time.Hour)) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: saved finalization requires stopped-owner evidence and an explicitly authorized host-validation deadline within one hour")
	}
	ctx, cancel := context.WithDeadline(ctx, request.ValidationDeadline)
	defer cancel()
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return core.ReviewFlow{}, err
	}
	flow, found := state.Graph.ReviewFlow(request.FlowID)
	dispatch, err := c.boundDispatch(flow.LastDispatchID())
	if err != nil || !found || flow.Status != "RUNNING" || flow.RunnerID != request.ExpectedRunnerID ||
		dispatch.Status != core.DispatchRunning || dispatch.Recipient != "REVIEWER" || dispatch.Result != nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: saved finalization requires the exact claimed running Reviewer without a formal result")
	}
	if err := checkDispatchOperation(&state, ctx, dispatch); err != nil {
		return core.ReviewFlow{}, err
	}
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if !flowContractMatches(state.Graph, flow, task) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: saved finalization requires the unchanged Flow contract")
	}
	if dispatch.Configuration != nil {
		identity, ok := c.runtime.(runtime.ConfigurationIdentity)
		if !ok {
			return core.ReviewFlow{}, fmt.Errorf("taxion: saved finalization requires its bound configuration")
		}
		var selection config.Config
		if err := core.DecodeGraphInput(strings.NewReader(string(identity.ExecutionConfiguration())), &selection); err != nil {
			return core.ReviewFlow{}, err
		}
		if _, err := checkDispatchConfiguration(state.Graph, dispatch.ID, selection); err != nil {
			return core.ReviewFlow{}, err
		}
	}
	if err := verifyStagePlanBinding(state.Records, flow); err != nil {
		return core.ReviewFlow{}, err
	}
	phase, err := savedReviewFinalization(state, dispatch)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if phase == nil {
		for _, audit := range state.Records.Budgets {
			if audit.FlowID != flow.ID || audit.Kind != "STAGE_OBSERVED" {
				continue
			}
			var report TaskBudgetReport
			if err := json.Unmarshal([]byte(audit.Payload), &report); err != nil {
				return core.ReviewFlow{}, err
			}
			if observation := report.StageObservation; observation != nil &&
				observation.DispatchID == dispatch.ID && observation.Phase == "validation-starting" {
				return core.ReviewFlow{}, fmt.Errorf("taxion: host validation started without a durable validated result; preserve its unknown outcome instead of replaying commands")
			}
		}
	}
	if phase != nil && phase.RuntimeClosed {
		if _, err := c.finishReviewedResult(ctx, dispatch, phase.Result, true); err != nil {
			return core.ReviewFlow{}, err
		}
		request.FinalizeSavedResult = false
		return RecoverReviewFlow(c.store, request)
	}
	reader, ok := c.runtime.(interface {
		ReadSavedTurn(context.Context, string) (runtime.SavedTurn, error)
	})
	if !ok {
		return core.ReviewFlow{}, fmt.Errorf("taxion: runtime cannot observe an ended saved turn without execution")
	}
	turn, err := reader.ReadSavedTurn(ctx, dispatch.RuntimeSessionID)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if turn.SessionID != dispatch.RuntimeSessionID || turn.HistoryHash == "" ||
		turn.SubmittedAt.Before(dispatch.CreatedAt) || !turn.SubmittedAt.Before(stageDeadline(flow, dispatch, false)) ||
		turn.FinishedAt.Before(turn.SubmittedAt) || !turn.FinishedAt.Before(stageDeadline(flow, dispatch, false)) ||
		turn.FinishedAt.After(time.Now()) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: saved result does not prove timely completion of the bound execution")
	}
	if err := c.adoptSavedTurn(dispatch, flow, request, turn); err != nil {
		return core.ReviewFlow{}, err
	}
	if phase != nil {
		_, err = c.finishReviewedResult(ctx, dispatch, phase.Result, false)
	} else {
		_, err = c.finishResult(ctx, dispatch, turn.Payload)
	}
	if err != nil {
		return core.ReviewFlow{}, err
	}
	request.FinalizeSavedResult = false
	return RecoverReviewFlow(c.store, request)
}

func revalidateFlowResult(graph core.TaskGraph, flow core.ReviewFlow, records ...store.Records) error {
	last, ok := graph.GraphDispatch(flow.LastDispatchID())
	if !ok || last.Status != core.DispatchSucceeded || last.Result == nil {
		return fmt.Errorf("flow checkpoint requires a successful saved result")
	}
	if len(records) != 0 {
		for _, audit := range records[0].ReviewAcceptances {
			if audit.FlowID != flow.ID {
				continue
			}
			if err := records[0].VerifyReviewAcceptances(graph); err != nil {
				return err
			}
			coder, _ := graph.GraphDispatch(flow.SourceDispatchID)
			for _, dispatch := range []core.GraphDispatch{coder, last} {
				state := store.Workspace{Graph: graph, Records: records[0]}
				if err := validateResultReceipt(&state, dispatch); err != nil {
					return err
				}
			}
			task, _ := graph.GraphTask(flow.TaskID)
			if records[0].Restore != nil {
				for _, path := range records[0].Restore.Paths {
					if path.Original == task.Worktree {
						task.Worktree = path.Local
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), DefaultLaneTimeout)
			defer cancel()
			return gitproof.VerifySavedSnapshotAcceptance(ctx, records[0].RestoredDispatch(coder),
				records[0].RestoredDispatch(last), task)
		}
		last = records[0].RestoredDispatch(last)
	}
	if last.Recipient == "CODER" {
		return revalidateCoderResult(last)
	}
	if err := gitproof.ReverifyReleasedReview(last, last.Result); err != nil {
		return err
	}
	if flow.Artifact != nil {
		if flow.Artifact.Validation.Status != "PASSED" || last.Recipient != "REVIEWER" ||
			last.Result.OutputTreeSHA != flow.Artifact.Target.TreeSHA {
			return fmt.Errorf("review differs from its validated input artifact")
		}
		if len(records) != 0 && records[0].Restore != nil {
			return fmt.Errorf("restored artifact acceptance requires explicit successor verification")
		}
		return gitproof.VerifyArtifact(flow.Contract, flow.Artifact.Target, flow.Artifact.Configuration.Selection)
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
