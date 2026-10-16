package openacp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/tools"
)

func TestMain(m *testing.M) {
	if os.Getenv("TAXION_FAKE_ACP") == "1" || len(os.Args) > 1 && os.Args[1] == "acp" {
		servePeer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func servePeer() {
	if len(os.Args) > 1 && os.Args[1] == "acp" {
		if err := checkNamespaceBoundary(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	mode := os.Getenv("TAXION_FAKE_MODE")
	var pending json.RawMessage
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		var result any
		if request.Method == "" {
			if string(request.ID) != "700" ||
				mode == "permission" && !strings.Contains(string(request.Result), `"cancelled"`) ||
				mode == "client-filesystem" && !strings.Contains(string(request.Error), "-32601") {
				os.Exit(4)
			}
			request.ID, request.Method = pending, "session/prompt"
		}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"name": "opencode", "version": "1.19.0"}}
			if mode == "version" {
				result = map[string]any{"protocolVersion": 1, "agentInfo": map[string]string{"version": "unknown"}}
			}
		case "session/new":
			model := "test/fake"
			if mode == "model" {
				model = "test/unapproved"
			}
			result = map[string]any{"sessionId": "fake-session", "configOptions": []any{
				map[string]string{"id": "model", "category": "model", "currentValue": model},
				map[string]string{"id": "mode", "category": "mode", "currentValue": "taxion"},
			}}
		case "session/prompt":
			if (mode == "permission" || mode == "client-filesystem") && pending == nil {
				pending = request.ID
				method := "session/request_permission"
				if mode == "client-filesystem" {
					method = "fs/read_text_file"
				}
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 700, "method": method, "params": map[string]any{}})
				continue
			}
			if mode == "disconnect" {
				return
			}
			if mode == "malformed" {
				fmt.Fprintln(os.Stdout, "not-json")
				continue
			}
			if mode == "rpc-error" {
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{
					"code": -32602, "message": "secret-provider-diagnostics",
				}})
				continue
			}
			if os.Getenv("TAXION_FAKE_HANG") == "1" {
				continue
			}
			if mode != "empty-output" {
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
					"sessionId": "fake-session", "update": map[string]any{"sessionUpdate": "agent_message_chunk",
						"content": map[string]string{"type": "text", "text": `{"ok":true}`}},
				}})
			}
			result = map[string]any{"stopReason": "end_turn", "usage": map[string]int{"inputTokens": 4, "outputTokens": 2}}
			if mode == "usage-missing" {
				result = map[string]any{"stopReason": "end_turn"}
			}
		case "session/cancel":
			continue
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]int{"code": -32601}})
			continue
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if mode == "stdin-blocked" && request.Method == "session/new" {
			time.Sleep(time.Hour)
		}
	}
}

func testConfig(t *testing.T, role string) Config {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(server, []byte(`{"model":"test/fake"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Role: role, SystemPrompt: "Return the approved result.", WorkingDirectory: t.TempDir(),
		CLIPath: exe, CLIHash: hashBytes(data), ServerConfig: server, ServerConfigHash: hashBytes([]byte(`{"model":"test/fake"}`)),
		FileAccess: &core.FileAccessPolicy{Level: core.FileAccessExact, Paths: []string{"result.txt"}}}
}

func fakeAdapter(t *testing.T, hang bool) *Adapter {
	t.Helper()
	config := testConfig(t, "CODER")
	a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	a.launch = func(ctx context.Context, root, binary string, readOnly bool) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = []string{"TAXION_FAKE_ACP=1", "TAXION_FAKE_MODE=" + os.Getenv("TAXION_FAKE_MODE"),
			fmt.Sprintf("TAXION_FAKE_HANG=%d", map[bool]int{true: 1, false: 0}[hang])}
		return cmd, nil
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

func TestNativeToolDefinitionsNeverGrantUnapprovedOperations(t *testing.T) {
	for _, role := range []string{"ORCHESTRATOR", "CODER", "REVIEWER"} {
		t.Run(role, func(t *testing.T) {
			config := testConfig(t, role)
			if role != "ORCHESTRATOR" {
				config.AvailableTools = []string{"view"}
			}
			if role == "CODER" {
				config.AvailableTools = append(config.AvailableTools, "edit")
			}
			a, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if err := a.prepare(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(a.root, "config/opencode/opencode.json"))
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				Permission map[string]string `json:"permission"`
				Agent      map[string]struct {
					Permission map[string]string `json:"permission"`
				} `json:"agent"`
			}
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			for _, permission := range []map[string]string{settings.Permission, settings.Agent["taxion"].Permission} {
				if permission["*"] != "deny" || permission["bash"] != "deny" || permission["skill"] == "allow" {
					t.Fatal("unapproved operation was enabled", permission)
				}
				for _, name := range []string{"read", "glob", "grep"} {
					want := "allow"
					if role == "ORCHESTRATOR" {
						want = "deny"
					}
					if permission[name] != want {
						t.Fatal("read permission differs from role tools", permission)
					}
				}
				if (permission["edit"] == "allow") != (role == "CODER") {
					t.Fatal("edit permission differs from role tools", permission)
				}
			}
		})
	}
}

func TestACPToolUpdatesRetainRawAndNormalizeExecutionEvidence(t *testing.T) {
	a, err := New(testConfig(t, "CODER"))
	if err != nil {
		t.Fatal(err)
	}
	a.session, a.busy = "session", true
	a.config.ToolPolicy = &tools.Policy{Skills: []tools.Asset{{Path: "/approved/writing"}}}
	for _, payload := range []string{
		`{"sessionId":"other","update":{"sessionUpdate":"tool_call","toolCallId":"ignored","title":"skill","rawInput":{"name":"writing"}}}`,
		`{"sessionId":"session","update":{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"skill","status":"pending","rawInput":{"name":"writing"}}}`,
		`{"sessionId":"session","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"loaded"}}],"rawOutput":"loaded"}}`,
		`{"sessionId":"session","update":{"sessionUpdate":"tool_call","toolCallId":"call-2","title":"read","status":"pending","rawInput":{"filePath":"/workspace/file.md","name":"writing"}}}`,
		`{"sessionId":"session","update":{"sessionUpdate":"tool_call_update","toolCallId":"call-2","status":"failed","content":[{"type":"content","content":{"type":"text","text":"denied"}}]}}`,
	} {
		a.update(json.RawMessage(payload))
	}
	raw := 0
	var observations []core.ExecutionObservation
	for _, event := range a.history {
		switch event.Type {
		case "acp_tool_update":
			raw++
		case runtime.EventExecution:
			var observation core.ExecutionObservation
			if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &observation); err != nil {
				t.Fatal("ACP execution evidence is not decodable by Controller.Finish", err)
			}
			observations = append(observations, observation)
		default:
			t.Fatal("unexpected execution event", event.Type)
		}
	}
	if raw != 4 || len(observations) != 4 {
		t.Fatal("raw or normalized evidence lost, or another session was included", raw, observations)
	}
	complete, failed := observations[1], observations[3]
	if complete.CallID != "call-1" || complete.Tool != "skill" || complete.Skill != "writing" ||
		complete.Phase != "tool-complete" || complete.Success == nil || !*complete.Success ||
		failed.CallID != "call-2" || failed.Path != "/workspace/file.md" || failed.Skill != "" || failed.Tool != "read" ||
		failed.Phase != "tool-complete" || failed.Success == nil || *failed.Success {
		t.Fatal("tool completion lost its invocation identity or failure", observations)
	}
}

func TestACPSubmissionUsesLatestAssistantMessage(t *testing.T) {
	a, err := New(testConfig(t, "CODER"))
	if err != nil {
		t.Fatal(err)
	}
	a.session, a.busy = "session", true
	for _, payload := range []string{
		`{"sessionId":"session","update":{"sessionUpdate":"agent_message_chunk","messageId":"commentary","content":{"type":"text","text":"I will edit the file."}}}`,
		`{"sessionId":"session","update":{"sessionUpdate":"agent_message_chunk","messageId":"final","content":{"type":"text","text":"{\"ok\":"}}}`,
		`{"sessionId":"other","update":{"sessionUpdate":"agent_message_chunk","messageId":"foreign","content":{"type":"text","text":"ignored"}}}`,
		`{"sessionId":"session","update":{"sessionUpdate":"agent_message_chunk","messageId":"final","content":{"type":"text","text":"true}"}}}`,
	} {
		a.update(json.RawMessage(payload))
	}
	if a.output.String() != `{"ok":true}` || a.outputMessageID != "final" {
		t.Fatal("commentary or another session contaminated the final submission", a.output.String())
	}
	var boundaries, deltas []string
	for _, event := range a.history {
		if event.Type == "acp_message_start" {
			boundaries = append(boundaries, event.Payload)
		}
		if event.Type == runtime.EventOutputDelta {
			deltas = append(deltas, event.Payload)
		}
	}
	if strings.Join(boundaries, ",") != "commentary,final" ||
		strings.Join(deltas, "") != `I will edit the file.{"ok":true}` {
		t.Fatal("message boundaries or original commentary were lost", boundaries, deltas)
	}
}

func TestAssistantSubmissionFraming(t *testing.T) {
	for _, output := range []string{
		`{"ok":true}`,
		"```json\n{\"ok\":true}\n```",
		"Verified actual edits.\n\n```json\n{\"ok\":true}\n```",
	} {
		submission, err := assistantSubmission(output)
		if err != nil || submission != `{"ok":true}` {
			t.Fatal("unambiguous JSON framing rejected", submission, err)
		}
	}
	for _, output := range []string{
		"",
		"Verified actual edits.",
		"Verified actual edits. {\"ok\":true}",
		"inline ```json\n{\"ok\":true}\n```",
		"```json\n{\"ok\":true}\n```\nTrailing commentary.",
		"```json\n{\"ok\":false}\n```\n```json\n{\"ok\":true}\n```",
		"Verified.\n```json\n{invalid}\n```",
	} {
		if _, err := assistantSubmission(output); err == nil {
			t.Fatal("ambiguous or invalid submission accepted", output)
		}
	}
}

func TestBoundSubmissionUsesHostResultIdentity(t *testing.T) {
	config := testConfig(t, "CODER")
	for _, id := range []string{"", "urn:taxion:dispatch-result:bound:v1"} {
		config.ResponseSchema = map[string]any{"$id": id}
		a, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		if a.UsesBoundResultIdentity() != (id != "") {
			t.Fatal("bound result identity differs from the approved response schema")
		}
	}
}

func TestFakePeerLifecycleOutputUsageAndIsolation(t *testing.T) {
	a := fakeAdapter(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := a.Start(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Resume(ctx, "previous-runtime-session"); err == nil {
		t.Fatal("cross-runtime session accepted")
	}
	if err := a.Send(ctx, id, "execute"); err != nil {
		t.Fatal(err)
	}
	events, err := a.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	output, usage := false, false
	for event := range events {
		switch event.Type {
		case runtime.EventOutput:
			output = event.Payload == `{"ok":true}`
		case runtime.EventUsage:
			var observed core.DispatchUsage
			if err := json.Unmarshal([]byte(event.Payload), &observed); err != nil {
				t.Fatal(err)
			}
			usage = observed.InputTokens != nil && *observed.InputTokens == 4 && observed.TotalNanoAIU == nil
		case runtime.EventFailed:
			t.Fatal(event.Payload)
		case runtime.EventTurnFinished:
			if !output || !usage {
				t.Fatalf("missing output/usage: %v %v", output, usage)
			}
			return
		}
	}
	t.Fatal("stream ended before completion")
}

func TestStopReapsUncooperativePeerAndPreservesPartialOutput(t *testing.T) {
	a := fakeAdapter(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := a.Start(ctx, "task")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Send(ctx, id, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.root, "workspace/result.txt"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.done:
	default:
		t.Fatal("Stop returned before subprocess exit")
	}
	content, err := os.ReadFile(filepath.Join(a.Worktree(), "result.txt"))
	if err != nil || string(content) != "partial" {
		t.Fatalf("partial output not preserved: %s %v", content, err)
	}
	if err := a.Resume(ctx, id); err == nil {
		t.Fatal("cancelled peer resumed")
	}
}

func TestBlockedACPInputCannotPreventDeadlineOrStop(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			t.Setenv("TAXION_FAKE_MODE", "stdin-blocked")
			a := fakeAdapter(t, false)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id, err := a.Start(ctx, "task")
			if err != nil {
				t.Fatal(err)
			}
			turnContext, cancelTurn := context.WithTimeout(ctx, 200*time.Millisecond)
			if stop {
				turnContext = ctx
			}
			defer cancelTurn()
			if err := a.Send(turnContext, id, strings.Repeat("x", 256*1024)); err != nil {
				t.Fatal(err)
			}
			if stop {
				time.Sleep(50 * time.Millisecond)
				if err := a.Stop(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-a.done:
			case <-ctx.Done():
				t.Fatal("blocked ACP stdin prevented process termination")
			}
		})
	}
}

func TestAssetChangesAndExecutableConfigRejectedBeforeLaunch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, malicious := range []string{`{"model":"test/fake","plugin":["unapproved"]}`, `{"model":"test/fake","permission":"allow"}`} {
		config := testConfig(t, "CODER")
		if err := os.WriteFile(config.ServerConfig, []byte(malicious), 0600); err != nil {
			t.Fatal(err)
		}
		config.ServerConfigHash = hashBytes([]byte(malicious))
		a, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Start(ctx, "task"); err == nil {
			t.Fatal("executable config accepted")
		}
		_ = a.Close()
	}
	a := fakeAdapter(t, false)
	if err := os.WriteFile(a.config.ServerConfig, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(ctx, "task"); err == nil {
		t.Fatal("changed server configuration accepted")
	}
}

func TestFakePeerFailuresDeniedClientOperationsAndUnknownUsage(t *testing.T) {
	for _, mode := range []string{"version", "model", "permission", "client-filesystem", "disconnect", "malformed", "rpc-error", "usage-missing", "empty-output"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TAXION_FAKE_MODE", mode)
			a := fakeAdapter(t, false)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id, err := a.Start(ctx, "task")
			if mode == "version" || mode == "model" {
				if err == nil || a.cmd == nil || a.cmd.ProcessState == nil {
					t.Fatalf("unverified session not rejected and reaped: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cursor, err := a.EventCursor(id)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Send(ctx, id, "execute"); err != nil {
				t.Fatal(err)
			}
			events, err := a.EventsAfter(ctx, id, cursor)
			if err != nil {
				t.Fatal(err)
			}
			capabilities, unknownUsage, observedUsage := false, false, false
			for event := range events {
				if strings.Contains(event.Payload, "secret-provider-diagnostics") {
					t.Fatal("provider diagnostics leaked")
				}
				switch event.Type {
				case "acp_capabilities":
					capabilities = true
				case "acp_usage_unavailable":
					unknownUsage = strings.Contains(event.Payload, "not zero/free/unlimited")
				case runtime.EventUsage:
					observedUsage = true
				case runtime.EventFailed:
					if mode == "permission" || mode == "client-filesystem" || mode == "usage-missing" {
						t.Fatal(event.Payload)
					}
					if err := a.waitTurn(ctx); err != nil {
						t.Fatal(err)
					}
					if err := a.Resume(ctx, id); err == nil {
						t.Fatal("failed peer was resumable")
					}
					return
				case runtime.EventTurnFinished:
					if mode != "permission" && mode != "client-filesystem" && mode != "usage-missing" {
						t.Fatal("failed transport produced successful completion")
					}
					if !capabilities || mode == "usage-missing" && (!unknownUsage || observedUsage) {
						t.Fatal("capability or unknown-usage evidence was lost")
					}
					return
				}
			}
			t.Fatal("no terminal event")
		})
	}
}

func TestExplicitDeadlineRequiredAndExpiredTurnIsReaped(t *testing.T) {
	a := fakeAdapter(t, true)
	if _, err := a.Start(context.Background(), "unbounded"); err == nil || a.cmd != nil {
		t.Fatal("unbounded execution started")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	id, err := a.Start(ctx, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Send(context.Background(), id, "unbounded"); err == nil {
		t.Fatal("unbounded prompt accepted")
	}
	turn, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := a.Send(turn, id, "bounded"); err != nil {
		t.Fatal(err)
	}
	if err := a.waitTurn(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.done:
	default:
		t.Fatal("deadline did not reap the process")
	}
	events, err := a.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Type == runtime.EventCancelled {
			return
		}
	}
	t.Fatal("terminal cancellation was lost")
}

func TestWorkspaceRejectsSymlinksUnapprovedWritesAndHostConflicts(t *testing.T) {
	for _, scenario := range []string{"symlink", "unapproved", "conflict", "reviewer"} {
		t.Run(scenario, func(t *testing.T) {
			config := testConfig(t, "CODER")
			if err := os.WriteFile(filepath.Join(config.WorkingDirectory, "result.txt"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "symlink" {
				if err := os.Symlink(t.TempDir(), filepath.Join(config.WorkingDirectory, "link")); err != nil {
					t.Fatal(err)
				}
				config.FileAccess.Paths = []string{"link/secret"}
			}
			directory := t.TempDir()
			before, err := copyWorkspace(config, directory)
			if scenario == "symlink" {
				if err == nil {
					t.Fatal("symlink traversed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			path := "result.txt"
			switch scenario {
			case "unapproved":
				path = "forbidden.txt"
			case "conflict":
				_ = os.WriteFile(filepath.Join(config.WorkingDirectory, path), []byte("host edit"), 0600)
			case "reviewer":
				config.Role = "REVIEWER"
			}
			if err := os.WriteFile(filepath.Join(directory, path), []byte("agent edit"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := exportWorkspace(config, directory, before); err == nil {
				t.Fatal("unsafe export succeeded")
			}
		})
	}
}

func TestNamespaceIsolation(t *testing.T) {
	if os.Getenv("TAXION_NAMESPACE_TEST") != "1" {
		t.Skip("opt in to Linux namespace execution with TAXION_NAMESPACE_TEST=1")
	}
	for _, role := range []string{"CODER", "REVIEWER"} {
		t.Run(role, func(t *testing.T) {
			config := testConfig(t, role)
			forbidden := filepath.Join(t.TempDir(), "host-secret-sentinel")
			if err := os.WriteFile(forbidden, []byte("must-not-be-visible"), 0600); err != nil {
				t.Fatal(err)
			}
			probe, err := json.Marshal(map[string]any{"forbidden": forbidden, "readOnly": role != "CODER"})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config.WorkingDirectory, "boundary.json"), probe, 0600); err != nil {
				t.Fatal(err)
			}
			config.FileAccess.Paths = []string{"boundary.json"}
			a, err := New(config)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := a.Start(ctx, "task"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkNamespaceBoundary() error {
	var probe struct {
		Forbidden string `json:"forbidden"`
		ReadOnly  bool   `json:"readOnly"`
	}
	data, err := os.ReadFile("/workspace/boundary.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if _, err := os.Stat(probe.Forbidden); !os.IsNotExist(err) {
		return fmt.Errorf("host sentinel is visible: %v", err)
	}
	if _, err := os.Stat("/bin/sh"); !os.IsNotExist(err) {
		return fmt.Errorf("unapproved shell is visible")
	}
	cmd := exec.Command("/bin/rg", "--no-config", "--files", ".")
	cmd.Dir = "/workspace"
	listing, err := cmd.Output()
	if err != nil || !strings.Contains(string(listing), "boundary.json") {
		return fmt.Errorf("native ripgrep cannot read the isolated workspace: %v", err)
	}
	if err := os.WriteFile("/bin/rg", []byte("changed"), 0700); err == nil {
		return fmt.Errorf("native ripgrep is writable")
	}
	if _, err := os.Stat("/etc/opencode"); !os.IsNotExist(err) {
		return fmt.Errorf("managed configuration is visible")
	}
	if err := os.WriteFile("/config/opencode/opencode.json", []byte("{}"), 0600); err == nil {
		return fmt.Errorf("sealed permission configuration is writable")
	}
	if err := os.WriteFile("/workspace/new.txt", []byte("probe"), 0600); (err != nil) != probe.ReadOnly {
		return fmt.Errorf("OS worktree write boundary differs from role: %v", err)
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil || !strings.Contains(string(status), "CapEff:\t0000000000000000") ||
		!strings.Contains(string(status), "CapBnd:\t0000000000000000") {
		return fmt.Errorf("namespace process retained OS capabilities")
	}
	return nil
}

func TestLiveOpenCodePrompt(t *testing.T) {
	if os.Getenv("TAXION_OPENCODE_ACP_PROMPT") != "free-model-diagnostic" {
		t.Skip("explicit free-model diagnostic prompt opt-in required")
	}
	config := testConfig(t, "ORCHESTRATOR")
	config.CLIPath = os.Getenv("TAXION_OPENCODE_PATH")
	config.CLIHash = os.Getenv("TAXION_OPENCODE_HASH")
	config.ServerConfig = os.Getenv("TAXION_OPENCODE_CONFIG")
	config.ServerConfigHash = os.Getenv("TAXION_OPENCODE_CONFIG_HASH")
	config.ResponseSchema = map[string]any{"type": "object", "properties": map[string]any{
		"ok": map[string]any{"type": "boolean"},
	}, "required": []string{"ok"}, "additionalProperties": false}
	a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	a.launch = func(ctx context.Context, root, binary string, readOnly bool) (*exec.Cmd, error) {
		cmd, err := sandboxCommand(ctx, root, binary, readOnly)
		if err == nil {
			cmd.Stderr = os.Stderr
		}
		return cmd, err
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	id, err := a.Start(ctx, "diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Send(ctx, id, `Return {"ok":true}. No tools.`); err != nil {
		t.Fatal(err)
	}
	events, err := a.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		t.Logf("%s: %s", event.Type, event.Payload)
		if event.Type == runtime.EventFailed || event.Type == runtime.EventCancelled {
			t.Fatal("real prompt failed")
		}
		if event.Type == runtime.EventTurnFinished {
			return
		}
	}
	t.Fatal("no completed prompt")
}

func TestLiveOpenCodeHandshake(t *testing.T) {
	if os.Getenv("TAXION_OPENCODE_ACP_HANDSHAKE") != "1" {
		t.Skip("explicit real OpenCode handshake opt-in required; no prompt is sent")
	}
	config := testConfig(t, "REVIEWER")
	config.CLIPath = os.Getenv("TAXION_OPENCODE_PATH")
	config.CLIHash = os.Getenv("TAXION_OPENCODE_HASH")
	config.ServerConfig = os.Getenv("TAXION_OPENCODE_CONFIG")
	config.ServerConfigHash = os.Getenv("TAXION_OPENCODE_CONFIG_HASH")
	a, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	a.launch = func(ctx context.Context, root, binary string, readOnly bool) (*exec.Cmd, error) {
		cmd, err := sandboxCommand(ctx, root, binary, readOnly)
		if err == nil {
			cmd.Stderr = os.Stderr
		}
		return cmd, err
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := a.Start(ctx, "task"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.model, "opencode/") {
		t.Fatalf("unexpected operator-prepared model: %s", a.model)
	}
	t.Logf("OpenCode 1.19.0 isolated handshake selected %s; no model prompt sent", a.model)
}
