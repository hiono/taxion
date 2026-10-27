package controller

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

type DispatchReconciliationRequest struct {
	FlowID                     string   `json:"flowId"`
	DispatchID                 string   `json:"dispatchId"`
	Actor                      string   `json:"actor"`
	Source                     string   `json:"source"`
	Reason                     string   `json:"reason"`
	Evidence                   []string `json:"evidence"`
	NoActiveExecutionConfirmed bool     `json:"noActiveExecutionConfirmed"`
	AcceptDeliveryUncertainty  bool     `json:"acceptDeliveryUncertainty"`
	ExpectedHash               string   `json:"expectedHash,omitempty"`
}

type DispatchReconciliationPreview struct {
	Version           int      `json:"version"`
	StateChanged      bool     `json:"stateChanged"`
	FlowID            string   `json:"flowId"`
	DispatchID        string   `json:"dispatchId"`
	InputContractHash string   `json:"inputContractHash"`
	InputTreeSHA      string   `json:"inputTreeSha"`
	InputOrigin       string   `json:"inputOrigin"`
	DeliveryStatus    string   `json:"deliveryStatus"`
	UsageStatus       string   `json:"usageStatus"`
	Transitions       []string `json:"transitions"`
	Hash              string   `json:"hash"`
}

func ReconcileStoppedDispatch(db *store.Store, request DispatchReconciliationRequest, previewOnly bool) (DispatchReconciliationPreview, error) {
	if db == nil {
		return DispatchReconciliationPreview{}, fmt.Errorf("taxion: reconciliation requires an existing workspace")
	}
	check := func(state store.Workspace) (DispatchReconciliationPreview, error) {
		return previewDispatchReconciliation(state, request)
	}
	if previewOnly {
		state, err := db.LoadWorkspace()
		if err != nil {
			return DispatchReconciliationPreview{}, err
		}
		return check(state)
	}
	var report DispatchReconciliationPreview
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var err error
		report, err = check(*state)
		if err != nil {
			return err
		}
		if request.ExpectedHash == "" || request.ExpectedHash != report.Hash {
			return fmt.Errorf("taxion: reconciliation changed; approve the exact new --preview hash")
		}
		flow, _ := state.Graph.ReviewFlow(request.FlowID)
		dispatch, _ := state.Graph.GraphDispatch(request.DispatchID)
		result := &core.DispatchResult{
			DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
			Repository: dispatch.Repository, Status: core.DispatchCancelled,
			StartedSHA:   dispatch.ExpectedStartSHA(),
			ChangedFiles: []string{},
			Evidence:     append([]string{"reconciled-stop:" + report.Hash, "delivery:unknown"}, request.Evidence...),
			Blockers:     []string{request.Reason}, NextTransition: core.Blocked,
		}
		if err := state.Graph.TransitionDispatch(dispatch.ID, core.DispatchCancelled, request.Actor,
			request.Source+": "+request.Reason, false, false, result); err != nil {
			return err
		}
		for i := range state.Ledger.Dispatches {
			if state.Ledger.Dispatches[i].ID == dispatch.ID {
				state.Ledger.Dispatches[i].Active = false
			}
		}
		cancelled, _ := state.Graph.GraphDispatch(dispatch.ID)
		target, err := core.ObserveFeedbackTarget(cancelled)
		if err != nil {
			return err
		}
		state.Records.DispatchReconciliations = append(state.Records.DispatchReconciliations, store.StoppedDispatchReconciliation{
			PreviewHash: report.Hash, InputContractHash: report.InputContractHash,
			Actor: request.Actor, Source: request.Source, Reason: request.Reason, Evidence: slices.Clone(request.Evidence),
			NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true, DeliveryStatus: "unknown",
			PreviousFlow: flow, PreviousDispatch: dispatch, Target: target, At: time.Now().UTC(),
		})
		report.StateChanged = true
		return nil
	})
	return report, err
}

func previewDispatchReconciliation(state store.Workspace, request DispatchReconciliationRequest) (DispatchReconciliationPreview, error) {
	fail := func(reason string) (DispatchReconciliationPreview, error) {
		return DispatchReconciliationPreview{}, fmt.Errorf("taxion: stopped Dispatch reconciliation: %s", reason)
	}
	for _, text := range append([]string{request.FlowID, request.DispatchID, request.Actor, request.Source, request.Reason}, request.Evidence...) {
		if strings.TrimSpace(text) == "" || strings.TrimSpace(text) != text {
			return fail("identities, decision and evidence must be nonempty and trimmed")
		}
	}
	if len(request.Evidence) == 0 || !request.NoActiveExecutionConfirmed || !request.AcceptDeliveryUncertainty {
		return fail("requires human-confirmed execution quiescence evidence and explicit acceptance of unknown delivery; missing session records are not proof")
	}
	if err := state.Graph.Validate(); err != nil {
		return DispatchReconciliationPreview{}, err
	}
	flow, found := state.Graph.ReviewFlow(request.FlowID)
	dispatch, exists := state.Graph.GraphDispatch(request.DispatchID)
	task, taskFound := state.Graph.GraphTask(flow.TaskID)
	if !state.HasGraph || !found || !exists || !taskFound || flow.Status != "STOPPED" ||
		flow.StopCode != "SAFETY_STOP" || !flow.HandoffHeld() || flow.RunnerID != "" ||
		len(flow.DispatchIDs) != 1 || flow.DispatchIDs[0] != dispatch.ID ||
		dispatch.TaskID != flow.TaskID || dispatch.Recipient != "CODER" || dispatch.Status != core.DispatchIssued ||
		dispatch.Acknowledged || dispatch.ExecutionStarted || dispatch.RuntimeSessionID != "" ||
		dispatch.RuntimeConfigHash != "" || dispatch.Result != nil ||
		dispatch.ExecutionStartSHA != "" || len(dispatch.RuntimeUsage) != 0 ||
		task.Status != core.Active || !flowContractMatches(state.Graph, flow, task) ||
		state.Graph.IsArchived(task.ID) || flow.PendingDraftID() != "" || len(flow.PendingFeedbackIDs()) != 0 ||
		flow.Contract.OutputMode != core.OutputModeWorktreeTreeSnapshot || flow.Contract.TargetTreeSHA == "" {
		return fail("requires a held, unclaimed SAFETY_STOP, its first issued Coder, no start/session/result, and an unchanged ACTIVE snapshot-input contract")
	}
	if operation := state.Records.Operation; operation != nil && operation.Active() {
		return fail("an operation is still active")
	}
	for _, other := range state.Graph.ReviewFlows {
		if other.Status == "RUNNING" || other.RunnerID != "" {
			return fail("a Flow is still running or claimed")
		}
	}
	for _, other := range state.Graph.Dispatches {
		if other.ID != dispatch.ID && active(other.Status) {
			return fail("another Dispatch is active")
		}
	}
	for _, legacy := range state.Ledger.Dispatches {
		if legacy.Active && legacy.ID != dispatch.ID {
			return fail("a legacy Dispatch is active")
		}
	}
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID == dispatch.ID {
			return fail("runtime receipts require separate execution reconciliation")
		}
	}
	for _, audit := range state.Records.DispatchReconciliations {
		if audit.PreviousFlow.ID == flow.ID {
			return fail("this Flow already has a reconciliation")
		}
	}
	if _, err := gitproof.ObserveSnapshotStart(state.Records.RestoredDispatch(dispatch)); err != nil {
		return DispatchReconciliationPreview{}, fmt.Errorf("taxion: approved carried input must remain intact: %w", err)
	}
	inputHash, err := core.CanonicalSHA256(flow.Contract)
	if err != nil {
		return DispatchReconciliationPreview{}, err
	}
	request.ExpectedHash = ""
	hash, err := core.CanonicalSHA256(struct {
		State   store.Workspace
		Request DispatchReconciliationRequest
	}{state, request})
	if err != nil {
		return DispatchReconciliationPreview{}, err
	}
	return DispatchReconciliationPreview{
		Version: 1, FlowID: flow.ID, DispatchID: dispatch.ID, InputContractHash: inputHash,
		InputTreeSHA: flow.Contract.TargetTreeSHA, InputOrigin: "approved-predecessor-input-contract",
		DeliveryStatus: "unknown", UsageStatus: "unavailable",
		Transitions: []string{"Dispatch: ISSUED -> CANCELLED", "Task: ACTIVE -> BLOCKED", "append reconciliation; preserve Flow, HOLD, stop reason, authorization and ceilings"},
		Hash:        hash,
	}, nil
}

func reconciledInput(state store.Workspace, flowID, hash string) (store.StoppedDispatchReconciliation, error) {
	if err := state.Records.VerifyDispatchReconciliations(state.Graph); err != nil {
		return store.StoppedDispatchReconciliation{}, err
	}
	for _, audit := range state.Records.DispatchReconciliations {
		if audit.PreviewHash == hash && hash != "" && audit.PreviousFlow.ID == flowID {
			return audit, nil
		}
	}
	return store.StoppedDispatchReconciliation{}, fmt.Errorf("taxion: exact saved stopped Dispatch reconciliation is required")
}

func verifyReconciledInput(state store.Workspace, task core.GraphTask, hash string) error {
	if task.CreatedFrom == "" || task.Supersedes == "" {
		return fmt.Errorf("taxion: reconciled input requires a sourced successor")
	}
	audit, err := reconciledInput(state, strings.TrimPrefix(task.CreatedFrom, "flow:"), hash)
	if err != nil {
		return err
	}
	if task.CreatedFrom != "flow:"+audit.PreviousFlow.ID || task.Supersedes != audit.PreviousFlow.TaskID {
		return fmt.Errorf("taxion: reconciled input predecessor differs")
	}
	previous, found := state.Graph.GraphTask(audit.PreviousFlow.TaskID)
	if !found || previous.Status != core.Blocked {
		return fmt.Errorf("taxion: reconciled predecessor must remain BLOCKED and not already superseded")
	}
	for _, other := range state.Graph.Tasks {
		if other.ID != task.ID && other.Supersedes == previous.ID && other.AuthorizationID != "" {
			return fmt.Errorf("taxion: reconciled predecessor already has an authorized successor")
		}
	}
	return gitproof.VerifyApprovedInputSuccessor(state.Records.RestoredDispatch(audit.PreviousDispatch), task)
}
