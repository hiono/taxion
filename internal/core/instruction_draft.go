package core

import (
	"reflect"
	"slices"
	"strings"
	"time"
)

// InstructionDraft is a proposal, not an executable Dispatch or approval.
type InstructionDraft struct {
	ID               string            `json:"id"`
	ExpectedDraftID  string            `json:"expectedDraftId"`
	ExpectedChangeID string            `json:"expectedChangeId"`
	Target           FeedbackTarget    `json:"target"`
	NextRole         string            `json:"nextRole"`
	Operation        string            `json:"operation"`
	Text             string            `json:"text"`
	Actor            string            `json:"actor"`
	Source           string            `json:"source"`
	Reason           string            `json:"reason"`
	Evidence         []string          `json:"evidence"`
	Unconfirmed      []string          `json:"unconfirmed"`
	FeedbackIDs      []string          `json:"feedbackIds"`
	Version          int               `json:"version,omitempty"`
	At               time.Time         `json:"at,omitempty"`
	Discard          *DraftDiscard     `json:"discard,omitempty"`
	Approval         *DraftApproval    `json:"approval,omitempty"`
	Publication      *DraftPublication `json:"publication,omitempty"`
}

type DraftDiscard struct {
	ID               string         `json:"id"`
	DraftID          string         `json:"draftId"`
	ExpectedChangeID string         `json:"expectedChangeId"`
	Target           FeedbackTarget `json:"target"`
	Actor            string         `json:"actor"`
	Source           string         `json:"source"`
	Reason           string         `json:"reason"`
	At               time.Time      `json:"at,omitempty"`
}

func validDraftSource(id, actor, source, reason string) bool {
	for _, value := range []string{id, actor, source, reason} {
		if value == "" || strings.TrimSpace(value) != value {
			return false
		}
	}
	return true
}

func validInstructionDraft(draft InstructionDraft) bool {
	return validDraftSource(draft.ID, draft.Actor, draft.Source, draft.Reason) &&
		slices.Contains([]string{"CODER", "REVIEWER"}, draft.NextRole) &&
		nonempty(draft.Operation) && nonempty(draft.Text) &&
		draft.Evidence != nil && validList(draft.Evidence) &&
		draft.Unconfirmed != nil && validList(draft.Unconfirmed) &&
		draft.FeedbackIDs != nil && validList(draft.FeedbackIDs)
}

func cloneInstructionDrafts(drafts []InstructionDraft) []InstructionDraft {
	copy := slices.Clone(drafts)
	for i := range copy {
		copy[i].Evidence = slices.Clone(drafts[i].Evidence)
		copy[i].Unconfirmed = slices.Clone(drafts[i].Unconfirmed)
		copy[i].FeedbackIDs = slices.Clone(drafts[i].FeedbackIDs)
		if drafts[i].Discard != nil {
			discard := *drafts[i].Discard
			copy[i].Discard = &discard
		}
		if drafts[i].Approval != nil {
			approval := *drafts[i].Approval
			copy[i].Approval = &approval
		}
		if drafts[i].Publication != nil {
			publication := *drafts[i].Publication
			copy[i].Publication = &publication
		}
	}
	return copy
}

func (f ReviewFlow) PendingDraftID() string {
	if len(f.InstructionDrafts) == 0 {
		return ""
	}
	last := f.InstructionDrafts[len(f.InstructionDrafts)-1]
	if last.Discard != nil || last.Publication != nil {
		return ""
	}
	return last.ID
}

func (g TaskGraph) checkDraftTarget(flow ReviewFlow, target FeedbackTarget, changeID string) error {
	last, _ := g.dispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	current, err := ObserveFeedbackTarget(last)
	if err != nil {
		return err
	}
	if current != target {
		return ErrFeedbackStale
	}
	if !flow.HandoffHeld() || changeID != flow.HandoffChanges[len(flow.HandoffChanges)-1].ID {
		return graphError("instruction draft requires the current held handoff; observe again")
	}
	return nil
}

func (g *TaskGraph) RecordInstructionDraft(flowID string, draft InstructionDraft) (ReviewFlow, error) {
	if !validInstructionDraft(draft) || draft.Version != 0 || !draft.At.IsZero() || draft.Discard != nil || draft.Approval != nil || draft.Publication != nil {
		return ReviewFlow{}, graphError("instruction draft requires sourced text, next role, operation and explicit evidence, unconfirmed and feedback ID lists; version, time and discard are controller-owned")
	}
	if err := g.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	flow, ok := g.ReviewFlow(flowID)
	if !ok {
		return ReviewFlow{}, graphError("instruction draft requires an existing flow")
	}
	for _, previous := range flow.InstructionDrafts {
		if previous.ID == draft.ID {
			previous.Version, previous.At, previous.Discard, previous.Approval, previous.Publication = 0, time.Time{}, nil, nil, nil
			if !reflect.DeepEqual(previous, draft) {
				return ReviewFlow{}, graphError("instruction draft ID was reused with different content")
			}
			return flow, nil
		}
	}
	if flow.Status != "RUNNING" {
		return ReviewFlow{}, graphError("new instruction draft requires a running flow")
	}
	if err := g.checkDraftTarget(flow, draft.Target, draft.ExpectedChangeID); err != nil {
		return ReviewFlow{}, err
	}
	priorID := ""
	if len(flow.InstructionDrafts) != 0 {
		priorID = flow.InstructionDrafts[len(flow.InstructionDrafts)-1].ID
	}
	if draft.ExpectedDraftID != priorID {
		return ReviewFlow{}, graphError("instruction draft version changed; observe the latest draft before revising")
	}
	if len(flow.InstructionDrafts) != 0 {
		last := flow.InstructionDrafts[len(flow.InstructionDrafts)-1]
		if last.Approval != nil && last.Discard == nil && last.Publication == nil {
			return ReviewFlow{}, graphError("discard the approved pending draft before revising")
		}
	}
	for _, id := range draft.FeedbackIDs {
		if !slices.ContainsFunc(flow.Feedback, func(feedback FlowFeedback) bool { return feedback.ID == id }) {
			return ReviewFlow{}, graphError("instruction draft references unknown feedback %s", id)
		}
	}
	draft.Version, draft.At = len(flow.InstructionDrafts)+1, time.Now().UTC()
	flow.InstructionDrafts = cloneInstructionDrafts(append(flow.InstructionDrafts, draft))
	return g.saveDraftFlow(flow)
}

func (g *TaskGraph) DiscardInstructionDraft(flowID string, discard DraftDiscard) (ReviewFlow, error) {
	if !validDraftSource(discard.ID, discard.Actor, discard.Source, discard.Reason) || !discard.At.IsZero() {
		return ReviewFlow{}, graphError("draft discard requires ID, actor, source and reason; timestamp is controller-owned")
	}
	if err := g.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	flow, ok := g.ReviewFlow(flowID)
	if !ok || len(flow.InstructionDrafts) == 0 {
		return ReviewFlow{}, graphError("draft discard requires an existing instruction draft")
	}
	for _, draft := range flow.InstructionDrafts {
		if previous := draft.Discard; previous != nil && previous.ID == discard.ID {
			recorded := *previous
			recorded.At = time.Time{}
			if recorded != discard {
				return ReviewFlow{}, graphError("draft discard ID was reused with different content")
			}
			return flow, nil
		}
	}
	index := len(flow.InstructionDrafts) - 1
	if flow.InstructionDrafts[index].ID != discard.DraftID || flow.InstructionDrafts[index].Discard != nil || flow.InstructionDrafts[index].Publication != nil {
		return ReviewFlow{}, graphError("only the current undiscarded instruction draft can be discarded")
	}
	if err := g.checkDraftTarget(flow, discard.Target, discard.ExpectedChangeID); err != nil {
		return ReviewFlow{}, err
	}
	discard.At = time.Now().UTC()
	flow.InstructionDrafts[index].Discard = &discard
	return g.saveDraftFlow(flow)
}

func (g *TaskGraph) saveDraftFlow(flow ReviewFlow) (ReviewFlow, error) {
	next := cloneGraph(*g)
	for i := range next.ReviewFlows {
		if next.ReviewFlows[i].ID == flow.ID {
			next.ReviewFlows[i] = flow
		}
	}
	if err := next.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	*g = next
	saved, _ := g.ReviewFlow(flow.ID)
	return saved, nil
}

func (g TaskGraph) validateInstructionDrafts(flow ReviewFlow) error {
	ids, discards := map[string]bool{}, map[string]bool{}
	priorID := ""
	for i, draft := range flow.InstructionDrafts {
		if !validInstructionDraft(draft) || ids[draft.ID] || draft.ExpectedDraftID != priorID ||
			draft.Version != i+1 || draft.At.IsZero() ||
			i > 0 && draft.At.Before(flow.InstructionDrafts[i-1].At) {
			return graphError("invalid instruction draft history in flow %s", flow.ID)
		}
		if err := g.validateSavedHandoffObservation(flow, draft.Target, draft.ExpectedChangeID, draft.At); err != nil {
			return err
		}
		for _, id := range draft.FeedbackIDs {
			if !slices.ContainsFunc(flow.Feedback, func(feedback FlowFeedback) bool {
				return feedback.ID == id && !feedback.At.After(draft.At)
			}) {
				return graphError("instruction draft lost its feedback source")
			}
		}
		if discard := draft.Discard; discard != nil {
			if !validDraftSource(discard.ID, discard.Actor, discard.Source, discard.Reason) ||
				discards[discard.ID] || discard.DraftID != draft.ID || discard.At.Before(draft.At) ||
				i+1 < len(flow.InstructionDrafts) && discard.At.After(flow.InstructionDrafts[i+1].At) {
				return graphError("invalid instruction draft discard")
			}
			if err := g.validateSavedHandoffObservation(flow, discard.Target, discard.ExpectedChangeID, discard.At); err != nil {
				return err
			}
			discards[discard.ID] = true
		}
		if err := g.validateDraftApproval(flow, draft, i); err != nil {
			return err
		}
		ids[draft.ID], priorID = true, draft.ID
	}
	return nil
}
