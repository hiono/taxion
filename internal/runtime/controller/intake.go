package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

type IntakeRequest struct {
	Request                 string         `json:"request"`
	Task                    core.GraphTask `json:"task"`
	Actor                   string         `json:"actor"`
	Source                  string         `json:"source"`
	PlanningSelection       *config.Config `json:"planningSelection,omitempty"`
	PlanningReason          string         `json:"planningReason,omitempty"`
	ConfigurationDecisionID string         `json:"configurationDecisionId,omitempty"`
}

type IntakeConfirmation struct {
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
}

func IntakeHash(task core.GraphTask) (string, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

// ProposeIntake records the original request and a contract for Human to inspect.
// It never treats natural-language text as authorization or starts a worker.
func ProposeIntake(db *store.Store, request IntakeRequest) (core.GraphTask, error) {
	if db == nil || strings.TrimSpace(request.Request) == "" || strings.TrimSpace(request.Actor) == "" ||
		strings.TrimSpace(request.Source) == "" || request.Task.Status != core.Draft ||
		request.Task.AuthorizationID != "" || len(request.Task.Decisions) != 0 ||
		request.Task.Owner != "CODER" || request.Task.Reviewer != "REVIEWER" {
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
		if request.PlanningSelection != nil {
			mode = request.PlanningSelection.Execution.BudgetGuidance
		}
		task.BudgetGuidance, err = core.NewBudgetGuidance(mode, "")
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
		for _, decision := range task.Decisions {
			if decision.ID == confirmation.ID {
				if decision != confirmation {
					return fmt.Errorf("taxion: intake confirmation cannot be rewritten")
				}
				result = task
				return nil
			}
		}
		hash, err := IntakeHash(task)
		if err != nil {
			return err
		}
		if hash != request.ExpectedHash || task.Status != core.Draft || task.AuthorizationID != "" ||
			task.Repo != request.Selection.Repository.Path || (len(task.Decisions) != 1 && len(task.Decisions) != 2) ||
			task.Decisions[0].ID != "intake:"+task.ID || len(task.ValidationCommands) == 0 {
			return fmt.Errorf("taxion: intake proposal changed or lacks explicit execution/validation contract")
		}
		if request.ReplacesFlow != "" {
			flow, ok := state.Graph.ReviewFlow(request.ReplacesFlow)
			if !ok || request.Target == nil || flow.RunnerID != "" || flow.Status == "COMPLETED" ||
				!flow.HandoffHeld() || task.Supersedes != flow.TaskID || task.CreatedFrom != "flow:"+flow.ID {
				return fmt.Errorf("taxion: revised contract requires a held predecessor, no runner, exact target and explicit supersedes/source relation")
			}
			for _, id := range flow.DispatchIDs {
				dispatch, _ := state.Graph.GraphDispatch(id)
				if active(dispatch.Status) {
					return fmt.Errorf("taxion: stop and reconcile every predecessor execution before reapproval")
				}
			}
			last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
			target, err := core.ObserveFeedbackTarget(last)
			if err != nil || target != *request.Target {
				return core.ErrFeedbackStale
			}
			if last.Recipient == "CODER" && last.Status == core.DispatchCancelled {
				if last.Result == nil || last.Result.PartialGitEvidence == nil {
					return fmt.Errorf("taxion: cancelled Coder work requires verified partial Git evidence before successor approval")
				}
				if err := gitproof.VerifyPartialSuccessor(last, task); err != nil {
					return fmt.Errorf("taxion: successor does not preserve the verified partial work: %w", err)
				}
			} else if flow.StopCode == "BUDGET_LIMIT" &&
				last.Recipient == "CODER" && last.Status == core.DispatchSucceeded {
				if err := gitproof.VerifyCompletedSuccessor(last, task); err != nil {
					return fmt.Errorf("taxion: successor does not preserve the completed Coder result: %w", err)
				}
			}
			if last.Result != nil && last.Result.OutputSHA != "" && task.Worktree == flow.Contract.Worktree &&
				task.BaselineSHA != last.Result.OutputSHA {
				return fmt.Errorf("taxion: successor using the same worktree must retain the recorded latest output SHA")
			}
			// Preserve prior result, instruction, approval and ceiling snapshots.
			flow.Status, flow.Reason = "STOPPED", "Human explicitly confirmed successor contract "+task.ID
			storeFlow(state, flow)
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
			if err := state.Graph.Transition(flow.TaskID, core.Superseded, request.Actor, request.Reason, core.TransitionProof{}); err != nil {
				return err
			}
		}
		result, _ = state.Graph.GraphTask(task.ID)
		return nil
	})
	return result, err
}
