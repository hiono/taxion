package core

import (
	"path/filepath"
	"slices"
	"time"
)

// ReviewFlow retains the approved execution contract and ceilings across
// controller restarts. Dispatches carry each observed execution separately.
type ReviewFlow struct {
	ID             string          `json:"id"`
	TaskID         string          `json:"taskId"`
	Contract       GraphTask       `json:"contract"`
	ReviewRoot     string          `json:"reviewRoot"`
	MaxRepairs     int             `json:"maxRepairs"`
	MaxTokens      int64           `json:"maxTokens"`
	StartedAt      time.Time       `json:"startedAt"`
	Deadline       time.Time       `json:"deadline"`
	Repairs        int             `json:"repairs"`
	DispatchIDs    []string        `json:"dispatchIds"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason"`
	Authorization  TaskDecision    `json:"authorization"`
	RunnerID       string          `json:"runnerId,omitempty"`
	Recoveries     []FlowRecovery  `json:"recoveries,omitempty"`
	HandoffChanges []HandoffChange `json:"handoffChanges,omitempty"`
}

type FlowRecovery struct {
	RunnerID string    `json:"runnerId"`
	Actor    string    `json:"actor"`
	Reason   string    `json:"reason"`
	Evidence []string  `json:"evidence"`
	At       time.Time `json:"at"`
}

func (g TaskGraph) ReviewFlow(id string) (ReviewFlow, bool) {
	for _, flow := range g.ReviewFlows {
		if flow.ID == id {
			flow.Contract = cloneTask(flow.Contract)
			flow.DispatchIDs = slices.Clone(flow.DispatchIDs)
			flow.Recoveries = cloneFlowRecoveries(flow.Recoveries)
			flow.HandoffChanges = slices.Clone(flow.HandoffChanges)
			return flow, true
		}
	}
	return ReviewFlow{}, false
}

func (g TaskGraph) validateReviewFlows() error {
	ids, dispatches, running := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, flow := range g.ReviewFlows {
		task, ok := g.task(flow.TaskID)
		if !ok || !nonempty(flow.ID) || ids[flow.ID] ||
			flow.Contract.ID != task.ID || flow.Contract.Repo != task.Repo ||
			flow.Contract.Owner != "CODER" || flow.Contract.Reviewer != "REVIEWER" ||
			flow.Contract.Status != Ready || !filepath.IsAbs(flow.ReviewRoot) ||
			flow.MaxRepairs < 0 || flow.Repairs < 0 || flow.Repairs > flow.MaxRepairs ||
			flow.MaxTokens <= 0 || flow.StartedAt.IsZero() || !flow.Deadline.After(flow.StartedAt) ||
			!slices.Contains([]string{"RUNNING", "STOPPED", "COMPLETED"}, flow.Status) ||
			!nonempty(flow.Authorization.ID) || !nonempty(flow.Authorization.Source) || !nonempty(flow.Authorization.Text) ||
			len(flow.DispatchIDs) == 0 || flow.Status != "RUNNING" && !nonempty(flow.Reason) {
			return graphError("invalid bounded review flow %q", flow.ID)
		}
		ids[flow.ID] = true
		if err := validateHandoffChanges(flow); err != nil {
			return err
		}
		for _, recovery := range flow.Recoveries {
			if !nonempty(recovery.RunnerID) || !nonempty(recovery.Actor) || !nonempty(recovery.Reason) ||
				recovery.At.IsZero() || len(recovery.Evidence) == 0 || !validList(recovery.Evidence) {
				return graphError("review flow recovery requires owner, actor, reason and evidence")
			}
		}
		if flow.Status == "RUNNING" {
			if running[task.ID] {
				return graphError("Task %s has multiple running review flows", task.ID)
			}
			running[task.ID] = true
		}
		for index, id := range flow.DispatchIDs {
			dispatch, ok := g.dispatch(id)
			role := "CODER"
			if index%2 == 1 {
				role = "REVIEWER"
			}
			if !ok || dispatches[id] || dispatch.Repository != task.Repo || dispatch.Recipient != role ||
				role == "CODER" && dispatch.TaskID != task.ID {
				return graphError("invalid Dispatch %q in review flow %s", id, flow.ID)
			}
			instruction := dispatch.Instruction
			if !slices.Equal(instruction.Acceptance, flow.Contract.Acceptance) ||
				!slices.Equal(instruction.AuthorizedActions, flow.Contract.Scope) ||
				!slices.Equal(instruction.ForbiddenActions, flow.Contract.OutOfScope) ||
				!slices.Equal(instruction.RequiredEvidence, flow.Contract.RequiredEvidence) ||
				!slices.Equal(instruction.ValidationCommands, flow.Contract.ValidationCommands) ||
				!slices.Equal(instruction.MemoryRefs, flow.Contract.MemoryRefs) {
				return graphError("review flow Dispatch changed the approved scope, acceptance or evidence")
			}
			if role == "CODER" && (instruction.Worktree != flow.Contract.Worktree ||
				instruction.Branch != flow.Contract.Branch || instruction.BaselineSHA != flow.Contract.BaselineSHA) {
				return graphError("review flow Coder Dispatch changed its execution environment")
			}
			if index > 0 {
				prior, _ := g.dispatch(flow.DispatchIDs[index-1])
				if prior.Status != DispatchSucceeded || prior.Result == nil ||
					instruction.TargetSHA != prior.Result.OutputSHA {
					return graphError("review flow continuation requires the previous verified output SHA")
				}
				if role == "REVIEWER" && (dispatch.RelatedID != prior.ID ||
					instruction.BaselineSHA != prior.Result.OutputSHA ||
					filepath.Dir(instruction.Worktree) != flow.ReviewRoot) {
					return graphError("review flow requires an independent worktree at the verified Coder SHA")
				}
			}
			dispatches[id] = true
		}
		if flow.Repairs != (len(flow.DispatchIDs)-1)/2 {
			return graphError("review flow repair count conflicts with Dispatch history")
		}
		if flow.Status == "COMPLETED" {
			last, _ := g.dispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
			if task.Status != Completed || last.Recipient != "REVIEWER" || last.Status != DispatchSucceeded ||
				last.Result == nil || last.Result.ReviewVerdict != "APPROVE" ||
				last.Result.OutputSHA != task.TargetSHA {
				return graphError("review flow completion requires exact-SHA independent approval")
			}
		}
	}
	return nil
}

func cloneFlowRecoveries(records []FlowRecovery) []FlowRecovery {
	copy := slices.Clone(records)
	for index := range copy {
		copy[index].Evidence = slices.Clone(copy[index].Evidence)
	}
	return copy
}
