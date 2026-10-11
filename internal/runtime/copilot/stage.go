package copilot

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
)

const ManagedStageToolName = "taxion_stage"

func (a *Adapter) stageTool(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
	if err := a.checkManagedInvocation(invocation, ManagedStageToolName); err != nil {
		return sdk.ToolResult{}, err
	}
	if a.config.ManagedCommit.Stage == nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: managed stage was not enabled for this session")
	}
	encoded, err := json.Marshal(invocation.Arguments)
	if err != nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: invalid managed stage arguments")
	}
	var arguments struct {
		Paths []string `json:"paths"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&arguments) != nil || len(arguments.Paths) == 0 {
		return sdk.ToolResult{}, fmt.Errorf("taxion: managed stage accepts only a nonempty paths array")
	}
	tree, err := a.config.ManagedCommit.Stage(invocation.TraceContext, invocation.SessionID, arguments.Paths)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	return sdk.ToolResult{
		ResultType: "success", TextResultForLLM: "Staged scanned index tree " + tree + ". No commit was created; use taxion_commit next.",
	}, nil
}
