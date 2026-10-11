package core

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

var ErrFeedbackStale = errors.New("taxion: feedback target changed")

// FeedbackTarget identifies saved instruction and result records, not live
// token output or Git changes that have not yet been recorded.
type FeedbackTarget struct {
	DispatchID      string         `json:"dispatchId"`
	TaskID          string         `json:"taskId"`
	Repository      string         `json:"repository"`
	Recipient       string         `json:"recipient"`
	InstructionHash string         `json:"instructionHash"`
	Status          DispatchStatus `json:"status"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	TargetSHA       string         `json:"targetSha"`
	OutputSHA       string         `json:"outputSha"`
	ResultHash      string         `json:"resultHash"`
}

type FlowFeedback struct {
	ID               string              `json:"id"`
	ExpectedChangeID string              `json:"expectedChangeId"`
	Target           FeedbackTarget      `json:"target"`
	Actor            string              `json:"actor"`
	Source           string              `json:"source"`
	Reason           string              `json:"reason"`
	Text             string              `json:"text"`
	Evidence         []string            `json:"evidence"`
	HoldChangeID     string              `json:"holdChangeId,omitempty"`
	At               time.Time           `json:"at,omitempty"`
	Resolution       *FeedbackResolution `json:"resolution,omitempty"`
}

func ObserveFeedbackTarget(dispatch GraphDispatch) (FeedbackTarget, error) {
	if !nonempty(dispatch.ID) || !nonempty(dispatch.InstructionHash) || dispatch.UpdatedAt.IsZero() {
		return FeedbackTarget{}, graphError("feedback observation requires a saved Dispatch")
	}
	target := FeedbackTarget{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Repository: dispatch.Repository,
		Recipient: dispatch.Recipient, InstructionHash: dispatch.InstructionHash,
		Status: dispatch.Status, UpdatedAt: dispatch.UpdatedAt, TargetSHA: dispatch.Instruction.TargetSHA,
	}
	if dispatch.Result != nil {
		data, err := json.Marshal(dispatch.Result)
		if err != nil {
			return FeedbackTarget{}, fmt.Errorf("taxion: encode feedback result observation: %w", err)
		}
		target.ResultHash = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		target.OutputSHA = dispatch.Result.OutputSHA
	}
	return target, nil
}

func validFeedbackInput(feedback FlowFeedback) bool {
	for _, value := range []string{feedback.ID, feedback.Actor, feedback.Source, feedback.Reason} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return false
		}
	}
	return nonempty(feedback.Text) && validList(feedback.Evidence)
}

func cloneFlowFeedback(records []FlowFeedback) []FlowFeedback {
	copy := slices.Clone(records)
	for index := range copy {
		copy[index].Evidence = slices.Clone(copy[index].Evidence)
		if records[index].Resolution != nil {
			resolution := *records[index].Resolution
			copy[index].Resolution = &resolution
		}
	}
	return copy
}

func (g TaskGraph) validateFlowFeedback(flow ReviewFlow) error {
	ids := map[string]bool{}
	resolutionIDs := map[string]bool{}
	for _, feedback := range flow.Feedback {
		if !validFeedbackInput(feedback) || ids[feedback.ID] {
			return graphError("invalid feedback record in flow %s", flow.ID)
		}
		if err := g.validateSavedHandoffObservation(flow, feedback.Target, feedback.HoldChangeID, feedback.At); err != nil {
			return err
		}
		ids[feedback.ID] = true
		if resolution := feedback.Resolution; resolution != nil {
			if !validFeedbackResolution(*resolution) || resolution.At.Before(feedback.At) ||
				resolutionIDs[resolution.ID] {
				return graphError("invalid feedback resolution in flow %s", flow.ID)
			}
			if err := g.validateSavedHandoffObservation(flow, resolution.Target, resolution.ExpectedChangeID, resolution.At); err != nil {
				return err
			}
			resolutionIDs[resolution.ID] = true
		}
	}
	return nil
}

func (g TaskGraph) validateSavedHandoffObservation(flow ReviewFlow, target FeedbackTarget, holdID string, at time.Time) error {
	if at.IsZero() || target.UpdatedAt.IsZero() || target.UpdatedAt.After(at) ||
		!validDispatchStatus(target.Status) || !slices.Contains(flow.DispatchIDs, target.DispatchID) {
		return graphError("invalid saved handoff observation in flow %s", flow.ID)
	}
	dispatch, ok := g.dispatch(target.DispatchID)
	if !ok || target.TaskID != dispatch.TaskID || target.Repository != dispatch.Repository ||
		target.Recipient != dispatch.Recipient || target.InstructionHash != dispatch.InstructionHash ||
		target.TargetSHA != dispatch.Instruction.TargetSHA {
		return graphError("handoff observation lost its instruction target in flow %s", flow.ID)
	}
	if target.OutputSHA != "" && !validSHA(target.OutputSHA) {
		return graphError("invalid handoff observation output SHA in flow %s", flow.ID)
	}
	if target.ResultHash != "" {
		hash := strings.TrimPrefix(target.ResultHash, "sha256:")
		if !strings.HasPrefix(target.ResultHash, "sha256:") || len(hash) != 64 || !validSHA(hash) {
			return graphError("invalid handoff observation result hash in flow %s", flow.ID)
		}
	}
	if !dispatchActive(target.Status) {
		current, err := ObserveFeedbackTarget(dispatch)
		if err != nil || current != target {
			return graphError("handoff observation terminal result target changed in flow %s", flow.ID)
		}
	}
	if !slices.ContainsFunc(flow.HandoffChanges, func(change HandoffChange) bool {
		return change.ID == holdID && change.Action == "HOLD" && !change.At.After(at)
	}) {
		return graphError("handoff observation has no corresponding hold in flow %s", flow.ID)
	}
	return nil
}

// RecordFlowFeedback records a Human response and its hold atomically.
// It does not reinterpret that response as an instruction or an approval.
func (g *TaskGraph) RecordFlowFeedback(flowID string, feedback FlowFeedback) (ReviewFlow, error) {
	if !validFeedbackInput(feedback) || !feedback.At.IsZero() || feedback.HoldChangeID != "" || feedback.Resolution != nil {
		return ReviewFlow{}, graphError("feedback requires ID, actor, source, reason and text; time and hold ID are controller-owned")
	}
	if err := g.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	flow, ok := g.ReviewFlow(flowID)
	if !ok {
		return ReviewFlow{}, graphError("feedback requires an existing review flow")
	}
	for _, previous := range flow.Feedback {
		if previous.ID != feedback.ID {
			continue
		}
		previous.At, previous.HoldChangeID, previous.Resolution = time.Time{}, "", nil
		if !reflect.DeepEqual(previous, feedback) {
			return ReviewFlow{}, graphError("feedback ID was reused with different content")
		}
		return flow, nil
	}
	if flow.Status != "RUNNING" {
		return ReviewFlow{}, graphError("new feedback requires a running review flow")
	}
	last, _ := g.dispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	target, err := ObserveFeedbackTarget(last)
	if err != nil {
		return ReviewFlow{}, err
	}
	if target != feedback.Target {
		return ReviewFlow{}, fmt.Errorf("%w: observe the latest saved Dispatch and result; use handoff HOLD separately to stop the next handoff", ErrFeedbackStale)
	}
	priorID := ""
	if len(flow.HandoffChanges) != 0 {
		priorID = flow.HandoffChanges[len(flow.HandoffChanges)-1].ID
	}
	if feedback.ExpectedChangeID != priorID {
		return ReviewFlow{}, graphError("feedback handoff state changed; observe the latest change before retrying")
	}
	next := cloneGraph(*g)
	if !flow.HandoffHeld() {
		flow, err = next.ChangeHandoff(flowID, HandoffChange{
			ID: "feedback:" + feedback.ID, ExpectedChangeID: priorID, Action: "HOLD",
			Actor: feedback.Actor, Source: feedback.Source, Reason: feedback.Reason,
		})
		if err != nil {
			return ReviewFlow{}, err
		}
	}
	feedback.Evidence = slices.Clone(feedback.Evidence)
	feedback.HoldChangeID = flow.HandoffChanges[len(flow.HandoffChanges)-1].ID
	feedback.At = time.Now().UTC()
	flow.Feedback = append(flow.Feedback, feedback)
	for index := range next.ReviewFlows {
		if next.ReviewFlows[index].ID == flowID {
			next.ReviewFlows[index] = flow
		}
	}
	if err := next.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	*g = next
	result, _ := g.ReviewFlow(flowID)
	return result, nil
}
