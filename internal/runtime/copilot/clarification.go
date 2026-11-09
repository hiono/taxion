package copilot

import (
	"encoding/json"
	"fmt"

	sdk "github.com/github/copilot-sdk/go"
)

const ClarificationToolName = "taxion_clarify"

func (a *Adapter) clarificationTools() []sdk.Tool {
	if a.config.ManagedClarification == nil {
		return nil
	}
	text := map[string]any{"type": "string", "minLength": 1}
	return []sdk.Tool{{
		Name: ClarificationToolName, Defer: sdk.ToolDeferNever,
		Description: "Record a clarification from this running Role with its saved Task, Dispatch, session and owner. " +
			"Consult saved sources first. Report the reason, uncertainty, consulted references, expected answer and affected decision. " +
			"Observation only: this does not accept answers, approve work, dispatch another Role or stop the runtime. " +
			"Coder destinations use the saved Reviewer relationship. Delivery HELD is not a Task HOLD or proof of delivery. " +
			"Do not proceed with work depending on an unanswered question. Return real blockers in the existing result contract.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"id": text, "question": text, "reason": text, "uncertainty": text,
				"decisionImpact": text, "stopCondition": text,
				"category": map[string]any{"type": "string", "enum": []string{
					"specification", "implementation", "evidence", "authority", "intent", "irreversible",
					"scope-change", "budget-change", "deadline-change", "safety", "identity", "integrity", "budget", "deadline",
				}},
				"references":             map[string]any{"type": "array", "minItems": 1, "items": text},
				"answerFormat":           map[string]any{"type": "string", "enum": []string{"text", "json"}},
				"deadline":               map[string]any{"type": "string", "format": "date-time"},
				"specificationOwnerRole": map[string]any{"type": "string", "enum": []string{"PLANNER", "HUMAN"}},
			},
			"required": []string{"id", "category", "question", "reason", "uncertainty", "references",
				"answerFormat", "decisionImpact", "stopCondition", "deadline"},
		},
		Handler: a.clarificationTool,
	}}
}

func (a *Adapter) clarificationTool(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
	if a.config.ManagedClarification == nil || invocation.ToolName != ClarificationToolName ||
		invocation.AgentID != "" || invocation.TraceContext == nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: clarification requires its bound root-session invocation")
	}
	handle, err := a.lookup(invocation.SessionID)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	handle.mu.Lock()
	active := handle.sdk != nil && handle.busy && !handle.stopped
	handle.mu.Unlock()
	if !active {
		return sdk.ToolResult{}, fmt.Errorf("taxion: clarification requires an active Role turn")
	}
	if err := invocation.TraceContext.Err(); err != nil {
		return sdk.ToolResult{}, err
	}
	body, err := json.Marshal(invocation.Arguments)
	if err != nil || len(body) > 64*1024 {
		return sdk.ToolResult{}, fmt.Errorf("taxion: clarification input exceeds the bounded record")
	}
	result, err := a.config.ManagedClarification.Request(invocation.TraceContext, invocation.SessionID, body)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	if !json.Valid(result) {
		return sdk.ToolResult{}, fmt.Errorf("taxion: invalid clarification host receipt")
	}
	return sdk.ToolResult{ResultType: "success", TextResultForLLM: string(result)}, nil
}
