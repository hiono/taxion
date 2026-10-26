package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

var ErrFlowStopped = errors.New("taxion: review flow stopped")
var ErrFlowClaimed = errors.New("taxion: review flow claimed by another runner")

type ReviewFlowRequest struct {
	ID                     string                     `json:"id"`
	TaskID                 string                     `json:"taskId"`
	ContinueFromDispatchID string                     `json:"continueFromDispatchId,omitempty"`
	Repository             string                     `json:"repository"`
	ReviewRoot             string                     `json:"reviewRoot"`
	MaxRepairs             *int                       `json:"maxRepairs"`
	MaxNanoAIU             float64                    `json:"maxNanoAiu"`
	TimeoutSeconds         int64                      `json:"timeoutSeconds"`
	Authorization          core.TaskDecision          `json:"authorization"`
	Configuration          core.ApprovalConfiguration `json:"configuration"`
}

// DefaultFlowTimeout applies when a Flow request omits timeoutSeconds.
const DefaultFlowTimeout = time.Hour

func StartReviewFlow(db *store.Store, ledgerPath string, request ReviewFlowRequest) (core.ReviewFlow, error) {
	return StartReviewFlowContext(context.Background(), db, ledgerPath, request)
}

func StartReviewFlowContext(ctx context.Context, db *store.Store, ledgerPath string, request ReviewFlowRequest) (core.ReviewFlow, error) {
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = int64(DefaultFlowTimeout / time.Second)
	}
	if db == nil || strings.TrimSpace(request.ID) == "" || strings.ContainsAny(request.ID, `/\`) ||
		request.MaxRepairs == nil || *request.MaxRepairs < 0 || request.TimeoutSeconds <= 0 ||
		math.IsNaN(request.MaxNanoAIU) || math.IsInf(request.MaxNanoAIU, 0) || request.MaxNanoAIU < 0 ||
		request.TimeoutSeconds > math.MaxInt64/int64(time.Second) ||
		!filepath.IsAbs(request.ReviewRoot) ||
		strings.TrimSpace(request.Authorization.ID) == "" || strings.TrimSpace(request.Authorization.Source) == "" ||
		strings.TrimSpace(request.Authorization.Text) == "" {
		return core.ReviewFlow{}, fmt.Errorf("taxion: review flow requires an ID, absolute review root, explicit ceilings and sourced authorization")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := checkOperation(state, ctx, request.TaskID, ""); err != nil {
			return err
		}
		task, ok := state.Graph.GraphTask(request.TaskID)
		if !state.HasGraph || !ok || task.Repo != request.Repository ||
			task.Status != core.Ready || task.Owner != "CODER" || task.Reviewer != "REVIEWER" {
			return fmt.Errorf("taxion: review flow requires an authorized READY Coder Task with independent REVIEWER")
		}
		if err := checkFlowApproval(state.Graph, task, request.Configuration); err != nil {
			return err
		}
		if request.MaxNanoAIU == 0 && !core.ExternalBudget(task.BudgetGuidance) {
			return fmt.Errorf("taxion: omitted maxNanoAiu requires approved provider billing or account guidance with reset-daily allocation")
		}
		if _, exists := state.Graph.ReviewFlow(request.ID); exists {
			return fmt.Errorf("taxion: review flow ID already exists")
		}
		for _, previous := range state.Graph.ReviewFlows {
			if previous.TaskID == task.ID && previous.Status == "RUNNING" {
				return fmt.Errorf("taxion: Task already has a running review flow")
			}
		}
		for _, dispatch := range state.Graph.Dispatches {
			if dispatch.TaskID == task.ID {
				return fmt.Errorf("taxion: initial review flow requires a Task with no previous Dispatch; preserve previous work in a sourced follow-up Task")
			}
		}
		now := time.Now().UTC()
		deadline := now.Add(time.Duration(request.TimeoutSeconds) * time.Second)
		if limit, ok := ctx.Deadline(); ok {
			if !now.Before(limit) {
				return fmt.Errorf("taxion: caller deadline reached before the review flow started")
			}
			if limit.Before(deadline) {
				deadline = limit.UTC()
			}
		}
		flow = core.ReviewFlow{
			ID: request.ID, TaskID: task.ID, SourceDispatchID: request.ContinueFromDispatchID,
			Contract: task, ReviewRoot: filepath.Clean(request.ReviewRoot),
			MaxRepairs: *request.MaxRepairs,
			Budget: core.FlowBudget{
				MaxDispatches:       2 + 2*(*request.MaxRepairs),
				MaxCoderSessions:    1 + *request.MaxRepairs,
				MaxReviewerSessions: 1 + *request.MaxRepairs,
				MaxNanoAIU:          request.MaxNanoAIU,
				AccountQuotaOnly:    request.MaxNanoAIU == 0 && core.AccountQuotaBudget(task.BudgetGuidance),
			},
			StartedAt: now, Deadline: deadline, Status: "RUNNING",
			Authorization: request.Authorization, DispatchIDs: []string{},
		}
		if request.MaxNanoAIU == 0 && core.ProviderBudget(task.BudgetGuidance) {
			flow.Budget.BudgetSource = core.ProviderStopBudgetSource
		}
		if request.ContinueFromDispatchID == "" {
			if err := issueFlowDispatch(state, ledgerPath, &flow, task, core.RelationshipNone, "", "", request.Configuration); err != nil {
				return err
			}
		} else {
			source, err := budgetContinuationSource(state.Graph, task, request.ContinueFromDispatchID, state.Records)
			if err != nil {
				return err
			}
			for _, status := range []core.TaskStatus{core.Active, core.WaitingForReview} {
				proof := core.TransitionProof{}
				if status == core.WaitingForReview {
					proof.OutputSHA = source.Result.OutputSHA
					if task.OutputMode == core.OutputModeWorktreeTreeSnapshot {
						proof.OutputTreeSHA = source.Result.OutputTreeSHA
						proof.OutputChangedFiles = source.Result.ChangedFiles
					}
					proof.Evidence = source.Result.Evidence
					proof.ActionPerformed = true
				}
				if err := state.Graph.Transition(task.ID, status, "controller", "continue the saved Coder result at independent review", proof); err != nil {
					return err
				}
			}
			decision, err := flowResultDecision(source)
			if err != nil {
				return err
			}
			if err := issueFlowReviewer(state, ledgerPath, &flow, source, decision, nil, request.Configuration); err != nil {
				return err
			}
		}
		state.Graph.ReviewFlows = append(state.Graph.ReviewFlows, flow)
		return nil
	})
	return flow, err
}

// TerminateUnstartedDispatch is the safe first half of re-contracting: it
// permanently cancels an issued, untouched Coder Dispatch and holds its flow.
// A successor Task must be explicitly approved and issued with a new hash.
func TerminateUnstartedDispatch(db *store.Store, flowID, dispatchID, actor, source, reason string) error {
	if db == nil || strings.TrimSpace(actor) == "" || strings.TrimSpace(source) == "" ||
		strings.TrimSpace(reason) == "" {
		return fmt.Errorf("taxion: terminate-and-recontract requires a store, actor, source and reason")
	}
	return stopUnstartedDispatch(db, flowID, dispatchID, actor, source, reason, "RECONTRACT")
}

func stopUnstartedDispatch(db *store.Store, flowID, dispatchID, actor, source, reason, stopCode string) error {
	reason = source + ": " + reason
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		flow, ok := state.Graph.ReviewFlow(flowID)
		if !state.HasGraph || !ok || flow.Status != "RUNNING" || flow.RunnerID != "" ||
			len(flow.DispatchIDs) == 0 || flow.DispatchIDs[len(flow.DispatchIDs)-1] != dispatchID {
			return fmt.Errorf("taxion: re-contract requires the latest Dispatch of an unclaimed running flow")
		}
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.Recipient != "CODER" || dispatch.Status != core.DispatchIssued ||
			dispatch.Acknowledged || dispatch.ExecutionStarted || dispatch.RuntimeSessionID != "" {
			return fmt.Errorf("taxion: re-contract may terminate only an untouched issued Coder Dispatch")
		}
		result := &core.DispatchResult{
			DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
			Repository: dispatch.Repository, Status: core.DispatchCancelled,
			StartedSHA: dispatch.ExpectedStartSHA(), ChangedFiles: []string{},
			Evidence: []string{"not-started:" + dispatch.ID}, Blockers: []string{reason},
			NextTransition: core.Blocked,
		}
		if err := state.Graph.TransitionDispatch(dispatchID, core.DispatchCancelled, actor, reason, false, false, result); err != nil {
			return err
		}
		if err := holdReviewFlowForStop(&state.Graph, &flow, reason); err != nil {
			return err
		}
		flow.StopCode, flow.Status, flow.Reason = stopCode, "STOPPED", reason
		storeFlow(state, flow)
		kind := stopCode
		if kind != "RECONTRACT" {
			kind = "BUDGET_STOP"
		}
		return appendBudgetAudit(state, flow, kind, taskBudgetReport(state.Graph, flow, time.Now().UTC()))
	})
}

func budgetContinuationSource(graph core.TaskGraph, task core.GraphTask, sourceID string, records ...store.Records) (core.GraphDispatch, error) {
	source, ok := graph.GraphDispatch(sourceID)
	if !ok {
		return core.GraphDispatch{}, fmt.Errorf("taxion: budget continuation requires a saved Coder result")
	}
	var predecessor core.ReviewFlow
	found := false
	for _, flow := range graph.ReviewFlows {
		if !slices.Contains(flow.DispatchIDs, sourceID) {
			continue
		}
		predecessor, found = flow, true
		break
	}
	if !found || !graph.HasBudgetContinuationSource(predecessor, source.ID) ||
		task.CreatedFrom != "flow:"+predecessor.ID || task.Supersedes != predecessor.TaskID {
		return core.GraphDispatch{}, fmt.Errorf("taxion: completed-Coder continuation requires the exact held stopped predecessor flow and successor Task")
	}
	located := source
	if len(records) != 0 {
		located = records[0].RestoredDispatch(source)
	}
	if err := gitproof.VerifyCompletedSuccessor(located, task); err != nil {
		return core.GraphDispatch{}, fmt.Errorf("taxion: completed Coder result cannot be continued safely: %w", err)
	}
	return source, nil
}

// AdvanceReviewFlow consumes only the latest stored result. A repeated call
// returns the already-issued Dispatch, so retries never duplicate a run.
func AdvanceReviewFlow(db *store.Store, ledgerPath, id string, configuration ...core.ApprovalConfiguration) (core.ReviewFlow, error) {
	var selected core.ApprovalConfiguration
	if len(configuration) != 0 {
		selected = configuration[0]
	}
	return advanceReviewFlow(db, ledgerPath, id, "", selected)
}

func advanceReviewFlow(db *store.Store, ledgerPath, id, runnerID string, configuration core.ApprovalConfiguration) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: review flow requires a store")
	}
	if runnerID == "" {
		state, err := db.LoadWorkspace()
		if err != nil {
			return core.ReviewFlow{}, err
		}
		flow, _ := state.Graph.ReviewFlow(id)
		if err := checkOperation(&state, context.Background(), flow.TaskID, id); err != nil {
			return core.ReviewFlow{}, err
		}
	}
	if err := preparePacketValidator(db, id, runnerID); err != nil {
		return core.ReviewFlow{}, err
	}
	var flow core.ReviewFlow
	var stopped error
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var ok bool
		flow, ok = state.Graph.ReviewFlow(id)
		if !state.HasGraph || !ok {
			return fmt.Errorf("taxion: review flow %q not found", id)
		}
		if flow.RunnerID != runnerID {
			return fmt.Errorf("%w: %s", ErrFlowClaimed, flow.RunnerID)
		}
		for _, id := range flow.DispatchIDs {
			dispatch, _ := state.Graph.GraphDispatch(id)
			if err := validateResultReceipt(state, dispatch); err != nil {
				return err
			}
		}
		if flow.Status != "RUNNING" {
			if flow.Status == "COMPLETED" {
				if err := revalidateFlowResult(state.Graph, flow, state.Records); err != nil {
					return fmt.Errorf("taxion: completed checkpoint revalidation: %w", err)
				}
			}
			if flow.Status == "STOPPED" {
				stopped = fmt.Errorf("%w: %s", ErrFlowStopped, flow.Reason)
			}
			return nil
		}
		stopWithCode := func(reason, code string) error {
			if err := holdReviewFlowForStop(&state.Graph, &flow, reason); err != nil {
				return err
			}
			flow.StopCode = code
			flow.Status, flow.Reason = "STOPPED", reason
			flow.StopCause = flowStopCause(flow)
			storeFlow(state, flow)
			if code == "BUDGET_STOP" {
				report := taskBudgetReport(state.Graph, flow, time.Now().UTC())
				if err := appendBudgetAudit(state, flow, code, report); err != nil {
					return err
				}
			}
			stopped = fmt.Errorf("%w: %s", ErrFlowStopped, reason)
			return nil
		}
		stop := func(reason string) error { return stopWithCode(reason, "SAFETY_STOP") }
		task, _ := state.Graph.GraphTask(flow.TaskID)
		last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		if !time.Now().UTC().Before(flow.Deadline) {
			return stopWithCode("deadline reached; reconcile and safely stop any active Dispatch before further authorization", "BUDGET_STOP")
		}
		if !flowContractMatches(state.Graph, flow, task) {
			return stop("Task scope, acceptance, authorization or execution contract changed; Human approval of a new flow is required")
		}
		if err := state.Graph.CheckTaskSpecificationUse(task.ID, "runtime"); err != nil {
			return stop("specification source or permission requires renewed Human approval: " + err.Error())
		}
		if slices.Contains([]core.TaskStatus{core.Cancelled, core.Rejected, core.Superseded}, task.Status) {
			return stop("Task ended with " + string(task.Status) + "; reconcile any active Dispatch")
		}
		if task.Status == core.Blocked && active(last.Status) {
			return stop("Task is BLOCKED while its Dispatch is active; inspect the saved blocker and safely stop execution")
		}
		if task.Status == core.HumanDecisionRequired || task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "" {
			return stop("Human decision required; present the Task's saved question and options")
		}
		if last.Status == core.DispatchSuspended || last.Status == core.DispatchCancelRequested {
			return stop("Dispatch " + last.ID + " requires safe stop or renewed authorization before continuation")
		}
		context, contextErr := memory.ResolveDispatchContext(ledgerPath, last.Instruction.MemoryRefs)
		if contextErr != nil {
			return fmt.Errorf("taxion: required flow Memory: %w", contextErr)
		}
		if !reflect.DeepEqual(context, last.Instruction.MemoryContext) {
			return stop("required Memory differs from the issued snapshot; confirm the updated decision with Human")
		}
		if flow.HandoffHeld() && !last.ExecutionStarted && active(last.Status) {
			stopped = fmt.Errorf("%w: %s", core.ErrHandoffHeld, flow.ID)
			return nil
		}
		if active(last.Status) {
			if reason := activeBudgetStop(taskBudgetReport(state.Graph, flow, time.Now().UTC())); reason != "" {
				return stopWithCode(reason, "BUDGET_STOP")
			}
			return nil
		}
		if last.Status != core.DispatchSucceeded || last.Result == nil {
			return stop("Dispatch " + last.ID + " ended with " + string(last.Status) + "; inspect its saved blockers and stop evidence")
		}
		if last.Instruction.Version < core.DispatchInstructionVersion || last.Result.GitEvidence == nil {
			return stop("latest result lacks current controller-observed Git evidence")
		}
		if err := last.ValidateResult(last.Status, last.Result); err != nil {
			return stop("latest result failed contract validation: " + err.Error())
		}
		if err := revalidateFlowResult(state.Graph, flow, state.Records); err != nil {
			return stop("result Git revalidation failed: " + err.Error())
		}
		if err := recordPacketValidation(state, flow, last); err != nil {
			return err
		}
		if flow.HandoffHeld() {
			stopped = fmt.Errorf("%w: %s", core.ErrHandoffHeld, flow.ID)
			return nil
		}
		if err := state.Graph.CheckApprovedDraft(flow); err != nil {
			return stop("approved draft no longer matches the handoff: " + err.Error())
		}
		var supplement *core.TaskDecision
		if flow.PendingDraftID() != "" {
			decision, err := flow.InstructionDrafts[len(flow.InstructionDrafts)-1].SupplementalDecision()
			if err != nil {
				return err
			}
			supplement = &decision
		}
		sourceDecision, err := flowResultDecision(last)
		if err != nil {
			return err
		}
		if last.Recipient == "CODER" {
			if task.Status != core.Blocked || task.DeferredReason != "Dispatch ended: SUCCEEDED" {
				return stop("Coder handoff requires the recorded completion state; inspect the Task's blocker or decision")
			}
			task.DeferredReason, task.ResumeCondition = "", ""
			if err := state.Graph.Track(task, "controller", "restore completed Coder contract for evidence handoff"); err != nil {
				return err
			}
			for _, status := range []core.TaskStatus{core.Ready, core.Active, core.WaitingForReview} {
				proof := core.TransitionProof{}
				if status == core.WaitingForReview {
					proof.OutputSHA = last.Result.OutputSHA
					if task.OutputMode == core.OutputModeWorktreeTreeSnapshot {
						proof.OutputTreeSHA = last.Result.OutputTreeSHA
						proof.OutputChangedFiles = last.Result.ChangedFiles
					}
					proof.Evidence = last.Result.Evidence
					proof.ActionPerformed = true
				}
				if err := state.Graph.Transition(task.ID, status, "controller", "verified Coder result handoff", proof); err != nil {
					return err
				}
			}
			if err := issueFlowReviewer(state, ledgerPath, &flow, last, sourceDecision, supplement, configuration); err != nil {
				return err
			}
		} else {
			if !last.Result.GitEvidence.WorktreeReleased || task.Status != core.WaitingForReview ||
				last.Result.OutputSHA != task.TargetSHA {
				return stop("review requires released worktree and the current Task target SHA")
			}
			switch last.Result.ReviewVerdict {
			case "APPROVE":
				proof := core.TransitionProof{
					ActionPerformed: true, Evidence: last.Result.Evidence,
					Acceptance: flow.Contract.Acceptance, ReviewedSHA: last.Result.OutputSHA,
					Reviewer: "REVIEWER",
				}
				if task.OutputMode == core.OutputModeWorktreeTreeSnapshot {
					proof.ReviewedTreeSHA = last.Result.OutputTreeSHA
				}
				for _, status := range []core.TaskStatus{core.Approved, core.Completed} {
					if err := state.Graph.Transition(task.ID, status, "controller", "independent exact-SHA review approved", proof); err != nil {
						return err
					}
				}
				flow.Status, flow.Reason = "COMPLETED", "independent review and acceptance verified for "+task.TargetSHA
			case "REQUEST_CHANGES":
				if flow.Repairs >= flow.MaxRepairs {
					return stopWithCode("repair ceiling reached; present saved findings and acceptance decisions to Human", "BUDGET_STOP")
				}
				for _, status := range []core.TaskStatus{core.RequestChanges, core.Blocked} {
					if err := state.Graph.Transition(task.ID, status, "controller", "review requested changes", core.TransitionProof{}); err != nil {
						return err
					}
				}
				task, _ = state.Graph.GraphTask(task.ID)
				task.Decisions = append(task.Decisions, sourceDecision)
				if supplement != nil {
					task.Decisions = append(task.Decisions, *supplement)
				}
				task.DeferredReason, task.ResumeCondition = "", ""
				if err := state.Graph.Track(task, "controller", "preserve findings and acceptance for authorized repair"); err != nil {
					return err
				}
				if err := state.Graph.Transition(task.ID, core.Ready, "controller", "repair authorized within flow ceiling", core.TransitionProof{}); err != nil {
					return err
				}
				flow.Repairs++
				previousCoder := flow.SourceDispatchID
				if len(flow.DispatchIDs) > 1 {
					previousCoder = flow.DispatchIDs[len(flow.DispatchIDs)-2]
				}
				if err := issueFlowDispatch(state, ledgerPath, &flow, task, core.RelationshipAmends, previousCoder, "repair the saved independent-review findings with the same acceptance", configuration); err != nil {
					return err
				}
			default:
				return stop("Reviewer is BLOCKED; inspect saved acceptance decisions and findings with Human")
			}
		}
		storeFlow(state, flow)
		return nil
	})
	var entryErr *core.EntryCheckError
	if err != nil && flow.ID != "" && !errors.Is(err, ErrFlowClaimed) && !errors.Is(err, core.ErrHandoffHeld) && !errors.As(err, &entryErr) {
		saveErr := db.UpdateWorkspace(func(state *store.Workspace) error {
			stored, ok := state.Graph.ReviewFlow(id)
			if !ok || stored.Status != "RUNNING" {
				return nil
			}
			reason := "advance failed: " + err.Error()
			if holdErr := holdReviewFlowForStop(&state.Graph, &stored, reason); holdErr != nil {
				return holdErr
			}
			if stored.StopCode == "" {
				var budgetErr *budgetStopError
				if errors.As(err, &budgetErr) {
					stored.StopCode = "BUDGET_STOP"
				} else {
					stored.StopCode = "SAFETY_STOP"
				}
			}
			stored.Status, stored.Reason = "STOPPED", reason
			storeFlow(state, stored)
			if stored.StopCode == "BUDGET_STOP" {
				report := taskBudgetReport(state.Graph, stored, time.Now().UTC())
				if auditErr := appendBudgetAudit(state, stored, "BUDGET_STOP", report); auditErr != nil {
					return auditErr
				}
			}
			flow = stored
			return nil
		})
		return flow, errors.Join(err, saveErr)
	}
	return flow, errors.Join(err, stopped)
}

func issueFlowDispatch(state *store.Workspace, ledgerPath string, flow *core.ReviewFlow, task core.GraphTask,
	relationship core.DispatchRelationship, relatedID, reason string, configuration core.ApprovalConfiguration) error {
	if err := memory.CheckDispatch(ledgerPath, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo}, task.Repo, task.MemoryRefs...); err != nil {
		return err
	}
	context, err := memory.ResolveDispatchContext(ledgerPath, task.MemoryRefs)
	if err != nil {
		return err
	}
	dispatchID := fmt.Sprintf("%s-dispatch-%d", flow.ID, len(flow.DispatchIDs))
	var stage core.ApprovalEntryStage
	switch relationship {
	case core.RelationshipAmends:
		if task.Owner != "CODER" || flow.Repairs == 0 {
			return fmt.Errorf("taxion: inconsistent repair entry for Task owner %q with %d repairs", task.Owner, flow.Repairs)
		}
		stage = core.EntryRepairDispatch
	case core.RelationshipIndependent:
		if task.Owner != "REVIEWER" {
			return fmt.Errorf("taxion: inconsistent review entry for Task owner %q", task.Owner)
		}
		stage = core.EntryReviewerDispatch
		if flow.Repairs > 0 {
			stage = core.EntryReReviewDispatch
		}
	case core.RelationshipNone:
		switch task.Owner {
		case "CODER":
			stage = core.EntryCoderDispatch
		case "REVIEWER":
			stage = core.EntryReviewerDispatch
		default:
			return fmt.Errorf("taxion: cannot classify dispatch entry for Task owner %q", task.Owner)
		}
	default:
		return fmt.Errorf("taxion: cannot classify dispatch entry for relationship %q", relationship)
	}
	if err := checkEntryApproval(state.Graph, task, stage, configuration); err != nil {
		return err
	}
	dispatch, err := core.NewGraphDispatch(state.Graph, dispatchID, task.ID, task.Repo, relationship, relatedID, reason, context)
	if err != nil {
		return err
	}
	if err := requireBudgetRoom(state.Graph, *flow, dispatch.Recipient); err != nil {
		return err
	}
	if err := issueLifecyclePacket(state, *flow, dispatch); err != nil {
		return err
	}
	if err := state.Graph.IssueDispatch(dispatch, "controller", "bounded review flow issued instruction"); err != nil {
		return err
	}
	state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
		ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true, CreatedAt: time.Now().UTC(),
	})
	flow.DispatchIDs = append(flow.DispatchIDs, dispatch.ID)
	if err := appendBudgetAudit(state, *flow, "DISPATCH_ISSUED", taskBudgetReport(state.Graph, *flow, time.Now().UTC())); err != nil {
		return err
	}
	if flow.PendingDraftID() != "" {
		index := len(flow.InstructionDrafts) - 1
		draft := &flow.InstructionDrafts[index]
		if draft.Approval == nil || draft.NextRole != dispatch.Recipient {
			return fmt.Errorf("taxion: issued instruction cannot consume an unapproved or mismatched draft")
		}
		draft.Publication = &core.DraftPublication{
			DispatchID: dispatch.ID, InstructionHash: dispatch.InstructionHash, At: time.Now().UTC(),
		}
	}
	return nil
}

func flowResultDecision(dispatch core.GraphDispatch) (core.TaskDecision, error) {
	result, err := json.Marshal(dispatch.Result)
	if err != nil {
		return core.TaskDecision{}, err
	}
	value, err := json.Marshal(struct {
		Kind            string `json:"kind"`
		DispatchID      string `json:"dispatchId"`
		InstructionHash string `json:"instructionHash"`
		ResultHash      string `json:"resultHash"`
	}{"taxion.result-ref.v1", dispatch.ID, dispatch.InstructionHash, receiptHash(string(result))})
	if err != nil {
		return core.TaskDecision{}, err
	}
	return core.TaskDecision{ID: "result:" + dispatch.ID, Source: "dispatch:" + dispatch.ID, Text: string(value)}, nil
}

func matchesFlowResultDecision(dispatch core.GraphDispatch, decision core.TaskDecision) bool {
	expected, err := flowResultDecision(dispatch)
	if err == nil && decision == expected {
		return true
	}
	legacy, err := legacyFlowResultDecision(dispatch)
	return err == nil && decision == legacy
}

func legacyFlowResultDecision(dispatch core.GraphDispatch) (core.TaskDecision, error) {
	value, err := json.Marshal(struct {
		Instruction core.DispatchInstruction `json:"instruction"`
		Hash        string                   `json:"instructionHash"`
		Result      *core.DispatchResult     `json:"result"`
	}{dispatch.Instruction, dispatch.InstructionHash, dispatch.Result})
	if err != nil {
		return core.TaskDecision{}, fmt.Errorf("taxion: encode flow result provenance: %w", err)
	}
	return core.TaskDecision{ID: "result:" + dispatch.ID, Source: "dispatch:" + dispatch.ID, Text: string(value)}, nil
}

func issueFlowReviewer(state *store.Workspace, ledgerPath string, flow *core.ReviewFlow,
	coder core.GraphDispatch, sourceDecision core.TaskDecision, supplement *core.TaskDecision,
	configuration core.ApprovalConfiguration) error {
	review := flow.Contract
	review.ID = fmt.Sprintf("%s-review-%d", flow.ID, flow.Repairs)
	review.Title = "Review: " + flow.Contract.Title
	review.Type, review.Owner, review.Reviewer, review.Status = "REVIEW", "REVIEWER", "", core.Draft
	review.ParentID, review.Supersedes, review.DependsOn = "", "", nil
	review.CreatedFrom = "dispatch:" + coder.ID
	if flow.SourceDispatchID == coder.ID {
		review.CreatedFrom = "flow:" + flow.ID
	}
	review.BaselineSHA, review.TargetSHA = coder.Result.OutputSHA, coder.Result.OutputSHA
	review.OutputMode = flow.Contract.OutputMode
	if review.OutputMode == core.OutputModeWorktreeTreeSnapshot {
		review.TargetTreeSHA = coder.Result.OutputTreeSHA
		review.TargetChangedFiles = slices.Clone(coder.Result.ChangedFiles)
	}
	review.Worktree = filepath.Join(flow.ReviewRoot, review.ID)
	review.AuthorizationID = review.ID + "-authorization"
	decisions := coder.Instruction.Decisions
	if flow.SourceDispatchID == coder.ID {
		decisions = flow.Contract.Decisions
	}
	review.Decisions = append(slices.Clone(decisions), core.TaskDecision{
		ID: review.AuthorizationID, Source: flow.Authorization.Source,
		Text: flow.Authorization.Text + "; independently review the recorded Coder result",
	}, sourceDecision)
	if flow.SourceDispatchID == coder.ID {
		for _, predecessor := range state.Graph.ReviewFlows {
			if "flow:"+predecessor.ID != flow.Contract.CreatedFrom {
				continue
			}
			progress, err := reviewProgressDecision(*state, predecessor, coder)
			if err != nil {
				return err
			}
			review.Decisions = append(review.Decisions, progress)
		}
	}
	review.HumanDecision = nil
	if supplement != nil {
		review.Decisions = append(review.Decisions, *supplement)
	}
	if err := prepareLifecycleReviewer(state, *flow, coder, &review, configuration); err != nil {
		return err
	}
	if err := state.Graph.Track(review, "controller", "independent review authorized by flow"); err != nil {
		return err
	}
	state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
		ID: review.ID, Title: review.Title, Repo: review.Repo, CreatedAt: time.Now().UTC(),
	})
	if err := state.Graph.Transition(review.ID, core.Ready, "controller", "review verified output SHA", core.TransitionProof{}); err != nil {
		return err
	}
	if err := approveLifecycleReviewer(state, *flow, review.ID, configuration); err != nil {
		return err
	}
	return issueFlowDispatch(state, ledgerPath, flow, review, core.RelationshipIndependent,
		coder.ID, "review the verified Coder output", configuration)
}

func holdReviewFlowForStop(graph *core.TaskGraph, flow *core.ReviewFlow, reason string) error {
	if flow.HandoffHeld() {
		return nil
	}
	if len(flow.DispatchIDs) == 0 {
		return fmt.Errorf("taxion: cannot hold a stopped flow without a Dispatch")
	}
	expected := ""
	if len(flow.HandoffChanges) != 0 {
		expected = flow.HandoffChanges[len(flow.HandoffChanges)-1].ID
	}
	held, err := graph.ChangeHandoff(flow.ID, core.HandoffChange{
		ID:               "flow-stop:" + flow.ID + ":" + flow.DispatchIDs[len(flow.DispatchIDs)-1],
		ExpectedChangeID: expected, Action: "HOLD", Actor: "controller",
		Source: "controller:flow-stop", Reason: reason,
	})
	if err != nil {
		return err
	}
	*flow = held
	return nil
}

func flowContractMatches(graph core.TaskGraph, flow core.ReviewFlow, current core.GraphTask) bool {
	approved := flow.Contract
	current.Status, current.TargetSHA = approved.Status, approved.TargetSHA
	current.TargetTreeSHA = approved.TargetTreeSHA
	current.TargetChangedFiles = slices.Clone(approved.TargetChangedFiles)
	current.DeferredReason, current.ResumeCondition, current.NextAction = approved.DeferredReason, approved.ResumeCondition, approved.NextAction
	for _, decision := range approved.Decisions {
		if !slices.Contains(current.Decisions, decision) {
			return false
		}
	}
	for _, decision := range current.Decisions {
		if slices.Contains(approved.Decisions, decision) {
			continue
		}
		recognized := false
		for _, draft := range flow.InstructionDrafts {
			if draft.Publication == nil || draft.NextRole != "CODER" {
				continue
			}
			expected, err := draft.SupplementalDecision()
			if err == nil && decision == expected {
				recognized = true
				break
			}
		}
		for _, id := range flow.DispatchIDs {
			dispatch, _ := graph.GraphDispatch(id)
			if dispatch.Result == nil {
				continue
			}
			if matchesFlowResultDecision(dispatch, decision) {
				recognized = true
				break
			}
		}
		if !recognized {
			return false
		}
	}
	current.Decisions = approved.Decisions
	return reflect.DeepEqual(approved, current)
}

func flowTokens(graph core.TaskGraph, flow core.ReviewFlow) (int64, bool) {
	var total int64
	known := true
	for _, id := range flow.DispatchIDs {
		dispatch, _ := graph.GraphDispatch(id)
		if !active(dispatch.Status) && len(dispatch.RuntimeUsage) == 0 {
			known = false
		}
		for _, usage := range dispatch.RuntimeUsage {
			if usage.InputTokens == nil || usage.OutputTokens == nil || usage.Scope == "latest-assistant-message" {
				known = false
			}
			for _, value := range []*int64{usage.InputTokens, usage.OutputTokens} {
				if value != nil {
					if *value > math.MaxInt64-total {
						return math.MaxInt64, known
					}
					total += *value
				}
			}
		}
	}
	return total, known
}

func flowNanoAIU(graph core.TaskGraph, flow core.ReviewFlow) (*float64, string) {
	var total float64
	seen, complete := false, true
	for _, id := range flow.DispatchIDs {
		dispatch, _ := graph.GraphDispatch(id)
		for _, usage := range dispatch.RuntimeUsage {
			if usage.TotalNanoAIU == nil || math.IsNaN(*usage.TotalNanoAIU) ||
				math.IsInf(*usage.TotalNanoAIU, 0) || *usage.TotalNanoAIU < 0 {
				complete = false
				continue
			}
			total += *usage.TotalNanoAIU
			seen = true
			if usage.AICreditsStatus == nil || *usage.AICreditsStatus != "complete" {
				complete = false
			}
		}
	}
	if !seen {
		return nil, "unavailable"
	}
	if !complete {
		return &total, "partial"
	}
	return &total, "complete"
}

func storeFlow(state *store.Workspace, flow core.ReviewFlow) {
	for index := range state.Graph.ReviewFlows {
		if state.Graph.ReviewFlows[index].ID == flow.ID {
			state.Graph.ReviewFlows[index] = flow
		}
	}
}

// checkEntryApproval enforces the contract gate at every execution entry point.
func checkEntryApproval(graph core.TaskGraph, task core.GraphTask, stage core.ApprovalEntryStage, configuration core.ApprovalConfiguration) error {
	switch graph.ApprovalRequirement() {
	case core.ApprovalBundleRequired:
		if len(configuration.Selection) == 0 {
			return fmt.Errorf("taxion: approval configuration is required for %s; re-approval is required", stage)
		}
		_, err := graph.CheckBundleAtEntry(task.ID, stage, configuration)
		return err
	case core.ApprovalVerifiedLegacy:
		return nil
	default:
		return fmt.Errorf("taxion: approval schema is ambiguous; re-approval is required before %s", stage)
	}
}

// checkFlowApproval enforces the contract gate before any flow side effect.
func checkFlowApproval(graph core.TaskGraph, task core.GraphTask, configuration core.ApprovalConfiguration) error {
	return checkEntryApproval(graph, task, core.EntryStartReviewFlow, configuration)
}
