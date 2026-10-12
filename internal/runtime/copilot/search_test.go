package copilot

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/runtime"
)

func TestHostSearchRootRoleQueryAndResumeIdentity(t *testing.T) {
	config := testConfig(t)
	config.Role = "ORCHESTRATOR"
	calls := 0
	config.ManagedSearch = &runtime.ManagedSearch{TaskID: "task", ScopeHash: "exact-scope", Query: "202",
		Search: func(context.Context, string) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{"specifications":[]}`), nil
		}}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	adapter.sessions["search"] = &managedSession{sdk: &sdk.Session{}, busy: true, watchers: map[*subscription]struct{}{}}
	handler := adapter.resumeConfig(nil).Tools[0].Handler
	invocation := sdk.ToolInvocation{SessionID: "search", ToolName: ManagedSearchToolName,
		TraceContext: context.Background(), Arguments: map[string]any{"query": "202"}}
	for _, kind := range []string{"role", "query", "scope", "agent", "session", "context"} {
		bad := invocation
		switch kind {
		case "role":
			bad.ToolName = "write_file"
		case "query":
			bad.Arguments = map[string]any{"query": "different"}
		case "scope":
			bad.Arguments = map[string]any{"query": "202", "path": "/private"}
		case "agent":
			bad.AgentID = "other"
		case "session":
			bad.SessionID = "other"
		case "context":
			bad.TraceContext = nil
		}
		if _, err := handler(bad); err == nil || calls != 0 {
			t.Fatal("search escaped its exact root invocation", kind, err)
		}
	}
	if result, err := handler(invocation); err != nil || result.ResultType != "success" || calls != 1 {
		t.Fatal("approved SDK search not connected", result, err)
	}
	config.ManagedSearch.Query = "different"
	changed, err := New(config)
	if err != nil || adapter.ConfigHash() == changed.ConfigHash() {
		t.Fatal("changed query did not invalidate resume identity", err)
	}
	config.Role = "REVIEWER"
	if _, err := New(config); err == nil {
		t.Fatal("Reviewer acquired Orchestrator search authority")
	}
}
