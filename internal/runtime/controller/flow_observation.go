package controller

import (
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

// runningFlowStop is shared by the observer and the transactional transition.
// Observations never issue work or replace the final transactional check.
func runningFlowStop(state store.Workspace, flow core.ReviewFlow, ledgerPath string) (string, string, error) {
	stop := func(reason string) (string, string, error) { return reason, "SAFETY_STOP", nil }
	budget := func(reason string) (string, string, error) { return reason, "BUDGET_STOP", nil }
	task, _ := state.Graph.GraphTask(flow.TaskID)
	last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
	if !time.Now().UTC().Before(flow.ExecutionDeadline()) {
		return budget("deadline reached; reconcile and safely stop any active Dispatch before further authorization")
	}
	if !flowContractMatches(state.Graph, flow, task) {
		return stop("Task scope, acceptance, authorization or execution contract changed; user approval of a new flow is required")
	}
	if err := state.Graph.CheckTaskSpecificationUse(task.ID, "runtime"); err != nil {
		return stop("specification source or permission requires renewed user approval: " + err.Error())
	}
	if slices.Contains([]core.TaskStatus{core.Cancelled, core.Rejected, core.Superseded}, task.Status) {
		return stop("Task ended with " + string(task.Status) + "; reconcile any active Dispatch")
	}
	if task.Status == core.Blocked && active(last.Status) {
		return stop("Task is BLOCKED while its Dispatch is active; inspect the saved blocker and safely stop execution")
	}
	if task.Status == core.HumanDecisionRequired || task.HumanDecision != nil && task.HumanDecision.SelectedOptionID == "" {
		return stop("user decision required; present the Task's saved question and options")
	}
	if last.Status == core.DispatchSuspended || last.Status == core.DispatchCancelRequested {
		return stop("Dispatch " + last.ID + " requires safe stop or renewed authorization before continuation")
	}
	if flow.Artifact != nil && len(flow.DispatchIDs) == 0 {
		return "", "", nil
	}
	context, err := memory.ResolveDispatchContext(ledgerPath, last.Instruction.MemoryRefs)
	if err != nil {
		return "", "", fmt.Errorf("taxion: required flow Memory: %w", err)
	}
	if !reflect.DeepEqual(context, last.Instruction.MemoryContext) {
		return stop("required Memory differs from the issued snapshot; confirm the updated decision with the user")
	}
	if active(last.Status) {
		if reason := stageBudgetStop(state.Graph, flow, last, time.Now().UTC()); reason != "" {
			return budget(reason)
		}
		if reason := activeBudgetStop(taskBudgetReport(state.Graph, flow, time.Now().UTC())); reason != "" {
			return budget(reason)
		}
	}
	return "", "", nil
}

func observeRunningFlow(db *store.Store, ledgerPath, id, runnerID string) (bool, error) {
	unchanged := false
	err := db.ObserveWorkspace(func(state store.Workspace) error {
		flow, found := state.Graph.ReviewFlow(id)
		if !state.HasGraph || !found {
			return fmt.Errorf("taxion: review flow %q not found", id)
		}
		if flow.RunnerID != runnerID {
			return fmt.Errorf("%w: %s", ErrFlowClaimed, flow.RunnerID)
		}
		if err := verifyStagePlanBinding(state.Records, flow); err != nil {
			return err
		}
		for _, id := range flow.DispatchIDs {
			dispatch, _ := state.Graph.GraphDispatch(id)
			if err := validateResultReceipt(&state, dispatch); err != nil {
				return err
			}
		}
		last, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
		if flow.Status != "RUNNING" || flow.HandoffHeld() || last.Status != core.DispatchRunning {
			return nil
		}
		reason, _, err := runningFlowStop(state, flow, ledgerPath)
		unchanged = err == nil && reason == ""
		return err
	})
	return unchanged, err
}
