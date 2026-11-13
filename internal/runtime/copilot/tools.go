package copilot

import (
	"context"
	"fmt"
	"slices"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/tools"
)

func (a *Adapter) extensionDirectories() ([]string, []string) {
	var skills, plugins []string
	for _, asset := range a.config.ToolPolicy.Skills {
		skills = append(skills, asset.Path)
	}
	for _, asset := range a.config.ToolPolicy.Plugins {
		plugins = append(plugins, asset.Path)
	}
	return skills, plugins
}

func (a *Adapter) checkToolPolicy(ctx context.Context, sessionID string) error {
	if a.config.ToolPolicy == nil {
		return fmt.Errorf("taxion: tools require an explicit role policy")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.config.AuthorizeTools != nil {
		if err := a.config.AuthorizeTools(ctx, sessionID); err != nil {
			return err
		}
	}
	return a.config.ToolPolicy.Check()
}

func (a *Adapter) mcpTools() []sdk.Tool {
	var result []sdk.Tool
	for _, binding := range a.config.ToolPolicy.MCP {
		result = append(result, sdk.Tool{
			Name: binding.Name, Defer: sdk.ToolDeferNever,
			Description: "Run the exact user-approved request on a trusted local MCP server. Different arguments require a new approval.",
			Parameters:  map[string]any{"type": "object", "const": binding.Arguments},
			Handler: func(invocation sdk.ToolInvocation) (sdk.ToolResult, error) {
				if invocation.ToolName != binding.Name || invocation.AgentID != "" || invocation.TraceContext == nil {
					return sdk.ToolResult{}, fmt.Errorf("taxion: MCP requires its approved root-session invocation")
				}
				handle, err := a.lookup(invocation.SessionID)
				if err != nil {
					return sdk.ToolResult{}, err
				}
				handle.mu.Lock()
				executing := handle.sdk != nil && handle.busy && !handle.stopped
				handle.mu.Unlock()
				if !executing {
					return sdk.ToolResult{}, fmt.Errorf("taxion: MCP requires an active approved role turn")
				}
				if err := a.checkToolPolicy(invocation.TraceContext, invocation.SessionID); err != nil {
					return sdk.ToolResult{}, err
				}
				body, err := a.config.ToolPolicy.CallMCP(invocation.TraceContext, binding.Name, invocation.Arguments)
				if err != nil {
					return sdk.ToolResult{}, err
				}
				return sdk.ToolResult{ResultType: "success", TextResultForLLM: string(body)}, nil
			},
		})
	}
	return result
}

func (a *Adapter) checkHook(sessionID string, invocation sdk.HookInvocation) error {
	if sessionID != invocation.SessionID {
		return fmt.Errorf("taxion: tool hook session differs from its SDK invocation")
	}
	handle, err := a.lookup(sessionID)
	if err != nil {
		return err
	}
	handle.mu.Lock()
	executing := handle.sdk != nil && handle.busy && !handle.stopped
	handle.mu.Unlock()
	if !executing {
		return fmt.Errorf("taxion: tool hooks require an active role turn")
	}
	return a.checkToolPolicy(context.Background(), sessionID)
}

func (a *Adapter) failToolEvidence(sessionID string, err error) error {
	if handle, lookupErr := a.lookup(sessionID); lookupErr == nil {
		handle.emit(runtime.Event{Type: runtime.EventFailed, Payload: "tool evidence could not be preserved"})
	}
	return err
}

func (a *Adapter) toolHooks() *sdk.SessionHooks {
	return &sdk.SessionHooks{
		OnPreToolUse: func(input sdk.PreToolUseHookInput, invocation sdk.HookInvocation) (*sdk.PreToolUseHookOutput, error) {
			reason := ""
			if err := a.checkHook(input.SessionID, invocation); err != nil {
				reason = err.Error()
			} else if !slices.Contains(a.config.AvailableTools, input.ToolName) ||
				slices.Contains([]string{"task", "run_subagent", "agent"}, input.ToolName) {
				reason = "Tool is outside the approved role surface"
			}
			if reason != "" {
				return &sdk.PreToolUseHookOutput{PermissionDecision: "deny", PermissionDecisionReason: reason}, nil
			}
			// Ordinary SDK permission callbacks still decide each operation.
			return nil, nil
		},
		OnPostToolUse: func(input sdk.PostToolUseHookInput, invocation sdk.HookInvocation) (*sdk.PostToolUseHookOutput, error) {
			if input.SessionID != invocation.SessionID {
				return nil, fmt.Errorf("taxion: tool evidence session identity differs")
			}
			receipt, err := a.config.ToolPolicy.Save(tools.Evidence{
				Session: input.SessionID, Tool: input.ToolName, Phase: "result", Input: input,
			})
			if err != nil {
				return nil, a.failToolEvidence(input.SessionID, err)
			}
			changed, err := a.config.ToolPolicy.Compress(context.Background(), input.ToolResult)
			if err != nil {
				return nil, a.failToolEvidence(input.SessionID, err)
			}
			return &sdk.PostToolUseHookOutput{ModifiedResult: changed, AdditionalContext: "Private original tool evidence: " + receipt}, nil
		},
		OnPostToolUseFailure: func(input sdk.PostToolUseFailureHookInput, invocation sdk.HookInvocation) (*sdk.PostToolUseFailureHookOutput, error) {
			if input.SessionID != invocation.SessionID {
				return nil, fmt.Errorf("taxion: failure evidence session identity differs")
			}
			receipt, err := a.config.ToolPolicy.Save(tools.Evidence{
				Session: input.SessionID, Tool: input.ToolName, Phase: "failure", Input: input,
			})
			if err != nil {
				return nil, a.failToolEvidence(input.SessionID, err)
			}
			return &sdk.PostToolUseFailureHookOutput{AdditionalContext: "Private failure evidence: " + receipt}, nil
		},
	}
}
