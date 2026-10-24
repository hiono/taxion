package controller

import (
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

const RepairStatusVersion = 1

const (
	RepairAuthorized         = "repair-authorized"
	RepairReapprovalRequired = "reapproval-required"
	RepairCeilingExhausted   = "ceiling-exhausted"
	RepairInvalid            = "invalid"
	// RepairHumanDecision covers stops that are not a pending review repair. A
	// STOPPED Flow is terminal, so continuing needs a Human decision.
	RepairHumanDecision = "human-decision-required"
	// RepairNotApplicable means the RUNNING Flow is not at a repair point.
	RepairNotApplicable = "not-applicable"
)

// RepairStatus tells a controller whether the next repair is an internal
// transition (ADR-0043) or needs Human action. It is an observation only.
type RepairStatus struct {
	Version      int      `json:"version"`
	FlowID       string   `json:"flowId"`
	TaskID       string   `json:"taskId"`
	FlowStatus   string   `json:"flowStatus"`
	StopCode     string   `json:"stopCode,omitempty"`
	Status       string   `json:"status"`
	Repairs      int      `json:"repairs"`
	MaxRepairs   int      `json:"maxRepairs"`
	Reasons      []string `json:"reasons"`
	NextAction   string   `json:"nextAction"`
	StateChanged bool     `json:"stateChanged"`
}

// ObserveRepairStatus classifies the saved Flow without changing state.
func ObserveRepairStatus(state store.Workspace, flowID string, configuration core.ApprovalConfiguration) (RepairStatus, error) {
	flow, ok := state.Graph.ReviewFlow(flowID)
	if !state.HasGraph || !ok {
		return RepairStatus{}, fmt.Errorf("taxion: repair status requires an existing review flow %q", flowID)
	}
	status := RepairStatus{
		Version: RepairStatusVersion, FlowID: flow.ID, TaskID: flow.TaskID, FlowStatus: flow.Status,
		StopCode: flow.StopCode, Repairs: flow.Repairs, MaxRepairs: flow.MaxRepairs, Reasons: []string{},
	}
	set := func(value, reason, next string) (RepairStatus, error) {
		status.Status, status.NextAction = value, next
		status.Reasons = append(status.Reasons, core.Redact(reason))
		return status, nil
	}
	const humanNext = "present the saved stop reason, findings and acceptance to Human"
	if err := state.Graph.Validate(); err != nil {
		return set(RepairInvalid, "saved Task, Flow or Dispatch records are inconsistent: "+err.Error(), humanNext)
	}
	if state.Graph.ApprovalRequirement() == core.ApprovalInvalidOrAmbiguous {
		return set(RepairInvalid, "approval classification is invalid or ambiguous", humanNext)
	}
	task, taskOK := state.Graph.GraphTask(flow.TaskID)
	if len(flow.DispatchIDs) == 0 || !taskOK {
		return set(RepairInvalid, "Flow has no current Task or Dispatch", humanNext)
	}
	last, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok {
		return set(RepairInvalid, "current Dispatch is missing", humanNext)
	}
	if !flowContractMatches(state.Graph, flow, task) {
		return set(RepairReapprovalRequired, "current Task differs from the saved Flow contract", "obtain Human approval of a new contract")
	}
	if state.Graph.ApprovalRequirement() == core.ApprovalBundleRequired {
		if _, ok := state.Graph.MatchingApproval(task.ID, flow.Contract, configuration); !ok {
			return set(RepairReapprovalRequired, "no saved ApprovalBundle covers the contract and effective configuration", "obtain Human approval of a new contract")
		}
	}
	reviewRepair := last.Recipient == "REVIEWER" && last.Status == core.DispatchSucceeded &&
		last.Result != nil && last.Result.ReviewVerdict == "REQUEST_CHANGES"
	if reviewRepair && flow.Repairs >= flow.MaxRepairs {
		return set(RepairCeilingExhausted, "saved repair ceiling reached", humanNext)
	}
	if flow.Status == "STOPPED" {
		switch flow.StopCode {
		case "BUDGET_STOP", "BUDGET_LIMIT":
			return set(RepairCeilingExhausted, "Flow stopped on a saved budget ceiling", humanNext)
		case "RECONTRACT":
			return set(RepairReapprovalRequired, "Flow stopped for recontract", "obtain Human approval of a new contract")
		}
		return set(RepairHumanDecision, "STOPPED Flow is terminal; repair runs only after independent review requests changes in a RUNNING Flow", humanNext)
	}
	if flow.Status != "RUNNING" || !reviewRepair {
		return set(RepairNotApplicable, "Flow is not waiting on a review repair", "continue the Flow as saved")
	}
	if !time.Now().UTC().Before(flow.Deadline) {
		return set(RepairCeilingExhausted, "saved deadline has passed", humanNext)
	}
	if err := requireSessionBudget(state.Graph, flow, "CODER"); err != nil {
		return set(RepairCeilingExhausted, err.Error(), humanNext)
	}
	return set(RepairAuthorized, "repair is an internal transition within the saved ceiling and approval",
		"run workflow advance or run for this Flow without asking Human")
}
