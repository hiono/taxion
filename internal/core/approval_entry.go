package core

import (
	"fmt"
	"slices"
)

// ApprovalEntryStage names the execution-entry point that consumes an
// approval bundle. The same checker guards every point.
type ApprovalEntryStage string

const (
	EntryStartReviewFlow  ApprovalEntryStage = "start-review-flow"
	EntryCoderDispatch    ApprovalEntryStage = "coder-dispatch"
	EntryReviewerDispatch ApprovalEntryStage = "reviewer-dispatch"
	EntryRepairDispatch   ApprovalEntryStage = "repair-dispatch"
	EntryReReviewDispatch ApprovalEntryStage = "re-review-dispatch"
)

// ApprovalEntryStages lists every guarded entry point.
func ApprovalEntryStages() []ApprovalEntryStage {
	return []ApprovalEntryStage{EntryStartReviewFlow, EntryCoderDispatch, EntryReviewerDispatch, EntryRepairDispatch, EntryReReviewDispatch}
}

// EntryCheckError reports a failed entry check without exposing any contract
// value: only dimensions and field paths are named.
type EntryCheckError struct {
	Stage      ApprovalEntryStage `json:"stage"`
	Code       string             `json:"code"`
	BundleID   string             `json:"bundleId,omitempty"`
	BundleHash string             `json:"bundleHash,omitempty"`
	Drifts     []ApprovalDrift    `json:"drifts,omitempty"`
	authorizationImpact
}

type authorizationImpact = string

func (e *EntryCheckError) Error() string {
	if len(e.Drifts) == 0 {
		return fmt.Sprintf("taxion: approval bundle missing for %s (%s)", e.Stage, e.Code)
	}
	return fmt.Sprintf("taxion: approval bundle %s does not authorize %s (%s); re-approval required", e.BundleID, e.Stage, e.Code)
}

// CheckBundleAtEntry verifies that a persisted approval bundle authorizes the
// given execution entry point for the current effective contract and
// configuration. It never mutates the graph and never echoes contract values.
func (g TaskGraph) CheckBundleAtEntry(taskID string, stage ApprovalEntryStage, current ApprovalConfiguration) (ApprovalBundle, error) {
	if !slices.Contains(ApprovalEntryStages(), stage) {
		return ApprovalBundle{}, &EntryCheckError{Stage: stage, Code: "unknown-stage"}
	}
	task, exists := g.GraphTask(taskID)
	if !exists {
		return ApprovalBundle{}, &EntryCheckError{Stage: stage, Code: "unknown-task"}
	}
	if task.Status != Ready {
		return ApprovalBundle{}, &EntryCheckError{Stage: stage, Code: "task-not-ready"}
	}
	var newest *ApprovalBundle
	for index := len(g.Approvals) - 1; index >= 0; index-- {
		bundle := g.Approvals[index]
		if bundle.TaskID != taskID {
			continue
		}
		if newest == nil {
			candidate := bundle
			newest = &candidate
		}
		if bundle.hashValid() != nil {
			continue
		}
		if err := bundle.Verify(task, current); err != nil {
			continue
		}
		return bundle, nil
	}
	failure := &EntryCheckError{Stage: stage, Code: "no-matching-bundle"}
	if newest != nil {
		failure.BundleID, failure.BundleHash = newest.ID, newest.BundleHash
		if drifts, err := newest.Drifts(task, current); err == nil {
			failure.Drifts = drifts
		}
		for _, drift := range failure.Drifts {
			if drift.AuthorizationImpact == ImpactInvalid {
				failure.Code = "invalid-bundle"
				break
			}
		}
	}
	return ApprovalBundle{}, failure
}
