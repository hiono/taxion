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
			"Ask in natural language. Only the question is required; provide category or other context when it materially helps routing. " +
			"The host derives Task, Dispatch, session, owner, source references and deadline from the bound execution. " +
			"Questions are observations only. Use action=answer with only id and answer to answer a Reviewer question explicitly carried by this successor Coder Dispatch. The host verifies the original Reviewer, Flow and Dispatch. An answer is not approval or permission to expand scope. " +
			"Do not proceed with work depending on an unanswered question. Return real blockers in the existing result contract.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"id": text, "question": text, "answer": text, "reason": text, "uncertainty": text,
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
			"required": []string{"question"},
		},
		Handler: a.clarificationTool,
	}}
	if a.config.ManagedClarification.ProtocolVersion >= 3 {
		tools[0].Description += " Use action=read with only id to read saved answers to your own question during this execution; " +
			"do not repeat the question or poll while waiting. Consult the canonical candidates, sources, conflicts and decision before revising your plan. " +
			"An accept verdict means input eligibility, not acceptance, approval or permission to resume dependent work. " +
			"This cannot receive questions addressed to another Role and does not enable the disabled Coder inbox."
		tools[0].Parameters["properties"].(map[string]any)["action"] = map[string]any{
			"type": "string", "enum": []string{"request", "read", "answer"},
		}
		required := tools[0].Parameters["required"]
		tools[0].Parameters["required"] = []string{}
		tools[0].Parameters["anyOf"] = []any{
			map[string]any{"required": required},
			map[string]any{"properties": map[string]any{"action": map[string]any{"const": "read"}}, "required": []string{"action", "id"}},
			map[string]any{"properties": map[string]any{"action": map[string]any{"const": "answer"}}, "required": []string{"action", "id", "answer"}},
		}
	}
	return tools
}

func (a *Adapter) clarificationInboxTool(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
	// SDK 1.0.22 also uses empty AgentID for unattributed child calls.
	return sdk.ToolResult{}, fmt.Errorf("taxion: clarification inbox disabled: SDK root attribution is unverified")
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
