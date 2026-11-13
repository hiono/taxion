package core

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type DraftApproval struct {
	ID               string         `json:"id"`
	DraftID          string         `json:"draftId"`
	ExpectedChangeID string         `json:"expectedChangeId"`
	Target           FeedbackTarget `json:"target"`
	Action           string         `json:"action"`
	Actor            string         `json:"actor"`
	Source           string         `json:"source"`
	Reason           string         `json:"reason"`
	ContractHash     string         `json:"contractHash"`
	At               time.Time      `json:"at,omitempty"`
}

type DraftPublication struct {
	DispatchID      string    `json:"dispatchId"`
	InstructionHash string    `json:"instructionHash"`
	At              time.Time `json:"at"`
}

func (f ReviewFlow) DraftContractHash() (string, error) {
	data, err := json.Marshal(struct {
		Contract      GraphTask    `json:"contract"`
		Authorization TaskDecision `json:"authorization"`
		ReviewRoot    string       `json:"reviewRoot"`
		MaxRepairs    int          `json:"maxRepairs"`
		MaxTokens     int64        `json:"maxTokens"`
		Deadline      time.Time    `json:"deadline"`
	}{f.Contract, f.Authorization, f.ReviewRoot, f.MaxRepairs, f.MaxTokens, f.Deadline})
	if err != nil {
		return "", fmt.Errorf("taxion: encode draft approval contract: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}

func (d InstructionDraft) SupplementalDecision() (TaskDecision, error) {
	if d.Approval == nil {
		return TaskDecision{}, graphError("supplemental decision requires approval")
	}
	copy := cloneInstructionDrafts([]InstructionDraft{d})[0]
	copy.Publication = nil
	data, err := json.Marshal(copy)
	if err != nil {
		return TaskDecision{}, fmt.Errorf("taxion: encode approved instruction supplement: %w", err)
	}
	return TaskDecision{ID: "draft:" + d.ID, Source: d.Approval.Source,
		Text: "Approved supplementary instruction within the unchanged execution contract; this does not waive acceptance, findings, forbidden actions or budgets: " + string(data)}, nil
}

func validDraftApproval(a DraftApproval) bool {
	return validDraftSource(a.ID, a.Actor, a.Source, a.Reason) &&
		a.Action == "APPROVE_SUPPLEMENT" && nonempty(a.DraftID) && nonempty(a.ContractHash)
}

func (g *TaskGraph) ApproveInstructionDraft(flowID string, approval DraftApproval) (ReviewFlow, error) {
	if !validDraftApproval(approval) || !approval.At.IsZero() {
		return ReviewFlow{}, graphError("draft approval requires sourced APPROVE_SUPPLEMENT, draft ID, target and contract hash; timestamp is controller-owned")
	}
	if err := g.Validate(); err != nil {
		return ReviewFlow{}, err
	}
	flow, ok := g.ReviewFlow(flowID)
	if !ok || len(flow.InstructionDrafts) == 0 {
		return ReviewFlow{}, graphError("approval requires an existing draft")
	}
	for _, draft := range flow.InstructionDrafts {
		if previous := draft.Approval; previous != nil && previous.ID == approval.ID {
			recorded := *previous
			recorded.At = time.Time{}
			if recorded != approval {
				return ReviewFlow{}, graphError("draft approval ID was reused with different content")
			}
			return flow, nil
		}
	}
	index := len(flow.InstructionDrafts) - 1
	draft := flow.InstructionDrafts[index]
	if flow.Status != "RUNNING" || draft.ID != approval.DraftID || draft.Approval != nil ||
		draft.Discard != nil || draft.Publication != nil {
		return ReviewFlow{}, graphError("approve only the latest unapproved, undiscarded draft of a running flow")
	}
	if err := g.checkDraftTarget(flow, approval.Target, approval.ExpectedChangeID); err != nil {
		return ReviewFlow{}, err
	}
	hash, err := flow.DraftContractHash()
	if err != nil {
		return ReviewFlow{}, err
	}
	if hash != approval.ContractHash || approval.Target != draft.Target {
		return ReviewFlow{}, graphError("draft basis or approved contract changed; revise and observe again")
	}
	if err := g.checkSupplementBoundary(flow, draft); err != nil {
		return ReviewFlow{}, err
	}
	for _, id := range draft.FeedbackIDs {
		if flow.FeedbackHasApprovedDraft(id) {
			return ReviewFlow{}, graphError("feedback already belongs to another approved supplement")
		}
		for _, feedback := range flow.Feedback {
			if feedback.ID == id && feedback.Resolution != nil {
				return ReviewFlow{}, graphError("draft cannot reapply feedback already disposed of")
			}
		}
	}
	approval.At = time.Now().UTC()
	flow.InstructionDrafts[index].Approval = &approval
	return g.saveDraftFlow(flow)
}

func (g TaskGraph) checkSupplementBoundary(flow ReviewFlow, draft InstructionDraft) error {
	last, _ := g.dispatch(flow.LastDispatchID())
	target, err := ObserveFeedbackTarget(last)
	if err != nil {
		return err
	}
	if target != draft.Target {
		return ErrFeedbackStale
	}
	if last.Status != DispatchSucceeded || last.Result == nil || last.Result.GitEvidence == nil {
		return graphError("supplement approval requires the latest successful saved result and Git evidence")
	}
	role := "REVIEWER"
	if last.Recipient == "REVIEWER" {
		role = "CODER"
		if last.Result.ReviewVerdict != "REQUEST_CHANGES" || flow.Repairs >= flow.MaxRepairs {
			return graphError("supplement requires an authorized repair boundary, not a verdict override")
		}
	}
	if draft.NextRole != role {
		return graphError("supplement cannot change the next authorized role")
	}
	return nil
}

func (g TaskGraph) CheckApprovedDraft(flow ReviewFlow) error {
	if flow.PendingDraftID() == "" {
		return nil
	}
	draft := flow.InstructionDrafts[len(flow.InstructionDrafts)-1]
	if draft.Approval == nil {
		return graphError("instruction draft requires explicit supplementary approval")
	}
	return g.checkSupplementBoundary(flow, draft)
}

func (g TaskGraph) validateDraftApproval(flow ReviewFlow, draft InstructionDraft, index int) error {
	if draft.Approval == nil {
		if draft.Publication != nil {
			return graphError("draft publication requires approval")
		}
		return nil
	}
	a := *draft.Approval
	hash, err := flow.DraftContractHash()
	if err != nil {
		return err
	}
	if !validDraftApproval(a) || a.DraftID != draft.ID || a.Target != draft.Target ||
		a.ContractHash != hash || a.At.Before(draft.At) ||
		draft.Discard != nil && draft.Discard.At.Before(a.At) ||
		index+1 < len(flow.InstructionDrafts) && draft.Discard == nil && draft.Publication == nil {
		return graphError("invalid supplementary draft approval")
	}
	basis, ok := g.dispatch(draft.Target.DispatchID)
	role := "REVIEWER"
	if basis.Recipient == "REVIEWER" {
		role = "CODER"
	}
	if !ok || basis.Status != DispatchSucceeded || basis.Result == nil || basis.Result.GitEvidence == nil ||
		draft.NextRole != role || role == "CODER" &&
		(basis.Result.ReviewVerdict != "REQUEST_CHANGES" || slices.Index(flow.DispatchIDs, basis.ID)/2 >= flow.MaxRepairs) {
		return graphError("supplement approval requires the original successful review or repair boundary")
	}
	if index+1 < len(flow.InstructionDrafts) && a.At.After(flow.InstructionDrafts[index+1].At) {
		return graphError("approval cannot follow a newer draft")
	}
	for _, id := range draft.FeedbackIDs {
		for i, other := range flow.InstructionDrafts {
			if i != index && draft.Discard == nil && other.Discard == nil && other.Approval != nil &&
				slices.Contains(other.FeedbackIDs, id) {
				return graphError("feedback belongs to multiple approved supplements")
			}
		}
		for _, feedback := range flow.Feedback {
			if feedback.ID == id && feedback.Resolution != nil && draft.Discard == nil {
				return graphError("approved supplement conflicts with feedback disposition")
			}
		}
	}
	if draft.Discard == nil && draft.Publication == nil &&
		slices.Index(flow.DispatchIDs, draft.Target.DispatchID)+1 < len(flow.DispatchIDs) {
		return graphError("flow advanced without recording approved draft publication")
	}
	if err := g.validateSavedHandoffObservation(flow, a.Target, a.ExpectedChangeID, a.At); err != nil {
		return err
	}
	for i, previous := range flow.InstructionDrafts {
		if i != index && previous.Approval != nil && previous.Approval.ID == a.ID {
			return graphError("duplicate draft approval ID")
		}
	}
	if p := draft.Publication; p != nil {
		if draft.Discard != nil || p.At.Before(a.At) {
			return graphError("draft publication conflicts with discard or approval time")
		}
		if index+1 < len(flow.InstructionDrafts) && p.At.After(flow.InstructionDrafts[index+1].At) {
			return graphError("publication cannot follow a newer draft")
		}
		dispatch, ok := g.dispatch(p.DispatchID)
		basis := slices.Index(flow.DispatchIDs, draft.Target.DispatchID)
		published := slices.Index(flow.DispatchIDs, p.DispatchID)
		if !ok || basis < 0 || published != basis+1 || dispatch.Recipient != draft.NextRole ||
			p.InstructionHash != dispatch.InstructionHash || dispatch.CreatedAt.After(p.At) {
			return graphError("draft publication lost its exact next Dispatch")
		}
		decision, err := draft.SupplementalDecision()
		if err != nil || !slices.Contains(dispatch.Instruction.Decisions, decision) {
			return graphError("published Dispatch does not contain the approved supplement")
		}
	}
	return nil
}
