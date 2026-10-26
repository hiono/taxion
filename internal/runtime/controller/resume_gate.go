package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

const GateResume = "resume"

// ResumeContractGateReport observes only a saved, unclaimed Coder boundary.
// It cannot create or approve a flow, acknowledge a Dispatch, or reset limits.
func ResumeContractGateReport(ctx context.Context, state store.Workspace, flowID string, configuration core.ApprovalConfiguration) (GateReport, error) {
	report := newGateReport(GateResume, "")
	report.FlowID = flowID
	reject := func(code, message string) (GateReport, error) {
		report.fail(code, message)
		return report, nil
	}
	flow, ok := state.Graph.ReviewFlow(flowID)
	if !state.HasGraph || !ok || flow.Status != "RUNNING" {
		return reject("resume-flow-not-running", "resume requires an existing RUNNING Flow")
	}
	report.TaskID = flow.TaskID
	task, ok := state.Graph.GraphTask(flow.TaskID)
	if !ok || task.Status != core.Active {
		return reject("resume-task-not-active", "resume requires the Flow's ACTIVE Task")
	}
	if flow.RunnerID != "" {
		return reject("resume-runner-claimed", "saved runner ownership must be formally reconciled before resume")
	}
	if len(flow.DispatchIDs) == 0 {
		return reject("resume-dispatch-missing", "saved Flow has no current Dispatch")
	}
	dispatch, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok || dispatch.TaskID != task.ID || dispatch.Recipient != "CODER" {
		return reject("resume-binding-mismatch", "Task, Flow and current Coder Dispatch must have an exact binding")
	}
	if dispatch.Status != core.DispatchIssued || dispatch.Acknowledged || dispatch.ExecutionStarted ||
		dispatch.RuntimeSessionID != "" || dispatch.Result != nil || len(dispatch.RuntimeUsage) != 0 {
		return reject("resume-dispatch-not-untouched", "resume requires an ISSUED, unacknowledged, unstarted Dispatch without a runtime session or result")
	}
	if flow.HandoffHeld() || flow.PendingDraftID() != "" {
		return reject("resume-handoff-held", "resolve the saved handoff or instruction draft before resume")
	}
	if !flowContractMatches(state.Graph, flow, task) {
		return reject("resume-contract-mismatch", "current Task differs from the saved Flow contract")
	}
	if err := state.Graph.Validate(); err != nil {
		return reject("resume-state-invalid", "saved Task, Flow, Dispatch or instruction hash is inconsistent")
	}
	if err := checkResumeAdmission(ctx, state, flow); err != nil {
		return reject("resume-operation-conflict", err.Error())
	}
	if err := checkResumeBudget(state, flow, dispatch); err != nil {
		return reject("resume-budget-binding", err.Error())
	}
	if !time.Now().UTC().Before(flow.Deadline) {
		return reject("resume-deadline-expired", "saved deadline has expired")
	}
	if err := requireSessionBudget(state.Graph, flow, dispatch.Recipient); err != nil {
		return reject("resume-budget-exhausted", "saved remaining budget is unknown or exhausted")
	}
	contractHash, err := core.CanonicalSHA256(flow.Contract)
	if err != nil {
		return GateReport{}, err
	}
	report.Hashes.Task = contractHash
	report.Hashes.Record = report.Hashes.Task
	bundle, ok := state.Graph.MatchingApproval(task.ID, flow.Contract, configuration)
	if !ok || state.Graph.ApprovalRequirement() != core.ApprovalBundleRequired {
		return reject("resume-authorization-mismatch", "saved contract requires a matching formal ApprovalBundle and exact effective configuration")
	}
	report.Hashes.Proposal, report.Hashes.Bundle = bundle.ProposalHash, bundle.BundleHash
	var candidate config.Config
	if err := core.DecodeGraphInput(bytes.NewReader(configuration.Selection), &candidate); err != nil {
		return reject("resume-configuration-invalid", "effective configuration is incomplete")
	}
	if _, err := checkDispatchConfiguration(state.Graph, dispatch.ID, candidate); err != nil {
		return reject("resume-configuration-mismatch", "task-bound and issued configuration approvals must match")
	}
	auth, enabled, err := lifecycleAuthorization(&state, flow)
	if err != nil || !enabled || auth.BundleHash != bundle.BundleHash {
		return reject("resume-packet-authorization", "resume requires a matching saved packet lifecycle authorization")
	}
	if err := checkLifecyclePacket(&state, dispatch.ID); err != nil {
		return reject("resume-packet-invalid", "canonical packet readback or binding verification failed")
	}
	matched := false
	for _, audit := range state.Records.PacketAudits {
		if audit.DispatchID != dispatch.ID || audit.Role != dispatch.Recipient {
			continue
		}
		packet, err := state.Records.ReadPacket(audit.PacketHash)
		if err != nil {
			return reject("resume-packet-invalid", "canonical packet is unavailable")
		}
		fields := packet.Readback()
		if fields.TaskID != auth.TaskID || fields.TaskHash != auth.TaskHash ||
			fields.BaselineSHA != auth.BaselineSHA || fields.ScopeDigest != auth.ScopeDigest ||
			fields.AcceptanceDigest != auth.Acceptance || fields.Role != dispatch.Recipient {
			return reject("resume-packet-invalid", "canonical packet contract or role differs from its saved authorization")
		}
		matched = true
	}
	if !matched {
		return reject("resume-packet-invalid", "current Dispatch has no canonical packet audit")
	}
	located := state.Records.RestoredDispatch(dispatch)
	located.Configuration = &core.DispatchConfiguration{Selection: configuration.Selection}
	gitCtx, cancel := context.WithTimeout(ctx, DefaultLaneTimeout)
	defer cancel()
	if err := gitproof.CheckResumeStart(gitCtx, located); err != nil {
		return reject("resume-worktree-mismatch", err.Error())
	}
	if !time.Now().UTC().Before(flow.Deadline) {
		return reject("resume-deadline-expired", "saved deadline expired during observation")
	}
	report.NextAuthorizedTransition = "resume the saved unstarted Coder Dispatch"
	return report, nil
}

func checkResumeAdmission(ctx context.Context, state store.Workspace, flow core.ReviewFlow) error {
	if operation := state.Records.Operation; operation != nil && operation.Active() {
		claim, owned := ctx.Value(operationKey{}).(store.Operation)
		if operation.FlowID != flow.ID || operation.TaskID != flow.TaskID ||
			operation.Owner != "" && (!owned || claim.ID != operation.ID || claim.Owner != operation.Owner) ||
			operation.State != "RUNNING" && operation.State != "BLOCKED" ||
			operation.Phase != "PREFLIGHT" && operation.Phase != "EXECUTION" {
			return fmt.Errorf("another operation or process owns the execution boundary")
		}
		if operation.AccountQuotaOnly != flow.Budget.AccountQuotaOnly ||
			operation.BudgetSource != flow.Budget.BudgetSource ||
			!operation.ExternalBudgetOnly() && (operation.PlanningUsageState != "complete" ||
				operation.MaxNanoAIU-operation.PlanningNanoAIU != flow.Budget.MaxNanoAIU) ||
			!operation.Deadline.Equal(flow.Deadline) {
			return fmt.Errorf("operation and Flow approved limits or deadline differ")
		}
	}
	for _, other := range state.Graph.ReviewFlows {
		if other.ID != flow.ID && (other.Status != "STOPPED" && other.Status != "COMPLETED" || other.RunnerID != "") {
			return fmt.Errorf("a competing Flow is not terminal and unclaimed")
		}
	}
	for _, other := range state.Graph.Dispatches {
		if other.ID != flow.DispatchIDs[len(flow.DispatchIDs)-1] && active(other.Status) {
			return fmt.Errorf("another Dispatch is not terminal")
		}
	}
	return nil
}

func checkResumeBudget(state store.Workspace, flow core.ReviewFlow, dispatch core.GraphDispatch) error {
	var saved *store.BudgetAudit
	for index := range state.Records.Budgets {
		audit := &state.Records.Budgets[index]
		if audit.FlowID == flow.ID {
			saved = audit
		}
	}
	if saved == nil || saved.Kind != "DISPATCH_ISSUED" || saved.TaskID != flow.TaskID ||
		saved.Hash != core.SHA256Hex([]byte(saved.Payload)) {
		return fmt.Errorf("saved issuance budget audit is missing or invalid")
	}
	var recorded TaskBudgetReport
	if err := json.Unmarshal([]byte(saved.Payload), &recorded); err != nil || recorded.ResumeBasis == nil {
		return fmt.Errorf("saved issuance lacks a versioned resume binding; no implicit migration")
	}
	basis := recorded.ResumeBasis
	contract, err := core.CanonicalSHA256(flow.Contract)
	if err != nil {
		return err
	}
	authorization, err := core.CanonicalSHA256(flow.Authorization)
	if err != nil {
		return err
	}
	if basis.Version != 1 || basis.ContractHash != contract || basis.AuthorizationHash != authorization ||
		basis.DispatchID != dispatch.ID || basis.InstructionHash != dispatch.InstructionHash ||
		!basis.Deadline.Equal(flow.Deadline) || !basis.StartedAt.Equal(flow.StartedAt) || basis.Budget != flow.Budget {
		return fmt.Errorf("saved contract, authorization, instruction, limits or deadline binding differs")
	}
	current := taskBudgetReport(state.Graph, flow, saved.At)
	recorded.ResumeBasis = nil
	// Wall-clock elapsed fields are observational, not consumed counters.
	recorded.ElapsedMilliseconds, recorded.ElapsedRemainingMillis = current.ElapsedMilliseconds, current.ElapsedRemainingMillis
	if !reflect.DeepEqual(recorded, current) || flow.Budget.MaxDispatches != 2+2*flow.MaxRepairs ||
		flow.Budget.MaxCoderSessions != 1+flow.MaxRepairs || flow.Budget.MaxReviewerSessions != 1+flow.MaxRepairs {
		return fmt.Errorf("saved budget, usage or counters differ from Dispatch history")
	}
	return nil
}
