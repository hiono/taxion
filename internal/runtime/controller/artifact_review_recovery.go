package controller

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

func verifyValidatedArtifactContract(prior core.ReviewFlow, next core.GraphTask) error {
	if prior.Artifact == nil || prior.Artifact.Validation.Status != "PASSED" ||
		next.Supersedes != prior.TaskID || next.CreatedFrom != "flow:"+prior.ID ||
		next.TargetSHA != prior.Artifact.Target.CommitSHA ||
		next.TargetTreeSHA != prior.Artifact.Target.TreeSHA ||
		!slices.Equal(next.TargetChangedFiles, prior.Artifact.Target.ChangedFiles) {
		return fmt.Errorf("taxion: validated artifact successor must retain its exact candidate")
	}
	old := prior.Contract
	next.ID, next.Status, next.AuthorizationID, next.Decisions = old.ID, old.Status, old.AuthorizationID, old.Decisions
	next.Supersedes, next.CreatedFrom = old.Supersedes, old.CreatedFrom
	next.NextAction, next.DeferredReason, next.ResumeCondition = old.NextAction, old.DeferredReason, old.ResumeCondition
	next.TargetSHA, next.TargetTreeSHA, next.TargetChangedFiles = old.TargetSHA, old.TargetTreeSHA, old.TargetChangedFiles
	if !reflect.DeepEqual(old, next) {
		return fmt.Errorf("taxion: validation reuse requires the unchanged contract, source worktree and validation commands")
	}
	return nil
}

func validatedArtifactSource(graph core.TaskGraph, task core.GraphTask, request ReviewFlowRequest) (core.ReviewFlow, error) {
	prior, ok := graph.ReviewFlow(strings.TrimPrefix(task.CreatedFrom, "flow:"))
	if !ok || task.CreatedFrom != "flow:"+prior.ID || prior.Status != "STOPPED" ||
		prior.StopCode != "SAFETY_STOP" || prior.RunnerID != "" || !prior.HandoffHeld() ||
		len(prior.DispatchIDs) != 1 {
		return core.ReviewFlow{}, fmt.Errorf("taxion: validation reuse requires its held stopped artifact predecessor")
	}
	dispatch, ok := graph.GraphDispatch(prior.LastDispatchID())
	if !ok || dispatch.Recipient != "REVIEWER" || dispatch.Status != core.DispatchCancelled ||
		dispatch.ExecutionStarted || dispatch.RuntimeSessionID != "" {
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
