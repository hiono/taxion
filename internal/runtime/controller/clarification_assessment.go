package controller

import (
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type ClarificationAssessment struct {
	ID         string                         `json:"id"`
	Receipt    ClarificationReceipt           `json:"receipt"`
	State      string                         `json:"state"`
	Decision   core.InputDecision             `json:"decision"`
	Budget     *TaskBudgetReport              `json:"savedBudget,omitempty"`
	Candidates []ClarificationAnswerCandidate `json:"candidates"`
	Resolution *ClarificationResolution       `json:"resolution,omitempty"`
}

// AssessClarification observes saved evidence; it never accepts an answer or resumes work.
func AssessClarification(db *store.Store, id string) (ClarificationAssessment, error) {
	state, err := db.LoadWorkspace()
	if err != nil {
		return ClarificationAssessment{}, err
	}
	return assessClarification(state, id)
}

func assessClarification(state store.Workspace, id string) (ClarificationAssessment, error) {
	history, err := clarificationHistory(state)
	if err != nil {
		return ClarificationAssessment{}, err
	}
	var saved Clarification
	for _, entry := range history {
		if entry.Request.ID == id {
			saved = entry
		}
	}
	if saved.Version == 0 {
		return ClarificationAssessment{}, fmt.Errorf("taxion: assessment requires a saved clarification")
	}
	current, contextErr := clarificationContext(state, saved.Request.TaskID, saved.Request.DispatchID)
	flow, hasFlow := clarificationFlow(state.Graph, saved.Request.TaskID, saved.Request.DispatchID)
	report := ClarificationAssessment{ID: id, State: saved.State, Resolution: saved.Resolution}
	report.Candidates, err = clarificationAnswerCandidates(state, id)
	if err != nil {
		return report, err
	}
	for _, record := range state.Records.Requests {
		if record.Kind == "planning" && record.RequestID == "clarification:"+id {
			report.Receipt = ClarificationReceipt{record.RequestID, record.Sequence, record.Hash}
		}
	}
	now := time.Now().UTC()
	if hasFlow {
		budget := taskBudgetReport(state.Graph, flow, now)
		report.Budget = &budget
	}
	report.Decision, err = core.NewInputDecision(struct {
		Question   Clarification
		Current    ClarificationContext
		Flow       core.ReviewFlow
		Operation  *store.Operation
		Candidates []ClarificationAnswerCandidate
	}{saved, current, flow, state.Records.Operation, report.Candidates})
	if err != nil {
		return report, err
	}
	report.Decision.Metadata = "present"
	report.Decision.NextActions = []string{
		"Observation only: accept is input eligibility, not ACCEPTED, AUTHORIZED or DISPATCHED.",
		"Use the existing target/configuration/evidence/approval/budget/deadline gates before continuing; recheck live quota where required.",
	}
	block := func(code, field, reason, resolution string, approval bool) {
		report.Decision.Block(core.InputCondition{
			Code: code, Field: field, Reason: reason, Resolution: resolution,
			EvidenceSource: report.Receipt.RequestID, RequiresApproval: approval,
		})
	}
	if !now.Before(saved.Request.Deadline) {
		block("clarification-deadline", "request.deadline", "The saved question deadline expired.",
			"Preserve the answer and use explicit change control; do not extend the question by replay.", false)
	}
	if contextErr != nil || current.ScopeHash != saved.Context.ScopeHash {
		block("clarification-context", "context.scopeHash", "The saved Task, Dispatch, scope or evidence no longer matches.",
			"Revalidate the current target through existing change control; do not reuse the old answer as accepted input.", false)
	}
	if prior := saved.Context.Dispatch; prior != nil && current.Dispatch != nil {
		d := current.Dispatch
		if d.RuntimeSessionID != prior.RuntimeSessionID || d.RuntimeConfigHash != prior.RuntimeConfigHash ||
			d.Recipient != prior.Recipient || d.Status != prior.Status {
			block("clarification-session", "context.dispatch", "The saved Role execution identity or status changed.",
				"Observe and revalidate the exact execution; do not resume or transfer to another session.", false)
		}
	}
	if contextErr == nil && current.Task.Status != saved.Context.Task.Status {
		block("clarification-task", "context.task.status", "The saved Task lifecycle changed.",
			"Recheck the current Task through existing gates; the answer does not restore its previous state.", false)
	}
	if origin := saved.Origin; origin != nil {
		op := state.Records.Operation
		if op == nil || op.ID != origin.OperationID || origin.Owner == "" || op.Owner != origin.Owner || op.Kind != "request" ||
			op.State != "RUNNING" || op.Phase != "EXECUTION" || !now.Before(op.Deadline) ||
			!hasFlow || op.FlowID != flow.ID || op.TaskID != flow.TaskID ||
			flow.RunnerID != origin.RunnerID || current.Dispatch == nil ||
			current.Dispatch.RuntimeSessionID != origin.SessionID {
			block("clarification-owner", "origin", "The originating execution lease or session is no longer current.",
				"Retain the original provenance and use the supported Recovery path; do not rebind ownership.", false)
		}
	}
	if hasFlow {
		if flow.Status != "RUNNING" || flow.HandoffHeld() {
			block("clarification-flow", "flow", "The saved Flow is stopped, finished or held.",
				"Use existing Stop/HOLD/Recovery controls; an answer does not release the Flow.", false)
		}
		if reason := activeBudgetStop(*report.Budget); reason != "" {
			block("clarification-budget", "savedBudget", reason,
				"Reconcile usage and obtain any required budget/deadline change through existing controls.", false)
		}
	}
	if saved.Route == "STOP" {
		block("clarification-stop", "route", "The question records a safety, identity, integrity, budget or deadline stop.",
			"Resolve the recorded stop independently; an answer is not permission to bypass it.", false)
	}
	if saved.Route == "ASK" && saved.Answer == nil {
		block("clarification-unanswered", "answer", "No answer has been observed.",
			"Use the routed Planner or an explicit source-attributed answer; do not start dependent work.", false)
	}
	if saved.Conflict != nil && saved.Resolution == nil {
		block("clarification-conflict", "conflict", "Different answer evidence is preserved and remains unresolved.",
			"Ask the specification owner or human to resolve the preserved evidence; no automatic resolution is supported.", false)
	}
	if saved.Recipient == "HUMAN" && saved.Request.Category != "specification" {
		block("clarification-human-approval", "request.category", "A human answer does not grant the requested execution authority.",
			"Use the existing explicit approval/change-control operation for this material decision.", true)
	}
	if saved.Handoff != nil && saved.Handoff.Status == "REQUESTED" {
		block("clarification-delivery-unknown", "handoff.status", "The handoff has no confirmed delivery outcome.",
			"Preserve the request and do not resend its payload or invent delivery or ACK evidence.", false)
	}
	return report, nil
}
