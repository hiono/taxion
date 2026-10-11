package copilot

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/runtime"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Role:             "CODER",
		SystemPrompt:     "Reply only with a test label.",
		WorkingDirectory: t.TempDir(),
		CLIPath:          "/bin/true",
		BaseDirectory:    t.TempDir(),
	}
}

func TestNewValidatesRolePromptAndWorktree(t *testing.T) {
	config := testConfig(t)
	if _, err := New(config); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, update := range map[string]func(*Config){
		"role":     func(c *Config) { c.Role = "UNKNOWN" },
		"prompt":   func(c *Config) { c.SystemPrompt = " " },
		"worktree": func(c *Config) { c.WorkingDirectory = filepath.Join(t.TempDir(), "missing") },
	} {
		t.Run(name, func(t *testing.T) {
			bad := testConfig(t)
			update(&bad)
			if _, err := New(bad); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestConfigHashIncludesRoleEnvironmentAndPrompt(t *testing.T) {
	first, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := testConfig(t)
	secondConfig.Role = "REVIEWER"
	second, err := New(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigHash() == second.ConfigHash() {
		t.Fatal("different role configuration produced the same hash")
	}
	schemaConfig := testConfig(t)
	schemaConfig.ResponseSchema = map[string]any{"type": "object", "required": []string{"status"}}
	withSchema, err := New(schemaConfig)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigHash() == withSchema.ConfigHash() {
		t.Fatal("different response schema produced the same configuration hash")
	}
}

func TestNormalizeEventUsesSDKNeutralTypes(t *testing.T) {
	message := "provider failure"
	model := "gpt-6-luna"
	auto := true
	inputTokens, outputTokens := int64(120), int64(30)
	cacheRead, cacheWrite := int64(100), int64(20)
	tests := []struct {
		name  string
		input sdk.SessionEvent
		want  runtime.Event
		ok    bool
	}{
		{"message", sdk.SessionEvent{Data: &sdk.AssistantMessageData{Content: "answer"}}, runtime.Event{Type: runtime.EventOutput, Payload: "answer"}, true},
		{"delta", sdk.SessionEvent{Data: &sdk.AssistantMessageDeltaData{DeltaContent: "part"}}, runtime.Event{Type: runtime.EventOutputDelta, Payload: "part"}, true},
		{"started", sdk.SessionEvent{Data: &sdk.AssistantTurnStartData{}}, runtime.Event{Type: runtime.EventTurnStarted}, true},
		{"ended", sdk.SessionEvent{Data: &sdk.AssistantTurnEndData{}}, runtime.Event{Type: runtime.EventTurnEnded}, true},
		{"finished", sdk.SessionEvent{Data: &sdk.SessionIdleData{}}, runtime.Event{Type: runtime.EventTurnFinished}, true},
		{"session error", sdk.SessionEvent{Data: &sdk.SessionErrorData{Message: message}}, runtime.Event{Type: runtime.EventFailed, Payload: message}, true},
		{"model error", sdk.SessionEvent{Data: &sdk.ModelCallFailureData{ErrorMessage: &message}}, runtime.Event{Type: runtime.EventFailed, Payload: message}, true},
		{"abort", sdk.SessionEvent{Data: &sdk.AbortData{}}, runtime.Event{Type: runtime.EventCancelled}, true},
		{"usage", sdk.SessionEvent{Data: &sdk.AssistantUsageData{
			Model: model, IsAuto: &auto, InputTokens: &inputTokens, OutputTokens: &outputTokens,
			CacheReadTokens: &cacheRead, CacheWriteTokens: &cacheWrite,
		}}, runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"gpt-6-luna","isAuto":true,"inputTokens":120,"outputTokens":30,"cacheReadTokens":100,"cacheWriteTokens":20}`}, true},
		{"context usage", sdk.SessionEvent{Data: &sdk.SessionUsageInfoData{CurrentTokens: 1200, TokenLimit: 8000}}, runtime.Event{Type: runtime.EventContextUsage, Payload: `{"currentTokens":1200,"tokenLimit":8000,"messagesLength":0}`}, true},
		{"compaction", sdk.SessionEvent{Data: &sdk.SessionCompactionStartData{Model: &model, CurrentTokens: &inputTokens}}, runtime.Event{Type: runtime.EventCompaction, Payload: `{"phase":"started","model":"gpt-6-luna","currentTokens":120}`}, true},
		{"permission completion", sdk.SessionEvent{Data: &sdk.PermissionCompletedData{Result: rpc.PermissionDeniedByPermissionRequestHook{}}}, runtime.Event{Type: runtime.EventPermission, Payload: `{"result":"denied-by-permission-request-hook"}`}, true},
		{"unmapped", sdk.SessionEvent{}, runtime.Event{}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := normalizeEvent(test.input)
			if got != test.want || ok != test.ok {
				t.Fatalf("normalizeEvent() = %+v, %v; want %+v, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestReviewerPermissionHandlerAllowsOnlyWorktreeReads(t *testing.T) {
	config := testConfig(t)
	config.Role = "REVIEWER"
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(config.WorkingDirectory, "source.go")
	if err := os.WriteFile(inside, []byte("package source"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := adapter.permissionHandler()
	decision, err := handler(&sdk.PermissionRequestRead{Path: inside}, sdk.PermissionInvocation{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decision.(*rpc.PermissionDecisionApproveReadOnlyForSession); !ok {
		t.Fatalf("worktree read decision = %T", decision)
	}
	for name, request := range map[string]sdk.PermissionRequest{
		"outside read": &sdk.PermissionRequestRead{Path: outside},
		"write":        &sdk.PermissionRequestWrite{FileName: inside},
		"shell":        &sdk.PermissionRequestShell{FullCommandText: "cat source.go"},
	} {
		t.Run(name, func(t *testing.T) {
			decision, err := handler(request, sdk.PermissionInvocation{})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := decision.(*rpc.PermissionDecisionReject); !ok {
				t.Fatalf("permission decision = %T", decision)
			}
		})
	}
}

func TestCoderPermissionDecisionLimitsAccessToWorktreeAndAllowlist(t *testing.T) {
	worktree := t.TempDir()
	inside := filepath.Join(worktree, "new.go")
	outside := filepath.Join(t.TempDir(), "outside.go")
	allowedTool := "taxion_test_write"
	allowed := coderPermissionDecision(worktree, []string{allowedTool}, &sdk.PermissionRequestWrite{FileName: inside})
	if _, ok := allowed.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Fatalf("worktree write decision = %T, want one-time approval", allowed)
	}
	for name, request := range map[string]sdk.PermissionRequest{
		"outside write": &sdk.PermissionRequestWrite{FileName: outside},
		"shell":         &sdk.PermissionRequestShell{FullCommandText: "touch new.go"},
		"unlisted tool": &sdk.PermissionRequestCustomTool{ToolName: "unlisted"},
		"managed tool": &sdk.PermissionRequestCustomTool{
			ToolName: allowedTool, ManagedApprovalRequired: boolPointer(true),
		},
	} {
		t.Run(name, func(t *testing.T) {
			decision := coderPermissionDecision(worktree, []string{allowedTool}, request)
			if _, ok := decision.(*rpc.PermissionDecisionReject); !ok {
				t.Fatalf("permission decision = %T, want rejection", decision)
			}
		})
	}
	decision := coderPermissionDecision(worktree, []string{allowedTool},
		&sdk.PermissionRequestCustomTool{ToolName: allowedTool})
	if _, ok := decision.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Fatalf("allowlisted tool decision = %T, want one-time approval", decision)
	}
}

func TestEventsReplayThenStreamAndCloseOnCancellation(t *testing.T) {
	handle := &managedSession{
		sdk:      &sdk.Session{},
		watchers: map[*subscription]struct{}{},
	}
	handle.emit(runtime.Event{Type: runtime.EventOutput, Payload: "earlier"})
	if cursor, err := (&Adapter{sessions: map[string]*managedSession{"session": handle}}).EventCursor("session"); err != nil || cursor != 1 {
		t.Fatalf("event cursor = %d, %v; want 1", cursor, err)
	}
	adapter := &Adapter{sessions: map[string]*managedSession{"session": handle}}
	ctx, cancel := context.WithCancel(context.Background())
	events, err := adapter.Events(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if event := <-events; event.Payload != "earlier" || event.Sequence != 1 {
		t.Fatalf("replay event = %+v", event)
	}
	handle.emit(runtime.Event{Type: runtime.EventOutput, Payload: "later"})
	if event := <-events; event.Payload != "later" || event.Sequence != 2 {
		t.Fatalf("live event = %+v", event)
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("event subscription did not close after context cancellation")
	}
}

func TestEventsAfterCursorSkipsCompletedTurns(t *testing.T) {
	handle := &managedSession{sdk: &sdk.Session{}, watchers: map[*subscription]struct{}{}}
	adapter := &Adapter{sessions: map[string]*managedSession{"session": handle}}
	handle.emit(runtime.Event{Type: runtime.EventTurnStarted})
	handle.emit(runtime.Event{Type: runtime.EventOutput, Payload: "previous"})
	handle.emit(runtime.Event{Type: runtime.EventTurnFinished})
	cursor, err := adapter.EventCursor("session")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := adapter.EventsAfter(ctx, "session", cursor)
	if err != nil {
		t.Fatal(err)
	}
	handle.emit(runtime.Event{Type: runtime.EventTurnStarted})
	handle.emit(runtime.Event{Type: runtime.EventOutput, Payload: "current"})
	handle.emit(runtime.Event{Type: runtime.EventTurnFinished})
	for _, want := range []string{runtime.EventTurnStarted, runtime.EventOutput, runtime.EventTurnFinished} {
		event := <-events
		if event.Type != want || event.Sequence <= cursor {
			t.Fatalf("event after cursor = %+v, want %s", event, want)
		}
		if event.Type == runtime.EventOutput && event.Payload != "current" {
			t.Fatalf("replayed previous turn output: %+v", event)
		}
	}
}

func TestStartFailureDoesNotCreateSession(t *testing.T) {
	config := testConfig(t)
	config.CLIPath = "/bin/false"
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if sessionID, err := adapter.Start(ctx, "task"); err == nil || sessionID != "" {
		t.Fatalf("failed SDK startup returned session %q, %v", sessionID, err)
	}
	if len(adapter.sessions) != 0 {
		t.Fatal("failed SDK startup registered a session")
	}
}

func TestLiveRoleSessionsAndResume(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_INTEGRATION=1 to use the signed-in Copilot CLI")
	}
	cliPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	plannerDir := filepath.Join(root, "planner")
	orchestratorDir := filepath.Join(root, "orchestrator")
	coderDir := filepath.Join(root, "coder")
	reviewerDir := filepath.Join(root, "reviewer")
	for _, dir := range []string{plannerDir, orchestratorDir, coderDir, reviewerDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	plannerConfig := Config{
		Role: "PLANNER", SystemPrompt: "For every request, reply with exactly PLANNER and no other text.",
		WorkingDirectory: plannerDir, CLIPath: cliPath, BaseDirectory: filepath.Join(home, ".copilot"),
	}
	orchestratorConfig := Config{
		Role: "ORCHESTRATOR", SystemPrompt: "For every request, reply with exactly ORCHESTRATOR and no other text.",
		WorkingDirectory: orchestratorDir, CLIPath: cliPath, BaseDirectory: filepath.Join(home, ".copilot"),
	}
	coderConfig := Config{
		Role: "CODER", SystemPrompt: "For every request, reply with exactly CODER and no other text.",
		WorkingDirectory: coderDir, CLIPath: cliPath, BaseDirectory: filepath.Join(home, ".copilot"),
	}
	reviewerConfig := Config{
		Role: "REVIEWER", SystemPrompt: "For every request, reply with exactly REVIEWER and no other text.",
		WorkingDirectory: reviewerDir, CLIPath: cliPath, BaseDirectory: filepath.Join(home, ".copilot"),
	}
	planner, err := New(plannerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer planner.Close()
	orchestrator, err := New(orchestratorConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer orchestrator.Close()
	coder, err := New(coderConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer coder.Close()
	reviewer, err := New(reviewerConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer reviewer.Close()

	plannerID, err := planner.Start(ctx, "task-planner")
	if err != nil {
		t.Fatal(err)
	}
	orchestratorID, err := orchestrator.Start(ctx, "task-orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	coderID, err := coder.Start(ctx, "task-coder")
	if err != nil {
		t.Fatal(err)
	}
	reviewerID, err := reviewer.Start(ctx, "task-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	plannerEvents, err := planner.Events(ctx, plannerID)
	if err != nil {
		t.Fatal(err)
	}
	orchestratorEvents, err := orchestrator.Events(ctx, orchestratorID)
	if err != nil {
		t.Fatal(err)
	}
	coderEvents, err := coder.Events(ctx, coderID)
	if err != nil {
		t.Fatal(err)
	}
	reviewerEvents, err := reviewer.Events(ctx, reviewerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range []struct {
		adapter *Adapter
		id      string
		events  <-chan runtime.Event
		want    string
	}{
		{planner, plannerID, plannerEvents, "PLANNER"},
		{orchestrator, orchestratorID, orchestratorEvents, "ORCHESTRATOR"},
		{coder, coderID, coderEvents, "CODER"},
		{reviewer, reviewerID, reviewerEvents, "REVIEWER"},
	} {
		for _, prompt := range []string{
			"Reply with exactly " + turn.want + ".",
			"Reply with exactly " + turn.want + " again.",
		} {
			if err := turn.adapter.Send(ctx, turn.id, prompt); err != nil {
				t.Fatal(err)
			}
			if got := awaitOutput(t, ctx, turn.events); got != turn.want {
				t.Fatalf("role response = %q, want %q", got, turn.want)
			}
		}
	}
	assertUsageCaptured(t, ctx, planner, plannerID, 2)
	assertUsageCaptured(t, ctx, orchestrator, orchestratorID, 2)
	assertUsageCaptured(t, ctx, coder, coderID, 2)
	assertUsageCaptured(t, ctx, reviewer, reviewerID, 2)

	if err := coder.Close(); err != nil {
		t.Fatalf("close coder client for resume: %v", err)
	}
	coder, err = New(coderConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer coder.Close()
	if err := coder.Resume(ctx, coderID); err != nil {
		t.Fatal(err)
	}
	coderEvents, err = coder.Events(ctx, coderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := coder.Send(ctx, coderID, "Repeat your assigned role after resume."); err != nil {
		t.Fatal(err)
	}
	if got := awaitOutput(t, ctx, coderEvents); got != "CODER" {
		t.Fatalf("resumed role response = %q, want CODER", got)
	}
	if err := coder.Send(ctx, coderID, "Write one thousand numbered lines of text."); err != nil {
		t.Fatal(err)
	}
	if err := coder.Stop(ctx, coderID); err != nil {
		t.Fatalf("cancel active Copilot turn: %v", err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-coderEvents:
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("event stream did not close after cancellation")
		}
	}
}

func TestLiveResponseSchema(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_INTEGRATION=1 to use the signed-in Copilot CLI")
	}
	cliPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adapter, err := New(Config{
		Role: "CODER", SystemPrompt: "Return the requested status using the response schema.",
		WorkingDirectory: t.TempDir(), CLIPath: cliPath,
		BaseDirectory: filepath.Join(home, ".copilot"),
		ResponseSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"READY"}},
			},
			"required": []string{"status"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	sessionID, err := adapter.Start(ctx, "schema-test")
	if err != nil {
		t.Fatal(err)
	}
	events, err := adapter.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Send(ctx, sessionID, "Return status READY."); err != nil {
		t.Fatal(err)
	}
	output := awaitOutput(t, ctx, events)
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("schema-constrained output is not JSON: %q: %v", output, err)
	}
	if result.Status != "READY" {
		t.Fatalf("schema-constrained status = %q, want READY", result.Status)
	}
}

func TestLiveReviewerSessionCannotWrite(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_INTEGRATION=1 to use the signed-in Copilot CLI")
	}
	cliPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adapter, err := New(Config{
		Role: "REVIEWER", SystemPrompt: "Follow the user request exactly.",
		WorkingDirectory: worktree, CLIPath: cliPath,
		BaseDirectory: filepath.Join(home, ".copilot"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err := adapter.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(worktree, "blocked.txt")
	handle := &managedSession{watchers: map[*subscription]struct{}{}}
	config := adapter.sessionConfig(handle.onEvent)
	config.Tools = []sdk.Tool{sdk.DefineTool("taxion_test_write", "Create a file in the assigned worktree",
		func(_ struct{}, _ sdk.ToolInvocation) (sdk.ToolResult, error) {
			if err := os.WriteFile(target, []byte("must not exist"), 0o600); err != nil {
				return sdk.ToolResult{}, err
			}
			return sdk.ToolResult{TextResultForLLM: "file created", ResultType: "success"}, nil
		})}
	config.AvailableTools = []string{"taxion_test_write"}
	session, err := adapter.client.CreateSession(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	handle.sdk = session
	adapter.mu.Lock()
	adapter.sessions[session.SessionID] = handle
	adapter.mu.Unlock()
	sessionID := session.SessionID
	events, err := adapter.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Send(ctx, sessionID, "Call taxion_test_write to create blocked.txt in this worktree. Do not skip the tool call."); err != nil {
		t.Fatal(err)
	}
	permissionRejected := false
	var observed []runtime.Event
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("reviewer event stream closed before turn completion: %+v", observed)
			}
			observed = append(observed, event)
			if event.Type == runtime.EventPermission {
				var permission struct {
					RequestKind string `json:"requestKind"`
					Decision    string `json:"decision"`
				}
				if err := json.Unmarshal([]byte(event.Payload), &permission); err != nil {
					t.Fatal(err)
				}
				if permission.RequestKind == "write" && permission.Decision == "reject" {
					permissionRejected = true
				}
				if permission.RequestKind == "custom-tool" && permission.Decision == "reject" {
					permissionRejected = true
				}
			}
			if event.Type == runtime.EventFailed {
				t.Fatalf("reviewer turn failed: %s", event.Payload)
			}
			if event.Type == runtime.EventTurnFinished {
				if !permissionRejected {
					t.Fatalf("SDK did not report the reviewer write denial; events: %+v", observed)
				}
				if _, err := os.Stat(filepath.Join(worktree, "blocked.txt")); !os.IsNotExist(err) {
					t.Fatalf("reviewer wrote the protected file: %v", err)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestLiveCompactionRetainsRoleAndDurableContract(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_INTEGRATION=1 to use the signed-in Copilot CLI")
	}
	cliPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	adapter, err := New(Config{
		Role: "CODER", SystemPrompt: "You are the CODER role. The durable Task Graph contract marker is M11-SYSTEM-PERSIST-731. Work only within the assigned scope.",
		WorkingDirectory: t.TempDir(), CLIPath: cliPath,
		BaseDirectory: filepath.Join(home, ".copilot"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err := adapter.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	handle := &managedSession{watchers: map[*subscription]struct{}{}}
	config := adapter.sessionConfig(handle.onEvent)
	backgroundThreshold, bufferThreshold := 0.005, 0.01
	config.InfiniteSessions = &sdk.InfiniteSessionConfig{
		Enabled:                       boolPointer(true),
		BackgroundCompactionThreshold: &backgroundThreshold,
		BufferExhaustionThreshold:     &bufferThreshold,
	}
	session, err := adapter.client.CreateSession(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	handle.sdk = session
	adapter.mu.Lock()
	adapter.sessions[session.SessionID] = handle
	adapter.mu.Unlock()
	events, err := adapter.Events(ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{
		"Remember this exact conversation marker: M11-CONTEXT-7F3Q. Reply briefly.",
		"Continue with a detailed story about a dragon and its castle.",
	} {
		if err := adapter.Send(ctx, session.SessionID, prompt); err != nil {
			t.Fatal(err)
		}
		awaitOutput(t, ctx, events)
	}
	var compaction runtime.Event
	var usageEvents []runtime.Event
	historyCtx, cancelHistory := context.WithTimeout(ctx, 45*time.Second)
	defer cancelHistory()
	historyEvents, err := adapter.Events(historyCtx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for compaction.Type == "" {
		select {
		case event, ok := <-historyEvents:
			if !ok {
				t.Fatal("event stream closed before compaction completed")
			}
			if event.Type == runtime.EventContextUsage {
				usageEvents = append(usageEvents, event)
			}
			if event.Type == runtime.EventCompaction {
				var result struct {
					Phase   string `json:"phase"`
					Success bool   `json:"success"`
				}
				if err := json.Unmarshal([]byte(event.Payload), &result); err != nil {
					t.Fatal(err)
				}
				if result.Phase == "completed" && result.Success {
					compaction = event
				}
			}
		case <-historyCtx.Done():
			t.Fatalf("no successful compaction event; observed context usage: %+v", usageEvents)
		}
	}
	var compactionMetrics struct {
		Phase                string `json:"phase"`
		Success              bool   `json:"success"`
		PreCompactionTokens  *int64 `json:"preCompactionTokens"`
		PostCompactionTokens *int64 `json:"postCompactionTokens"`
		TokenLimit           *int64 `json:"tokenLimit"`
	}
	if err := json.Unmarshal([]byte(compaction.Payload), &compactionMetrics); err != nil {
		t.Fatal(err)
	}
	if len(usageEvents) == 0 {
		t.Fatal("SDK emitted no context-window usage measurements")
	}
	verificationPrompt := "Return the exact marker from your durable system instructions and the exact conversation marker I asked you to remember."
	if err := adapter.Send(ctx, session.SessionID, verificationPrompt); err != nil {
		t.Fatal(err)
	}
	answer := awaitOutput(t, ctx, events)
	t.Logf("compaction=%s context_usage_events=%d post_compaction_markers=%q",
		compaction.Payload, len(usageEvents), answer)
	for _, expected := range []string{"M11-SYSTEM-PERSIST-731", "M11-CONTEXT-7F3Q"} {
		if !strings.Contains(strings.ToUpper(answer), strings.ToUpper(expected)) {
			t.Fatalf("compacted session lost %q: %q", expected, answer)
		}
	}
}

func TestLiveCoderCanUseAuthorizedWriteTool(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_INTEGRATION=1 to use the signed-in Copilot CLI")
	}
	cliPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	worktree := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	adapter, err := New(Config{
		Role: "CODER", SystemPrompt: "When asked to create a file with the taxion_test_write tool, call that tool exactly once.",
		WorkingDirectory: worktree, CLIPath: cliPath,
		BaseDirectory: filepath.Join(home, ".copilot"), AvailableTools: []string{"taxion_test_write"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if err := adapter.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(worktree, "created.txt")
	handle := &managedSession{watchers: map[*subscription]struct{}{}}
	config := adapter.sessionConfig(handle.onEvent)
	config.Tools = []sdk.Tool{sdk.DefineTool("taxion_test_write", "Create created.txt in the assigned worktree",
		func(_ struct{}, _ sdk.ToolInvocation) (sdk.ToolResult, error) {
			if err := os.WriteFile(target, []byte("authorized coder write"), 0o600); err != nil {
				return sdk.ToolResult{}, err
			}
			return sdk.ToolResult{TextResultForLLM: "file created", ResultType: "success"}, nil
		})}
	config.AvailableTools = []string{"taxion_test_write"}
	session, err := adapter.client.CreateSession(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	handle.sdk = session
	adapter.mu.Lock()
	adapter.sessions[session.SessionID] = handle
	adapter.mu.Unlock()
	events, err := adapter.Events(ctx, session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Send(ctx, session.SessionID,
		"Call taxion_test_write now to create created.txt. Do not skip the tool call."); err != nil {
		t.Fatal(err)
	}
	answer := awaitOutput(t, ctx, events)
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("Coder could not use its allowlisted write tool: %v; response=%q", err, answer)
	}
	if string(content) != "authorized coder write" {
		t.Fatalf("unexpected Coder tool output: %q", content)
	}
}

func TestLiveNewVersusContinuingSessionUsage(t *testing.T) {
	if os.Getenv("TAXION_COPILOT_INTEGRATION") != "1" {
		t.Skip("set TAXION_COPILOT_INTEGRATION=1 to use the signed-in Copilot CLI")
	}
	cliPath, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	config := Config{
		Role: "CODER", SystemPrompt: "For each request, reply with exactly COMPARISON_OK.",
		WorkingDirectory: t.TempDir(), CLIPath: cliPath,
		BaseDirectory: filepath.Join(home, ".copilot"),
	}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	continuingID, err := adapter.Start(ctx, "continuing-session-comparison")
	if err != nil {
		t.Fatal(err)
	}
	continuingEvents, err := adapter.Events(ctx, continuingID)
	if err != nil {
		t.Fatal(err)
	}
	prompt := "Report the fixed label."
	type observation struct {
		Strategy   string `json:"strategy"`
		Turn       int    `json:"turn"`
		Model      string `json:"model"`
		IsAuto     *bool  `json:"isAuto,omitempty"`
		Input      *int64 `json:"inputTokens,omitempty"`
		Output     *int64 `json:"outputTokens,omitempty"`
		CacheRead  *int64 `json:"cacheReadTokens,omitempty"`
		CacheWrite *int64 `json:"cacheWriteTokens,omitempty"`
	}
	var results []observation
	for turn := 1; turn <= 2; turn++ {
		freshID, err := adapter.Start(ctx, "fresh-session-comparison")
		if err != nil {
			t.Fatal(err)
		}
		freshEvents, err := adapter.Events(ctx, freshID)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range []struct {
			strategy string
			id       string
			events   <-chan runtime.Event
		}{
			{"new", freshID, freshEvents},
			{"continuing", continuingID, continuingEvents},
		} {
			if err := adapter.Send(ctx, run.id, prompt); err != nil {
				t.Fatal(err)
			}
			_, usage := awaitTurnUsage(t, ctx, run.events)
			if len(usage) == 0 {
				t.Fatalf("%s turn %d missing SDK usage", run.strategy, turn)
			}
			for _, item := range usage {
				results = append(results, observation{
					Strategy: run.strategy, Turn: turn, Model: item.Model,
					IsAuto: item.IsAuto, Input: item.InputTokens,
					Output: item.OutputTokens, CacheRead: item.CacheReadTokens,
					CacheWrite: item.CacheWriteTokens,
				})
			}
		}
		if err := adapter.Stop(ctx, freshID); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("same prompts/system prompt, model selection Auto, usage comparison: %s", encoded)
}

type usageObservation struct {
	Model            string `json:"model"`
	IsAuto           *bool  `json:"isAuto,omitempty"`
	InputTokens      *int64 `json:"inputTokens,omitempty"`
	OutputTokens     *int64 `json:"outputTokens,omitempty"`
	CacheReadTokens  *int64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens *int64 `json:"cacheWriteTokens,omitempty"`
}

func awaitTurnUsage(t *testing.T, ctx context.Context, events <-chan runtime.Event) (string, []usageObservation) {
	t.Helper()
	var output string
	var usage []usageObservation
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before the turn completed")
			}
			switch event.Type {
			case runtime.EventOutput:
				output = event.Payload
			case runtime.EventUsage:
				var item usageObservation
				if err := json.Unmarshal([]byte(event.Payload), &item); err != nil {
					t.Fatal(err)
				}
				usage = append(usage, item)
			case runtime.EventFailed:
				t.Fatalf("Copilot turn failed: %s", event.Payload)
			case runtime.EventTurnFinished:
				return output, usage
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Copilot turn: %v", ctx.Err())
		}
	}
}

func boolPointer(value bool) *bool { return &value }

func awaitOutput(t *testing.T, ctx context.Context, events <-chan runtime.Event) string {
	t.Helper()
	var output string
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before the turn completed")
			}
			switch event.Type {
			case runtime.EventOutput:
				output = event.Payload
			case runtime.EventFailed:
				t.Fatalf("Copilot turn failed: %s", event.Payload)
			case runtime.EventTurnFinished:
				return output
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Copilot turn: %v", ctx.Err())
		}
	}
}

func assertUsageCaptured(t *testing.T, ctx context.Context, adapter *Adapter, sessionID string, turns int) {
	t.Helper()
	events, err := adapter.Events(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	turnsFinished := 0
	foundUsage := false
	for turnsFinished < turns {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before replayed turns were inspected")
			}
			if event.Type == runtime.EventUsage {
				var usage struct {
					Model       string `json:"model"`
					InputTokens *int64 `json:"inputTokens"`
				}
				if err := json.Unmarshal([]byte(event.Payload), &usage); err != nil {
					t.Fatal(err)
				}
				if usage.Model == "" || usage.InputTokens == nil {
					t.Fatalf("usage event omitted selected model or input tokens: %+v", usage)
				}
				foundUsage = true
			}
			if event.Type == runtime.EventTurnFinished {
				turnsFinished++
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if !foundUsage {
		t.Fatal("SDK did not emit model/token usage")
	}
}
