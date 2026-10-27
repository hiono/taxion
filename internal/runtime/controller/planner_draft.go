package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

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

type QuestionDraftRequest struct {
	PlannerDraftRequest
	NextRole         string `json:"nextRole"`
	Selected         []int  `json:"selected"`
	HumanInstruction string `json:"humanInstruction"`
}

// RecordQuestionDraft applies only explicitly selected confirmed findings,
// retaining their evidence and user instruction without copying ordinary chat.
func RecordQuestionDraft(db *store.Store, ledgerPath string, request QuestionDraftRequest) (core.ReviewFlow, error) {
	if request.Proposal.Context.Planner != nil || request.Proposal.Answer == nil ||
		!slices.Contains([]string{"CODER", "REVIEWER"}, request.NextRole) || len(request.Selected) == 0 ||
		strings.TrimSpace(request.HumanInstruction) == "" {
		return core.ReviewFlow{}, fmt.Errorf("taxion: question adoption requires selected confirmed findings, next role and user instruction")
	}
	answer := *request.Proposal.Answer
	answer.Confirmed = []QuestionClaim{}
	seen := map[int]bool{}
	for _, i := range request.Selected {
		if i < 0 || i >= len(request.Proposal.Answer.Confirmed) || seen[i] {
			return core.ReviewFlow{}, fmt.Errorf("taxion: selected question findings must be unique existing confirmed indices")
		}
		seen[i] = true
		answer.Confirmed = append(answer.Confirmed, request.Proposal.Answer.Confirmed[i])
	}
	return recordAdviceDraft(db, ledgerPath, request.PlannerDraftRequest, request.NextRole, &answer, request.Selected, request.HumanInstruction)
}

// RecordPlannerDraft adopts advice as an unapproved supplement, not a verdict,
// contract revision or authorization. Existing draft approval publishes it.
func RecordPlannerDraft(db *store.Store, ledgerPath string, request PlannerDraftRequest) (core.ReviewFlow, error) {
	if request.Proposal.Answer == nil || request.Proposal.Context.Planner == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: planner draft requires unchanged sourced planning advice")
	}
	role := ""
	switch request.Proposal.Answer.Recommendation {
	case "REVIEW":
		role = "REVIEWER"
	case "REPAIR":
		role = "CODER"
	default:
		return core.ReviewFlow{}, fmt.Errorf("taxion: this planner action requires separate investigation, replanning or user decision; it cannot become an executable supplement")
	}
	return recordAdviceDraft(db, ledgerPath, request, role, request.Proposal.Answer, nil, "")
}

func recordAdviceDraft(db *store.Store, ledgerPath string, request PlannerDraftRequest, role string, answer *QuestionAnswer, selected []int, humanInstruction string) (core.ReviewFlow, error) {
	if db == nil || request.Proposal.Context.Observation == nil || request.Proposal.Changed {
		return core.ReviewFlow{}, fmt.Errorf("taxion: adoption requires unchanged sourced advice")
	}
	if err := validateQuestionAnswer(*request.Proposal.Answer, request.Proposal.Context); err != nil {
		return core.ReviewFlow{}, err
	}
	memoryEntries := request.Proposal.Context.Task.MemoryContext
	targets := []core.FeedbackTarget{}
	if request.Proposal.Context.Planner != nil {
		memoryEntries = request.Proposal.Context.Planner.Memory
		for _, result := range request.Proposal.Context.Planner.Results {
			targets = append(targets, result.Target)
		}
	} else {
		for _, dispatch := range request.Proposal.Context.Task.Dispatches {
			target, err := core.ObserveFeedbackTarget(dispatch)
			if err != nil {
				return core.ReviewFlow{}, err
			}
			targets = append(targets, target)
		}
	}
	type memorySource struct {
		Reference string   `json:"reference"`
		Version   int      `json:"version"`
		Sources   []string `json:"sources"`
	}
	references := make([]memorySource, 0, len(memoryEntries))
	for _, entry := range memoryEntries {
		references = append(references, memorySource{entry.Reference, entry.Version, entry.SourceRefs})
	}
	original, err := json.Marshal(request.Proposal)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	proposalHash := ""
	if request.Proposal.Context.Planner == nil {
		proposalHash = fmt.Sprintf("sha256:%x", sha256.Sum256(original))
	}
	text, err := json.Marshal(struct {
		SessionID        string                `json:"sessionId"`
		Observation      *PlanObservation      `json:"observation"`
		Targets          []core.FeedbackTarget `json:"targets"`
		Memory           []memorySource        `json:"memory"`
		Answer           *QuestionAnswer       `json:"answer"`
		ProposalHash     string                `json:"proposalHash,omitempty"`
		Selected         []int                 `json:"selected,omitempty"`
		HumanInstruction string                `json:"humanInstruction,omitempty"`
		Investigation    *FileInvestigation    `json:"investigation,omitempty"`
	}{
		request.Proposal.SessionID, request.Proposal.Context.Observation, targets, references, answer,
		proposalHash, selected, humanInstruction, request.Proposal.Context.Investigation,
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
	if request.Proposal.Context.Investigation != nil {
		draft.Evidence = append(draft.Evidence, request.Proposal.Context.Investigation.Observation.ContentHash)
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
	if !before.HasGraph || !ok || flow.TaskID != request.Proposal.Context.Task.Task.ID {
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
	last, _ := before.Graph.GraphDispatch(flow.LastDispatchID())
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
	observe := ObserveQuestion
	if request.Proposal.Context.Planner != nil {
		observe = observePlannerQuestion
	}
	current, err := observe(db, ledgerPath, flow.Contract.Repo, flow.TaskID)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	matching, err := questionContextsEqual(current, request.Proposal.Context)
	if err != nil {
		return core.ReviewFlow{}, err
	}
	if current.Observation.ContentHash != request.Proposal.Context.Observation.ContentHash || !matching {
		return core.ReviewFlow{}, core.ErrFeedbackStale
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := verifyInvestigation(ctx, request.Proposal.Context); err != nil {
		return core.ReviewFlow{}, err
	}
	currentMemory := current.Task.MemoryContext
	if current.Planner != nil {
		currentMemory = current.Planner.Memory
	}
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph || !reflect.DeepEqual(state.Graph, before.Graph) {
			return core.ErrFeedbackStale
		}
		context, err := memory.ResolveDispatchContext(ledgerPath, task.MemoryRefs)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(context, currentMemory) {
			return fmt.Errorf("taxion: planner Memory changed before adoption")
		}
		flow, err = state.Graph.RecordInstructionDraft(request.FlowID, draft)
		return err
	})
	return flow, err
}
