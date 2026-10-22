package store

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// Operation is the workspace's durable admission record, not execution approval.
type Operation struct {
	Version            int             `json:"version"`
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	TaskID             string          `json:"taskId"`
	FlowID             string          `json:"flowId,omitempty"`
	State              string          `json:"state"`
	Phase              string          `json:"phase"`
	Owner              string          `json:"owner,omitempty"`
	Deadline           time.Time       `json:"deadline"`
	SessionCreditLimit float64         `json:"sessionSoftCreditLimit"`
	MaxNanoAIU         float64         `json:"maxNanoAiu"`
	PlanningNanoAIU    float64         `json:"planningNanoAiu"`
	PlanningUsageState string          `json:"planningUsageState"`
	Checkpoint         json.RawMessage `json:"checkpoint,omitempty"`
}

func (o Operation) Active() bool {
	return o.State != "COMPLETED" && o.State != "CANCELLED"
}

func (o Operation) validate() error {
	if o.Version != 1 || strings.TrimSpace(o.ID) == "" || strings.TrimSpace(o.TaskID) == "" ||
		o.Kind != "request" && o.Kind != "question" || o.Kind == "request" && o.Deadline.IsZero() ||
		math.IsNaN(o.SessionCreditLimit) || math.IsInf(o.SessionCreditLimit, 0) || o.SessionCreditLimit < 0 ||
		math.IsNaN(o.MaxNanoAIU) || math.IsInf(o.MaxNanoAIU, 0) || o.MaxNanoAIU < 0 ||
		math.IsNaN(o.PlanningNanoAIU) || math.IsInf(o.PlanningNanoAIU, 0) || o.PlanningNanoAIU < 0 ||
		o.Kind == "request" && o.PlanningUsageState != "complete" && o.PlanningUsageState != "unknown" ||
		len(o.Checkpoint) != 0 && !json.Valid(o.Checkpoint) {
		return fmt.Errorf("invalid operation identity, version, budget or checkpoint")
	}
	switch o.State {
	case "RUNNING", "WAITING_FOR_ANSWER", "WAITING_FOR_APPROVAL", "BLOCKED", "USAGE_EXCEEDED", "COMPLETED", "CANCELLED":
	default:
		return fmt.Errorf("unknown operation state %q", o.State)
	}
	switch o.Phase {
	case "PLANNING", "PREFLIGHT", "EXECUTION", "QUESTION":
	default:
		return fmt.Errorf("unknown operation phase %q", o.Phase)
	}
	return nil
}

// claimOperation is called only while holding the process-lifetime reservation.
// Resume retains the exact saved ceilings and checkpoint.
func (s *Store) claimOperation(request Operation, resume bool) (Operation, error) {
	var claimed Operation
	err := s.UpdateWorkspace(func(state *Workspace) error {
		previous := state.Records.Operation
		if resume {
			if previous == nil || previous.ID != request.ID || !previous.Active() {
				return fmt.Errorf("operation cannot be resumed: missing or terminal")
			}
			claimed = *previous
			claimed.Owner = request.Owner
		} else {
			if previous != nil && previous.Active() {
				return fmt.Errorf("operation %s is %s; answer or resume that operation before starting another", previous.ID, previous.State)
			}
			claimed = request
		}
		for _, flow := range state.Graph.ReviewFlows {
			if (flow.Status == "RUNNING" || flow.RunnerID != "") && flow.ID != claimed.FlowID {
				return fmt.Errorf("workflow %s is active; another operation cannot start", flow.ID)
			}
		}
		for _, dispatch := range state.Graph.Dispatches {
			runtimeActive := false
			switch dispatch.Status {
			case core.DispatchIssued, core.DispatchAcknowledged, core.DispatchRunning, core.DispatchCancelRequested, core.DispatchSuspended:
				runtimeActive = dispatch.ExecutionStarted || dispatch.RuntimeSessionID != ""
			}
			if runtimeActive {
				inFlow := false
				for _, flow := range state.Graph.ReviewFlows {
					if flow.ID == claimed.FlowID {
						for _, id := range flow.DispatchIDs {
							inFlow = inFlow || id == dispatch.ID
						}
					}
				}
				if !inFlow {
					return fmt.Errorf("Dispatch %s is active; another operation cannot start", dispatch.ID)
				}
			}
		}
		if claimed.Owner == "" {
			return fmt.Errorf("operation requires an owner")
		}
		state.Records.Operation = &claimed
		return nil
	})
	return claimed, err
}

func (s *Store) SaveOperation(operation Operation) error {
	return s.UpdateWorkspace(func(state *Workspace) error {
		current := state.Records.Operation
		if current == nil || current.ID != operation.ID || current.Owner == "" || current.Owner != operation.Owner {
			return fmt.Errorf("operation ownership changed")
		}
		if current.Version != operation.Version || current.Kind != operation.Kind ||
			!current.Deadline.Equal(operation.Deadline) || current.SessionCreditLimit != operation.SessionCreditLimit ||
			current.MaxNanoAIU != operation.MaxNanoAIU || operation.PlanningNanoAIU < current.PlanningNanoAIU ||
			current.PlanningUsageState == "unknown" && operation.PlanningUsageState != "unknown" {
			return fmt.Errorf("operation identity and approved ceilings are immutable")
		}
		state.Records.Operation = &operation
		return nil
	})
}

func (s *Store) ReleaseOperation(id, owner string) error {
	return s.UpdateWorkspace(func(state *Workspace) error {
		current := state.Records.Operation
		if current == nil || current.ID != id || current.Owner == "" || current.Owner != owner {
			return fmt.Errorf("operation ownership changed during release")
		}
		current.Owner = ""
		return nil
	})
}
