package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrHandoffHeld = errors.New("taxion: review flow handoff held")

type HandoffChange struct {
	ID               string    `json:"id"`
	ExpectedChangeID string    `json:"expectedChangeId"`
	Action           string    `json:"action"`
	Actor            string    `json:"actor"`
	Source           string    `json:"source"`
	Reason           string    `json:"reason"`
	At               time.Time `json:"at,omitempty"`
}

func (f ReviewFlow) HandoffHeld() bool {
	return len(f.HandoffChanges) != 0 && f.HandoffChanges[len(f.HandoffChanges)-1].Action == "HOLD"
}

func validHandoffChange(change HandoffChange) bool {
	for _, text := range []string{change.ID, change.Actor, change.Source, change.Reason} {
		if strings.TrimSpace(text) == "" || strings.TrimSpace(text) != text {
			return false
		}
	}
	return change.Action == "HOLD" || change.Action == "RELEASE"
}

func validateHandoffChanges(flow ReviewFlow) error {
	seen := map[string]bool{}
	priorID, priorAction := "", "RELEASE"
	for _, change := range flow.HandoffChanges {
		if !validHandoffChange(change) || change.At.IsZero() || seen[change.ID] ||
			change.ExpectedChangeID != priorID || change.Action == priorAction {
			return graphError("invalid handoff change history for flow %s", flow.ID)
		}
		seen[change.ID] = true
		priorID, priorAction = change.ID, change.Action
	}
	return nil
}

// ChangeHandoff uses the last change ID for compare-and-swap. Replaying an
// earlier request returns current state without applying that action again.
func (g *TaskGraph) ChangeHandoff(flowID string, change HandoffChange) (ReviewFlow, error) {
	if !validHandoffChange(change) || !change.At.IsZero() {
		return ReviewFlow{}, graphError("handoff change requires ID, action, actor, source and reason; timestamp is controller-owned")
	}
	flow, ok := g.ReviewFlow(flowID)
	if !ok {
		return ReviewFlow{}, graphError("review flow %s not found", flowID)
	}
	for _, previous := range flow.HandoffChanges {
		if previous.ID != change.ID {
			continue
		}
		previous.At = time.Time{}
		if previous != change {
			return ReviewFlow{}, graphError("handoff request ID was reused with different content")
		}
		return flow, nil
	}
	if flow.Status != "RUNNING" && !(flow.Status == "STOPPED" && change.Action == "RELEASE") {
		return ReviewFlow{}, graphError("handoff changes require a running flow; only release is allowed after a safety stop")
	}
	priorID := ""
	if len(flow.HandoffChanges) != 0 {
		priorID = flow.HandoffChanges[len(flow.HandoffChanges)-1].ID
	}
	if change.ExpectedChangeID != priorID {
		return ReviewFlow{}, graphError("handoff state changed; observe the latest change before retrying")
	}
	if (change.Action == "HOLD") == flow.HandoffHeld() {
		return ReviewFlow{}, graphError("handoff action does not change the current hold state")
	}
	if change.Action == "RELEASE" && flow.Status == "RUNNING" && len(flow.PendingFeedbackIDs()) != 0 {
		return ReviewFlow{}, graphError("handoff has unresolved feedback %v; record an explicit disposition before release", flow.PendingFeedbackIDs())
	}
	if change.Action == "RELEASE" && flow.Status == "RUNNING" && flow.PendingDraftID() != "" {
		return ReviewFlow{}, graphError("handoff has unissued instruction draft %s; explicitly discard it before continuing the unchanged contract", flow.PendingDraftID())
	}
	change.At = time.Now().UTC()
	flow.HandoffChanges = append(flow.HandoffChanges, change)
	next := cloneGraph(*g)
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

func (g TaskGraph) CheckHandoff(taskID string) error {
	task, ok := g.task(taskID)
	if !ok {
		return graphError("handoff check requires a tracked Task")
	}
	for _, flow := range g.ReviewFlows {
		if !flow.HandoffHeld() {
			continue
		}
		if flow.TaskID == taskID {
			return fmt.Errorf("%w: %s", ErrHandoffHeld, flow.ID)
		}
		for _, id := range flow.DispatchIDs {
			dispatch, _ := g.dispatch(id)
			if dispatch.TaskID == taskID || task.CreatedFrom == "dispatch:"+id {
				return fmt.Errorf("%w: %s", ErrHandoffHeld, flow.ID)
			}
		}
	}
	return nil
}

// Existing execution may still be attached for observation or safe stop.
// Sending another turn uses CheckHandoff even if execution has already started.
func (g TaskGraph) CheckHandoffStart(dispatchID string) error {
	dispatch, ok := g.dispatch(dispatchID)
	if !ok {
		return graphError("handoff start check requires a saved Dispatch")
	}
	if dispatch.ExecutionStarted {
		return nil
	}
	return g.CheckHandoff(dispatch.TaskID)
}
