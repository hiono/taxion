package controller

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

// FollowUpPlan is reconstructed from durable records, independent of a role's
// session history. Actions describe the next check, not permission to execute.
type FollowUpPlan struct {
	Repository string         `json:"repository"`
	Tasks      []PlanningTask `json:"tasks"`
}

type PlanningTask struct {
	Task              core.GraphTask             `json:"task"`
	Action            string                     `json:"action"`
	Instruction       string                     `json:"instruction"`
	Gaps              []string                   `json:"gaps"`
	UnmetDependencies []PlanningDependency       `json:"unmetDependencies"`
	Concerns          []core.Concern             `json:"concerns"`
	History           []core.GraphEvent          `json:"history"`
	MemoryContext     []core.DispatchMemoryEntry `json:"memoryContext"`
	Dispatches        []core.GraphDispatch       `json:"dispatches"`
}

type PlanningDependency struct {
	TaskID        string          `json:"taskId"`
	Repository    string          `json:"repository"`
	RequiredState core.TaskStatus `json:"requiredState"`
	ObservedState core.TaskStatus `json:"observedState"`
}

func Plan(db *store.Store, ledgerPath, repo string) (FollowUpPlan, error) {
	if db == nil || strings.TrimSpace(repo) == "" {
		return FollowUpPlan{}, fmt.Errorf("taxion: planning requires a store and explicit repository")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return FollowUpPlan{}, err
	}
	if !state.HasGraph {
		return FollowUpPlan{}, fmt.Errorf("%w: planning requires a Task Graph; use task track", core.ErrGraphRejected)
	}
	plan := FollowUpPlan{Repository: repo, Tasks: []PlanningTask{}}
	for _, task := range state.Graph.Tasks {
		if task.Repo != repo {
			continue
		}
		step := PlanningTask{
			Task: task, Gaps: []string{}, UnmetDependencies: []PlanningDependency{},
			Concerns: []core.Concern{}, History: []core.GraphEvent{}, Dispatches: []core.GraphDispatch{},
		}
		step.MemoryContext, err = memory.ResolveDispatchContext(ledgerPath, task.MemoryRefs)
		if err != nil {
			return FollowUpPlan{}, fmt.Errorf("taxion: planning Task %s Memory: %w", task.ID, err)
		}
		for _, concern := range state.Graph.Concerns {
			if concern.TaskID == task.ID {
				step.Concerns = append(step.Concerns, concern)
			}
		}
		for _, event := range state.Graph.History {
			if event.TaskID == task.ID {
				step.History = append(step.History, event)
			}
		}
		for _, dependency := range task.DependsOn {
			prerequisite, _ := state.Graph.GraphTask(dependency.TaskID)
			if prerequisite.Status != dependency.State &&
				!(dependency.State == core.Approved && prerequisite.Status == core.Completed) {
				step.UnmetDependencies = append(step.UnmetDependencies, PlanningDependency{
					TaskID: prerequisite.ID, Repository: prerequisite.Repo,
					RequiredState: dependency.State, ObservedState: prerequisite.Status,
				})
			}
		}
		active := false
		for _, dispatch := range state.Graph.Dispatches {
			if dispatch.TaskID != task.ID {
				continue
			}
			// Retain the issued scope, acceptance, SHA and Memory version along
			// with each result; earlier findings remain evidence of that run.
			step.Dispatches = append(step.Dispatches, dispatch)
			if slices.Contains([]core.DispatchStatus{
				core.DispatchIssued, core.DispatchAcknowledged, core.DispatchRunning,
				core.DispatchCancelRequested, core.DispatchSuspended,
			}, dispatch.Status) {
				active = true
			}
		}
		switch {
		case slices.Contains([]core.TaskStatus{core.Completed, core.Rejected, core.Cancelled, core.Superseded}, task.Status):
			step.Action = "RETAIN"
			step.Instruction = "Reuse this Task ID, decisions and outcome when comparing later work with existing work."
		case active:
			step.Action = "RECONCILE_DISPATCH"
			step.Instruction = "Observe the recorded Dispatch before planning another execution; preserve its issued contract."
		case task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "":
			step.Action = "ASK_HUMAN"
			step.Instruction = "Present the recorded unresolved question and options; reuse existing decisions."
		case task.DeferredReason != "":
			step.Action = "REASSESS_DEFERRED"
			step.Instruction = "Evaluate the saved resume condition and dependencies for the recorded next action. Record the decision on this Task before changing its contract or status."
		case len(step.UnmetDependencies) != 0:
			step.Action = "WAIT_FOR_DEPENDENCIES"
			step.Instruction = "Reevaluate this Task after the listed dependencies reach their required states."
		case task.Status == core.Ready:
			if err := state.Graph.CheckExecutable(task.ID, repo); err != nil {
				return FollowUpPlan{}, err
			}
			step.Gaps = contractGaps(task)
			if err := memory.CheckDispatch(ledgerPath, core.Task{ID: task.ID, Title: task.Title, Repo: repo}, repo, task.MemoryRefs...); err != nil {
				return FollowUpPlan{}, fmt.Errorf("taxion: planning Task %s policy: %w", task.ID, err)
			}
			step.Action = "DISPATCH"
			step.Instruction = "Issue a new Dispatch using this saved contract and Memory snapshot; verify Git at execution start."
			if len(step.Gaps) != 0 {
				step.Action = "RESOLVE_GAPS"
				step.Instruction = "Record an approved contract update for the listed gaps before issuing a Dispatch."
			}
		case task.Status == core.WaitingForEvidence:
			step.Action = "COLLECT_EVIDENCE"
			step.Instruction = "Collect the saved required evidence for the recorded output SHA."
		case task.Status == core.WaitingForReview || task.Status == core.RequestChanges:
			step.Action = "RECONCILE_REVIEW"
			step.Instruction = "Compare the saved review scope, acceptance and output SHA with this Task. Preserve findings and their Dispatch provenance; require review of the current output SHA."
		case task.Status == core.Approved:
			step.Action = "CHECK_COMPLETION"
			step.Instruction = "Verify saved approval, required child results and completion evidence before recording COMPLETED."
		case task.Status == core.Active:
			step.Action = "RESOLVE_GAPS"
			step.Gaps = append(step.Gaps, "active Dispatch")
			step.Instruction = "Reconcile the missing execution record with this active Task before issuing another Dispatch."
		default:
			step.Action = "RESTORE_CONTRACT"
			step.Instruction = "Reuse recorded decisions and results to prepare the next contract update. Request Human approval for new decisions."
			step.Gaps = contractGaps(task)
		}
		plan.Tasks = append(plan.Tasks, step)
	}
	if len(plan.Tasks) == 0 {
		return FollowUpPlan{}, fmt.Errorf("%w: no tracked Tasks for repository %q", core.ErrGraphRejected, repo)
	}
	return plan, nil
}

func contractGaps(task core.GraphTask) []string {
	gaps := []string{}
	for _, field := range []struct{ name, value string }{
		{"objective", task.Objective}, {"exitCondition", task.ExitCondition},
		{"worktree", task.Worktree}, {"branch", task.Branch}, {"baselineSha", task.BaselineSHA},
		{"authorizationId", task.AuthorizationID},
	} {
		if strings.TrimSpace(field.value) == "" {
			gaps = append(gaps, field.name)
		}
	}
	for _, field := range []struct {
		name   string
		values []string
	}{
		{"scope", task.Scope}, {"acceptance", task.Acceptance},
		{"requiredEvidence", task.RequiredEvidence}, {"validationCommands", task.ValidationCommands},
	} {
		if len(field.values) == 0 {
			gaps = append(gaps, field.name)
		}
	}
	if task.Owner != "CODER" && task.Owner != "REVIEWER" {
		gaps = append(gaps, "owner")
	}
	if task.Owner == "REVIEWER" && strings.TrimSpace(task.TargetSHA) == "" {
		gaps = append(gaps, "targetSha")
	}
	if task.AuthorizationID != "" && !slices.ContainsFunc(task.Decisions, func(decision core.TaskDecision) bool {
		return decision.ID == task.AuthorizationID
	}) {
		gaps = append(gaps, "authorization decision")
	}
	if strings.HasPrefix(task.Issue, "PENDING:") {
		gaps = append(gaps, "Issue registration")
	}
	return gaps
}
