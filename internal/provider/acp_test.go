package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("TAXION_ACP_HELPER") != "1" {
		return
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
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		switch request.Method {
		case "initialize":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
		case "session/new":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"sessionId": "fake-session"}})
		case "session/prompt":
			write(map[string]any{
				"jsonrpc": "2.0", "id": 77, "method": "session/request_permission",
				"params": map[string]any{"sessionId": "fake-session", "toolCall": map[string]any{"title": "run"}},
			})
			if !scanner.Scan() {
				return
			}
			var permissionReply struct {
				Result struct {
					Outcome struct {
						Outcome string `json:"outcome"`
					} `json:"outcome"`
				} `json:"result"`
			}
			if json.Unmarshal(scanner.Bytes(), &permissionReply) != nil || permissionReply.Result.Outcome.Outcome != "cancelled" {
				_, _ = writer.WriteString("permission was not denied\n")
				_ = writer.Flush()
				return
			}
			write(map[string]any{
				"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{"sessionId": "fake-session", "update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content": map[string]string{"type": "text", "text": "fixed ACP response"},
				}},
			})
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]string{"stopReason": "end_turn"}})
		case "session/cancel":
			// Cancellation is a notification and has no response.
		}
	}
}

func TestACPAdapterUsesOneProcessPerRoleAndDeniesToolRequests(t *testing.T) {
	t.Setenv("TAXION_ACP_HELPER", "1")
	identity := testIdentity()
	adapter, err := NewACPAdapter(ACPConfig{
		Command: os.Args[0],
		Args: []string{"-test.run=TestACPHelperProcess"},
		WorkingDirectory: t.TempDir(),
		Identity: identity,
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
		OperationID: "operation_start_1", JobID: "job_1", RoleExecutionID: "role_execution_1",
		RequestSHA256: strings.Repeat("a", 64), PromptSHA256: sha256Hex(startPrompt),
		Prompt: startPrompt, MaxTurns: 2, MaxTokens: 1000, Deadline: time.Now().Add(8*time.Second),
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
		OperationID: "operation_turn_1", TurnID: "turn_1", SessionID: result.SessionID,
		PromptSHA256: sha256Hex(prompt), Prompt: prompt, MaxTokens: 1000,
		Deadline: time.Now().Add(8*time.Second),
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
	if _, err := NewACPAdapter(ACPConfig{}); err == nil {
		t.Fatal("empty ACP configuration was accepted")
	}
	if _, err := NewACPAdapter(ACPConfig{
		Command: "copilot", WorkingDirectory: t.TempDir(),
		Identity: Identity{ProviderID: "provider", AccountID: "account"},
	}); err == nil {
		t.Fatal("invalid Provider identity was accepted")
	}
}

var _ = exec.ErrNotFound
var _ = fmt.Sprintf
