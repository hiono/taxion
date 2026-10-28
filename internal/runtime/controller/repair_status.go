package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	// RepairHumanDecision identifies an actual unresolved user decision.
	RepairHumanDecision          = "human-decision-required"
	RepairReconciliationRequired = "reconciliation-required"
	// RepairNotApplicable means the RUNNING Flow is not at a repair point.
	RepairNotApplicable = "not-applicable"
)

// RepairStatus tells a controller whether the next repair is an internal
// transition (ADR-0043) or needs user action. It is an observation only.
type RepairStatus struct {
	StopCause                   string   `json:"stopCause,omitempty"`
	ReviewContinuationCandidate string   `json:"reviewContinuationCandidate,omitempty"`
	Version                     int      `json:"version"`
	FlowID                      string   `json:"flowId"`
	TaskID                      string   `json:"taskId"`
	FlowStatus                  string   `json:"flowStatus"`
	StopCode                    string   `json:"stopCode,omitempty"`
	Status                      string   `json:"status"`
	Repairs                     int      `json:"repairs"`
	MaxRepairs                  int      `json:"maxRepairs"`
	Reasons                     []string `json:"reasons"`
	NextAction                  string   `json:"nextAction"`
	StateChanged                bool     `json:"stateChanged"`
}

func flowStopCause(flow core.ReviewFlow) string {
	if flow.Status != "STOPPED" {
		return ""
	}
	if flow.StopCause != "" {
		return flow.StopCause
	}
	switch {
	case flow.Reason == "elapsed budget reached", strings.HasPrefix(flow.Reason, "deadline reached"),
		strings.Contains(flow.Reason, "stage submission deadline"),
		strings.Contains(flow.Reason, "stage deadline"), strings.Contains(flow.Reason, "insufficient stage time"),
		strings.Contains(flow.Reason, context.DeadlineExceeded.Error()):
		return "time-limit"
	case strings.HasPrefix(flow.Reason, "repair ceiling reached"):
		return "review-findings"
	case flow.StopCode == "BUDGET_STOP" || flow.StopCode == "BUDGET_LIMIT":
		return "usage-limit"
	default:
		return "other"
	}
}

// ObserveRepairStatus classifies the saved Flow without changing state.
func ObserveRepairStatus(state store.Workspace, flowID string, configuration core.ApprovalConfiguration) (RepairStatus, error) {
	flow, ok := state.Graph.ReviewFlow(flowID)
	if !state.HasGraph || !ok {
		return RepairStatus{}, fmt.Errorf("taxion: repair status requires an existing review flow %q", core.Redact(flowID))
	}
	status := RepairStatus{
		Version: RepairStatusVersion, FlowID: flow.ID, TaskID: flow.TaskID, FlowStatus: flow.Status,
		StopCode: flow.StopCode, Repairs: flow.Repairs, MaxRepairs: flow.MaxRepairs, Reasons: []string{},
	}
	status.StopCause = flowStopCause(flow)
	set := func(value, reason, next string) (RepairStatus, error) {
		status.Status, status.NextAction = value, next
		status.Reasons = append(status.Reasons, core.Redact(reason))
		return status, nil
	}

	const humanNext = "inspect saved evidence and reconcile through supported controller operations; present a consolidated user question only for a new decision, authority, resource or essential unavailable information"
	if err := state.Graph.Validate(); err != nil {
		return set(RepairInvalid, "saved Task, Flow or Dispatch records are inconsistent: "+err.Error(), humanNext)
	}
	if state.Graph.ApprovalRequirement() == core.ApprovalInvalidOrAmbiguous {
		return set(RepairInvalid, "approval classification is invalid or ambiguous", humanNext)
	}
	task, taskOK := state.Graph.GraphTask(flow.TaskID)
	if flow.Artifact != nil && len(flow.DispatchIDs) == 0 && taskOK {
		if flow.Status == "STOPPED" {
			return set(RepairReconciliationRequired, "input validation stopped; preserve its receipt and input, never replay commands implicitly", humanNext)
		}
		return set(RepairNotApplicable, "authorized non-model input validation is pending or in progress", "inspect workflow show; use workflow run at an unclaimed, unheld boundary")
	}
	if len(flow.DispatchIDs) == 0 || !taskOK {
		return set(RepairInvalid, "Flow has no current Task or Dispatch", humanNext)
	}
	last, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok {
		return set(RepairInvalid, "current Dispatch is missing", humanNext)
	}
	if !flowContractMatches(state.Graph, flow, task) {
		return set(RepairReapprovalRequired, "current Task differs from the saved Flow contract", "obtain user approval of a new contract")
	}
	if state.Graph.ApprovalRequirement() == core.ApprovalBundleRequired {
		if _, ok := state.Graph.MatchingApproval(task.ID, flow.Contract, configuration); !ok {
			return set(RepairReapprovalRequired, "no saved ApprovalBundle covers the contract and effective configuration", "obtain user approval of a new contract")
		}
	}
	candidateID := last.ID
	if last.Recipient == "REVIEWER" {
		candidateID = last.RelatedID
	}
	if state.Graph.HasBudgetContinuationSource(flow, candidateID) {
		coder, _ := state.Graph.GraphDispatch(candidateID)
		if err := validateResultReceipt(&state, coder); err != nil {
			return set(RepairInvalid, "completed Coder receipt requires reconciliation: "+err.Error(), humanNext)
		}
		if last.ID != coder.ID {
			if err := validateResultReceipt(&state, last); err != nil {
				return set(RepairInvalid, "stopped Reviewer receipt requires reconciliation: "+err.Error(), humanNext)
			}
		}
		status.ReviewContinuationCandidate = coder.ID
		value := RepairReconciliationRequired
		if flow.StopCode == "BUDGET_STOP" || flow.StopCode == "BUDGET_LIMIT" {
			value = RepairCeilingExhausted
		} else if flow.StopCode == "RECONTRACT" {
			value = RepairReapprovalRequired
		}
		return set(value, "saved completed Coder output is a candidate for review-only continuation; this observation grants no execution authority",
			"obtain user approval for a successor Task and a new review budget using continueFromDispatchId="+coder.ID+
				"; revalidate receipts, Git target and configuration before review; do not rerun the completed Coder")
	}
	reviewRepair := last.Recipient == "REVIEWER" && last.Status == core.DispatchSucceeded &&
		last.Result != nil && last.Result.ReviewVerdict == "REQUEST_CHANGES"
	if reviewRepair && status.StopCause == "other" {
		status.StopCause = "review-findings"
	}
	if reviewRepair && flow.Repairs >= flow.MaxRepairs {
		return set(RepairCeilingExhausted, "saved repair ceiling reached", humanNext)
	}
	if flow.Status == "STOPPED" {
		switch flow.StopCode {
		case "BUDGET_STOP", "BUDGET_LIMIT":
			return set(RepairCeilingExhausted, "Flow stopped on a saved budget ceiling", humanNext)
		case "RECONTRACT":
			return set(RepairReapprovalRequired, "Flow stopped for recontract", "obtain user approval of a new contract")
		}
		return set(RepairReconciliationRequired, "STOPPED Flow is terminal; repair runs only after independent review requests changes in a RUNNING Flow", humanNext)
	}
	if flow.Status != "RUNNING" || !reviewRepair {
		return set(RepairNotApplicable, "Flow is not waiting on a review repair", "continue the Flow as saved")
	}
	if !time.Now().UTC().Before(flow.Deadline) {
		return set(RepairCeilingExhausted, "saved deadline has passed", humanNext)
	}
	if err := state.Graph.CheckTaskSpecificationUse(task.ID, "runtime"); err != nil {
		return set(RepairReapprovalRequired, "specification source or permission requires renewed approval: "+err.Error(),
			"obtain user approval of a new contract")
	}
	if task.Status == core.HumanDecisionRequired ||
		task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "" {
		return set(RepairHumanDecision, "Task status "+string(task.Status)+" or a pending user decision stops the Flow", humanNext)
	}
	if slices.Contains([]core.TaskStatus{core.Cancelled, core.Rejected, core.Superseded, core.Blocked}, task.Status) {
		return set(RepairReconciliationRequired, "Task status "+string(task.Status)+" requires saved-evidence reconciliation", humanNext)
	}
	if err := requireSessionBudget(state.Graph, flow, "CODER"); err != nil {
		return set(RepairCeilingExhausted, err.Error(), humanNext)
	}
	if err := requireBudgetRoom(state.Graph, flow, "CODER"); err != nil {
		return set(RepairCeilingExhausted, err.Error(), humanNext)
	}
	if task.Status != core.WaitingForReview || last.Result.GitEvidence == nil || !last.Result.GitEvidence.WorktreeReleased ||
		last.Result.OutputSHA != task.TargetSHA {
		return set(RepairReconciliationRequired, "review result does not match the Task waiting for review with a released worktree and current target SHA", humanNext)
	}
	if flow.HandoffHeld() {
		return set(RepairReconciliationRequired, "handoff is held; release it before the repair continues", humanNext)
	}
	return set(RepairAuthorized, "repair is an internal transition within the saved ceiling and approval",
		"run workflow advance or run for this Flow without asking the user; advance still revalidates Git evidence, Memory and the approved draft")
}
