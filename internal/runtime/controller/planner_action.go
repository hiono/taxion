package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

// RecordPlannerAction records a selected non-execution decision at a held
// boundary. Investigation, successor intake or user input remain explicit;
// adopting a gap never turns it into a repair instruction.
func RecordPlannerAction(db *store.Store, ledgerPath string, request PlannerDraftRequest) (core.ReviewFlow, error) {
	if db == nil || request.Proposal.Answer == nil || request.Proposal.Context.Planner == nil ||
		request.Proposal.Context.Observation == nil || request.Proposal.Context.Investigation != nil || request.Proposal.Changed ||
		!slices.Contains([]string{"REPLAN", "INVESTIGATE", "HOLD", "HUMAN_REQUIRED", "REPORT"}, request.Proposal.Answer.Recommendation) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: planner action requires unchanged sourced non-execution advice")
	}
	if err := validateQuestionAnswer(*request.Proposal.Answer, request.Proposal.Context); err != nil {
		return core.ReviewFlow{}, err
	}
	text, err := json.Marshal(struct {
		Operation       string         `json:"operation"`
		Proposal        QuestionResult `json:"proposal"`
		ExpectedDraftID string         `json:"expectedDraftId"`
	}{"SELECT_PLANNER_ACTION", request.Proposal, request.ExpectedDraftID})
	if err != nil {
		return core.ReviewFlow{}, err
	}
	feedback := core.FlowFeedback{ID: request.ID, ExpectedChangeID: request.ExpectedChangeID,
		Target: request.Target, Actor: request.Actor, Source: request.Source, Reason: request.Reason,
		Text: string(text), Evidence: []string{request.Proposal.Context.Observation.ContentHash}}
	before, err := db.LoadWorkspace()
	if err != nil {
		return core.ReviewFlow{}, err
	}
	flow, ok := before.Graph.ReviewFlow(request.FlowID)
	if !before.HasGraph || !ok || flow.TaskID != request.Proposal.Context.Planner.Task.ID {
		return core.ReviewFlow{}, fmt.Errorf("taxion: selected Planner action requires its held flow")
	}
	for _, old := range flow.Feedback {
		if old.ID == request.ID {
			return RecordFeedback(db, FeedbackRequest{FlowID: request.FlowID, FlowFeedback: feedback})
		}
	}
	if !flow.HandoffHeld() {
		return core.ReviewFlow{}, fmt.Errorf("taxion: selected Planner action requires its held flow")
	}
	current, err := observePlannerQuestion(db, ledgerPath, flow.Contract.Repo, flow.TaskID)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if current.Observation.ContentHash != request.Proposal.Context.Observation.ContentHash {
		return core.ReviewFlow{}, core.ErrFeedbackStale
	}
	matching, err := questionContextsEqual(current, request.Proposal.Context)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if !matching || request.ExpectedDraftID != latestDraftID(flow) {
		return core.ReviewFlow{}, core.ErrFeedbackStale
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		if !reflect.DeepEqual(state.Graph, before.Graph) {
			return core.ErrFeedbackStale
		}
		task, _ := state.Graph.GraphTask(flow.TaskID)
		resolved, err := memory.ResolveDispatchContext(ledgerPath, task.MemoryRefs)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(resolved, current.Planner.Memory) {
			return core.ErrFeedbackStale
		}
		flow, err = state.Graph.RecordFlowFeedback(request.FlowID, feedback)
		return err
	})
	return flow, err
}

func latestDraftID(flow core.ReviewFlow) string {
	if len(flow.InstructionDrafts) == 0 {
		return ""
	}
	return flow.InstructionDrafts[len(flow.InstructionDrafts)-1].ID
}
