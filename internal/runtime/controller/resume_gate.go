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

// ResumeContractGateReport observes a saved, unclaimed execution boundary.
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
	if !ok {
		return reject("resume-task-not-active", "resume requires the Flow's saved Task")
	}
	if flow.RunnerID != "" {
		return reject("resume-runner-claimed", "saved runner ownership must be formally reconciled before resume")
	}
	if len(flow.DispatchIDs) == 0 {
		return reject("resume-dispatch-missing", "saved Flow has no current Dispatch")
	}
	dispatch, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	roleTask, roleFound := state.Graph.GraphTask(dispatch.TaskID)
	if !ok || !roleFound || dispatch.Repository != task.Repo {
		return reject("resume-binding-mismatch", "Task, Flow and current Dispatch must have an exact binding")
	}
	if roleTask.Status != core.Active {
		return reject("resume-task-not-active", "resume requires the issued role's ACTIVE Task")
	}
	if err := checkUnstartedDispatch(flow, dispatch); err != nil {
		return reject("resume-dispatch-not-untouched", err.Error())
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
	if !time.Now().UTC().Before(flow.ExecutionDeadline()) {
		return reject("resume-deadline-expired", "saved deadline has expired")
	}
	if dispatch.RuntimeSessionID == "" {
		if err := requireSessionBudget(state.Graph, flow, dispatch.Recipient); err != nil {
			return reject("resume-budget-exhausted", "saved remaining budget is unknown or exhausted")
		}
	} else if reason := activeBudgetStop(taskBudgetReport(state.Graph, flow, time.Now().UTC())); reason != "" {
		return reject("resume-budget-exhausted", reason)
	}
	contractHash, err := core.CanonicalSHA256(flow.Contract)
	if err != nil {
		return GateReport{}, err
	}
	report.Hashes.Task = contractHash
	report.Hashes.Record = report.Hashes.Task
	// IssueDispatch changes only READY to ACTIVE. Verify its approved record
	// without changing the saved Task or deriving any new authority.
	roleTask.Status = core.Ready
	switch state.Graph.ApprovalRequirement() {
	case core.ApprovalBundleRequired:
		bundle, matched := state.Graph.MatchingApproval(roleTask.ID, roleTask, configuration)
		if !matched {
			return reject("resume-authorization-mismatch", "issued role requires its saved ApprovalBundle and exact effective configuration")
		}
		report.Hashes.Proposal, report.Hashes.Bundle = bundle.ProposalHash, bundle.BundleHash
	case core.ApprovalVerifiedLegacy:
	case core.ApprovalInvalidOrAmbiguous:
		return reject("resume-authorization-mismatch", "saved approval schema is invalid or ambiguous")
	}
	var candidate config.Config
	if err := core.DecodeGraphInput(bytes.NewReader(configuration.Selection), &candidate); err != nil {
		return reject("resume-configuration-invalid", "effective configuration is incomplete")
	}
	if _, err := checkDispatchConfiguration(state.Graph, dispatch.ID, candidate); err != nil {
		return reject("resume-configuration-mismatch", "task-bound and issued configuration approvals must match")
	}
	if err := checkLifecyclePacket(&state, dispatch.ID); err != nil {
		return reject("resume-packet-invalid", "canonical packet readback or binding verification failed")
	}
	located := state.Records.RestoredDispatch(dispatch)
	located.Configuration = &core.DispatchConfiguration{Selection: configuration.Selection}
	gitCtx, cancel := context.WithTimeout(ctx, DefaultLaneTimeout)
	defer cancel()
	if dispatch.Recipient == "REVIEWER" {
		source := state.Records.RestoredDispatch(core.GraphDispatch{Instruction: core.DispatchInstruction{Worktree: flow.Contract.Worktree}})
		err = gitproof.CheckResumeReview(gitCtx, located, source.Instruction.Worktree)
	} else {
		err = gitproof.CheckResumeStart(gitCtx, located)
	}
	if err != nil {
		return reject("resume-worktree-mismatch", err.Error())
	}
	if !time.Now().UTC().Before(flow.ExecutionDeadline()) {
		return reject("resume-deadline-expired", "saved deadline expired during observation")
	}
	report.NextAuthorizedTransition = "resume the saved unstarted " + dispatch.Recipient + " Dispatch"
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
		if audit.FlowID == flow.ID && audit.Kind == "DISPATCH_ISSUED" {
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
	if !reflect.DeepEqual(basis.StagePlan, flow.StagePlan) {
		return fmt.Errorf("saved stage plan differs; a new exact plan approval is required")
	}
	issuanceGraph := state.Graph
	if dispatch.RuntimeSessionID != "" {
		issuanceGraph.Dispatches = append([]core.GraphDispatch(nil), state.Graph.Dispatches...)
		for i := range issuanceGraph.Dispatches {
			if issuanceGraph.Dispatches[i].ID == dispatch.ID {
				issuanceGraph.Dispatches[i].RuntimeSessionID = ""
			}
		}
	}
	current := taskBudgetReport(issuanceGraph, flow, saved.At)
	// Extensions have their own immutable approval audits. The original issuance
	// still binds every unchanged field, while telemetry uses effective limits.
	recorded.MaxNanoAIU = current.MaxNanoAIU
	recorded.ElapsedLimitMilliseconds = current.ElapsedLimitMilliseconds
	recorded.ResumeBasis = nil
	// Wall-clock elapsed fields are observational, not consumed counters.
	recorded.ElapsedMilliseconds, recorded.ElapsedRemainingMillis = current.ElapsedMilliseconds, current.ElapsedRemainingMillis
	normalLimits := flow.Budget.MaxDispatches == 2+2*flow.MaxRepairs && flow.Budget.MaxCoderSessions == 1+flow.MaxRepairs
	continuedLimits := flow.SourceDispatchID != "" && flow.Budget.MaxDispatches == 1+2*flow.MaxRepairs &&
		flow.Budget.MaxCoderSessions == flow.MaxRepairs
	artifactLimits := flow.Artifact != nil && flow.MaxRepairs == 0 &&
		flow.Budget.MaxDispatches == 1 && flow.Budget.MaxCoderSessions == 0
	if !reflect.DeepEqual(recorded, current) || !normalLimits && !continuedLimits && !artifactLimits ||
		flow.Budget.MaxReviewerSessions != 1+flow.MaxRepairs {
		return fmt.Errorf("saved budget, usage or counters differ from Dispatch history")
	}
	return nil
}
