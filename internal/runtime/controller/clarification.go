package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type ClarificationContext struct {
	Task      core.GraphTask         `json:"task"`
	Dispatch  *core.GraphDispatch    `json:"dispatch,omitempty"`
	Documents []core.ContextDocument `json:"documents"`
	Hash      string                 `json:"hash"`
	ScopeHash string                 `json:"scopeHash"`
}

type ClarificationAssumption struct {
	Text       string   `json:"text"`
	Impact     []string `json:"impact"`
	Reversible bool     `json:"reversible"`
}

type ClarificationRequest struct {
	ID                     string                   `json:"id"`
	TaskID                 string                   `json:"taskId"`
	DispatchID             string                   `json:"dispatchId,omitempty"`
	Role                   string                   `json:"role"`
	Category               string                   `json:"category"`
	SpecificationOwnerRole string                   `json:"specificationOwnerRole,omitempty"`
	BasisHash              string                   `json:"basisHash"`
	Question               string                   `json:"question"`
	Reason                 string                   `json:"reason"`
	Uncertainty            string                   `json:"uncertainty"`
	References             []string                 `json:"references"`
	AnswerFormat           string                   `json:"answerFormat"`
	DecisionImpact         string                   `json:"decisionImpact"`
	StopCondition          string                   `json:"stopCondition"`
	Deadline               time.Time                `json:"deadline"`
	SavedSourceID          string                   `json:"savedSourceId,omitempty"`
	Assumption             *ClarificationAssumption `json:"assumption,omitempty"`
}

type ClarificationAnswer struct {
	Role    string                `json:"role"`
	Actor   string                `json:"actor"`
	Source  string                `json:"source"`
	Text    string                `json:"text"`
	At      time.Time             `json:"at"`
	Receipt *ClarificationReceipt `json:"receipt,omitempty"`
}

type ClarificationReceipt struct {
	RequestID string `json:"requestId"`
	Sequence  uint64 `json:"sequence"`
	Hash      string `json:"hash"`
}

type clarificationKey struct{}

type ClarificationOrigin struct {
	SessionID   string `json:"sessionId"`
	OperationID string `json:"operationId"`
	Owner       string `json:"owner"`
	RunnerID    string `json:"runnerId"`
}

// These are planning receipts, not authorization or executable instructions.
type Clarification struct {
	Version    int                      `json:"version"`
	Phase      string                   `json:"phase"`
	Request    ClarificationRequest     `json:"request"`
	Context    ClarificationContext     `json:"context"`
	Signature  string                   `json:"signature"`
	Route      string                   `json:"route"`
	Recipient  string                   `json:"recipient,omitempty"`
	State      string                   `json:"state"`
	Answer     *ClarificationAnswer     `json:"answer,omitempty"`
	Conflict   *ClarificationAnswer     `json:"conflict,omitempty"`
	Resolution *ClarificationResolution `json:"resolution,omitempty"`
	At         time.Time                `json:"at"`
	Origin     *ClarificationOrigin     `json:"origin,omitempty"`
	Delivery   *ClarificationDelivery   `json:"delivery,omitempty"`
	Handoff    *ClarificationHandoff    `json:"handoff,omitempty"`
}

func ObserveClarification(db *store.Store, taskID, dispatchID string) (ClarificationContext, error) {
	state, err := db.LoadWorkspace()
	if err != nil {
		return ClarificationContext{}, err
	}
	return clarificationContext(state, taskID, dispatchID)
}

func clarificationContext(state store.Workspace, taskID, dispatchID string) (ClarificationContext, error) {
	task, ok := state.Graph.GraphTask(taskID)
	if !state.HasGraph || !ok {
		return ClarificationContext{}, fmt.Errorf("taxion: clarification requires a saved Task")
	}
	context := ClarificationContext{Task: task}
	documents, err := core.ContextDocumentsFromDecisions(task.Decisions)
	if err != nil {
		return context, err
	}
	if dispatchID != "" {
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.TaskID != taskID {
			return context, fmt.Errorf("taxion: clarification Dispatch does not belong to the Task")
		}
		context.Dispatch = &dispatch
		documents, err = DispatchContextDocuments(state, dispatch)
		if err != nil {
			return context, err
		}
	}
	configuration, instruction := "", ""
	if context.Dispatch != nil {
		configuration, err = core.CanonicalSHA256(context.Dispatch.Configuration)
		if err != nil {
			return context, err
		}
		instruction = context.Dispatch.InstructionHash
	} else {
		configuration, err = core.CanonicalSHA256(nil)
		if err != nil {
			return context, err
		}
	}
	dimensions, err := core.BundleDimensions(task, core.ApprovalConfiguration{Digest: configuration})
	if err != nil {
		return context, err
	}
	// Reuse the contract dimensions; progress and accounting are observations,
	// not new specifications or execution authority.
	flowID, deadline := "", time.Time{}
	if flow, ok := clarificationFlow(state.Graph, taskID, dispatchID); ok {
		flowID, deadline = flow.ID, flow.ExecutionDeadline()
	}
	context.ScopeHash, err = core.CanonicalSHA256(struct {
		TaskID, Repo, Type, Objective, ParentID, Supersedes, CreatedFrom, TargetSHA string
		Issue, ChangeRef                                                            string
		Dependencies                                                                []core.TaskDependency
		Dimensions                                                                  core.ApprovalDimensions
		DispatchID, InstructionHash, DocumentsHash string
		FlowID                                     string
		Deadline                                  time.Time
	}{task.ID, task.Repo, task.Type, task.Objective, task.ParentID, task.Supersedes, task.CreatedFrom, task.TargetSHA, task.Issue, task.ChangeRef,
		task.DependsOn, dimensions, dispatchID, instruction, core.ContextDocumentsHash(documents), flowID, deadline})
	if err != nil {
		return context, err
	}
	body, err := json.Marshal(task)
	if err != nil {
		return context, err
	}
	document, err := core.CaptureContextDocument(core.ContextDocument{
		ID: "taxion:clarification-task", Kind: "contract", Source: "task:" + task.ID,
		Version: "1", Text: string(body),
	})
	if err != nil {
		return context, err
	}
	context.Documents = append(documents, document)
	context.Hash, err = core.CanonicalSHA256(context)
	return context, err
}

func RequestClarification(db *store.Store, input ClarificationRequest) (Clarification, error) {
	var result Clarification
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var err error
		result, err = requestClarification(state, input, nil)
		return err
	})
	return result, err
}

func requestClarification(state *store.Workspace, input ClarificationRequest, origin *ClarificationOrigin) (Clarification, error) {
	var result Clarification
	err := func() error {
		context, err := clarificationContext(*state, input.TaskID, input.DispatchID)
		if err != nil {
			return err
		}
		if input.BasisHash == "" || input.BasisHash != context.Hash {
			return fmt.Errorf("taxion: clarification source changed; observe the current Task and Dispatch")
		}
		for _, required := range []string{input.ID, input.Question, input.Reason, input.Uncertainty,
			input.DecisionImpact, input.StopCondition} {
			if strings.TrimSpace(required) == "" {
				return fmt.Errorf("taxion: clarification requires identity, question, reason, uncertainty, impact and stop condition")
			}
		}
		if len(input.References) == 0 || input.Deadline.IsZero() ||
			input.AnswerFormat != "text" && input.AnswerFormat != "json" {
			return fmt.Errorf("taxion: clarification requires consulted references, deadline and text/json answer format")
		}
		for _, reference := range input.References {
			if strings.TrimSpace(reference) == "" {
				return fmt.Errorf("taxion: clarification reference must not be empty")
			}
			if flow, ok := clarificationFlow(state.Graph, input.TaskID, input.DispatchID); ok &&
				!flow.ExecutionDeadline().IsZero() && input.Deadline.After(flow.ExecutionDeadline()) {
				input.Deadline = flow.ExecutionDeadline()
			}
		}
		if input.Role != "PLANNER" && input.Role != "CODER" && input.Role != "REVIEWER" {
			return fmt.Errorf("taxion: clarification requires a Planner, Coder or Reviewer sender")
		}
		if input.Role != "PLANNER" && context.Dispatch == nil ||
			context.Dispatch != nil && context.Dispatch.Recipient != input.Role {
			return fmt.Errorf("taxion: clarification sender must match its saved Dispatch")
		}
		if input.SpecificationOwnerRole != "" && input.SpecificationOwnerRole != "PLANNER" &&
			input.SpecificationOwnerRole != "HUMAN" {
			return fmt.Errorf("taxion: specification owner must be Planner or Human")
		}
		route, recipient := "ASK", "PLANNER"
		switch input.Category {
		case "specification":
			if input.SpecificationOwnerRole != "" {
				recipient = input.SpecificationOwnerRole
			} else if input.Role == "PLANNER" {
				recipient = "HUMAN"
			}
		case "implementation", "evidence":
			if input.Role != "REVIEWER" {
				return fmt.Errorf("taxion: implementation and evidence questions require a Reviewer sender")
			}
			recipient = "CODER"
		case "authority", "intent", "irreversible", "scope-change", "budget-change", "deadline-change":
			recipient = "HUMAN"
		case "safety", "identity", "integrity", "budget", "deadline":
			route, recipient = "STOP", ""
		case "saved":
			if input.SavedSourceID == "" || !slices.Contains(input.References, input.SavedSourceID) {
				return fmt.Errorf("taxion: saved answer requires a consulted, bound original source")
			}
			found := false
			for _, document := range context.Documents {
				found = found || document.ID == input.SavedSourceID && document.Kind != "reference"
			}
			if !found {
				return fmt.Errorf("taxion: saved clarification source is not bound to this Task or Dispatch")
			}
			route, recipient = "REUSE", ""
		case "minor":
			a := input.Assumption
			if a == nil || !a.Reversible || strings.TrimSpace(a.Text) == "" || len(a.Impact) == 0 {
				return fmt.Errorf("taxion: minor clarification requires a reversible assumption and explicit impact")
			}
			for _, impact := range a.Impact {
				if !slices.Contains(context.Task.Scope, impact) {
					return fmt.Errorf("taxion: assumption impact exceeds the saved Task scope")
				}
			}
			route, recipient = "ASSUME", ""
		default:
			return fmt.Errorf("taxion: unknown clarification category")
		}
		now := time.Now().UTC()
		if !now.Before(input.Deadline) {
			route, recipient = "STOP", ""
		}
		signature, err := core.CanonicalSHA256(struct {
			Task, Dispatch, Role, Category, Uncertainty, Basis string
			Owner, Format, SavedSource                         string
			Assumption                                         *ClarificationAssumption
		}{input.TaskID, input.DispatchID, input.Role, input.Category, input.Uncertainty, context.ScopeHash,
			input.SpecificationOwnerRole, input.AnswerFormat, input.SavedSourceID, input.Assumption})
		if err != nil {
			return err
		}
		if origin != nil {
			contract := input
			contract.ID, contract.BasisHash, contract.Deadline = "", context.ScopeHash, time.Time{}
			signature, err = core.CanonicalSHA256(struct {
				Request ClarificationRequest
				Origin  ClarificationOrigin
			}{contract, *origin})
			if err != nil {
				return err
			}
		}
		history, err := clarifications(*state)
		if err != nil {
			return err
		}
		for _, saved := range history {
			if saved.Request.ID == input.ID && saved.Signature != signature {
				return fmt.Errorf("taxion: clarification ID already binds different question evidence")
			}
			if saved.Signature == signature {
				if origin != nil && (saved.Origin == nil || *saved.Origin != *origin ||
					saved.Request.TaskID != input.TaskID || saved.Request.DispatchID != input.DispatchID ||
					saved.Request.Role != input.Role) {
					return fmt.Errorf("taxion: clarification replay belongs to a different execution origin")
				}
				if origin != nil {
					prior, current := saved.Request, input
					prior.ID, prior.BasisHash, prior.Deadline = "", context.ScopeHash, time.Time{}
					current.ID, current.BasisHash, current.Deadline = "", context.ScopeHash, time.Time{}
					if !reflect.DeepEqual(prior, current) {
						return fmt.Errorf("taxion: clarification replay differs from the original question contract")
					}
				}
				if !now.Before(saved.Request.Deadline) {
					return fmt.Errorf("taxion: saved clarification deadline expired; do not extend it through replay")
				}
				// Return the original deadline and contract, not a caller's replacement.
				result = saved
			}
		}
		if result.Version != 0 {
			return nil
		}
		result = Clarification{
			Version: 1, Phase: "clarification.request", Request: input, Context: context,
			Signature: signature, Route: route, Recipient: recipient, State: "OBSERVED", At: now, Origin: origin,
		}
		return appendClarification(state, result)
	}()
	if err == nil && result.Route == "ASK" && result.Recipient == "CODER" {
		var delivery ClarificationDelivery
		delivery, err = resolveCoderClarification(state, result.Request.ID)
		result.Delivery = &delivery
	}
	return result, err
}

func AnswerClarification(db *store.Store, id string, answer ClarificationAnswer) (Clarification, error) {
	var result Clarification
	var conflictErr error
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		history, err := clarifications(*state)
		if err != nil {
			return err
		}
		for _, saved := range history {
			if saved.Request.ID == id {
				result = saved
			}
		}
		if result.Version == 0 || result.Route != "ASK" {
			return fmt.Errorf("taxion: answer requires a saved, routed clarification")
		}
		if answer.Receipt != nil || answer.Role != result.Recipient && answer.Role != "HUMAN" ||
			strings.TrimSpace(answer.Actor) == "" || strings.TrimSpace(answer.Source) == "" ||
			strings.TrimSpace(answer.Text) == "" {
			return fmt.Errorf("taxion: answer requires the routed respondent and explicit provenance")
		}
		if result.Request.AnswerFormat == "json" && !json.Valid([]byte(answer.Text)) {
			return fmt.Errorf("taxion: clarification requires a JSON answer")
		}
		if result.Answer != nil {
			old := *result.Answer
			old.At, answer.At = time.Time{}, time.Time{}
			if old.Receipt != nil || old.Role != answer.Role || old.Actor != answer.Actor ||
				old.Source != answer.Source || old.Text != answer.Text {
				conflictErr = fmt.Errorf("taxion: conflicting answer retained as OBSERVED; resolution and revalidation are required")
				for _, saved := range history {
					prior := saved.Conflict
					if saved.Request.ID == id && prior != nil && prior.Role == answer.Role && prior.Actor == answer.Actor &&
						prior.Source == answer.Source && prior.Text == answer.Text {
						if result.Resolution != nil {
							conflictErr = nil
						}
						return nil
					}
				}
				answer.At = time.Now().UTC()
				result.Phase, result.Conflict, result.Resolution = "clarification.conflict", &answer, nil
				return appendClarification(state, result)
			}
			if result.Conflict != nil && result.Resolution == nil {
				conflictErr = fmt.Errorf("taxion: earlier conflicting answer remains unresolved")
			}
			return nil
		}
		answer.At = time.Now().UTC()
		result.Phase, result.Answer = "clarification.answer", &answer
		return appendClarification(state, result)
	})
	if err != nil {
		return result, err
	}
	return result, conflictErr
}

func ClarificationHistory(db *store.Store) ([]Clarification, error) {
	state, err := db.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	return clarificationHistory(state)
}

func clarificationHistory(state store.Workspace) ([]Clarification, error) {
	history, err := clarifications(state)
	if err != nil {
		return nil, err
	}
	for i := range history {
		history[i].Delivery, err = savedClarificationDelivery(state, history[i].Request.ID)
		if err != nil {
			return nil, err
		}
		history[i].Handoff, _, err = savedClarificationHandoff(state, history[i].Request.ID)
		if err != nil {
			return nil, err
		}
		if err := hydrateClarificationAnswer(state, history[i].Answer); err != nil {
			return nil, err
		}
		if history[i].Resolution != nil {
			if err := validateClarificationResolution(state, history[i]); err != nil {
				return nil, err
			}
		}
	}
	return history, nil
}

func hydrateClarificationAnswer(state store.Workspace, answer *ClarificationAnswer) error {
	if answer == nil || answer.Receipt == nil {
		return nil
	}
	for _, record := range state.Records.Requests {
		ref := answer.Receipt
		if record.Kind != "planning" || record.RequestID != ref.RequestID || record.Sequence != ref.Sequence {
			continue
		}
		var terminal struct {
			Answer json.RawMessage `json:"answer"`
		}
		if record.Hash != ref.Hash || json.Unmarshal([]byte(record.Payload), &terminal) != nil ||
			len(terminal.Answer) == 0 || string(terminal.Answer) == "null" {
			return fmt.Errorf("taxion: clarification answer receipt is invalid")
		}
		answer.Text = string(terminal.Answer)
		return nil
	}
	return fmt.Errorf("taxion: clarification answer receipt is unavailable")
}

// AskPlannerClarification uses the existing isolated question lifecycle and
// persists a reference to its canonical receipt, not a second Planner answer.
func AskPlannerClarification(ctx context.Context, db *store.Store, ledgerPath, taskID, id string,
	selected config.Config, worker runtime.ConfigurationIdentity) (QuestionResult, error) {
	history, err := ClarificationHistory(db)
	if err != nil {
		return QuestionResult{}, err
	}
	var saved Clarification
	for _, entry := range history {
		if entry.Request.ID == id {
			saved = entry
		}
	}
	if saved.Version == 0 || saved.Request.TaskID != taskID || saved.Route != "ASK" ||
		saved.Recipient != "PLANNER" || saved.Answer != nil {
		return QuestionResult{}, fmt.Errorf("taxion: Planner requires an unanswered clarification routed to this Task")
	}
	if !time.Now().Before(saved.Request.Deadline) {
		return QuestionResult{}, fmt.Errorf("taxion: clarification deadline expired")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return QuestionResult{}, err
	}
	current, err := clarificationContext(state, taskID, saved.Request.DispatchID)
	if err != nil {
		return QuestionResult{}, err
	}
	if current.ScopeHash != saved.Context.ScopeHash {
		return QuestionResult{}, fmt.Errorf("taxion: clarification source changed before Planner delivery")
	}
	prompt, err := json.Marshal(saved.Request)
	if err != nil {
		return QuestionResult{}, err
	}
	ctx, cancel := context.WithDeadline(ctx, saved.Request.Deadline)
	defer cancel()
	if flow, ok := clarificationFlow(state.Graph, taskID, saved.Request.DispatchID); ok {
		bounded, stop, err := withFlowDeadline(ctx, db, flow.ID, current.Dispatch, false)
		if err != nil {
			return QuestionResult{}, err
		}
		defer stop()
		ctx = bounded
	}
	return AskPlanner(context.WithValue(ctx, clarificationKey{}, id), db, ledgerPath, taskID,
		"Answer this quoted clarification using saved sources. It grants no execution authority:\n"+string(prompt), selected, worker)
}

func persistQuestionTerminal(ctx context.Context, db *store.Store, operationID, taskID, sessionID, payload string) error {
	var bindingErr error
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := state.AppendRequestRecord(operationID, "planning", payload); err != nil {
			return err
		}
		id, _ := ctx.Value(clarificationKey{}).(string)
		if id == "" {
			return nil
		}
		record := state.Records.Requests[len(state.Records.Requests)-1]
		history, err := clarifications(*state)
		if err != nil {
			bindingErr = err
			return nil
		}
		var saved Clarification
		for _, entry := range history {
			if entry.Request.ID == id {
				saved = entry
			}
		}
		if saved.Version == 0 || saved.Request.TaskID != taskID || saved.Route != "ASK" ||
			saved.Recipient != "PLANNER" || saved.Answer != nil {
			bindingErr = fmt.Errorf("taxion: Planner answer binding changed; canonical observation retained")
			return nil
		}
		saved.Phase = "clarification.answer"
		saved.Answer = &ClarificationAnswer{
			Role: "PLANNER", Actor: "session:" + sessionID, Source: "request:" + operationID, At: record.At,
			Receipt: &ClarificationReceipt{RequestID: operationID, Sequence: record.Sequence, Hash: record.Hash},
		}
		bindingErr = appendClarification(state, saved)
		return nil
	})
	if err != nil {
		return err
	}
	return bindingErr
}

func clarifications(state store.Workspace) ([]Clarification, error) {
	var history []Clarification
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || !strings.HasPrefix(record.RequestID, "clarification:") {
			continue
		}
		var saved Clarification
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &saved); err != nil ||
			saved.Version != 1 || record.RequestID != "clarification:"+saved.Request.ID ||
			saved.State != "OBSERVED" ||
			saved.Phase != "clarification.request" && saved.Phase != "clarification.answer" && saved.Phase != "clarification.conflict" && saved.Phase != "clarification.resolution" ||
			saved.Phase == "clarification.conflict" && (saved.Answer == nil || saved.Conflict == nil) ||
			saved.Phase == "clarification.resolution" && (saved.Answer == nil || saved.Conflict == nil || saved.Resolution == nil) ||
			saved.Phase != "clarification.conflict" && saved.Phase != "clarification.resolution" && saved.Conflict != nil ||
			saved.Phase != "clarification.resolution" && saved.Resolution != nil {
			return nil, fmt.Errorf("taxion: invalid saved clarification receipt")
		}
		history = append(history, saved)
	}
	return history, nil
}

func appendClarification(state *store.Workspace, value Clarification) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if core.Redact(string(body)) != string(body) {
		return fmt.Errorf("taxion: clarification contains credentials; supply explicitly redacted input")
	}
	return state.AppendRequestRecord("clarification:"+value.Request.ID, "planning", string(body))
}

func clarificationFlow(graph core.TaskGraph, taskID, dispatchID string) (core.ReviewFlow, bool) {
	if dispatchID != "" {
		return flowForDispatch(graph, dispatchID)
	}
	for i := len(graph.ReviewFlows) - 1; i >= 0; i-- {
		flow := graph.ReviewFlows[i]
		if flow.TaskID == taskID {
			return flow, true
		}
		for _, id := range flow.DispatchIDs {
			if dispatch, ok := graph.GraphDispatch(id); ok && dispatch.TaskID == taskID {
				return flow, true
			}
		}
	}
	return core.ReviewFlow{}, false
}
