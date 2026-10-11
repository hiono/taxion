package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

// Compact result decisions retain exact result evidence but do not recursively
// repeat prior instructions. The saved Graph remains the full source of truth.
func compactResultDecisions(graph core.TaskGraph, decisions []core.TaskDecision) ([]core.TaskDecision, error) {
	compacted := slices.Clone(decisions)
	for index, decision := range decisions {
		if !strings.HasPrefix(decision.ID, "result:") {
			continue
		}
		sourceID := strings.TrimPrefix(decision.ID, "result:")
		source, ok := graph.GraphDispatch(sourceID)
		if !ok || source.Result == nil {
			return nil, fmt.Errorf("taxion: compact context requires saved result source %s", sourceID)
		}
		expected, err := flowResultDecision(source)
		if err != nil {
			return nil, err
		}
		if expected != decision {
			return nil, fmt.Errorf("taxion: compact context result differs from saved source %s", sourceID)
		}
		result, err := json.Marshal(source.Result)
		if err != nil {
			return nil, err
		}
		content, err := json.Marshal(struct {
			DispatchID      string               `json:"dispatchId"`
			InstructionHash string               `json:"instructionHash"`
			ResultHash      string               `json:"resultHash"`
			Result          *core.DispatchResult `json:"result"`
		}{source.ID, source.InstructionHash, fmt.Sprintf("sha256:%x", sha256.Sum256(result)), source.Result})
		if err != nil {
			return nil, err
		}
		compacted[index].Text = string(content)
	}
	return compacted, nil
}

func RenderDispatchContext(db *store.Store, dispatch core.GraphDispatch, compact bool) (string, error) {
	if !compact {
		return core.RenderDispatchInstruction(dispatch)
	}
	if db == nil {
		return "", fmt.Errorf("taxion: compact context requires a workspace")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return "", err
	}
	saved, ok := state.Graph.GraphDispatch(dispatch.ID)
	if !state.HasGraph || !ok || saved.InstructionHash != dispatch.InstructionHash {
		return "", fmt.Errorf("taxion: compact context requires the saved instruction")
	}
	dispatch = saved
	dispatch.Instruction.Decisions, err = compactResultDecisions(state.Graph, saved.Instruction.Decisions)
	if err != nil {
		return "", err
	}
	text, err := core.RenderDispatchInstruction(dispatch)
	if err != nil {
		return "", err
	}
	return "CONTEXT PROJECTION: prior result decisions include exact results and source hashes; nested prior instructions remain in the saved Graph. This projection changes no authorization or verdict.\n" + text, nil
}

type PlannerContext struct {
	Task              core.GraphTask             `json:"task"`
	RecordedNextCheck string                     `json:"recordedNextCheck"`
	Gaps              []string                   `json:"gaps"`
	Dependencies      []PlanningDependency       `json:"unmetDependencies"`
	Concerns          []core.Concern             `json:"concerns"`
	Memory            []core.DispatchMemoryEntry `json:"memory"`
	Results           []PlannerResult            `json:"results"`
	Flows             []PlannerFlow              `json:"flows"`
	Observation       *PlanObservation           `json:"observation"`
}

type PlannerResult struct {
	Target core.FeedbackTarget  `json:"target"`
	Result *core.DispatchResult `json:"result"`
}

type PlannerFlow struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
	MaxTokens  int64     `json:"maxTokens"`
	Repairs    int       `json:"repairs"`
	MaxRepairs int       `json:"maxRepairs"`
	Deadline   time.Time `json:"deadline"`
	Held       bool      `json:"held"`
}

// ObservePlannerContext is input for judgment, not a judgment or permission to
// execute. Related review results are included without nested instructions.
func ObservePlannerContext(db *store.Store, ledgerPath, repository, taskID string) (PlannerContext, error) {
	plan, err := Plan(db, ledgerPath, repository)
	if err != nil {
		return PlannerContext{}, err
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return PlannerContext{}, err
	}
	for _, step := range plan.Tasks {
		if step.Task.ID != taskID {
			continue
		}
		current, ok := state.Graph.GraphTask(taskID)
		if !ok || !reflect.DeepEqual(current, step.Task) {
			return PlannerContext{}, fmt.Errorf("taxion: Task changed while observing planner context")
		}
		result := PlannerContext{
			Task: current, RecordedNextCheck: step.Action, Gaps: step.Gaps,
			Dependencies: step.UnmetDependencies, Concerns: step.Concerns,
			Memory: step.MemoryContext, Results: []PlannerResult{}, Flows: []PlannerFlow{},
		}
		result.Task.Decisions, err = compactResultDecisions(state.Graph, current.Decisions)
		if err != nil {
			return PlannerContext{}, err
		}
		related := map[string]bool{}
		for _, flow := range state.Graph.ReviewFlows {
			if flow.TaskID == taskID {
				result.Flows = append(result.Flows, PlannerFlow{
					ID: flow.ID, Status: flow.Status, Reason: flow.Reason,
					MaxTokens: flow.MaxTokens, Repairs: flow.Repairs, MaxRepairs: flow.MaxRepairs,
					Deadline: flow.Deadline, Held: flow.HandoffHeld(),
				})
				for _, id := range flow.DispatchIDs {
					related[id] = true
				}
			}
		}
		for _, dispatch := range state.Graph.Dispatches {
			if dispatch.TaskID != taskID && !related[dispatch.ID] {
				continue
			}
			target, err := core.ObserveFeedbackTarget(dispatch)
			if err != nil {
				return PlannerContext{}, err
			}
			result.Results = append(result.Results, PlannerResult{Target: target, Result: dispatch.Result})
		}
		content, err := json.Marshal(result)
		if err != nil {
			return PlannerContext{}, err
		}
		result.Observation = &PlanObservation{
			StartedAt: plan.Observation.StartedAt, CompletedAt: time.Now().UTC(),
			ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(content)),
		}
		return result, nil
	}
	return PlannerContext{}, fmt.Errorf("taxion: planner context requires a tracked Task in the selected repository")
}

func observePlannerQuestion(db *store.Store, ledgerPath, repository, taskID string) (QuestionContext, error) {
	planning, err := ObservePlannerContext(db, ledgerPath, repository, taskID)
	if err != nil {
		return QuestionContext{}, err
	}
	observation := *planning.Observation
	planning.Observation = nil
	context := QuestionContext{
		Task: PlanningTask{Task: planning.Task}, Flows: []core.ReviewFlow{}, Planner: &planning,
	}
	content, err := json.Marshal(context)
	if err != nil {
		return QuestionContext{}, err
	}
	observation.ContentHash = fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	context.Observation = &observation
	return context, nil
}
