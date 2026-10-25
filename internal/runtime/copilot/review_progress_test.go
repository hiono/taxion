package copilot

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func TestReviewProgressToolIsBoundReadOnlyAndExplicit(t *testing.T) {
	cfg := testConfig(t)
	cfg.Role = "REVIEWER"
	basis := core.ReviewProgress{
		Version: 1, DispatchID: "review", InstructionHash: "instruction", CoderDispatchID: "coder",
		TargetSHA: strings.Repeat("a", 40), TargetTreeSHA: strings.Repeat("b", 40),
	}
	calls := 0
	cfg.ManagedReviewProgress = &runtime.ManagedReviewProgress{Basis: basis,
		Save: func(ctx context.Context, session string, progress core.ReviewProgress) (string, error) {
			calls++
			if session != "review-session" || progress.DispatchID != basis.DispatchID || progress.TargetTreeSHA != basis.TargetTreeSHA ||
				!reflect.DeepEqual(progress.ReviewedScope, []string{"file:1-2"}) {
				t.Fatal("target binding lost")
			}
			return "review-progress:hash", nil
		}}
	adapter, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	hash := adapter.ConfigHash()
	disabled := cfg
	disabled.ManagedReviewProgress = nil
	other, err := New(disabled)
	if err != nil || other.ConfigHash() == hash {
		t.Fatal("opt-in not bound to config", err)
	}
	for _, tools := range [][]sdk.Tool{adapter.sessionConfig(nil).Tools, adapter.resumeConfig(nil).Tools} {
		if len(tools) != 1 || tools[0].Name != ReviewProgressToolName || tools[0].SkipPermission || tools[0].IsTerminal {
			t.Fatal("invalid progress tool")
		}
	}
	if !slices.Contains(adapter.sessionConfig(nil).AvailableTools, ReviewProgressToolName) {
		t.Fatal("missing allowlist")
	}
	handle := &managedSession{sdk: &sdk.Session{}, busy: true, watchers: map[*subscription]struct{}{}}
	adapter.sessions["review-session"] = handle
	invocation := sdk.ToolInvocation{ToolName: ReviewProgressToolName, SessionID: "review-session", TraceContext: t.Context(),
		Arguments: map[string]any{"reviewedScope": []string{"file:1-2"}, "unreviewedScope": []string{"remaining"}, "findings": []core.DispatchFinding{}, "evidence": []string{"read:file:1-2"}}}
	for _, kind := range []string{"session", "agent", "context", "cancelled", "target-injection", "oversized", "stopped", "missing-arrays", "null-arrays"} {
		t.Run(kind, func(t *testing.T) {
			bad := invocation
			switch kind {
			case "session":
				bad.SessionID = "other"
			case "agent":
				bad.AgentID = "nested"
			case "context":
				bad.TraceContext = nil
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				bad.TraceContext = ctx
			case "target-injection":
				bad.Arguments = map[string]any{"targetSha": "forged"}
			case "oversized":
				bad.Arguments = map[string]any{"reviewedScope": strings.Repeat("x", core.ReviewProgressMaxBytes)}
			case "missing-arrays":
				bad.Arguments = map[string]any{}
			case "null-arrays":
				bad.Arguments = map[string]any{"reviewedScope": nil, "unreviewedScope": nil, "findings": nil, "evidence": nil}
			case "stopped":
				handle.stopped = true
				defer func() { handle.stopped = false }()
			}
			if result, err := adapter.reviewProgressTool(bad); err == nil || result.ResultType == "success" || calls != 0 {
				t.Fatal("invalid invocation reached save", err)
			}
		})
	}
	decision, err := adapter.permissionHandler()(&sdk.PermissionRequestCustomTool{ToolName: ReviewProgressToolName}, sdk.PermissionInvocation{SessionID: "review-session"})
	if err != nil || decision.Kind() != "approve-once" {
		t.Fatal("read-only progress denied", decision, err)
	}
	writeDecision, _ := adapter.permissionHandler()(&sdk.PermissionRequestWrite{FileName: "file"}, sdk.PermissionInvocation{SessionID: "review-session"})
	if writeDecision.Kind() != "reject" {
		t.Fatal("progress enabled reviewer writes")
	}
	result, err := adapter.reviewProgressTool(invocation)
	if err != nil || result.ResultType != "success" || calls != 1 {
		t.Fatal(result, err)
	}
	adapter.config.ManagedReviewProgress.Save = func(context.Context, string, core.ReviewProgress) (string, error) {
		return "", errors.New("write failed")
	}
	if _, err := adapter.reviewProgressTool(invocation); err == nil {
		t.Fatal("write failure hidden")
	}
}
