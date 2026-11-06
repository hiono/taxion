package controller

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestFailedReviewPublicCLISynthetic(t *testing.T) {
	binary := os.Getenv("TAXION_TEST_FAILED_REVIEW_CLI")
	if binary == "" {
		t.Skip("set TAXION_TEST_FAILED_REVIEW_CLI to the candidate binary for isolated public-CLI validation")
	}
	if binary == "build" {
		binary = filepath.Join(t.TempDir(), "taxion")
		command := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/taxion")
		command.Dir = filepath.Join("..", "..", "..")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("candidate CLI build failed: %s %v", output, err)
		}
		t.Setenv("TAXION_TEST_FAILED_REVIEW_CLI", binary)
	}
	testFailedReviewSupplement(t, "artifact", true)
}

func testFailedReviewPublicCLI(t *testing.T, db *store.Store, ledger string, flow core.ReviewFlow, dispatch core.GraphDispatch, request FailedReviewRequest, turn runtime.SavedTurn) {
	t.Helper()
	dir := t.TempDir()
	message, turnID := "synthetic-request", "synthetic-review"
	var shutdown sdk.SessionShutdownData
	if err := json.Unmarshal([]byte(`{"shutdownType":"routine","modelMetrics":{"native-model":{"usage":{"inputTokens":15,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}}}}`), &shutdown); err != nil {
		t.Fatal(err)
	}
	events := []sdk.SessionEvent{
		{ID: "start", Timestamp: turn.SubmittedAt, Data: &sdk.SessionStartData{
			SessionID: turn.SessionID, Context: &rpc.WorkingDirectoryContext{Cwd: dispatch.Instruction.Worktree},
		}},
		{ID: "input", Timestamp: turn.SubmittedAt, Data: &sdk.UserMessageData{MessageID: &message, Content: "synthetic saved review"}},
		{ID: "tool", Timestamp: turn.SubmittedAt, Data: &sdk.ToolExecutionCompleteData{ToolCallID: "read-original", Success: true}},
		{ID: "answer", Timestamp: turn.SubmittedAt, Data: &sdk.AssistantMessageData{
			Content: turn.Payload, OriginatingMessageID: &message, TurnID: &turnID,
		}},
		{ID: "end", Timestamp: turn.FinishedAt, Data: &sdk.AssistantTurnEndData{TurnID: turnID}},
		{ID: "shutdown", Timestamp: turn.ShutdownAt, Data: &shutdown},
	}
	history := filepath.Join(dir, "history.json")
	if err := os.WriteFile(history, mustFlowJSON(t, events), 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	peer := filepath.Join(dir, "native-peer")
	if err := os.WriteFile(peer, []byte("#!/bin/sh\nexec "+strconv.Quote(self)+" -test.run '^TestFailedReviewNativePeer$'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	policyDir := filepath.Join(flow.Contract.Worktree, ".taxion")
	if err := os.MkdirAll(policyDir, 0700); err != nil {
		t.Fatal(err)
	}
	policy := map[string]any{"schemaVersion": 1, "mode": "development", "storeDirectory": filepath.Dir(ledger)}
	if err := os.WriteFile(filepath.Join(policyDir, "operation-policy.json"), mustFlowJSON(t, policy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(ledger), "operation-policy.json"), mustFlowJSON(t, policy), 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "decision.json")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	call := func(preview bool, timeout string) ([]byte, error) {
		t.Helper()
		if err := os.WriteFile(input, mustFlowJSON(t, request), 0600); err != nil {
			t.Fatal(err)
		}
		args := []string{"--store", ledger, "workflow", "record-failed-review", "--file", input, "--cli", peer, "--base-directory", dir, "--observation-timeout", timeout}
		if preview {
			args = append(args, "--preview")
		}
		cmd := exec.CommandContext(t.Context(), os.Getenv("TAXION_TEST_FAILED_REVIEW_CLI"), args...)
		cmd.Dir = flow.Contract.Worktree
		cmd.Env = append(os.Environ(), "TAXION_TEST_NATIVE_HISTORY="+history, "TAXION_TEST_NATIVE_ERROR="+filepath.Join(dir, "peer-error"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if err != nil {
			t.Logf("public CLI stdout=%s stderr=%s", stdout.String(), stderr.String())
			if diagnostic, readErr := os.ReadFile(filepath.Join(dir, "peer-error")); readErr == nil {
				t.Logf("synthetic peer failure: %s", diagnostic)
			}
		}
		return stdout.Bytes(), err
	}
	body, err := call(true, "30s")
	var preview FailedReviewReport
	if err != nil || json.Unmarshal(body, &preview) != nil || preview.StateChanged || preview.Hash == "" {
		t.Fatalf("public preview failed: %s %v", body, err)
	}
	request.ExpectedHash = preview.Hash
	body, err = call(false, "1ns")
	var failure FailedReviewOperationError
	if err == nil || json.Unmarshal(body, &failure) != nil || failure.Persistence != "NOT_WRITTEN" {
		t.Fatalf("pre-write timeout lacks safe diagnostics: %s %v", body, err)
	}
	afterTimeout, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, afterTimeout) {
		t.Fatal("failed public apply changed original state", err)
	}
	body, err = call(false, "30s")
	var applied FailedReviewReport
	if err != nil || json.Unmarshal(body, &applied) != nil || !applied.StateChanged ||
		applied.Hash != preview.Hash || applied.QualityAccepted || applied.ExecutionAuthorized {
		t.Fatalf("same-hash public apply failed: %s %v", body, err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || len(after.Records.FailedReviewSupplements) != 1 {
		t.Fatal("public apply did not persist exactly one supplement", err)
	}
	before.Records.FailedReviewSupplements = after.Records.FailedReviewSupplements
	if !reflect.DeepEqual(before, after) {
		t.Fatal("public apply rewrote original history, Flow, result or usage")
	}
	if _, err := call(false, "30s"); err == nil {
		t.Fatal("duplicate public apply was accepted")
	}
}

func TestFailedReviewNativePeer(t *testing.T) {
	path := os.Getenv("TAXION_TEST_NATIVE_HISTORY")
	if path == "" {
		t.Skip("subprocess-only synthetic peer")
	}
	fail := func(err any) {
		fmt.Fprintln(os.Stderr, err)
		_ = os.WriteFile(os.Getenv("TAXION_TEST_NATIVE_ERROR"), []byte(fmt.Sprint(err)), 0600)
		os.Exit(1)
	}
	history, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		header, err := textproto.NewReader(reader).ReadMIMEHeader()
		if errors.Is(err, io.EOF) {
			os.Exit(0)
		}
		if err != nil {
			fail(err)
		}
		length, err := strconv.Atoi(header.Get("Content-Length"))
		if err != nil || length <= 0 || length > 1024*1024 {
			fail("invalid synthetic RPC frame")
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			fail(err)
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			fail(err)
		}
		result := map[string]any{}
		switch request.Method {
		case "connect":
			result = map[string]any{"ok": true, "protocolVersion": sdk.SDKProtocolVersion}
		case "session.resume":
			available, ok := request.Params["availableTools"].([]any)
			if request.Params["sessionId"] != "sdk-session" || !ok || len(available) != 0 ||
				request.Params["continuePendingWork"] != false || request.Params["allowTranscriptRecovery"] != false ||
				request.Params["disableResume"] != true || request.Params["enableSkills"] != false ||
				request.Params["enableConfigDiscovery"] != false || request.Params["enableHostGitOperations"] != false {
				fail(fmt.Sprintf("nonexecuting observer enabled execution or transcript changes: %v", request.Params))
			}
			result["sessionId"] = "sdk-session"
		case "session.options.update", "session.detach", "runtime.shutdown":
			result["success"] = true
		case "session.getMessages":
			result["events"] = json.RawMessage(history)
		default:
			fail(fmt.Sprintf("nonexecuting observer issued forbidden RPC %s", request.Method))
		}
		response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if err != nil {
			fail(err)
		}
		if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(response), response); err != nil {
			fail(err)
		}
	}
}
