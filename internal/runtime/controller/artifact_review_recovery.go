package controller

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

func verifyValidatedArtifactContract(prior core.ReviewFlow, next core.GraphTask) error {
	if !prior.InputReadyForReview() ||
		next.Supersedes != prior.TaskID || next.CreatedFrom != "flow:"+prior.ID ||
		next.TargetSHA != prior.Artifact.Target.CommitSHA ||
		next.TargetTreeSHA != prior.Artifact.Target.TreeSHA ||
		!slices.Equal(next.TargetChangedFiles, prior.Artifact.Target.ChangedFiles) {
		return fmt.Errorf("taxion: validated artifact successor must retain its exact candidate")
	}
	old := prior.Contract
	oldInput, err := core.ReviewInputFromDecisions(old.Decisions)
	if err != nil {
		return err
	}
	nextInput, err := core.ReviewInputFromDecisions(next.Decisions)
	if err != nil || !reflect.DeepEqual(oldInput, nextInput) {
		return fmt.Errorf("taxion: validation reuse cannot change or discard supplied review purpose and diagnostics")
	}
	next.ID, next.Status, next.AuthorizationID, next.Decisions = old.ID, old.Status, old.AuthorizationID, old.Decisions
	next.Supersedes, next.CreatedFrom = old.Supersedes, old.CreatedFrom
	next.NextAction, next.DeferredReason, next.ResumeCondition = old.NextAction, old.DeferredReason, old.ResumeCondition
	next.TargetSHA, next.TargetTreeSHA, next.TargetChangedFiles = old.TargetSHA, old.TargetTreeSHA, old.TargetChangedFiles
	// JobID is the new collaboration ownership boundary, not part of the
	// predecessor's artifact-validation contract. The successor receives its
	// own Job approval; this comparison only proves the saved validation inputs
	// and candidate are unchanged.
	next.JobID = old.JobID
	if !reflect.DeepEqual(old, next) {
		return fmt.Errorf("taxion: validation reuse requires the unchanged contract, source worktree and validation commands")
	}
	return nil
}

func validatedArtifactSource(graph core.TaskGraph, task core.GraphTask, request ReviewFlowRequest) (core.ReviewFlow, error) {
	prior, ok := graph.ReviewFlow(strings.TrimPrefix(task.CreatedFrom, "flow:"))
	if !ok || task.CreatedFrom != "flow:"+prior.ID || prior.Status != "STOPPED" ||
		!slices.Contains([]string{"SAFETY_STOP", "BUDGET_STOP", "BUDGET_LIMIT"}, prior.StopCode) || prior.RunnerID != "" || !prior.HandoffHeld() ||
		len(prior.DispatchIDs) != 1 {
		return core.ReviewFlow{}, fmt.Errorf("taxion: validation reuse requires its held stopped artifact predecessor")
	}
	dispatch, ok := graph.GraphDispatch(prior.LastDispatchID())
	if !ok || dispatch.Recipient != "REVIEWER" || dispatch.Status != core.DispatchCancelled ||
		(dispatch.ExecutionStarted || dispatch.RuntimeSessionID != "") && !prior.StoppedExecutedArtifactReviewer(dispatch) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: validation reuse requires a reconciled unstarted Reviewer")
	}
	if err := verifyValidatedArtifactContract(prior, task); err != nil {
		return core.ReviewFlow{}, err
	}
	if request.ArtifactSource != prior.Artifact.Target.Source ||
		!reflect.DeepEqual(request.Configuration, prior.Artifact.Configuration) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: validation reuse cannot change artifact provenance or effective configuration")
	}
	return prior, nil
}
