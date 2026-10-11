package copilot

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
)

func TestManagedStageToolBindingAndArguments(t *testing.T) {
	config := commitToolConfig(t)
	commitOnly, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	config.ManagedCommit.Stage = func(ctx context.Context, sessionID string, paths []string) (string, error) {
		calls++
		if sessionID != "session" || !slices.Equal(paths, []string{"src/file.txt"}) || ctx == nil {
			t.Fatal("stage callback lost session or explicit paths")
		}
		return strings.Repeat("a", 40), nil
	}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.ConfigHash() == commitOnly.ConfigHash() {
		t.Fatal("commit-only session silently gained stage permission")
	}
	for _, tools := range [][]sdk.Tool{adapter.sessionConfig(nil).Tools, adapter.resumeConfig(nil).Tools} {
		if len(tools) != 2 || tools[1].Name != ManagedStageToolName || tools[1].Handler == nil ||
			tools[1].SkipPermission || tools[1].OverridesBuiltInTool || tools[1].IsTerminal {
			t.Fatal("stage definition missing or bypasses normal permission and turn handling")
		}
		if tools[1].Parameters["additionalProperties"] != false {
			t.Fatal("stage tool permits host selection overrides")
		}
	}
	if !slices.Contains(adapter.sessionConfig(nil).AvailableTools, ManagedStageToolName) ||
		!slices.Contains(adapter.resumeConfig(nil).AvailableTools, ManagedStageToolName) {
		t.Fatal("stage tool not allowlisted on create and resume")
	}
	adapter.sessions["session"] = &managedSession{sdk: &sdk.Session{}, busy: true}
	invocation := sdk.ToolInvocation{ToolName: ManagedStageToolName, SessionID: "session", TraceContext: context.Background(),
		Arguments: map[string]any{"paths": []string{"src/file.txt"}}}
	for _, badArguments := range []any{nil, map[string]any{}, map[string]any{"paths": []string{}},
		map[string]any{"paths": "src/file.txt"}, map[string]any{"paths": []int{1}},
		map[string]any{"paths": []string{"src/file.txt"}, "force": true}} {
		bad := invocation
		bad.Arguments = badArguments
		result, err := adapter.stageTool(bad)
		if err == nil || result.ResultType == "success" || calls != 0 {
			t.Fatal("invalid stage arguments reached callback")
		}
	}
	for _, badSession := range []string{"", "unknown"} {
		bad := invocation
		bad.SessionID = badSession
		if _, err := adapter.stageTool(bad); err == nil || calls != 0 {
			t.Fatal("unknown stage session accepted")
		}
	}
	bad := invocation
	bad.AgentID = "child"
	if _, err := adapter.stageTool(bad); err == nil {
		t.Fatal("sub-agent stage invocation accepted")
	}
	result, err := adapter.resumeConfig(nil).Tools[1].Handler(invocation)
	if err != nil || result.ResultType != "success" || calls != 1 ||
		!strings.Contains(result.TextResultForLLM, "No commit was created") {
		t.Fatal("stage result lost index/commit boundary", result, err)
	}
	adapter.config.ManagedCommit.Stage = func(context.Context, string, []string) (string, error) {
		return "", errors.New("scanner failed")
	}
	result, err = adapter.stageTool(invocation)
	if err == nil || result.ResultType == "success" {
		t.Fatal("stage failure became success")
	}
}
