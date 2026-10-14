package copilot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/core"
)

func TestStopOnlyBindingCannotExecuteOrChangeSession(t *testing.T) {
	dispatch := core.GraphDispatch{
		Recipient: "CODER", Status: core.DispatchSuspended,
		RuntimeSessionID: "saved-session", RuntimeConfigHash: strings.Repeat("a", 64),
		Instruction: core.DispatchInstruction{Version: 4, Worktree: t.TempDir()},
	}

	adapter, err := NewForStop(dispatch, "/bin/true", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if adapter.ConfigHash() != dispatch.RuntimeConfigHash || adapter.Role() != dispatch.Recipient {
		t.Fatal("stop attachment lost its durable identity")
	}
	if _, err := adapter.Start(t.Context(), "task"); err == nil {
		t.Fatal("stop-only adapter created a session")
	}
	if err := adapter.Send(t.Context(), dispatch.RuntimeSessionID, "execute"); err == nil {
		t.Fatal("stop-only adapter sent a paid instruction")
	}
	if err := adapter.Resume(t.Context(), dispatch.RuntimeSessionID); err == nil {
		t.Fatal("stop-only adapter resumed execution")
	}
	if err := adapter.ResumeForStop(t.Context(), "other-session"); err == nil {
		t.Fatal("stop-only adapter attached to another session")
	}
	if err := adapter.Stop(t.Context(), "other-session"); err == nil {
		t.Fatal("stop-only adapter cancelled another session")
	}
	config := adapter.stopResumeConfig(nil)
	if *config.EnableSkills || *config.EnableFileHooks || *config.EnableConfigDiscovery ||
		*config.ContinuePendingWork || *config.AllowTranscriptRecovery || !config.SuppressResumeEvent ||
		*config.EnableHostGitOperations || config.Model != "" || config.SystemMessage != nil ||
		len(config.AvailableTools) != 0 || len(config.Tools) != 0 || config.Hooks != nil ||
		len(config.SkillDirectories) != 0 || len(config.PluginDirectories) != 0 {
		t.Fatal("stop-only attachment exposes executable tools or discovery")
	}
	decision, err := config.OnPermissionRequest(
		&sdk.PermissionRequestRead{Path: dispatch.Instruction.Worktree}, sdk.PermissionInvocation{})
	if _, rejected := decision.(*rpc.PermissionDecisionReject); err != nil || !rejected {
		t.Fatal("stop-only attachment did not deny all permissions", err)
	}
	dispatch.RuntimeConfigHash = ""
	if _, err := NewForStop(dispatch, "/bin/true", t.TempDir()); err == nil {
		t.Fatal("stop-only adapter accepted a missing saved hash")
	}
}

func TestStopOnlySDKProtocolNeverCreatesOrSends(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			done <- err
			return
		}
		done <- serveStopProtocol(conn)
	}()
	dispatch := core.GraphDispatch{
		Recipient: "CODER", Status: core.DispatchSuspended,
		RuntimeSessionID: "saved-session", RuntimeConfigHash: strings.Repeat("a", 64),
		Instruction: core.DispatchInstruction{Version: 4, Worktree: t.TempDir()},
	}
	adapter, err := NewForStop(dispatch, "/bin/true", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adapter.client = sdk.NewClient(&sdk.ClientOptions{
		Connection: sdk.URIConnection{URL: listener.Addr().String()}, Mode: sdk.ModeEmpty,
	})
	defer adapter.Close()
	if err := adapter.ResumeForStop(t.Context(), dispatch.RuntimeSessionID); err != nil {
		t.Fatalf("resume: %v; protocol fixture: %v", err, <-done)
	}
	if err := adapter.Stop(t.Context(), dispatch.RuntimeSessionID); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func serveStopProtocol(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	var methods []string
	for {
		header, err := textproto.NewReader(reader).ReadMIMEHeader()
		if errors.Is(err, io.EOF) {
			// SDK shutdown detaches its registered session again after Adapter.Stop.
			if !reflect.DeepEqual(methods, []string{"connect", "session.resume", "session.options.update", "session.abort", "session.detach", "session.detach"}) {
				return fmt.Errorf("unexpected cancellation sequence: %v", methods)
			}
			return nil
		}
		if err != nil {
			return err
		}
		length, err := strconv.Atoi(header.Get("Content-Length"))
		if err != nil || length <= 0 || length > 1024*1024 {
			return fmt.Errorf("invalid SDK fixture frame length: %q", header.Get("Content-Length"))
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(reader, body); err != nil {
			return err
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			return err
		}
		methods = append(methods, request.Method)
		result := map[string]any{}
		switch request.Method {
		case "connect":
			result = map[string]any{"ok": true, "protocolVersion": sdk.SDKProtocolVersion}
		case "session.resume":
			emptyTools, ok := request.Params["availableTools"].([]any)
			if !ok || len(emptyTools) != 0 ||
				request.Params["sessionId"] != "saved-session" ||
				request.Params["continuePendingWork"] != false ||
				request.Params["allowTranscriptRecovery"] != false ||
				request.Params["disableResume"] != true ||
				request.Params["enableSkills"] != false ||
				request.Params["enableConfigDiscovery"] != false ||
				request.Params["enableHostGitOperations"] != false {
				return fmt.Errorf("stop attachment changed session or enabled pending work")
			}
			result["sessionId"] = "saved-session"
		case "session.options.update", "session.abort", "session.detach":
			if request.Params["sessionId"] != "saved-session" {
				return fmt.Errorf("cancellation RPC targeted another session")
			}
			result["success"] = true
		default:
			return fmt.Errorf("stop-only adapter issued unauthorized RPC %q", request.Method)
		}
		response, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(conn, "Content-Length: %d\r\n\r\n%s", len(response), response); err != nil {
			return err
		}
	}
}
