package controller

import (
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
	ID                     string            `json:"id"`
	TaskID                 string            `json:"taskId"`
	ContinueFromDispatchID string            `json:"continueFromDispatchId,omitempty"`
	Repository             string            `json:"repository"`
	ReviewRoot             string            `json:"reviewRoot"`
	MaxRepairs             *int              `json:"maxRepairs"`
	MaxTokens              int64             `json:"maxTokens"`
	TimeoutSeconds         int64             `json:"timeoutSeconds"`
	Authorization          core.TaskDecision `json:"authorization"`
}

func StartReviewFlow(db *store.Store, ledgerPath string, request ReviewFlowRequest) (core.ReviewFlow, error) {
	if db == nil || strings.TrimSpace(request.ID) == "" || strings.ContainsAny(request.ID, `/\`) ||
		request.MaxRepairs == nil || *request.MaxRepairs < 0 || request.MaxTokens <= 0 || request.TimeoutSeconds <= 0 ||
		request.TimeoutSeconds > math.MaxInt64/int64(time.Second) ||
		!filepath.IsAbs(request.ReviewRoot) ||
		strings.TrimSpace(request.Authorization.ID) == "" || strings.TrimSpace(request.Authorization.Source) == "" ||
		strings.TrimSpace(request.Authorization.Text) == "" {
		return core.ReviewFlow{}, fmt.Errorf("taxion: review flow requires an ID, absolute review root, explicit ceilings and sourced authorization")
	}
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		task, ok := state.Graph.GraphTask(request.TaskID)
		if !state.HasGraph || !ok || task.Repo != request.Repository ||
			task.Status != core.Ready || task.Owner != "CODER" || task.Reviewer != "REVIEWER" {
			return fmt.Errorf("taxion: review flow requires an authorized READY Coder Task with independent REVIEWER")
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
		flow = core.ReviewFlow{
			ID: request.ID, TaskID: task.ID, SourceDispatchID: request.ContinueFromDispatchID,
			Contract: task, ReviewRoot: filepath.Clean(request.ReviewRoot),
			MaxRepairs: *request.MaxRepairs, MaxTokens: request.MaxTokens,
			StartedAt: now, Deadline: now.Add(time.Duration(request.TimeoutSeconds) * time.Second), Status: "RUNNING",
			Authorization: request.Authorization, DispatchIDs: []string{},
		}
		if request.ContinueFromDispatchID == "" {
			if err := issueFlowDispatch(state, ledgerPath, &flow, task, core.RelationshipNone, "", ""); err != nil {
				return err
			}
		} else {
			source, err := budgetContinuationSource(state.Graph, task, request.ContinueFromDispatchID)
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
			if err := issueFlowReviewer(state, ledgerPath, &flow, source, decision, nil); err != nil {
				return err
			}
		}
		state.Graph.ReviewFlows = append(state.Graph.ReviewFlows, flow)
		return nil
	})
	return flow, err
}

func budgetContinuationSource(graph core.TaskGraph, task core.GraphTask, sourceID string) (core.GraphDispatch, error) {
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
	if !found || predecessor.Status != "STOPPED" || predecessor.StopCode != "BUDGET_LIMIT" ||
		predecessor.RunnerID != "" || !predecessor.HandoffHeld() ||
		len(predecessor.DispatchIDs) == 0 || predecessor.DispatchIDs[len(predecessor.DispatchIDs)-1] != source.ID ||
		task.CreatedFrom != "flow:"+predecessor.ID || task.Supersedes != predecessor.TaskID {
		return core.GraphDispatch{}, fmt.Errorf("taxion: budget continuation requires the exact held predecessor flow and successor Task")
	}
	if err := gitproof.VerifyCompletedSuccessor(source, task); err != nil {
		return core.GraphDispatch{}, fmt.Errorf("taxion: completed Coder result cannot be continued safely: %w", err)
	}
	return source, nil
}

// AdvanceReviewFlow consumes only the latest stored result. A repeated call
// returns the already-issued Dispatch, so retries never duplicate a run.
func AdvanceReviewFlow(db *store.Store, ledgerPath, id string) (core.ReviewFlow, error) {
	return advanceReviewFlow(db, ledgerPath, id, "")
}

func advanceReviewFlow(db *store.Store, ledgerPath, id, runnerID string) (core.ReviewFlow, error) {
	if db == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: review flow requires a store")
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
				if err := revalidateFlowResult(state.Graph, flow); err != nil {
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
			storeFlow(state, flow)
			stopped = fmt.Errorf("%w: %s", ErrFlowStopped, reason)
			return nil
		}
		stop := func(reason string) error { return stopWithCode(reason, "SAFETY_STOP") }
		stopForBudget := func(tokens int64) error {
			overage := tokens - flow.MaxTokens
			if overage < 0 {
				overage = 0
			}
			reason := fmt.Sprintf(
				"recorded token usage %d against ceiling %d (over by %d); Human approval of a new budget is required",
				tokens, flow.MaxTokens, overage,
			)
			return stopWithCode(reason, "BUDGET_LIMIT")
		}
		task, _ := state.Graph.GraphTask(flow.TaskID)
		last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		if !time.Now().UTC().Before(flow.Deadline) {
			return stop("deadline reached; reconcile and safely stop any active Dispatch before further authorization")
		}
		if !flowContractMatches(state.Graph, flow, task) {
			return stop("Task scope, acceptance, authorization or execution contract changed; Human approval of a new flow is required")
		}
		if err := state.Graph.CheckTaskSpecificationUse(task.ID, "copilot"); err != nil {
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
		tokens, known := flowTokens(state.Graph, flow)
		completedApproval := last.Status == core.DispatchSucceeded && last.Recipient == "REVIEWER" &&
			last.Result != nil && last.Result.ReviewVerdict == "APPROVE"
		if tokens >= flow.MaxTokens && !completedApproval {
			return stopForBudget(tokens)
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
			if !known && len(last.RuntimeUsage) != 0 {
				return stop("runtime reported incomplete input/output usage; safely stop and obtain usage evidence")
			}
			return nil
		}
		if last.Status != core.DispatchSucceeded || last.Result == nil {
			return stop("Dispatch " + last.ID + " ended with " + string(last.Status) + "; inspect its saved blockers and stop evidence")
		}
		if !known {
			return stop("runtime input/output usage is unknown; obtain usage evidence before authorizing further execution")
		}
		if last.Instruction.Version < core.DispatchInstructionVersion || last.Result.GitEvidence == nil {
			return stop("latest result lacks current controller-observed Git evidence")
		}
		if err := last.ValidateResult(last.Status, last.Result); err != nil {
			return stop("latest result failed contract validation: " + err.Error())
		}
		if err := revalidateFlowResult(state.Graph, flow); err != nil {
			return stop("result Git revalidation failed: " + err.Error())
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
			if err := issueFlowReviewer(state, ledgerPath, &flow, last, sourceDecision, supplement); err != nil {
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
					return stop("repair ceiling reached; present saved findings and acceptance decisions to Human")
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
				if err := issueFlowDispatch(state, ledgerPath, &flow, task, core.RelationshipAmends, previousCoder, "repair the saved independent-review findings with the same acceptance"); err != nil {
					return err
				}
			default:
				return stop("Reviewer is BLOCKED; inspect saved acceptance decisions and findings with Human")
			}
		}
		storeFlow(state, flow)
		return nil
	})
	if err != nil && flow.ID != "" && !errors.Is(err, ErrFlowClaimed) && !errors.Is(err, core.ErrHandoffHeld) {
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
				stored.StopCode = "SAFETY_STOP"
			}
			stored.Status, stored.Reason = "STOPPED", reason
			storeFlow(state, stored)
			flow = stored
			return nil
		})
		return flow, errors.Join(err, saveErr)
	}
	return flow, errors.Join(err, stopped)
}

func issueFlowDispatch(state *store.Workspace, ledgerPath string, flow *core.ReviewFlow, task core.GraphTask,
	relationship core.DispatchRelationship, relatedID, reason string) error {
	if err := memory.CheckDispatch(ledgerPath, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo}, task.Repo, task.MemoryRefs...); err != nil {
		return err
	}
	context, err := memory.ResolveDispatchContext(ledgerPath, task.MemoryRefs)
	if err != nil {
		return err
	}
	dispatchID := fmt.Sprintf("%s-dispatch-%d", flow.ID, len(flow.DispatchIDs))
	dispatch, err := core.NewGraphDispatch(state.Graph, dispatchID, task.ID, task.Repo, relationship, relatedID, reason, context)
	if err != nil {
		return err
	}
	if err := state.Graph.IssueDispatch(dispatch, "controller", "bounded review flow issued instruction"); err != nil {
		return err
	}
	state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
		ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true, CreatedAt: time.Now().UTC(),
	})
	flow.DispatchIDs = append(flow.DispatchIDs, dispatch.ID)
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
	coder core.GraphDispatch, sourceDecision core.TaskDecision, supplement *core.TaskDecision) error {
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
	review.Decisions = append(slices.Clone(coder.Instruction.Decisions), core.TaskDecision{
		ID: review.AuthorizationID, Source: flow.Authorization.Source,
		Text: flow.Authorization.Text + "; independently review the recorded Coder result",
	}, sourceDecision)
	review.HumanDecision = nil
	if supplement != nil {
		review.Decisions = append(review.Decisions, *supplement)
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
	return issueFlowDispatch(state, ledgerPath, flow, review, core.RelationshipIndependent,
		coder.ID, "review the verified Coder output")
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
			expected, err := flowResultDecision(dispatch)
			if err == nil && decision == expected {
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
			if usage.InputTokens == nil || usage.OutputTokens == nil {
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

func storeFlow(state *store.Workspace, flow core.ReviewFlow) {
	for index := range state.Graph.ReviewFlows {
		if state.Graph.ReviewFlows[index].ID == flow.ID {
			state.Graph.ReviewFlows[index] = flow
		}
	}
}
