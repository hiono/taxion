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
					"toolCall": map[string]any{"title": "run", "kind": "execute", "rawInput": map[string]string{"command": "git status", "cwd": "/tmp/worktree"}},
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
						Outcome string
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
			if block := acpHelperSetting("block"); block != "" && promptText == block {
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
				write(map[string]any{
					"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{"sessionId": "fake-session", "update": map[string]any{
						"sessionUpdate": "agent_message_chunk",
						"content":       map[string]string{"type": "text", "text": "fixed ACP response"},
					}},
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
				write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"stopReason": stopReason}})
				os.Exit(0)
			}
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"stopReason": stopReason}})
		case "session/cancel":
			// Cancellation is a notification and has no response.
		}
	}
}

func TestACPAdapterUsesOneProcessPerRoleAndDeniesToolRequests(t *testing.T) {
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
	if turn.Output != "fixed ACP response" || turn.SessionID != result.SessionID {
		t.Fatalf("unexpected Turn result: %#v", turn)
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
	_, err = adapter.Send(context.Background(), TurnRequest{
		OperationID: "operation_turn_after_cancel", TurnID: "turn_after_cancel", SessionID: start.SessionID,
		PromptSHA256: sha256Hex("again"), Prompt: "again", MaxTokens: 1000, Deadline: time.Now().Add(time.Second),
	}, nil)
	if !errors.As(err, &operationErr) || operationErr.Code != OperationCodeNotFound {
		t.Fatalf("Send after cancellation error = %v, want session not found", err)
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

func TestACPAdapterStopInvalidatesStartCache(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	request := newACPTestStartRequest("start", "role_execution_1")
	start, err := adapter.Start(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err = adapter.Stop(context.Background(), StopRequest{OperationID: "operation_stop_1", SessionID: start.SessionID}, nil)
	var operationErr *OperationError
	if !errors.As(err, &operationErr) || operationErr.Class != FailureAmbiguous {
		t.Fatalf("Stop error = %v, want ambiguous outcome", err)
	}
	if len(adapter.byOp) != 0 || len(adapter.byID) != 0 || len(adapter.byRole) != 0 {
		t.Fatal("Stop retained Start or session caches")
	}
}

func TestACPAdapterAcceptsFinalResponseBeforePeerEOF(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1;exit=exit")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	if _, err := adapter.Start(context.Background(), newACPTestStartRequest("exit", "role_execution_1"), nil); err != nil {
		t.Fatalf("Start lost final response sent before peer EOF: %v", err)
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
	t.Setenv("TAXION_ACP_HELPER", "1;delay-initialize=700")
	adapter := newACPTestAdapter(t)
	defer adapter.Close()
	first := newACPTestStartRequest("first", "role_execution_first")
	first.Deadline = time.Now().Add(5 * time.Second)
	started, err := adapter.Start(context.Background(), first, nil)
	if err != nil {
		t.Fatalf("Start first role: %v", err)
	}
	second := newACPTestStartRequest("second", "role_execution_second")
	second.Deadline = time.Now().Add(5 * time.Second)
	secondDone := make(chan error, 1)
	go func() {
		_, startErr := adapter.Start(context.Background(), second, nil)
		secondDone <- startErr
	}()
	time.Sleep(50 * time.Millisecond)
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
	case <-secondDone:
	case <-time.After(6 * time.Second):
		t.Fatal("second Start did not finish")
	}
}

type acpTestPermissionAuthorizer struct { calls int }

func (a *acpTestPermissionAuthorizer) Authorize(_ context.Context, request ACPToolPermissionRequest) error {
	a.calls++
	if request.OperationID != "operation_start_1" || request.JobID != "job_1" ||
		request.RoleExecutionID != "role_execution_1" || request.TaskID != "task_1" ||
		request.ExecutionPermissionID != "permission_1" || request.SessionID != "fake-session" ||
		request.RequestSHA256 != strings.Repeat("a", 64) || request.ProviderIdentity.ProviderID != "provider" ||
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
	if err != nil { t.Fatalf("NewACPAdapter: %v", err) }
	defer adapter.Close()
	prompt := "execute the approved task"
	request := StartRequest{
		OperationID: "operation_start_1", JobID: "job_1", RoleExecutionID: "role_execution_1",
		TaskID: "task_1", ExecutionPermissionID: "permission_1",
		RequestSHA256: strings.Repeat("a", 64), Prompt: prompt, PromptSHA256: sha256Hex(prompt),
		MaxTurns: 1, MaxTokens: 1000, Deadline: time.Now().Add(8 * time.Second),
	}
	if _, err := adapter.Start(context.Background(), request, nil); err != nil {
		t.Fatalf("Start with authorized Task tool request: %v", err)
	}
	if authorizer.calls != 1 { t.Fatalf("permission authorizer calls = %d, want 1", authorizer.calls) }
}

func TestACPProcessWakesReaderWhenTurnConsumerExitsWithFullQueue(t *testing.T) {
	process := newACPProcess(nil, &acpTestWriter{})
	process.closing.Store(true)
	process.beginUpdateConsumer()
	notification := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk"}}}` + "
"
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
