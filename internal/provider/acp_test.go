package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func acpHelperSetting(name string) string {
	for _, part := range strings.Split(os.Getenv("TAXION_ACP_HELPER"), ";") {
		key, value, found := strings.Cut(part, "=")
		if found && key == name {
			return value
		}
	}
	return ""
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("TAXION_ACP_HELPER") == "" {
		return
	}
	if os.Getenv("TAXION_ACP_HOLD_STDOUT") == "1" {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("COPILOT_GITHUB_TOKEN") != "" || os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != "" {
		os.Exit(17)
	}
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	write := func(value any) {
		encoded, _ := json.Marshal(value)
		_, _ = writer.Write(append(encoded, '\n'))
		_ = writer.Flush()
	}
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		switch request.Method {
		case "initialize":
			if signalPath := acpHelperSetting("signal"); signalPath != "" {
				file, openErr := os.OpenFile(signalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if openErr == nil {
					_, _ = io.WriteString(file, "initialize\n")
					_ = file.Close()
				}
			}
			if delay := acpHelperSetting("delay-initialize"); delay != "" {
				milliseconds, _ := strconv.Atoi(delay)
				time.Sleep(time.Duration(milliseconds) * time.Millisecond)
			}
			version := 1
			if configured := acpHelperSetting("protocol"); configured != "" {
				version, _ = strconv.Atoi(configured)
			}
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": version}})
		case "session/new":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"sessionId": "fake-session"}})
		case "session/prompt":
			var promptRequest struct {
				Prompt []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			_ = json.Unmarshal(request.Params, &promptRequest)
			promptText := ""
			if len(promptRequest.Prompt) > 0 {
				promptText = promptRequest.Prompt[0].Text
			}
			write(map[string]any{
				"jsonrpc": "2.0", "id": 77, "method": "session/request_permission",
				"params": map[string]any{
					"sessionId": "fake-session",
					"toolCall":  map[string]any{"title": "run", "kind": "execute", "rawInput": map[string]string{"command": "git status", "cwd": "/tmp/worktree"}},
					"options": []map[string]string{
						{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
						{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
					},
				},
			})
			if !scanner.Scan() {
				return
			}
			var permissionReply struct {
				Result struct {
					Outcome struct {
						Outcome  string
						OptionID string
					}
				}
			}
			if json.Unmarshal(scanner.Bytes(), &permissionReply) != nil {
				_, _ = writer.WriteString("permission response was invalid\n")
				_ = writer.Flush()
				return
			}
			selected := permissionReply.Result.Outcome.Outcome == "selected" && permissionReply.Result.Outcome.OptionID == "allow-once"
			if permissionReply.Result.Outcome.Outcome != "cancelled" && !selected {
				_, _ = writer.WriteString("permission response was not a one-time allow or denial\n")
				_ = writer.Flush()
				return
			}
			if acpHelperSetting("require-allow") == "1" && !selected {
				_, _ = writer.WriteString("expected one-time permission approval\n")
				_ = writer.Flush()
				return
			}
			if acpHelperSetting("require-deny") == "1" && permissionReply.Result.Outcome.Outcome != "cancelled" {
				_, _ = writer.WriteString("expected default permission denial\n")
				_ = writer.Flush()
				return
			}
			if block := acpHelperSetting("block"); block != "" && promptText == block {
				if signalPath := acpHelperSetting("signal"); signalPath != "" {
					file, openErr := os.OpenFile(signalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
					if openErr == nil {
						_, _ = io.WriteString(file, "prompt\n")
						_ = file.Close()
					}
				}
				write(map[string]any{
					"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{"sessionId": "fake-session", "update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]string{"type": "text", "text": "partial"},
					}},
				})
				if !scanner.Scan() {
					return
				}
				var cancellation struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal(scanner.Bytes(), &cancellation)
				if cancellation.Method == "session/cancel" {
					write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"stopReason": "cancelled"}})
				}
				continue
			}
			updates := 1
			if configured := acpHelperSetting("updates"); configured != "" {
				updates, _ = strconv.Atoi(configured)
			}
			for index := 0; index < updates; index++ {
				update := map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]string{"type": "text", "text": "fixed ACP response"},
				}
				if acpHelperSetting("child") != "" && promptText == "continue" {
					update["_meta"] = map[string]any{"opencode/child-session": map[string]any{"id": "child-session"}}
				}
				write(map[string]any{
					"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{"sessionId": "fake-session", "update": update},
				})
			}
			stopReason := "end_turn"
			if configured := acpHelperSetting("stop"); configured != "" {
				parts := strings.SplitN(configured, ":", 2)
				if len(parts) == 2 && promptText == parts[0] {
					stopReason = parts[1]
				}
			}
			if exit := acpHelperSetting("exit"); exit != "" && promptText == exit {
				child := exec.Command(os.Args[0], "-test.run=TestACPHelperProcess")
				child.Env = append(os.Environ(), "TAXION_ACP_HOLD_STDOUT=1")
				child.Stdout = os.Stdout
				if err := child.Start(); err != nil {
					os.Exit(18)
				}
				promptResult := map[string]any{"stopReason": stopReason}
				if acpHelperSetting("usage") != "missing" || promptText != "continue" {
					promptResult["usage"] = map[string]uint64{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15}
				}
				write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": promptResult})
				os.Exit(0)
			}
			promptResult := map[string]any{"stopReason": stopReason}
			if acpHelperSetting("usage") != "missing" || promptText != "continue" {
				promptResult["usage"] = map[string]uint64{"inputTokens": 10, "outputTokens": 5, "totalTokens": 15}
			}
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": promptResult})
		case "session/cancel":
			// Cancellation is a notification and has no response.
		}
	}
}

func TestACPAdapterUsesOneProcessPerRoleAndDeniesToolRequests(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;require-deny=1")
	t.Setenv("GH_TOKEN", "host-gh-token")
	t.Setenv("GITHUB_TOKEN", "host-github-token")
	identity := testIdentity()
	adapter, err := NewACPAdapter(ACPConfig{
		Command:          os.Args[0],
		Args:             []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(),
		Identity:         identity,
		Environment:      []string{"TAXION_ACP_HELPER"},
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	defer func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	startPrompt := "start the assigned task"
	start := StartRequest{
		OperationID:     "operation_start_1",
		JobID:           "job_1",
		RoleExecutionID: "role_execution_1",
		RequestSHA256:   strings.Repeat("a", 64),
		PromptSHA256:    sha256Hex(startPrompt),
		Prompt:          startPrompt,
		MaxTurns:        2,
		MaxTokens:       1000,
		Deadline:        time.Now().Add(8 * time.Second),
	}
	result, err := adapter.Start(ctx, start, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.SessionID != "fake-session" || result.OperationID != start.OperationID || !validSHA256(result.EvidenceSHA256) {
		t.Fatalf("unexpected Start result: %#v", result)
	}

	prompt := "continue"
	turn, err := adapter.Send(ctx, TurnRequest{
		OperationID:  "operation_turn_1",
		TurnID:       "turn_1",
		SessionID:    result.SessionID,
		PromptSHA256: sha256Hex(prompt),
		Prompt:       prompt,
		MaxTokens:    1000,
		Deadline:     time.Now().Add(8 * time.Second),
	}, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.Output != "fixed ACP response" || turn.SessionID != result.SessionID || turn.TokensUsed != 15 {
		t.Fatalf("unexpected Turn result or usage: %#v", turn)
	}

	capabilities, err := adapter.Capabilities(ctx)
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if capabilities.Has(CapabilityStartIdempotency) || capabilities.Has(CapabilitySendIdempotency) ||
		capabilities.Has(CapabilityStartLookup) || capabilities.Has(CapabilitySendLookup) {
		t.Fatal("ACP transport was allowed to claim unverified recovery capabilities")
	}

	if _, err := adapter.Start(ctx, start, nil); err != nil {
		t.Fatalf("same in-process Start operation was not reused: %v", err)
	}
}

func TestACPAdapterFailsClosedWhenUsageIsMissingOrIncomplete(t *testing.T) {
	for _, helperSetting := range []string{"1;usage=missing", "1;child=1"} {
		t.Run(helperSetting, func(t *testing.T) {
			t.Setenv("TAXION_ACP_HELPER", helperSetting)
			adapter := newACPTestAdapter(t)
			defer adapter.Close()
			started, err := adapter.Start(context.Background(), newACPTestStartRequest("start", "role_execution_usage"), nil)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			prompt := "continue"
			_, err = adapter.Send(context.Background(), TurnRequest{
				OperationID: "operation_turn_usage", TurnID: "turn_usage", SessionID: started.SessionID,
				PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 1000, Deadline: time.Now().Add(8 * time.Second),
			}, nil)
			var operationErr *OperationError
			if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous || operationErr.Code != OperationCodeUnknown {
				t.Fatalf("Send error = %v, want ambiguous unknown when usage is incomplete", err)
			}
		})
	}
}

func TestACPAdapterReportsOverBudgetUsageAsFailed(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	started, err := adapter.Start(context.Background(), newACPTestStartRequest("start", "role_execution_budget"), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	prompt := "continue"
	turn, err := adapter.Send(context.Background(), TurnRequest{
		OperationID: "operation_turn_budget", TurnID: "turn_budget", SessionID: started.SessionID,
		PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 5, Deadline: time.Now().Add(8 * time.Second),
	}, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if turn.Status != "FAILED" || turn.TokensUsed != 15 || turn.Output != "" {
		t.Fatalf("over-budget turn = %#v, want failed with actual usage and no output", turn)
	}
}

func TestACPAdapterRejectsMissingConfiguration(t *testing.T) {
	identity := testIdentity()
	if _, err := NewACPAdapter(ACPConfig{
		Command: "copilot", WorkingDirectory: t.TempDir(), Identity: identity,
		Environment: []string{"GH_TOKEN"},
	}); err == nil {
		t.Fatal("secret-bearing environment variable was allowed")
	}
	if _, err := NewACPAdapter(ACPConfig{}); err == nil {
		t.Fatal("empty ACP configuration was accepted")
	}
	if _, err := NewACPAdapter(ACPConfig{
		Command:          "copilot",
		WorkingDirectory: t.TempDir(),
		Identity:         Identity{ProviderID: "provider", AccountID: "account"},
	}); err == nil {
		t.Fatal("invalid Provider identity was accepted")
	}
}

func TestACPStartPromptIncludesValidatedContextDocuments(t *testing.T) {
	content := "source context"
	document := ContextDocument{ID: "docs/README.md", SHA256: sha256Hex(content), Content: content}
	prompt := acpStartPrompt(StartRequest{Prompt: "complete the task", Context: []ContextDocument{document}})
	for _, expected := range []string{"complete the task", "docs/README.md", document.SHA256, content} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("ACP start prompt omitted context component %q", expected)
		}
	}
}

type syntheticACPTestAdapter struct {
	*ACPAdapter
}

func (a *syntheticACPTestAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	identity := a.Identity()
	evidence := func(capability Capability) CapabilityEvidence {
		return CapabilityEvidence{
			Capability:     capability,
			Identity:       identity,
			TestID:         "runtime-test",
			EvidenceSHA256: sha256Hex("synthetic ACP fake-peer evidence"),
			EvidenceLevel:  EvidenceSynthetic,
			VerifiedAt:     time.Now().UTC(),
		}
	}
	return Capabilities{
		Identity:         identity,
		StartIdempotency: true,
		SendIdempotency:  true,
		Evidence: []CapabilityEvidence{
			evidence(CapabilityStartIdempotency),
			evidence(CapabilitySendIdempotency),
		},
	}, nil
}

func TestACPFakePeerThroughProviderRuntime(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	t.Setenv("GH_TOKEN", "host-gh-token")
	t.Setenv("GITHUB_TOKEN", "host-github-token")
	identity := testIdentity()
	adapter, err := NewACPAdapter(ACPConfig{
		Command:          os.Args[0],
		Args:             []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(),
		Identity:         identity,
		Environment:      []string{"TAXION_ACP_HELPER"},
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	defer adapter.Close()

	runtime, err := newTestRuntime(&syntheticACPTestAdapter{ACPAdapter: adapter},
		&testBroker{secret: []byte("runtime-test-secret")})
	if err != nil {
		t.Fatalf("newTestRuntime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	startPrompt := "start the approved task"
	startRequest := StartRequest{
		OperationID:     "runtime_start_1",
		JobID:           "job_1",
		RoleExecutionID: "role_execution_1",
		RequestSHA256:   sha256Hex("immutable request"),
		PromptSHA256:    sha256Hex(startPrompt),
		Prompt:          startPrompt,
		MaxTurns:        2,
		MaxTokens:       1000,
		Deadline:        time.Now().Add(8 * time.Second),
	}
	startResult, err := runtime.Start(ctx, startRequest)
	if err != nil {
		t.Fatalf("Runtime.Start with fake ACP peer: %v", err)
	}
	prompt := "continue the same task"
	turnResult, err := runtime.Send(ctx, TurnRequest{
		OperationID:  "runtime_turn_1",
		TurnID:       "turn_1",
		SessionID:    startResult.SessionID,
		PromptSHA256: sha256Hex(prompt),
		Prompt:       prompt,
		MaxTokens:    1000,
		Deadline:     time.Now().Add(8 * time.Second),
	})
	if err != nil {
		t.Fatalf("Runtime.Send with fake ACP peer: %v", err)
	}
	if turnResult.Output != "fixed ACP response" {
		t.Fatalf("unexpected Runtime.Send output: %q", turnResult.Output)
	}
}

type acpTestWriter struct {
	strings.Builder
	onWrite func([]byte)
}

func (w *acpTestWriter) Write(data []byte) (int, error) {
	n, err := w.Builder.Write(data)
	if err == nil && w.onWrite != nil {
		w.onWrite(append([]byte(nil), data...))
	}
	return n, err
}

func (*acpTestWriter) Close() error { return nil }

func TestACPProcessRespondsToUnknownServerRequest(t *testing.T) {
	writer := &acpTestWriter{}
	process := newACPProcess(nil, writer)
	request := `{"jsonrpc":"2.0","id":1,"method":"unknown/request"}` + "\n"
	process.read(strings.NewReader(request))
	var response struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(writer.String())), &response); err != nil {
		t.Fatalf("decode unknown-request response: %v", err)
	}
	if response.Error.Code != -32601 {
		t.Fatalf("unknown request returned error code %d, want -32601", response.Error.Code)
	}
}

func TestACPProcessRejectsMalformedJSONRPCAndTreatsEOFAsTerminal(t *testing.T) {
	t.Run("malformed JSON", func(t *testing.T) {
		process := newACPProcess(nil, &acpTestWriter{})
		process.read(strings.NewReader("{invalid}\n"))
		if process.failure() == nil {
			t.Fatal("malformed ACP message did not fail the process")
		}
	})
	t.Run("missing JSON-RPC version", func(t *testing.T) {
		process := newACPProcess(nil, &acpTestWriter{})
		process.read(strings.NewReader(`{"id":1,"result":{}}` + "\n"))
		if process.failure() == nil {
			t.Fatal("response without jsonrpc version did not fail the process")
		}
	})
	t.Run("unexpected EOF", func(t *testing.T) {
		process := newACPProcess(nil, &acpTestWriter{})
		process.read(strings.NewReader(""))
		if process.failure() == nil {
			t.Fatal("unexpected stdout EOF did not mark the process terminal")
		}
	})
}

func TestACPProcessRetiresOnAgentOutputWithoutActiveTurnConsumer(t *testing.T) {
	process := newACPProcess(nil, &acpTestWriter{})
	process.closing.Store(true)
	notification := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk"}}}` + "\n"
	go process.read(strings.NewReader(strings.Repeat(notification, 300)))
	select {
	case <-process.readerDone:
	case <-time.After(time.Second):
		t.Fatal("ACP reader did not retire after idle agent output")
	}
	if process.failure() == nil {
		t.Fatal("idle agent output did not fail the transport")
	}
	if len(process.updates) != 0 {
		t.Fatalf("idle agent output was queued without a consumer: %d", len(process.updates))
	}
}

func TestACPProcessCallDrainsNotificationsWhileWaitingForResponse(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	defer stdoutReader.Close()
	writer := &acpTestWriter{}
	process := newACPProcess(nil, writer)
	process.closing.Store(true)
	writer.onWrite = func(frame []byte) {
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(frame), &request); err != nil {
			return
		}
		go func() {
			encoder := json.NewEncoder(stdoutWriter)
			for index := 0; index < 300; index++ {
				_ = encoder.Encode(map[string]any{
					"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{"update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]string{"type": "text", "text": "x"},
					}},
				})
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"status": "ok"}})
			_ = stdoutWriter.Close()
		}()
	}
	readerDone := make(chan struct{})
	go func() {
		process.read(stdoutReader)
		close(readerDone)
	}()
	updates := 0
	callCtx, cancelCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCall()
	result, err := process.call(callCtx, "session/prompt", map[string]string{"sessionId": "fake"}, func(json.RawMessage) error {
		updates++
		return nil
	})
	if err != nil {
		t.Fatalf("call with more than queue capacity of updates: %v", err)
	}
	if updates != 300 {
		t.Fatalf("consumed %d updates, want 300", updates)
	}
	if !strings.Contains(string(result), "ok") {
		t.Fatalf("unexpected response: %s", result)
	}
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("ACP reader did not finish")
	}
}

func TestACPProcessWriteHonorsContextDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	process := newACPProcess(nil, writer)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := process.write(ctx, map[string]any{"jsonrpc": "2.0", "method": "blocked"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write error = %v, want context deadline exceeded", err)
	}
	if process.failure() == nil {
		t.Fatal("interrupted partial write did not retire the ACP process")
	}
}

func TestACPProcessCloseHonorsContextDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	process := newACPProcess(nil, writer)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := process.close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("close exceeded context deadline: %s", elapsed)
	}
}

func newACPTestStartRequest(prompt, roleExecutionID string) StartRequest {
	return StartRequest{
		OperationID: "operation_start_1", JobID: "job_1", RoleExecutionID: roleExecutionID,
		RequestSHA256: strings.Repeat("a", 64), PromptSHA256: sha256Hex(prompt),
		Prompt: prompt, MaxTurns: 2, MaxTokens: 1000, Deadline: time.Now().Add(8 * time.Second),
	}
}

func newACPTestAdapter(t *testing.T) *ACPAdapter {
	t.Helper()
	adapter, err := NewACPAdapter(ACPConfig{
		Command: os.Args[0], Args: []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(), Identity: testIdentity(),
		Environment: []string{"TAXION_ACP_HELPER"},
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	return adapter
}

func TestACPAdapterCopiesConfigurationSlices(t *testing.T) {
	args := []string{"--model", "test-model"}
	environment := []string{"LANG"}
	adapter, err := NewACPAdapter(ACPConfig{
		Command: "copilot", Args: args, Environment: environment,
		WorkingDirectory: t.TempDir(), Identity: testIdentity(),
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	args[0] = "--no-sandbox"
	environment[0] = "GH_TOKEN"
	if adapter.config.Args[0] != "--model" || adapter.config.Environment[0] != "LANG" {
		t.Fatalf("adapter retained caller-owned slices: %#v", adapter.config)
	}
}

func TestACPAdapterRejectsUnsupportedProtocolVersion(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;protocol=2")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	_, err := adapter.Start(context.Background(), newACPTestStartRequest("start", "role_execution_1"), nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Code != OperationCodeUnsupported {
		t.Fatalf("Start error = %v, want unsupported protocol version", err)
	}
	if len(adapter.byID) != 0 || len(adapter.byOp) != 0 {
		t.Fatal("unsupported protocol version published a session or operation cache")
	}
}

func TestACPAdapterInitialTurnIDIsBounded(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	request := newACPTestStartRequest("start", strings.Repeat("r", 251))
	if _, err := adapter.Start(context.Background(), request, nil); err != nil {
		t.Fatalf("Start with maximum RoleExecutionID: %v", err)
	}
}

func TestACPAdapterDoesNotCacheNonEndTurnResults(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;stop=limit:max_tokens")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	start, err := adapter.Start(context.Background(), newACPTestStartRequest("start", "role_execution_1"), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	prompt := "limit"
	_, err = adapter.Send(context.Background(), TurnRequest{
		OperationID: "operation_turn_limit", TurnID: "turn_limit", SessionID: start.SessionID,
		PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 1000, Deadline: time.Now().Add(5 * time.Second),
	}, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
		t.Fatalf("Send error = %v, want ambiguous non-end-turn result", err)
	}
	session := adapter.byID[start.SessionID]
	session.mu.RLock()
	_, cached := session.turns["operation_turn_limit"]
	session.mu.RUnlock()
	if cached {
		t.Fatal("non-end-turn result was cached as completed")
	}
}

func TestACPAdapterPreservesSessionWhenSendIsCancelledBeforeDispatch(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;require-deny=1")
	adapter, err := NewACPAdapter(ACPConfig{
		Command: os.Args[0], Args: []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(), Identity: testIdentity(),
		Environment: []string{"TAXION_ACP_HELPER"},
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	defer func() {
		if err := adapter.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prompt := "initial prompt"
	start, err := adapter.Start(ctx, StartRequest{
		OperationID: "operation_pre_cancel_start", JobID: "job_1",
		RoleExecutionID: "role_execution_pre_cancel",
		RequestSHA256:   strings.Repeat("a", 64), PromptSHA256: sha256Hex(prompt),
		Prompt: prompt, MaxTurns: 2, MaxTokens: 1000, Deadline: time.Now().Add(4 * time.Second),
	}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	turnPrompt := "retry after pre-dispatch cancellation"
	request := TurnRequest{
		OperationID: "operation_pre_cancel_turn", TurnID: "turn_pre_cancel",
		SessionID: start.SessionID, PromptSHA256: sha256Hex(turnPrompt),
		Prompt: turnPrompt, MaxTokens: 1000, Deadline: time.Now().Add(4 * time.Second),
	}
	cancelledCtx, cancelSend := context.WithCancel(ctx)
	cancelSend()
	if _, err := adapter.Send(cancelledCtx, request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send with pre-cancelled context = %v; want context.Canceled", err)
	}
	result, err := adapter.Send(ctx, request, nil)
	if err != nil {
		t.Fatalf("Send after pre-dispatch cancellation: %v", err)
	}
	if result.Status != "COMPLETED" || result.SessionID != start.SessionID {
		t.Fatalf("session was not preserved after pre-dispatch cancellation: %#v", result)
	}
}

func TestACPAdapterInvalidatesSessionAfterCancelledPrompt(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;block=block")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	start, err := adapter.Start(context.Background(), newACPTestStartRequest("start", "role_execution_1"), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	prompt := "block"
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = adapter.Send(ctx, TurnRequest{
		OperationID: "operation_turn_cancel", TurnID: "turn_cancel", SessionID: start.SessionID,
		PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 1000, Deadline: time.Now().Add(time.Second),
	}, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
		t.Fatalf("cancelled Send error = %v, want ambiguous outcome", err)
	}
	retireDeadline := time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		_, stillRegistered := adapter.byID[start.SessionID]
		adapter.mu.Unlock()
		if !stillRegistered {
			break
		}
		if time.Now().After(retireDeadline) {
			t.Fatal("cancelled ACP session was not retired after process exit")
		}
		time.Sleep(time.Millisecond)
	}
	_, err = adapter.Send(context.Background(), TurnRequest{
		OperationID: "operation_turn_after_cancel", TurnID: "turn_after_cancel", SessionID: start.SessionID,
		PromptSHA256: sha256Hex("again"), Prompt: "again", MaxTokens: 1000, Deadline: time.Now().Add(time.Second),
	}, nil)
	if !errors.As(err, &operationErr) || operationErr.Code != OperationCodeNotFound {
		t.Fatalf("Send after cancellation error = %v, want session not found", err)
	}
}

func TestACPAdapterKeepsRoleReservedUntilProcessExit(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()

	process := &acpProcess{done: make(chan struct{})}
	session := &acpSession{
		roleExecutionID: "role_execution_stopping", sessionID: "session_stopping",
		startOperationID: "operation_start_stopping", process: process,
		turnGate: make(chan struct{}, 1), turns: make(map[string]TurnResult),
	}
	session.turnGate <- struct{}{}
	adapter.mu.Lock()
	adapter.byRole[session.roleExecutionID] = session
	adapter.byID[session.sessionID] = session
	adapter.mu.Unlock()
	if err := adapter.trackProcess(process); err != nil {
		t.Fatalf("trackProcess: %v", err)
	}

	replacement := newACPTestStartRequest("replacement", session.roleExecutionID)
	replacement.OperationID = "operation_start_replacement"
	if _, err := adapter.Start(context.Background(), replacement, nil); err == nil {
		t.Fatal("Start launched a replacement CLI before the prior process exited")
	}
	adapter.mu.Lock()
	reserved := adapter.byRole[session.roleExecutionID] == session
	processCount := len(adapter.processes)
	adapter.mu.Unlock()
	if !reserved || processCount != 1 {
		t.Fatalf("stopping RoleExecution reservation was lost: reserved=%v processes=%d", reserved, processCount)
	}

	close(process.done)
	retireDeadline := time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		_, stillReserved := adapter.byRole[session.roleExecutionID]
		adapter.mu.Unlock()
		if !stillReserved {
			break
		}
		if time.Now().After(retireDeadline) {
			t.Fatal("RoleExecution reservation was not released after process.done")
		}
		time.Sleep(time.Millisecond)
	}
	replacement.OperationID = "operation_start_replacement_after_exit"
	if _, err := adapter.Start(context.Background(), replacement, nil); err != nil {
		t.Fatalf("Start after prior process exit: %v", err)
	}
}

func TestACPAdapterCloseDuringStartDoesNotPublishOperationCache(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;block=block")
	adapter := newACPTestAdapter(t)
	startResult := make(chan error, 1)
	go func() {
		_, err := adapter.Start(context.Background(), newACPTestStartRequest("block", "role_execution_1"), nil)
		startResult <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		adapter.mu.Lock()
		registered := len(adapter.byID) > 0
		adapter.mu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Start did not register its session before prompt")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close during Start: %v", err)
	}
	if err := <-startResult; err == nil {
		t.Fatal("Start reported success after concurrent Close")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.byOp) != 0 || len(adapter.byID) != 0 || len(adapter.byRole) != 0 {
		t.Fatal("Close left Start operation or session caches published")
	}
}

func TestACPAdapterCloseInvalidatesStartCacheAndRejectsNewStarts(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	request := newACPTestStartRequest("start", "role_execution_1")
	if _, err := adapter.Start(context.Background(), request, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(adapter.byOp) != 0 {
		t.Fatal("Close retained cached Start results")
	}
	_, err := adapter.Start(context.Background(), request, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Code != OperationCodeUnavailable {
		t.Fatalf("Start after Close error = %v, want unavailable", err)
	}
}

func TestACPAdapterStopSerializesWithPermissionResponse(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	start, err := adapter.Start(context.Background(), newACPTestStartRequest("start", "role_execution_stop_permission"), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	adapter.mu.Lock()
	session := adapter.byID[start.SessionID]
	adapter.mu.Unlock()
	if session == nil {
		t.Fatal("Start did not publish its Session")
	}

	// The permission handler holds promptMu from its final authorization check
	// until its allow/deny response is written. Stop must not send cancellation
	// or terminate the process ahead of that response.
	session.promptMu.Lock()
	session.promptCtx = context.Background()
	stopStarted := make(chan struct{})
	stopDone := make(chan error, 1)
	go func() {
		close(stopStarted)
		_, stopErr := adapter.Stop(context.Background(), StopRequest{
			OperationID: "operation_stop_permission", SessionID: start.SessionID,
		}, nil)
		stopDone <- stopErr
	}()
	<-stopStarted
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned while permission response owned promptMu: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	session.promptMu.Unlock()

	select {
	case err := <-stopDone:
		var operationErr *OperationError
		if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
			t.Fatalf("Stop error = %v, want ambiguous outcome", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish after permission response released promptMu")
	}
	select {
	case <-session.process.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not terminate the ACP process")
	}
}

func TestACPAdapterStopInvalidatesStartCache(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	request := newACPTestStartRequest("start", "role_execution_1")
	start, err := adapter.Start(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	adapter.mu.Lock()
	session := adapter.byID[start.SessionID]
	adapter.mu.Unlock()
	_, err = adapter.Stop(context.Background(), StopRequest{OperationID: "operation_stop_1", SessionID: start.SessionID}, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
		t.Fatalf("Stop error = %v, want ambiguous outcome", err)
	}
	select {
	case <-session.process.done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopped ACP process did not exit")
	}
	deadline := time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		retired := adapter.detached[start.SessionID] == session
		adapter.mu.Unlock()
		if retired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stopped Session ownership was not retired after process exit")
		}
		time.Sleep(time.Millisecond)
	}
	adapter.mu.Lock()
	_, cachedStart := adapter.byOp[request.OperationID]
	_, attached := adapter.byID[start.SessionID]
	_, roleAttached := adapter.byRole[request.RoleExecutionID]
	_, retained := adapter.detached[start.SessionID]
	adapter.mu.Unlock()
	if cachedStart || attached || roleAttached || !retained {
		t.Fatalf("Stop completion state: cachedStart=%v attached=%v roleAttached=%v retained=%v",
			cachedStart, attached, roleAttached, retained)
	}
}

func TestACPAdapterAcceptsFinalResponseBeforePeerEOF(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;exit=exit")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	request := newACPTestStartRequest("exit", "role_execution_1")
	started, err := adapter.Start(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("Start lost final response sent before peer EOF: %v", err)
	}
	adapter.mu.Lock()
	session := adapter.byID[started.SessionID]
	if session == nil {
		session = adapter.detached[started.SessionID]
	}
	adapter.mu.Unlock()
	if session == nil {
		t.Fatal("final-response Session was not retained for result retrieval")
	}
	select {
	case <-session.process.done:
	case <-time.After(2 * time.Second):
		t.Fatal("ACP process did not reach EOF after final response")
	}
	deadline := time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		retired := adapter.detached[started.SessionID] == session
		adapter.mu.Unlock()
		if retired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dead ACP process was not retired from the live session indexes")
		}
		time.Sleep(time.Millisecond)
	}
	adapter.mu.Lock()
	_, attached := adapter.byID[started.SessionID]
	_, roleAttached := adapter.byRole[request.RoleExecutionID]
	_, cachedStart := adapter.byOp[request.OperationID]
	_, retained := adapter.detached[started.SessionID]
	adapter.mu.Unlock()
	if attached || roleAttached || cachedStart || !retained {
		t.Fatalf("dead peer attachment state: attached=%v roleAttached=%v cachedStart=%v retainedResult=%v",
			attached, roleAttached, cachedStart, retained)
	}
	result, err := adapter.Result(context.Background(), ResultRequest{
		OperationID: request.OperationID, SessionID: started.SessionID,
	}, nil)
	if err != nil {
		t.Fatalf("Result after peer EOF: %v", err)
	}
	if !result.Final || result.Content != "fixed ACP response" || result.TurnID == "" {
		t.Fatalf("final output was not retained after peer EOF: %#v", result)
	}
	if _, err := adapter.Start(context.Background(), request, nil); err == nil {
		t.Fatal("Start replayed a retired OperationID after peer EOF")
	} else {
		var operationErr *OperationError
		if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
			t.Fatalf("retired Start replay error = %v; want ambiguous", err)
		}
	}
}

func TestACPAdapterResultReturnsInitialStartPromptOutput(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	request := newACPTestStartRequest("start", "role_execution_1")
	start, err := adapter.Start(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	result, err := adapter.Result(context.Background(), ResultRequest{
		OperationID: request.OperationID, SessionID: start.SessionID,
	}, nil)
	if err != nil {
		t.Fatalf("Result for initial Start prompt: %v", err)
	}
	if !result.Final || result.Content != "fixed ACP response" {
		t.Fatalf("initial Start result = %#v", result)
	}
}

func TestACPAdapterStopDoesNotWaitForBlockedWrite(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	process := newACPProcess(nil, writer)
	<-process.writeGate // Simulate a concurrent write holding the transport gate.
	session := &acpSession{
		roleExecutionID: "role_execution_1",
		sessionID:       "session_1",
		process:         process,
	}
	adapter := &ACPAdapter{
		byRole: map[string]*acpSession{session.roleExecutionID: session},
		byID:   map[string]*acpSession{session.sessionID: session},
		byOp:   make(map[string]StartResult),
	}
	started := time.Now()
	_, err := adapter.Stop(context.Background(), StopRequest{
		OperationID: "operation_stop_1", SessionID: session.sessionID,
	}, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
		t.Fatalf("Stop error = %v, want ambiguous outcome", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop waited for blocked transport write: %s", elapsed)
	}
	if !session.stopping.Load() || adapter.byID[session.sessionID] != session ||
		adapter.byRole[session.roleExecutionID] != session {
		t.Fatal("Stop released RoleExecution ownership before process exit was confirmed")
	}
}

func TestACPAdapterCloseReapsCLIWhenDescendantKeepsStdoutOpen(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;exit=exit")
	adapter := newACPTestAdapter(t)
	request := newACPTestStartRequest("exit", "role_execution_pipe")
	if _, err := adapter.Start(context.Background(), request, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	started := time.Now()
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close after CLI exit: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 1750*time.Millisecond {
		t.Fatalf("Close waited for descendant-held stdout: %s", elapsed)
	}
}

func TestACPAdapterStopDoesNotWaitForAnotherRoleStartNegotiation(t *testing.T) {
	signalFile, err := os.CreateTemp(t.TempDir(), "acp-init-signal-")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	signalPath := signalFile.Name()
	if err := signalFile.Close(); err != nil {
		t.Fatalf("Close signal file: %v", err)
	}
	t.Setenv("TAXION_ACP_HELPER", "1;delay-initialize=700;signal="+signalPath)
	adapter, err := NewACPAdapter(ACPConfig{
		Command: os.Args[0], Args: []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(), Identity: testIdentity(),
		Environment: []string{"TAXION_ACP_HELPER"},
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	defer adapter.Close()
	first := newACPTestStartRequest("first", "role_execution_first")
	started, err := adapter.Start(context.Background(), first, nil)
	if err != nil {
		t.Fatalf("Start first role: %v", err)
	}
	second := newACPTestStartRequest("second", "role_execution_second")
	second.OperationID = "operation_start_2"
	secondDone := make(chan struct {
		result StartResult
		err    error
	}, 1)
	go func() {
		result, startErr := adapter.Start(context.Background(), second, nil)
		secondDone <- struct {
			result StartResult
			err    error
		}{result: result, err: startErr}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, readErr := os.ReadFile(signalPath)
		if readErr == nil && strings.Count(string(data), "initialize\n") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second ACP peer did not enter initialize negotiation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stopStarted := time.Now()
	_, stopErr := adapter.Stop(context.Background(), StopRequest{
		OperationID: "operation_stop_first", SessionID: started.SessionID,
	}, nil)
	if stopErr == nil {
		t.Fatal("Stop unexpectedly claimed quiescence")
	}
	if elapsed := time.Since(stopStarted); elapsed > 500*time.Millisecond {
		t.Fatalf("Stop blocked behind another role's Start negotiation: %s", elapsed)
	}
	select {
	case outcome := <-secondDone:
		if outcome.err != nil {
			t.Fatalf("second Start failed after negotiation: %v", outcome.err)
		}
		if outcome.result.OperationID != second.OperationID {
			t.Fatalf("second Start result operation = %q, want %q", outcome.result.OperationID, second.OperationID)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("second Start did not finish")
	}
}

func TestACPAdapterKeepsOperationIDReservedUntilInitialPromptCompletes(t *testing.T) {
	signalFile, err := os.CreateTemp(t.TempDir(), "acp-prompt-signal-")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	signalPath := signalFile.Name()
	if err := signalFile.Close(); err != nil {
		t.Fatalf("Close signal file: %v", err)
	}
	t.Setenv("TAXION_ACP_HELPER", "1;block=blocked-start;signal="+signalPath)
	adapter, err := NewACPAdapter(ACPConfig{
		Command: os.Args[0], Args: []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(), Identity: testIdentity(),
		Environment: []string{"TAXION_ACP_HELPER"},
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	first := newACPTestStartRequest("blocked-start", "role_execution_first")
	firstDone := make(chan error, 1)
	go func() {
		_, startErr := adapter.Start(context.Background(), first, nil)
		firstDone <- startErr
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, readErr := os.ReadFile(signalPath)
		if readErr == nil && strings.Contains(string(data), "prompt\n") {
			break
		}
		if time.Now().After(deadline) {
			_ = adapter.Close()
			t.Fatal("initial prompt did not reach the blocked fake peer")
		}
		time.Sleep(5 * time.Millisecond)
	}
	second := newACPTestStartRequest("second", "role_execution_second")
	_, err = adapter.Start(context.Background(), second, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
		_ = adapter.Close()
		t.Fatalf("duplicate in-flight OperationID error = %v, want ambiguous", err)
	}
	negotiationDeadline := time.Now().Add(250 * time.Millisecond)
	for {
		data, readErr := os.ReadFile(signalPath)
		if readErr != nil {
			_ = adapter.Close()
			t.Fatalf("ReadFile signal: %v", readErr)
		}
		if strings.Count(string(data), "initialize\n") != 1 {
			_ = adapter.Close()
			t.Fatalf("duplicate in-flight OperationID started another ACP peer: %q", string(data))
		}
		if time.Now().After(negotiationDeadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case startErr := <-firstDone:
		if startErr == nil {
			t.Fatal("blocked Start unexpectedly succeeded after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked Start did not finish after Close")
	}
}

func TestACPAdapterInitialSendRejectsDetachedSessionInstance(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	first := newACPTestStartRequest("first", "role_execution_first")
	started, err := adapter.Start(context.Background(), first, nil)
	if err != nil {
		t.Fatalf("Start first session: %v", err)
	}
	adapter.mu.Lock()
	firstSession := adapter.byID[started.SessionID]
	adapter.mu.Unlock()
	if firstSession == nil {
		t.Fatal("first Session was not registered")
	}
	_, _ = adapter.Stop(context.Background(), StopRequest{
		OperationID: "operation_stop_first", SessionID: started.SessionID,
	}, nil)
	retireDeadline := time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		_, stillRegistered := adapter.byID[started.SessionID]
		adapter.mu.Unlock()
		if !stillRegistered {
			break
		}
		if time.Now().After(retireDeadline) {
			t.Fatal("first ACP session was not retired after Stop")
		}
		time.Sleep(time.Millisecond)
	}
	second := newACPTestStartRequest("second", "role_execution_second")
	second.OperationID = "operation_start_2"
	if _, err := adapter.Start(context.Background(), second, nil); err != nil {
		t.Fatalf("Start replacement session: %v", err)
	}
	staleTurn := TurnRequest{
		OperationID: "operation_stale_turn", TurnID: "turn_stale",
		SessionID: firstSession.sessionID, Prompt: "must not be sent",
		PromptSHA256: sha256Hex("must not be sent"), MaxTokens: 100,
		Deadline: time.Now().Add(time.Second),
	}
	if _, err := adapter.send(context.Background(), firstSession, staleTurn, nil); err == nil {
		t.Fatal("stale session instance was allowed to send through its replacement")
	}
}

func (a *acpTestPermissionAuthorizer) Authorize(_ context.Context, request ACPToolPermissionRequest) error {
	a.calls++
	if request.OperationID != "operation_start_1" || request.JobID != "job_1" ||
		request.RoleExecutionID != "role_execution_1" || request.TaskID != "task_1" ||
		request.ExecutionPermissionID != "permission_1" || request.WorktreeAssignmentID != "worktree_1" ||
		request.WorktreePath == "" || request.SessionID != "fake-session" ||
		request.RequestSHA256 != strings.Repeat("a", 64) || request.ProviderIdentity.ProviderID != "provider.test" ||
		!strings.Contains(string(request.ToolCall), "git status") {
		return errors.New("ACP permission request did not preserve its exact Task authority")
	}
	return nil
}

func TestACPAdapterAllowsOnlyAuthorizedTaskPermissionOnce(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;require-allow=1")
	identity := testIdentity()
	authorizer := &acpTestPermissionAuthorizer{}
	adapter, err := NewACPAdapter(ACPConfig{
		Command: os.Args[0], Args: []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(), Identity: identity,
		Environment: []string{"TAXION_ACP_HELPER"}, PermissionAuthorizer: authorizer,
	})
	if err != nil {
		t.Fatalf("NewACPAdapter: %v", err)
	}
	defer adapter.Close()
	prompt := "execute the approved task"
	request := StartRequest{
		OperationID: "operation_start_1", JobID: "job_1", RoleExecutionID: "role_execution_1",
		TaskID: "task_1", ExecutionPermissionID: "permission_1",
		WorktreeAssignmentID: "worktree_1", WorktreePath: t.TempDir(),
		RequestSHA256: strings.Repeat("a", 64), Prompt: prompt, PromptSHA256: sha256Hex(prompt),
		MaxTurns: 1, MaxTokens: 1000, Deadline: time.Now().Add(8 * time.Second),
	}
	if _, err := adapter.Start(context.Background(), request, nil); err != nil {
		t.Fatalf("Start with authorized Task tool request: %v", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("permission authorizer calls = %d, want 1", authorizer.calls)
	}
}

func TestACPProcessWakesReaderWhenTurnConsumerExitsWithFullQueue(t *testing.T) {
	process := newACPProcess(nil, &acpTestWriter{})
	process.closing.Store(true)
	process.beginUpdateConsumer()
	notification := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk"}}}` + "\n"
	go process.read(strings.NewReader(strings.Repeat(notification, cap(process.updates)+1)))
	deadline := time.Now().Add(time.Second)
	for len(process.updates) < cap(process.updates) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(process.updates) != cap(process.updates) {
		t.Fatalf("reader queued %d updates, want %d", len(process.updates), cap(process.updates))
	}
	if err := process.endUpdateConsumer(); err == nil {
		t.Fatal("consumer exit with unprocessed agent output was not rejected")
	}
	select {
	case <-process.readerDone:
	case <-time.After(time.Second):
		t.Fatal("reader remained blocked after its active consumer exited")
	}
}

func TestACPProcessBoundsQueuedUpdateBytes(t *testing.T) {
	process := newACPProcess(nil, &acpTestWriter{})
	process.closing.Store(true)
	process.beginUpdateConsumer()
	chunk := strings.Repeat("x", 2*1024*1024)
	notification := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"` + chunk + `"}}}}` + "\n"
	go process.read(strings.NewReader(strings.Repeat(notification, 5)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	updates := 0
	for {
		if err := process.drainUpdates(ctx, func(json.RawMessage) error {
			updates++
			return nil
		}); err != nil {
			t.Fatalf("drainUpdates: %v", err)
		}
		select {
		case <-process.readerDone:
			if err := process.drainUpdates(ctx, func(json.RawMessage) error {
				updates++
				return nil
			}); err != nil {
				t.Fatalf("final drainUpdates: %v", err)
			}
			if updates != 5 {
				t.Fatalf("drained %d updates, want 5", updates)
			}
			if process.updateBytes != 0 {
				t.Fatalf("queued bytes after drain = %d, want 0", process.updateBytes)
			}
			if err := process.endUpdateConsumer(); err != nil {
				t.Fatalf("endUpdateConsumer: %v", err)
			}
			return
		case <-ctx.Done():
			t.Fatal("reader did not resume after queued bytes were released")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestACPProcessNotificationDrainHonorsDeadline(t *testing.T) {
	process := newACPProcess(nil, &acpTestWriter{})
	update := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk"}}}`)
	for i := 0; i < 32; i++ {
		process.updates <- update
		process.updateBytes += int64(len(update))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	err := process.drainUpdates(ctx, func(json.RawMessage) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drainUpdates error = %v, want deadline exceeded", err)
	}
}

type acpTestProcessCloser struct {
	done chan struct{}
}

func (w *acpTestProcessCloser) Write(data []byte) (int, error) {
	return len(data), nil
}

func (w *acpTestProcessCloser) Close() error {
	close(w.done)
	return nil
}

func TestACPAdapterConcurrentCloseReapsLateTrackedProcess(t *testing.T) {
	adapter := newACPTestAdapter(t)
	reservation := &acpStartReservation{cancel: func() {}, done: make(chan struct{})}
	adapter.mu.Lock()
	adapter.startingRole["role_execution_late"] = reservation
	adapter.startingOp["operation_late"] = reservation
	adapter.mu.Unlock()

	closeResults := make(chan error, 2)
	go func() { closeResults <- adapter.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		closeStarted := adapter.closeDone != nil
		adapter.mu.Unlock()
		if closeStarted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first Close did not begin")
		}
		time.Sleep(time.Millisecond)
	}

	process := newACPProcess(nil, &acpTestWriter{})
	process.stdin = &acpTestProcessCloser{done: process.done}
	if err := adapter.trackProcess(process); err == nil {
		t.Fatal("late process was accepted after adapter Close")
	}
	go func() { closeResults <- adapter.Close() }()
	adapter.releaseStartReservation("role_execution_late", "operation_late", reservation)
	close(reservation.done)

	for i := 0; i < 2; i++ {
		if closeErr := <-closeResults; closeErr != nil {
			t.Fatalf("concurrent Close %d: %v", i+1, closeErr)
		}
	}
	select {
	case <-process.done:
	default:
		t.Fatal("Close returned before the late-tracked process exited")
	}
	deadline = time.Now().Add(time.Second)
	for {
		adapter.mu.Lock()
		remaining := len(adapter.processes)
		reservations := len(adapter.startingRole) + len(adapter.startingOp)
		adapter.mu.Unlock()
		if remaining == 0 && reservations == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Close left processes=%d reservations=%d", remaining, reservations)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestACPAdapterCloseTracksDetachedProcesses(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	started, err := adapter.Start(context.Background(), newACPTestStartRequest("detach", "role_execution_detached"), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	adapter.mu.Lock()
	session := adapter.byID[started.SessionID]
	adapter.mu.Unlock()
	if session == nil {
		t.Fatal("Session was not registered")
	}
	process := session.process
	adapter.detachSession(session)
	adapter.mu.Lock()
	_, tracked := adapter.processes[process]
	adapter.mu.Unlock()
	if !tracked {
		t.Fatal("detached ACP process was removed from the adapter process registry")
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close detached process: %v", err)
	}
	select {
	case <-process.done:
	case <-time.After(time.Second):
		t.Fatal("Close returned before the detached CLI process exited")
	}
}

type acpTestPermissionAuthorizer struct {
	calls int
}
