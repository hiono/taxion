package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type FlowExtensionRequest struct {
	FlowID             string    `json:"flowId"`
	PreviousDeadline   time.Time `json:"previousDeadline"`
	Deadline           time.Time `json:"deadline"`
	PreviousMaxNanoAIU float64   `json:"previousMaxNanoAiu"`
	MaxNanoAIU         float64   `json:"maxNanoAiu"`
	Actor              string    `json:"actor"`
	Source             string    `json:"source"`
	Reason             string    `json:"reason"`
	ExpectedHash       string    `json:"expectedHash,omitempty"`
}

type FlowExtensionReport struct {
	Hash         string             `json:"hash"`
	StateChanged bool               `json:"stateChanged"`
	FlowID       string             `json:"flowId"`
	DispatchID   string             `json:"dispatchId"`
	SessionID    string             `json:"sessionId,omitempty"`
	Extension    core.FlowExtension `json:"extension"`
	Budget       TaskBudgetReport   `json:"budget"`
}

// ExtendReviewFlow is a narrow concurrent control, not a second runner. Its
// stable hash excludes live telemetry so receiving usage cannot invalidate consent.
func ExtendReviewFlow(db *store.Store, request FlowExtensionRequest, preview bool) (FlowExtensionReport, error) {
	if db == nil {
		return FlowExtensionReport{}, fmt.Errorf("taxion: extension requires an existing workspace")
	}
	var report FlowExtensionReport
	apply := func(state *store.Workspace) error {
		var err error
		report, err = previewFlowExtension(*state, request, time.Now().UTC())
		if err != nil || preview {
			return err
		}
		if request.ExpectedHash == "" || request.ExpectedHash != report.Hash {
			return fmt.Errorf("taxion: extension requires approval of the exact current preview hash")
		}
		flow, _ := state.Graph.ReviewFlow(request.FlowID)
		flow.Extensions = append(flow.Extensions, report.Extension)
		storeFlow(state, flow)
		if err := appendBudgetAudit(state, flow, "RESOURCE_EXTENDED", taskBudgetReport(state.Graph, flow, report.Extension.At)); err != nil {
			return err
		}
		report.StateChanged, report.Budget = true, taskBudgetReport(state.Graph, flow, report.Extension.At)
		return nil
	}
	if preview {
		state, err := db.LoadWorkspace()
		if err != nil {
			return report, err
		}
		err = apply(&state)
		return report, err
	}
	err := db.UpdateWorkspace(apply)
	return report, err
}

func previewFlowExtension(state store.Workspace, request FlowExtensionRequest, now time.Time) (FlowExtensionReport, error) {
	flow, ok := state.Graph.ReviewFlow(request.FlowID)
	dispatch, found := state.Graph.GraphDispatch(flow.LastDispatchID())
	if !state.HasGraph || !ok || !found || flow.Status != "RUNNING" || flow.HandoffHeld() ||
		flow.PendingDraftID() != "" || !active(dispatch.Status) ||
		dispatch.Status == core.DispatchSuspended || dispatch.Status == core.DispatchCancelRequested {
		return FlowExtensionReport{}, fmt.Errorf("taxion: extend requires the same RUNNING Flow and current non-held authorized Dispatch; use recover-review after a formal stop")
	}
	if state.Records.Restore != nil {
		return FlowExtensionReport{}, fmt.Errorf("taxion: restored execution cannot be extended")
	}
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if !flowContractMatches(state.Graph, flow, task) ||
		!now.Before(stageDeadline(flow, dispatch, false)) ||
		activeBudgetStop(taskBudgetReport(state.Graph, flow, now)) != "" ||
		stageBudgetStop(state.Graph, flow, dispatch, now) != "" {
		return FlowExtensionReport{}, fmt.Errorf("taxion: existing contract, stage or hard limit no longer permits a live extension; preserve work and use formal stopped recovery")
	}
	if err := verifyStagePlanBinding(state.Records, flow); err != nil {
		return FlowExtensionReport{}, err
	}
	for _, field := range []string{request.Actor, request.Source, request.Reason} {
		if strings.TrimSpace(field) == "" || strings.TrimSpace(field) != field || core.Redact(field) != field {
			return FlowExtensionReport{}, fmt.Errorf("taxion: extension requires non-secret, trimmed actor, approval source and reason")
		}
	}
	// Session and instruction bindings remain fixed across usage/output events.
	basis, err := core.CanonicalSHA256(struct {
		FlowID        string                      `json:"flowId"`
		Contract      core.GraphTask              `json:"contract"`
		Authorization core.TaskDecision           `json:"authorization"`
		StartedAt     time.Time                   `json:"startedAt"`
		Deadline      time.Time                   `json:"deadline"`
		Budget        core.FlowBudget             `json:"budget"`
		StagePlan     *core.StagePlan             `json:"stagePlan"`
		Extensions    []core.FlowExtension        `json:"extensions"`
		RunnerID      string                      `json:"runnerId"`
		DispatchID    string                      `json:"dispatchId"`
		Instruction   string                      `json:"instructionHash"`
		SessionID     string                      `json:"sessionId"`
		ConfigHash    string                      `json:"configHash"`
		Configuration *core.DispatchConfiguration `json:"configuration"`
		Operation     *store.Operation            `json:"operation,omitempty"`
	}{flow.ID, flow.Contract, flow.Authorization, flow.StartedAt, flow.Deadline, flow.Budget, flow.StagePlan,
		flow.Extensions, flow.RunnerID, dispatch.ID, dispatch.InstructionHash, dispatch.RuntimeSessionID,
		dispatch.RuntimeConfigHash, dispatch.Configuration, state.Records.Operation})
	if err != nil {
		return FlowExtensionReport{}, err
	}
	extension := core.FlowExtension{
		BasisHash: basis, PreviousDeadline: request.PreviousDeadline, Deadline: request.Deadline,
		PreviousMaxNanoAIU: request.PreviousMaxNanoAIU, MaxNanoAIU: request.MaxNanoAIU,
		Actor: request.Actor, Source: request.Source, Reason: request.Reason, At: now,
	}
	extension.Hash, err = extension.ApprovalHash()
	if err != nil {
		return FlowExtensionReport{}, err
	}
	flow.Extensions = append(flow.Extensions, extension)
	// Graph validation enforces monotone bounded extensions and the exact prior limits.
	proposed := state.Graph
	proposed.ReviewFlows = append([]core.ReviewFlow(nil), proposed.ReviewFlows...)
	for i := range proposed.ReviewFlows {
		if proposed.ReviewFlows[i].ID == flow.ID {
			proposed.ReviewFlows[i] = flow
		}
	}
	if err := proposed.Validate(); err != nil {
		return FlowExtensionReport{}, err
	}
	return FlowExtensionReport{Hash: extension.Hash, FlowID: flow.ID, DispatchID: dispatch.ID,
		SessionID: dispatch.RuntimeSessionID, Extension: extension, Budget: taskBudgetReport(state.Graph, flow, now)}, nil
}
