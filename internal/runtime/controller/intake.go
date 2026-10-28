package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

type IntakeRequest struct {
	ReconciliationHash      string         `json:"reconciliationHash,omitempty"`
	Request                 string         `json:"request"`
	Task                    core.GraphTask `json:"task"`
	Actor                   string         `json:"actor"`
	Source                  string         `json:"source"`
	PlanningSelection       *config.Config `json:"planningSelection,omitempty"`
	PlanningReason          string         `json:"planningReason,omitempty"`
	ConfigurationDecisionID string         `json:"configurationDecisionId,omitempty"`
}

type IntakeConfirmation struct {
	ReconciliationHash      string               `json:"reconciliationHash,omitempty"`
	TaskID                  string               `json:"taskId"`
	ExpectedHash            string               `json:"expectedHash"`
	Actor                   string               `json:"actor"`
	Source                  string               `json:"source"`
	Reason                  string               `json:"reason"`
	Selection               config.Config        `json:"selection"`
	AdditionalTaskIDs       []string             `json:"additionalTaskIds"`
	ConfigurationDecisionID string               `json:"configurationDecisionId,omitempty"`
	ReplacesFlow            string               `json:"replacesFlow,omitempty"`
	Target                  *core.FeedbackTarget `json:"target,omitempty"`
	RequestOperationID      string               `json:"requestOperationId,omitempty"`
	RequestBasisHash        string               `json:"requestBasisHash,omitempty"`
}

// VerifyRequestConfirmation reads the formal intake decision and its original
// contract. A checkpoint's approval flag is never an authorization source.
func VerifyRequestConfirmation(graph core.TaskGraph, taskID, operationID, proposalHash, authorizationID, basisHash string, selected config.Config) error {
	task, ok := graph.GraphTask(taskID)
	if !ok || taskID != operationID || authorizationID != "intake-confirmation:"+taskID ||
		task.AuthorizationID != authorizationID || basisHash == "" {
		return fmt.Errorf("taxion: request approval identity mismatch")
	}
	var confirmation *IntakeConfirmation
	for _, decision := range task.Decisions {
		if decision.ID != authorizationID {
			continue
		}
		var record IntakeConfirmation
		if confirmation != nil || core.DecodeGraphInput(strings.NewReader(decision.Text), &record) != nil ||
			decision.Source != "human:console:"+operationID {
			return fmt.Errorf("taxion: request approval is invalid or ambiguous")
		}
		confirmation = &record
	}
	if confirmation == nil || confirmation.TaskID != taskID || confirmation.ExpectedHash != proposalHash ||
		confirmation.RequestOperationID != operationID || confirmation.RequestBasisHash != basisHash ||
		confirmation.Selection != selected || confirmation.Source != "human:console:"+operationID {
		return fmt.Errorf("taxion: request checkpoint does not match formal approval")
	}
	var draftFound bool
	var approved *core.GraphTask
	for _, event := range graph.History {
		if event.TaskID != taskID {
			continue
		}
		if event.Task.Status == core.Draft {
			hash, err := IntakeHash(event.Task)
			draftFound = draftFound || err == nil && hash == proposalHash
		}
		if event.Task.Status == core.Ready && event.Task.AuthorizationID == authorizationID && approved == nil {
			copy := event.Task
			approved = &copy
		}
	}
	if !draftFound || approved == nil {
		return fmt.Errorf("taxion: request approval lacks its original contract")
	}
	flow, exists := graph.ReviewFlow(operationID + "-flow")
	if exists {
		if flow.TaskID != taskID || !reflect.DeepEqual(flow.Contract, *approved) || !flowContractMatches(graph, flow, task) {
			return fmt.Errorf("taxion: approved request contract or execution identity drifted")
		}
	} else if !reflect.DeepEqual(task, *approved) {
		return fmt.Errorf("taxion: approved request contract drifted")
	}
	return nil
}

func IntakeHash(task core.GraphTask) (string, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

// ProposeIntake records the original request and a contract for the user to inspect.
// It never treats natural-language text as authorization or starts a worker.
func ProposeIntake(db *store.Store, request IntakeRequest) (core.GraphTask, error) {
	if db == nil || strings.TrimSpace(request.Request) == "" || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Source) == "" || request.Task.Status != core.Draft ||
		request.Task.AuthorizationID != "" || len(request.Task.Decisions) != 0 ||
		(request.Task.Owner != "CODER" && !(request.Task.Owner == "USER" && request.ReconciliationHash != "")) ||
		request.Task.Reviewer != "REVIEWER" {
		return core.GraphTask{}, fmt.Errorf("taxion: intake requires an original request, sourced DRAFT contract and independent Coder/Reviewer without authorization")
	}
	text, err := json.Marshal(struct {
		Kind    string `json:"kind"`
		Request string `json:"request"`
		Actor   string `json:"actor"`
	}{"human-request", request.Request, request.Actor})
	if err != nil {
		return core.GraphTask{}, err
	}
	task := request.Task
	if task.FileAccess == nil {
		task.FileAccess = core.DefaultFileAccessPolicy()
	}
	if task.BudgetGuidance == nil {
		mode := ""
		var selected config.Config
		if request.PlanningSelection != nil {
			mode = request.PlanningSelection.Execution.BudgetGuidance
			selected = *request.PlanningSelection
		}
		task.BudgetGuidance, err = core.NewBillingBudgetGuidance(mode, "", selected.Billing)
		if err != nil {
			return core.GraphTask{}, err
		}
	}
	task.Decisions = []core.TaskDecision{{ID: "intake:" + task.ID, Source: request.Source, Text: string(text)}}
	if request.PlanningSelection != nil {
		if strings.TrimSpace(request.PlanningReason) == "" || request.PlanningSelection.Repository.Path != task.Repo {
			return core.GraphTask{}, fmt.Errorf("taxion: isolated intake planning requires explicit configuration reason and matching repository")
		}
		selection := *request.PlanningSelection
		decisionID := request.ConfigurationDecisionID
		if decisionID == "" {
			decisionID = selection.Execution.ApprovalRef
		}
		approval, err := json.Marshal(config.Approval{
			Kind: config.ApprovalKind, TaskID: task.ID, Reason: request.PlanningReason, Allowed: []config.Config{selection},
		})
		if err != nil {
			return core.GraphTask{}, err
		}
		if err := config.CheckApproval(string(approval), task.ID, decisionID, selection); err != nil {
			return core.GraphTask{}, err
		}
		task.Decisions = append(task.Decisions, core.TaskDecision{ID: decisionID, Source: request.Source, Text: string(approval)})
	} else if request.PlanningReason != "" || request.ConfigurationDecisionID != "" {
		return core.GraphTask{}, fmt.Errorf("taxion: planning reason requires an explicit selected configuration")
	}
	var result core.GraphTask
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			state.HasGraph, state.Graph = true, core.TaskGraph{Version: 1}
		}
		predecessor, err := artifactPredecessorDecision(state.Graph, task)
		if err != nil {
			return err
		}
		if predecessor != nil {
			task.Decisions = append(task.Decisions, *predecessor)
		}
		if request.ReconciliationHash != "" {
			if err := verifyReconciledInput(*state, task, request.ReconciliationHash); err != nil {
				return err
			}
			task.Decisions = append(task.Decisions, core.TaskDecision{
				ID: "reconciled-input:" + task.ID, Source: request.Source, Text: request.ReconciliationHash,
			})
		}
		if previous, ok := state.Graph.GraphTask(task.ID); ok {
			if !reflect.DeepEqual(previous, task) {
				return fmt.Errorf("taxion: intake Task ID already contains another proposal; use a new Task ID")
			}
			result = previous
			return nil
		}
		for _, previous := range state.Ledger.Tasks {
			if previous.ID == task.ID {
				return fmt.Errorf("taxion: intake Task ID already exists in the ledger; use a new Task ID")
			}
		}
		if err := state.Graph.Track(task, request.Actor, "Propose explicit scope and acceptance for the original request"); err != nil {
			return err
		}
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo, CreatedAt: time.Now().UTC()})
		result, _ = state.Graph.GraphTask(task.ID)
		return nil
	})
	return result, err
}

// ConfirmIntake confirms exactly the displayed contract. A changed contract is
// a sourced successor Task, never an edit to issued instructions or old budgets.
func ConfirmIntake(db *store.Store, request IntakeConfirmation) (core.GraphTask, error) {
	if db == nil || strings.TrimSpace(request.Actor) == "" || strings.TrimSpace(request.Source) == "" ||
		strings.TrimSpace(request.Reason) == "" || request.ExpectedHash == "" {
		return core.GraphTask{}, fmt.Errorf("taxion: intake confirmation requires exact proposal hash, actor, source and reason")
	}
	if err := request.Selection.Validate(); err != nil {
		return core.GraphTask{}, err
	}
	text, err := json.Marshal(request)
	if err != nil {
		return core.GraphTask{}, err
	}
	confirmation := core.TaskDecision{ID: "intake-confirmation:" + request.TaskID, Source: request.Source, Text: string(text)}
	var result core.GraphTask
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		task, ok := state.Graph.GraphTask(request.TaskID)
		if !state.HasGraph || !ok {
			return fmt.Errorf("taxion: intake confirmation requires a saved proposal")
		}
		if err := task.BudgetGuidance.CheckBillingContract(request.Selection.Billing); err != nil {
			return err
		}
		for _, decision := range task.Decisions {
			if decision.ID == confirmation.ID {
				if decision != confirmation {
					return fmt.Errorf("taxion: intake confirmation cannot be rewritten")
				}
				if err := appendConfirmedIntakeApproval(state, task, request); err != nil {
					return err
				}
				result = task
				return nil
			}
		}
		hash, err := IntakeHash(task)
		if err != nil {
			return err
		}
		decisionCount := len(task.Decisions)
		if decisionCount == 0 {
			return fmt.Errorf("taxion: intake proposal lacks its original request")
		}
		predecessor, err := artifactPredecessorDecision(state.Graph, task)
		if err != nil {
			return err
		}
		if predecessor != nil {
			if request.ReplacesFlow != strings.TrimPrefix(predecessor.Source, "flow:") ||
				request.Target != nil || !slices.Contains(task.Decisions, *predecessor) {
				return fmt.Errorf("taxion: artifact successor requires its exact proposal-bound failed receipt, replacesFlow and omitted target")
			}
			decisionCount--
		}
		if request.ReconciliationHash != "" {
			if request.ReplacesFlow == "" || request.Target == nil {
				return fmt.Errorf("taxion: reconciled successor requires its predecessor and exact cancelled Dispatch target")
			}
			decision := core.TaskDecision{ID: "reconciled-input:" + task.ID,
				Source: task.Decisions[0].Source, Text: request.ReconciliationHash}
			if !slices.Contains(task.Decisions, decision) {
				return fmt.Errorf("taxion: reconciliation must be included in the exact intake proposal hash")
			}
			if err := verifyReconciledInput(*state, task, request.ReconciliationHash); err != nil {
				return err
			}
			decisionCount--
		}
		if hash != request.ExpectedHash || task.Status != core.Draft || task.AuthorizationID != "" ||
			task.Repo != request.Selection.Repository.Path || (decisionCount != 1 && decisionCount != 2) ||
			task.Decisions[0].ID != "intake:"+task.ID || len(task.ValidationCommands) == 0 {
			return fmt.Errorf("taxion: intake proposal changed or lacks explicit execution/validation contract")
		}
		if request.ReplacesFlow != "" {
			flow, ok := state.Graph.ReviewFlow(request.ReplacesFlow)
			if !ok || flow.RunnerID != "" || flow.Status == "COMPLETED" ||
				!flow.HandoffHeld() || task.Supersedes != flow.TaskID || task.CreatedFrom != "flow:"+flow.ID {
				return fmt.Errorf("taxion: revised contract requires a held predecessor, no runner, exact target and explicit supersedes/source relation")
			}
			for _, id := range flow.DispatchIDs {
				dispatch, _ := state.Graph.GraphDispatch(id)
				if active(dispatch.Status) {
					return fmt.Errorf("taxion: stop and reconcile every predecessor execution before reapproval")
				}
			}
			if flow.Artifact != nil {
				if len(flow.DispatchIDs) == 0 && request.Target != nil || task.BaselineSHA != flow.Contract.BaselineSHA ||
					len(flow.DispatchIDs) == 0 && task.TargetSHA != flow.Artifact.Target.CommitSHA ||
					task.TargetTreeSHA != flow.Artifact.Target.TreeSHA ||
					!slices.Equal(task.TargetChangedFiles, flow.Artifact.Target.ChangedFiles) {
					return fmt.Errorf("taxion: artifact successor must retain its exact input target without a model feedback target")
				}
				if len(flow.DispatchIDs) != 0 {
					last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
					target, err := core.ObserveFeedbackTarget(last)
					if request.Target == nil || err != nil || target != *request.Target {
						return core.ErrFeedbackStale
					}
				}
				if err := gitproof.VerifyArtifact(flow.Contract, flow.Artifact.Target, flow.Artifact.Configuration.Selection); err != nil {
					return err
				}
				if len(flow.DispatchIDs) == 0 {
					selection, err := json.Marshal(request.Selection)
					if err != nil {
						return err
					}
					if err := gitproof.VerifyArtifact(task, flow.Artifact.Target, selection); err != nil {
						return fmt.Errorf("taxion: successor worktree does not preserve the failed artifact input: %w", err)
					}
				}
			} else {
				last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
				unstarted := last.Recipient == "CODER" && last.Status == core.DispatchCancelled &&
					!last.ExecutionStarted && last.RuntimeSessionID == "" && last.Result != nil &&
					flow.StopCode == "RECONTRACT" &&
					slices.Contains(last.Result.Evidence, "not-started:"+last.ID)
				if unstarted {
					if request.Target != nil || task.BaselineSHA != last.Instruction.BaselineSHA {
						return fmt.Errorf("taxion: unstarted Dispatch re-contract must retain its baseline and omit a feedback target")
					}
				} else {
					if request.Target == nil {
						return fmt.Errorf("taxion: executed predecessor re-contract requires an observed target")
					}
					target, err := core.ObserveFeedbackTarget(last)
					if err != nil || target != *request.Target {
						return core.ErrFeedbackStale
					}
				}
				if last.Recipient == "CODER" && last.Status == core.DispatchCancelled {
					if unstarted {
						// An unstarted Dispatch has no worktree changes to transfer.
					} else if request.ReconciliationHash != "" {
						audit, err := reconciledInput(*state, flow.ID, request.ReconciliationHash)
						if err != nil || audit.Target != *request.Target {
							return fmt.Errorf("taxion: reconciled input does not match the cancelled predecessor")
						}
					} else if last.Result == nil || last.Result.PartialGitEvidence == nil {
						return fmt.Errorf("taxion: cancelled Coder work requires verified partial Git evidence before successor approval")
					} else {
						if err := gitproof.VerifyPartialSuccessor(state.Records.RestoredDispatch(last), task); err != nil {
							return fmt.Errorf("taxion: successor does not preserve the verified partial work: %w", err)
						}
					}
				} else if flow.Status == "STOPPED" &&
					last.Recipient == "CODER" && last.Status == core.DispatchSucceeded {
					if _, err := budgetContinuationSource(state.Graph, task, last.ID, state.Records); err != nil {
						return fmt.Errorf("taxion: successor does not preserve the completed Coder result: %w", err)
					}
				} else if last.Recipient == "REVIEWER" && (flow.StopCode == "BUDGET_LIMIT" ||
					flow.StopCode == "SAFETY_STOP" && last.Status == core.DispatchCancelled) {
					if len(flow.DispatchIDs) < 2 {
						return fmt.Errorf("taxion: stopped Reviewer requires a saved Coder predecessor")
					}
					if _, err := budgetContinuationSource(state.Graph, task, flow.DispatchIDs[len(flow.DispatchIDs)-2], state.Records); err != nil {
						return err
					}
				}
				if last.Result != nil && last.Result.OutputSHA != "" && task.Worktree == flow.Contract.Worktree &&
					task.BaselineSHA != last.Result.OutputSHA {
					return fmt.Errorf("taxion: successor using the same worktree must retain the recorded latest output SHA")
				}
			}
			// Preserve prior result, instruction, approval and ceiling snapshots.
			if request.ReconciliationHash == "" && flow.Status != "STOPPED" {
				flow.Status, flow.Reason = "STOPPED", "the user explicitly confirmed successor contract "+task.ID
				storeFlow(state, flow)
			}
		} else if task.Supersedes != "" || request.Target != nil {
			return fmt.Errorf("taxion: a superseding contract requires explicit predecessor-flow reapproval")
		}
		approvalText, err := json.Marshal(config.Approval{
			Kind: config.ApprovalKind, TaskID: task.ID, Reason: request.Reason,
			Allowed: []config.Config{request.Selection}, AdditionalTaskIDs: request.AdditionalTaskIDs,
		})
		if err != nil {
			return err
		}
		decisionID := request.ConfigurationDecisionID
		if decisionID == "" {
			decisionID = request.Selection.Execution.ApprovalRef
		}
		if err := config.CheckApproval(string(approvalText), task.ID, decisionID, request.Selection); err != nil {
			return err
		}
		existingApproval := false
		for _, decision := range task.Decisions {
			if decision.ID == decisionID {
				if err := config.CheckApproval(decision.Text, task.ID, decision.ID, request.Selection); err != nil {
					return err
				}
				if len(request.AdditionalTaskIDs) != 0 {
					return fmt.Errorf("taxion: approval expansion needs a fresh explicit configuration approval ID")
				}
				existingApproval = true
			}
		}
		task.AuthorizationID = confirmation.ID
		task.Decisions = append(task.Decisions, confirmation)
		if !existingApproval {
			task.Decisions = append(task.Decisions, core.TaskDecision{
				ID: decisionID, Source: request.Source, Text: string(approvalText),
			})
		}
		if err := state.Graph.Track(task, request.Actor, request.Reason); err != nil {
			return err
		}
		if err := state.Graph.Transition(task.ID, core.Ready, request.Actor, request.Reason, core.TransitionProof{}); err != nil {
			return err
		}
		if request.ReplacesFlow != "" {
			flow, _ := state.Graph.ReviewFlow(request.ReplacesFlow)
			previous, _ := state.Graph.GraphTask(flow.TaskID)
			if previous.Status != core.Superseded {
				if err := state.Graph.Transition(flow.TaskID, core.Superseded, request.Actor, request.Reason, core.TransitionProof{}); err != nil {
					return err
				}
			}
		}
		result, _ = state.Graph.GraphTask(task.ID)
		return appendConfirmedIntakeApproval(state, result, request)
	})
	return result, err
}

// A bundle is a representation of the exact formal confirmation, not a second
// human decision. Legacy graphs keep their existing authorization semantics.
func appendConfirmedIntakeApproval(state *store.Workspace, task core.GraphTask, request IntakeConfirmation) error {
	switch state.Graph.ApprovalRequirement() {
	case core.ApprovalVerifiedLegacy:
		return nil
	case core.ApprovalBundleRequired:
	default:
		return fmt.Errorf("taxion: cannot reuse confirmation with an ambiguous approval schema")
	}
	selection, err := json.Marshal(request.Selection)
	if err != nil {
		return err
	}
	configuration, err := core.SelectApprovalConfiguration(selection, nil)
	if err != nil {
		return err
	}
	if _, ok := state.Graph.MatchingApproval(task.ID, task, configuration); ok {
		return nil
	}
	if task.Status != core.Ready {
		for _, event := range state.Graph.History {
			if event.TaskID == task.ID && event.Task.Status == core.Ready {
				bundle, ok := state.Graph.MatchingApproval(task.ID, event.Task, configuration)
				if ok && bundle.ID == "intake-confirmation:"+task.ID {
					return nil // Replay preserves the original approval, not current execution eligibility.
				}
				break
			}
		}
	}
	if len(state.Graph.ApprovalsForTask(task.ID)) != 0 {
		return fmt.Errorf("taxion: existing approval conflicts with the confirmed contract; use explicit reauthorization")
	}
	if task.Status != core.Ready || task.AuthorizationID != "intake-confirmation:"+task.ID {
		return fmt.Errorf("taxion: confirmation reuse requires an unchanged unexecuted READY Task")
	}
	for _, flow := range state.Graph.ReviewFlows {
		if flow.TaskID == task.ID {
			return fmt.Errorf("taxion: confirmation reuse cannot authorize a Task with an existing Flow")
		}
	}
	for _, dispatch := range state.Graph.Dispatches {
		if dispatch.TaskID == task.ID {
			return fmt.Errorf("taxion: confirmation reuse cannot authorize a Task with an existing Dispatch")
		}
	}
	for _, dispatch := range state.Ledger.Dispatches {
		if dispatch.TaskID == task.ID {
			return fmt.Errorf("taxion: confirmation reuse cannot authorize a Task with a ledger Dispatch")
		}
	}
	if _, err := checkTaskConfiguration(state.Graph, task.ID, request.Selection); err != nil {
		return err
	}
	proposalHash, err := core.ImmutableProposalHash(task)
	if err != nil || proposalHash != request.ExpectedHash {
		return fmt.Errorf("taxion: confirmation reuse lacks its immutable proposal")
	}
	draftFound := false
	var approved *core.GraphEvent
	for i := range state.Graph.History {
		event := &state.Graph.History[i]
		if event.TaskID != task.ID {
			continue
		}
		if event.Task.Status == core.Draft {
			hash, err := IntakeHash(event.Task)
			draftFound = draftFound || err == nil && hash == proposalHash
		}
		if event.Task.Status == core.Ready && approved == nil {
			approved = event
		}
	}
	if !draftFound || approved == nil || !reflect.DeepEqual(approved.Task, task) ||
		approved.Actor != request.Actor || approved.Reason != request.Reason {
		return fmt.Errorf("taxion: confirmation reuse requires the original unchanged approved contract and history")
	}
	bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash(
		"intake-confirmation:"+task.ID, proposalHash, task, configuration,
		request.Actor, request.Source, request.Reason, approved.At)
	if err != nil {
		return err
	}
	if err := bundle.Verify(task, configuration); err != nil {
		return err
	}
	return state.Graph.AppendApproval(bundle)
}
