package copilot

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
)

const ReviewProgressToolName = "taxion_review_progress"

func (a *Adapter) reviewProgressTools() []sdk.Tool {
	if a.config.ManagedReviewProgress == nil {
		return nil
	}
	array := map[string]any{"type": "array", "maxItems": 128,
		"items": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048}}
	return []sdk.Tool{{
		Name: ReviewProgressToolName, Defer: sdk.ToolDeferNever,
		Description: "Save an explicit cumulative checkpoint of reviewed/unreviewed scope, findings and evidence before proceeding. " +
			"Call after each meaningful review unit, not just at the end. This writes only controller records, never worktree files. " +
			"Reported coverage is untrusted, not approval or a final result. Never claim checks not performed. " +
			"Use empty arrays when nothing is known. The host binds the exact target; prior reports are not implicitly merged.",
		Parameters: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"reviewedScope": array, "unreviewedScope": array, "evidence": array,
				"findings": map[string]any{"type": "array", "maxItems": 128, "items": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{
						"severity": map[string]any{"type": "string", "enum": []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}},
						"title":    map[string]any{"type": "string", "minLength": 1, "maxLength": 2048},
						"file":     map[string]any{"type": "string", "maxLength": 2048},
						"lines":    map[string]any{"type": "string", "maxLength": 2048}, "evidence": array,
					}, "required": []string{"severity", "title", "file", "lines", "evidence"},
				}},
			}, "required": []string{"reviewedScope", "unreviewedScope", "findings", "evidence"},
		},
		Handler: a.reviewProgressTool,
	}}
}

func (a *Adapter) reviewProgressTool(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
	if a.config.ManagedReviewProgress == nil || invocation.ToolName != ReviewProgressToolName ||
		invocation.AgentID != "" || invocation.TraceContext == nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: review progress requires its bound root Reviewer invocation")
	}
	handle, err := a.lookup(invocation.SessionID)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	handle.mu.Lock()
	active := handle.sdk != nil && handle.busy && !handle.stopped
	handle.mu.Unlock()
	if !active {
		return sdk.ToolResult{}, fmt.Errorf("taxion: review progress requires an active Reviewer turn")
	}
	if err := invocation.TraceContext.Err(); err != nil {
		return sdk.ToolResult{}, err
	}
	body, err := json.Marshal(invocation.Arguments)
	if err != nil || len(body) > core.ReviewProgressMaxBytes {
		return sdk.ToolResult{}, fmt.Errorf("taxion: review progress arguments exceed the bounded record")
	}
	var arguments struct {
		ReviewedScope   []string               `json:"reviewedScope"`
		UnreviewedScope []string               `json:"unreviewedScope"`
		Findings        []core.DispatchFinding `json:"findings"`
		Evidence        []string               `json:"evidence"`
	}
	if err := core.DecodeGraphInput(strings.NewReader(string(body)), &arguments); err != nil {
		return sdk.ToolResult{}, fmt.Errorf("taxion: invalid review progress arguments")
	}
	progress := a.config.ManagedReviewProgress.Basis
	progress.ReviewedScope, progress.UnreviewedScope = arguments.ReviewedScope, arguments.UnreviewedScope
	progress.Findings, progress.Evidence = arguments.Findings, arguments.Evidence
	if err := progress.ValidateContent(); err != nil {
		return sdk.ToolResult{}, err
	}
	reference, err := a.config.ManagedReviewProgress.Save(invocation.TraceContext, invocation.SessionID, progress)
	if err != nil {
		return sdk.ToolResult{}, err
	}
	return sdk.ToolResult{ResultType: "success", TextResultForLLM: "Saved " + reference +
		". Reported coverage only; all acceptance and independent review requirements remain."}, nil
}
