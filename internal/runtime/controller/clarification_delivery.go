package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type ClarificationTarget struct {
	TaskID      string              `json:"taskId"`
	DispatchID  string              `json:"dispatchId"`
	Role        string              `json:"role"`
	SessionID   string              `json:"sessionId,omitempty"`
	ConfigHash  string              `json:"configHash,omitempty"`
	ScopeHash   string              `json:"scopeHash"`
	Status      core.DispatchStatus `json:"status"`
	FlowID      string              `json:"flowId"`
	OperationID string              `json:"operationId,omitempty"`
	Owner       string              `json:"owner,omitempty"`
	RunnerID    string              `json:"runnerId,omitempty"`
}

// HELD describes delivery only. It is neither a Task HOLD nor authorization.
type ClarificationDelivery struct {
	Version  int                  `json:"version"`
	Phase    string               `json:"phase"`
	Question ClarificationReceipt `json:"question"`
	Status   string               `json:"status"`
	Reason   string               `json:"reason"`
	Target   *ClarificationTarget `json:"target,omitempty"`
	At       time.Time            `json:"at"`
}

func ResolveCoderClarification(db *store.Store, id string) (ClarificationDelivery, error) {
	var result ClarificationDelivery
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var err error
		result, err = resolveCoderClarification(state, id)
		return err
	})
	return result, err
}

func resolveCoderClarification(state *store.Workspace, id string) (ClarificationDelivery, error) {
	history, err := clarifications(*state)
	if err != nil {
		return ClarificationDelivery{}, err
	}
	var question Clarification
	for _, saved := range history {
		if saved.Request.ID == id {
			question = saved
		}
	}
	if question.Version != 1 || question.Route != "ASK" || question.Recipient != "CODER" ||
		question.Request.Role != "REVIEWER" {
		return ClarificationDelivery{}, fmt.Errorf("taxion: Coder resolution requires a saved Reviewer question routed to Coder")
	}
	var reference ClarificationReceipt
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || record.RequestID != "clarification:"+id {
			continue
		}
		var saved Clarification
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &saved); err != nil {
			return ClarificationDelivery{}, err
		}
		if saved.Phase == "clarification.request" {
			if reference.RequestID != "" {
				return ClarificationDelivery{}, fmt.Errorf("taxion: ambiguous canonical clarification")
			}
			reference = ClarificationReceipt{record.RequestID, record.Sequence, record.Hash}
		}
	}
	if reference.RequestID == "" {
		return ClarificationDelivery{}, fmt.Errorf("taxion: canonical clarification request missing")
	}
	target, reason := coderClarificationTarget(*state, question)
	result := ClarificationDelivery{
		Version: 1, Phase: "clarification.delivery", Question: reference, Status: "HELD", Reason: reason, Target: target, At: time.Now().UTC(),
	}
	recordID := "clarification-delivery:" + id
	prior, err := savedClarificationDelivery(*state, id)
	if err != nil {
		return ClarificationDelivery{}, err
	}
	if prior != nil {
		if prior.Question != reference {
			return ClarificationDelivery{}, fmt.Errorf("taxion: clarification delivery refers to different question evidence")
		}
		// A retry may report drift, but cannot rebind the saved destination.
		if !reflect.DeepEqual(prior.Target, target) {
			prior.Reason = "target-identity-changed"
		} else {
			prior.Reason = reason
		}
		return *prior, nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if core.Redact(string(body)) != string(body) {
		return result, fmt.Errorf("taxion: clarification destination contains credentials")
	}
	return result, state.AppendRequestRecord(recordID, "planning", string(body))
}

func savedClarificationDelivery(state store.Workspace, id string) (*ClarificationDelivery, error) {
	var result *ClarificationDelivery
	for _, record := range state.Records.Requests {
		if record.Kind != "planning" || record.RequestID != "clarification-delivery:"+id {
			continue
		}
		var saved ClarificationDelivery
		if err := core.DecodeGraphInput(strings.NewReader(record.Payload), &saved); err != nil ||
			saved.Version != 1 || saved.Phase != "clarification.delivery" || saved.Status != "HELD" ||
			saved.Question.RequestID != "clarification:"+id || saved.Question.Sequence == 0 ||
			saved.Question.Hash == "" || result != nil {
			return nil, fmt.Errorf("taxion: invalid or ambiguous clarification delivery receipt")
		}
		found := false
		for _, question := range state.Records.Requests {
			if question.Kind == "planning" && question.RequestID == saved.Question.RequestID &&
				question.Sequence == saved.Question.Sequence && question.Hash == saved.Question.Hash {
				var original Clarification
				if err := core.DecodeGraphInput(strings.NewReader(question.Payload), &original); err != nil ||
					original.Phase != "clarification.request" || original.Request.ID != id {
					return nil, fmt.Errorf("taxion: clarification delivery does not refer to the original request")
				}
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("taxion: clarification delivery lost its canonical question")
		}
		result = &saved
	}
	return result, nil
}

func coderClarificationTarget(state store.Workspace, question Clarification) (*ClarificationTarget, string) {
	source, found := state.Graph.GraphDispatch(question.Request.DispatchID)
	saved := question.Context.Dispatch
	if !state.HasGraph || !found || saved == nil || source.ID != saved.ID ||
		source.TaskID != question.Request.TaskID || source.TaskID != saved.TaskID ||
		source.TaskID != question.Context.Task.ID ||
		source.Recipient != "REVIEWER" || source.Relationship != core.RelationshipIndependent ||
		source.RelatedID == "" || source.RelatedID != saved.RelatedID ||
		source.RuntimeSessionID != saved.RuntimeSessionID || source.RuntimeConfigHash != saved.RuntimeConfigHash {
		return nil, "source-identity-unverified"
	}
	var coder core.GraphDispatch
	matches := 0
	for _, candidate := range state.Graph.Dispatches {
		if candidate.ID == source.RelatedID {
			coder, matches = candidate, matches+1
		}
	}
	if matches != 1 || coder.Recipient != "CODER" {
		return nil, "coder-not-unique"
	}
	var flow core.ReviewFlow
	flows := 0
	for _, candidate := range state.Graph.ReviewFlows {
		if slices.Contains(candidate.DispatchIDs, source.ID) {
			flow, flows = candidate, flows+1
		}
	}
	if flows != 1 || !slices.Contains(flow.DispatchIDs, coder.ID) && flow.SourceDispatchID != coder.ID {
		return nil, "flow-identity-mismatch"
	}
	task, found := state.Graph.GraphTask(coder.TaskID)
	reviewTask, reviewFound := state.Graph.GraphTask(source.TaskID)
	if !found || task.Owner != "CODER" || task.Repo != coder.Repository || coder.Repository != source.Repository ||
		coder.Instruction.TaskID != task.ID || coder.Instruction.DispatchID != coder.ID ||
		coder.Instruction.DispatchTo != "CODER" ||
		flow.SourceDispatchID != coder.ID && task.ID != flow.TaskID ||
		flow.SourceDispatchID == coder.ID && task.ID != flow.Contract.Supersedes ||
		!reviewFound || reviewTask.Owner != "REVIEWER" ||
		reviewTask.CreatedFrom != "dispatch:"+coder.ID &&
			!(flow.SourceDispatchID == coder.ID && reviewTask.CreatedFrom == "flow:"+flow.ID) {
		return nil, "task-or-dispatch-mismatch"
	}
	context, err := clarificationContext(state, coder.TaskID, coder.ID)
	if err != nil {
		return nil, "coder-context-unverified"
	}
	target := &ClarificationTarget{
		TaskID: task.ID, DispatchID: coder.ID, Role: coder.Recipient, SessionID: coder.RuntimeSessionID,
		ConfigHash: coder.RuntimeConfigHash, ScopeHash: context.ScopeHash, Status: coder.Status, FlowID: flow.ID,
	}
	sourceContext, err := clarificationContext(state, source.TaskID, source.ID)
	if err != nil || sourceContext.ScopeHash != question.Context.ScopeHash {
		return target, "source-context-changed"
	}
	if question.Answer != nil {
		return target, "question-already-answered"
	}
	now := time.Now().UTC()
	if !now.Before(question.Request.Deadline) {
		return target, "question-expired"
	}
	if !active(coder.Status) || coder.Status == core.DispatchCancelRequested || coder.Status == core.DispatchSuspended {
		return target, "coder-ended-or-stopping"
	}
	if target.SessionID == "" || target.ConfigHash == "" {
		return target, "coder-session-unverified"
	}
	if source.Status != core.DispatchRunning || source.RuntimeSessionID == "" || source.RuntimeConfigHash == "" {
		return target, "reviewer-session-unverified"
	}
	operation := state.Records.Operation
	if operation == nil || operation.Kind != "request" || operation.State != "RUNNING" ||
		operation.Phase != "EXECUTION" || operation.FlowID != flow.ID || operation.TaskID != flow.TaskID ||
		operation.Owner == "" || flow.RunnerID == "" || flow.Status != "RUNNING" ||
		!now.Before(operation.Deadline) || !now.Before(flow.ExecutionDeadline()) {
		return target, "coder-owner-unverified"
	}
	target.OperationID, target.Owner, target.RunnerID = operation.ID, operation.Owner, flow.RunnerID
	origin := question.Origin
	if origin == nil || origin.OperationID != operation.ID || origin.Owner != operation.Owner ||
		origin.RunnerID != flow.RunnerID || origin.SessionID != source.RuntimeSessionID {
		return target, "source-owner-changed"
	}
	if flow.HandoffHeld() {
		return target, "handoff-held"
	}
	// Controller.Send is a no-op for RUNNING and Copilot.Send rejects busy
	// sessions. Neither is an acknowledged notification path; do not bypass it.
	return target, "no-safe-input-path"
}
