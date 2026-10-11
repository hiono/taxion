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

type PlannerDraftRequest struct {
	FlowID           string              `json:"flowId"`
	ID               string              `json:"id"`
	ExpectedDraftID  string              `json:"expectedDraftId"`
	ExpectedChangeID string              `json:"expectedChangeId"`
	Target           core.FeedbackTarget `json:"target"`
	Actor            string              `json:"actor"`
	Source           string              `json:"source"`
	Reason           string              `json:"reason"`
	Proposal         QuestionResult      `json:"proposal"`
}

// RecordPlannerDraft adopts advice as an unapproved supplement, not a verdict,
// contract revision or authorization. Existing draft approval publishes it.
func RecordPlannerDraft(db *store.Store, ledgerPath string, request PlannerDraftRequest) (core.ReviewFlow, error) {
	if db == nil || request.Proposal.Answer == nil || request.Proposal.Context.Planner == nil ||
		request.Proposal.Context.Observation == nil || request.Proposal.Changed {
		return core.ReviewFlow{}, fmt.Errorf("taxion: planner draft requires unchanged sourced planning advice")
	}
	if err := validateQuestionAnswer(*request.Proposal.Answer, request.Proposal.Context); err != nil {
		return core.ReviewFlow{}, err
	}
	role := ""
	switch request.Proposal.Answer.Recommendation {
	case "REVIEW":
		role = "REVIEWER"
	case "REPAIR":
		role = "CODER"
	default:
		return core.ReviewFlow{}, fmt.Errorf("taxion: this planner action requires separate investigation, replanning or Human decision; it cannot become an executable supplement")
	}
	targets := make([]core.FeedbackTarget, 0, len(request.Proposal.Context.Planner.Results))
	for _, result := range request.Proposal.Context.Planner.Results {
		targets = append(targets, result.Target)
	}
	type memorySource struct {
		Reference string   `json:"reference"`
		Version   int      `json:"version"`
		Sources   []string `json:"sources"`
	}
	references := make([]memorySource, 0, len(request.Proposal.Context.Planner.Memory))
	for _, entry := range request.Proposal.Context.Planner.Memory {
		references = append(references, memorySource{entry.Reference, entry.Version, entry.SourceRefs})
	}
	text, err := json.Marshal(struct {
		SessionID   string                `json:"sessionId"`
		Observation *PlanObservation      `json:"observation"`
		Targets     []core.FeedbackTarget `json:"targets"`
		Memory      []memorySource        `json:"memory"`
		Answer      *QuestionAnswer       `json:"answer"`
	}{
		request.Proposal.SessionID, request.Proposal.Context.Observation, targets, references, request.Proposal.Answer,
	})
	if err != nil {
		return core.ReviewFlow{}, err
	}
	draft := core.InstructionDraft{
		ID: request.ID, ExpectedDraftID: request.ExpectedDraftID, ExpectedChangeID: request.ExpectedChangeID,
		Target: request.Target, NextRole: role, Operation: "SUPPLEMENT",
		Text: string(text), Actor: request.Actor, Source: request.Source, Reason: request.Reason,
		Evidence:    []string{request.Proposal.Context.Observation.ContentHash},
		Unconfirmed: []string{}, FeedbackIDs: []string{},
	}
	for _, item := range append(slices.Clone(request.Proposal.Answer.Assumptions), request.Proposal.Answer.Unconfirmed...) {
		if !slices.Contains(draft.Unconfirmed, item) {
			draft.Unconfirmed = append(draft.Unconfirmed, item)
		}
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		return core.ReviewFlow{}, err
	}
	flow, ok := before.Graph.ReviewFlow(request.FlowID)
	if !before.HasGraph || !ok || flow.TaskID != request.Proposal.Context.Planner.Task.ID {
		return core.ReviewFlow{}, fmt.Errorf("taxion: planner proposal does not target this flow's Task")
	}
	for _, previous := range flow.InstructionDrafts {
		if previous.ID == draft.ID {
			// The core checks exact content and returns the original version.
			return RecordInstructionDraft(db, InstructionDraftRequest{FlowID: request.FlowID, InstructionDraft: draft})
		}
	}
	if flow.Status != "RUNNING" || !flow.HandoffHeld() {
		return core.ReviewFlow{}, fmt.Errorf("taxion: planner adoption requires a running held flow")
	}
	task, _ := before.Graph.GraphTask(flow.TaskID)
	last, _ := before.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	target, err := core.ObserveFeedbackTarget(last)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if target != request.Target || last.Status != core.DispatchSucceeded || last.Result == nil ||
		!flowContractMatches(before.Graph, flow, task) {
		return core.ReviewFlow{}, core.ErrFeedbackStale
	}
	if role == "REVIEWER" && last.Recipient != "CODER" ||
		role == "CODER" && (last.Recipient != "REVIEWER" || last.Result.ReviewVerdict != "REQUEST_CHANGES" || flow.Repairs >= flow.MaxRepairs) {
		return core.ReviewFlow{}, fmt.Errorf("taxion: planner action conflicts with the saved result or repair ceiling")
	}
	current, err := observePlannerQuestion(db, ledgerPath, flow.Contract.Repo, flow.TaskID)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if current.Observation.ContentHash != request.Proposal.Context.Observation.ContentHash ||
		!reflect.DeepEqual(current.Planner, request.Proposal.Context.Planner) ||
		!reflect.DeepEqual(current.Task, request.Proposal.Context.Task) ||
		!reflect.DeepEqual(current.Flows, request.Proposal.Context.Flows) {
		return core.ReviewFlow{}, core.ErrFeedbackStale
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph || !reflect.DeepEqual(state.Graph, before.Graph) {
			return core.ErrFeedbackStale
		}
		context, err := memory.ResolveDispatchContext(ledgerPath, task.MemoryRefs)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(context, current.Planner.Memory) {
			return fmt.Errorf("taxion: planner Memory changed before adoption")
		}
		flow, err = state.Graph.RecordInstructionDraft(request.FlowID, draft)
		return err
	})
	return flow, err
}
