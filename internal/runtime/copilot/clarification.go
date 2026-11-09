package copilot

import (
	"encoding/json"
	"fmt"

	sdk "github.com/github/copilot-sdk/go"
)

const ClarificationToolName = "taxion_clarify"
const ClarificationInboxToolName = "taxion_clarification_inbox"

func (a *Adapter) clarificationTools() []sdk.Tool {
	if a.config.ManagedClarification == nil {
		return nil
	}
	text := map[string]any{"type": "string", "minLength": 1}
	tools := []sdk.Tool{{
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
	if a.config.Role == "CODER" && a.config.ManagedClarification.ProtocolVersion == 2 && a.config.ManagedClarification.Inbox != nil {
		tools = append(tools, sdk.Tool{
			Name: ClarificationInboxToolName, Defer: sdk.ToolDeferNever,
			Description: "At a safe tool-input boundary, receive one explicitly handed-off saved question for this Coder. " +
				"Read the question, then call action ack with its id and the received receipt.hash. " +
				"REQUESTED is not delivery proof. Missing delivery events may require a later ack retry, never a payload resend. " +
				"Questions and acknowledgements do not accept answers, approve work or change the authorized instruction.",
			Parameters: map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"receive", "ack"}},
					"id":     text, "receipt": text,
				},
				"required": []string{"action"},
			},
			Handler: a.clarificationInboxTool,
		})
	}
	return tools
}

func (a *Adapter) clarificationInboxTool(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
	binding := a.config.ManagedClarification
	if binding == nil || binding.ProtocolVersion != 2 || binding.Inbox == nil || a.config.Role != "CODER" ||
		invocation.ToolName != ClarificationInboxToolName || invocation.ToolCallID == "" ||
		invocation.AgentID != "" || invocation.TraceContext == nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: inbox requires its bound root Coder tool invocation")
	}
	handle, err := a.lookup(invocation.SessionID)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	handle.mu.Lock()
	active := handle.sdk != nil && handle.busy && !handle.stopped
	handle.mu.Unlock()
	if !active {
		return sdk.ToolResult{}, fmt.Errorf("taxion: inbox requires an active Coder turn")
	}
	if err := invocation.TraceContext.Err(); err != nil {
		return sdk.ToolResult{}, err
	}
	body, err := json.Marshal(invocation.Arguments)
	if err != nil || len(body) > 4096 {
		return sdk.ToolResult{}, fmt.Errorf("taxion: invalid bounded inbox input")
	}
	result, err := binding.Inbox(invocation.TraceContext, invocation.SessionID, invocation.ToolCallID, body)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	if !json.Valid(result) {
		return sdk.ToolResult{}, fmt.Errorf("taxion: invalid inbox receipt")
	}
	return sdk.ToolResult{ResultType: "success", TextResultForLLM: string(result)}, nil
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
