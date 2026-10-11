package copilot

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
)

const ManagedCommitToolName = "taxion_commit"

func (a *Adapter) managedTools() []sdk.Tool {
	if a.config.ManagedCommit == nil {
		return nil
	}
	return []sdk.Tool{{
		Name: ManagedCommitToolName,
		Description: "Commit the already-staged index for this Coder Dispatch using host scope, message and local secret checks. " +
			"Does not stage files or finish the Task. Independent review and acceptance remain required.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"message": map[string]any{
				"type": "string", "description": "Complete Conventional/Contextual commit message", "minLength": 1,
			}},
			"required": []string{"message"},
		},
		Handler: a.commitTool,
	}}
}

func (a *Adapter) commitTool(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
	if a.config.ManagedCommit == nil || invocation.ToolName != ManagedCommitToolName ||
		invocation.AgentID != "" || invocation.TraceContext == nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: managed commit requires its bound root-session tool invocation")
	}
	handle, err := a.lookup(invocation.SessionID)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	handle.mu.Lock()
	executing := handle.sdk != nil && handle.busy && !handle.stopped
	handle.mu.Unlock()
	if !executing {
		return sdk.ToolResult{}, fmt.Errorf("taxion: managed commit requires an active Coder turn")
	}
	if err := invocation.TraceContext.Err(); err != nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: managed commit invocation interrupted: %w", err)
	}
	encoded, err := json.Marshal(invocation.Arguments)
	if err != nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: invalid managed commit arguments")
	}
	var arguments struct {
		Message string `json:"message"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&arguments) != nil || strings.TrimSpace(arguments.Message) == "" {
		return sdk.ToolResult{}, fmt.Errorf("taxion: managed commit accepts only a nonempty message")
	}
	sha, err := a.config.ManagedCommit.Commit(invocation.TraceContext, invocation.SessionID, arguments.Message)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	return sdk.ToolResult{
		ResultType:       "success",
		TextResultForLLM: "Committed " + sha + ". Independent review and acceptance remain required; the Task is not complete.",
	}, nil
}
