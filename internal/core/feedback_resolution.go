package core

import (
	"reflect"
	"strings"
	"time"
)

// KEEP_CONTRACT explicitly disposes of feedback without changing execution
// authorization. Applying a new instruction requires a separate contract path.
type FeedbackResolution struct {
	ID               string         `json:"id"`
	ExpectedChangeID string         `json:"expectedChangeId"`
	Target           FeedbackTarget `json:"target"`
	Action           string         `json:"action"`
	Actor            string         `json:"actor"`
	Source           string         `json:"source"`
	Reason           string         `json:"reason"`
	At               time.Time      `json:"at,omitempty"`
}

func validFeedbackResolution(resolution FeedbackResolution) bool {
	for _, text := range []string{resolution.ID, resolution.Actor, resolution.Source, resolution.Reason} {
		if strings.TrimSpace(text) == "" || strings.TrimSpace(text) != text {
			return false
		}
	}
	return resolution.Action == "KEEP_CONTRACT"
}

func (f ReviewFlow) PendingFeedbackIDs() []string {
	pending := []string{}
	for _, feedback := range f.Feedback {
		if feedback.Resolution == nil && !f.FeedbackHasApprovedDraft(feedback.ID) {
			pending = append(pending, feedback.ID)
		}
	}
	return pending
}

func (f ReviewFlow) FeedbackHasApprovedDraft(id string) bool {
	for i, draft := range f.InstructionDrafts {
		if draft.Approval != nil && draft.Discard == nil &&
			(draft.Publication != nil || i == len(f.InstructionDrafts)-1) {
			for _, feedbackID := range draft.FeedbackIDs {
				if feedbackID == id {
					return true
				}
			}
		}
	}
	return false
}

func (g *TaskGraph) ResolveFlowFeedback(flowID, feedbackID string, resolution FeedbackResolution) (ReviewFlow, error) {
	if !validFeedbackResolution(resolution) || !resolution.At.IsZero() {
		return ReviewFlow{}, graphError("feedback resolution requires ID, KEEP_CONTRACT, actor, source and reason; timestamp is controller-owned")
	}
	if err := g.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	flow, ok := g.ReviewFlow(flowID)
	if !ok {
		return ReviewFlow{}, graphError("feedback resolution requires an existing flow")
	}
	index := -1
	for i, feedback := range flow.Feedback {
		if feedback.ID == feedbackID {
			index = i
		}
		if feedback.Resolution != nil && feedback.Resolution.ID == resolution.ID && feedback.ID != feedbackID {
			return ReviewFlow{}, graphError("resolution ID already belongs to another feedback")
		}
	}
	if index < 0 {
		return ReviewFlow{}, graphError("feedback %s not found", feedbackID)
	}
	if previous := flow.Feedback[index].Resolution; previous != nil {
		recorded := *previous
		recorded.At = time.Time{}
		if !reflect.DeepEqual(recorded, resolution) {
			return ReviewFlow{}, graphError("feedback resolution cannot be rewritten")
		}
		return flow, nil
	}
	if flow.FeedbackHasApprovedDraft(feedbackID) {
		return ReviewFlow{}, graphError("feedback already belongs to an approved instruction supplement")
	}
	if !flow.HandoffHeld() {
		return ReviewFlow{}, graphError("feedback resolution requires a held handoff")
	}
	last, _ := g.dispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	target, err := ObserveFeedbackTarget(last)
	if err != nil {
		return ReviewFlow{}, err
	}
	if target != resolution.Target {
		return ReviewFlow{}, ErrFeedbackStale
	}
	if resolution.ExpectedChangeID != flow.HandoffChanges[len(flow.HandoffChanges)-1].ID {
		return ReviewFlow{}, graphError("feedback resolution handoff changed; observe the latest state")
	}
	resolution.At = time.Now().UTC()
	flow.Feedback[index].Resolution = &resolution
	next := cloneGraph(*g)
	for i := range next.ReviewFlows {
		if next.ReviewFlows[i].ID == flowID {
			next.ReviewFlows[i] = flow
		}
	}
	if err := next.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	*g = next
	result, _ := g.ReviewFlow(flowID)
	return result, nil
}
