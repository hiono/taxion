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

func TestFailedReviewObservationCannotExecute(t *testing.T) {
	dispatch := core.GraphDispatch{
		Recipient: "REVIEWER", Status: core.DispatchFailed,
		RuntimeSessionID: "saved-session", RuntimeConfigHash: strings.Repeat("a", 64),
		Instruction: core.DispatchInstruction{Version: 5, Worktree: t.TempDir()},
	}
	if _, err := NewForStop(dispatch, "/bin/true", t.TempDir()); err == nil {
		t.Fatal("ordinary stop constructor now accepts a failed Dispatch")
	}
	adapter, err := NewForFailedReview(dispatch, "/bin/true", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	if _, err := adapter.Start(t.Context(), "task"); err == nil {
		t.Fatal("failed review observer started a model")
	}
	if err := adapter.Send(t.Context(), dispatch.RuntimeSessionID, "execute"); err == nil {
		t.Fatal("failed review observer sent model work")
	}
	if err := adapter.Resume(t.Context(), dispatch.RuntimeSessionID); err == nil {
		t.Fatal("failed review observer resumed execution")
	}
	dispatch.Recipient = "CODER"
	if _, err := NewForFailedReview(dispatch, "/bin/true", t.TempDir()); err == nil {
		t.Fatal("failed Coder accepted as negative review")
	}
}

func TestStopOnlySDKProtocolNeverCreatesOrSends(t *testing.T) {
	for _, observe := range []bool{false, true} {
		t.Run(fmt.Sprintf("observe=%t", observe), func(t *testing.T) {
			testStopOnlySDKProtocol(t, observe, false)
		})
	}
	t.Run("failed-review-observation", func(t *testing.T) {
		testStopOnlySDKProtocol(t, true, true)
	})
}

func testStopOnlySDKProtocol(t *testing.T, observe, failedReview bool) {
	worktree := t.TempDir()
	var history []sdk.SessionEvent
	if observe {
		history = completedSavedHistory(worktree)
	}
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
		done <- serveStopProtocol(conn, history)
	}()
	dispatch := core.GraphDispatch{
		Recipient: "CODER", Status: core.DispatchSuspended,
		RuntimeSessionID: "saved-session", RuntimeConfigHash: strings.Repeat("a", 64),
		Instruction: core.DispatchInstruction{Version: 4, Worktree: worktree},
	}
	var adapter *Adapter
	if failedReview {
		dispatch.Recipient, dispatch.Status = "REVIEWER", core.DispatchFailed
		adapter, err = NewForFailedReview(dispatch, "/bin/true", t.TempDir())
	} else {
		adapter, err = NewForStop(dispatch, "/bin/true", t.TempDir())
	}
	if err != nil {
		t.Fatal(err)
	}
	adapter.client = sdk.NewClient(&sdk.ClientOptions{
		Connection: sdk.URIConnection{URL: listener.Addr().String()}, Mode: sdk.ModeEmpty,
	})
	defer adapter.Close()
	if observe {
		result, err := adapter.ReadSavedTurn(t.Context(), dispatch.RuntimeSessionID)
		if err != nil || result.Payload == "" || result.HistoryHash == "" {
			t.Fatalf("saved observation: %+v, %v", result, err)
		}
		handle, err := adapter.lookup(dispatch.RuntimeSessionID)
		if err != nil {
			t.Fatal(err)
		}
		handle.mu.Lock()
		handle.busy = true
		handle.mu.Unlock()
		if err := adapter.CloseSavedObservation(); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := adapter.ResumeForStop(t.Context(), dispatch.RuntimeSessionID); err != nil {
			t.Fatalf("resume: %v; protocol fixture: %v", err, <-done)
		}
		if err := adapter.Stop(t.Context(), dispatch.RuntimeSessionID); err != nil {
			t.Fatal(err)
		}
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSavedTurnRequiresBoundCompletedMainAgentHistory(t *testing.T) {
	worktree := t.TempDir()
	history := func() []sdk.SessionEvent {
		return completedSavedHistory(worktree)
	}
	for _, test := range []struct {
		name   string
		change func([]sdk.SessionEvent) []sdk.SessionEvent
	}{
		{"missing-shutdown", func(events []sdk.SessionEvent) []sdk.SessionEvent { return events[:4] }},
		{"wrong-session", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[0].Data.(*sdk.SessionStartData).SessionID = "another-session"
			return events
		}},
		{"wrong-worktree", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[0].Data.(*sdk.SessionStartData).Context.Cwd = t.TempDir()
			return events
		}},
		{"unbound-message", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[3].Data.(*sdk.AssistantMessageData).OriginatingMessageID = nil
			return events
		}},
		{"wrong-turn", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[4].Data.(*sdk.AssistantTurnEndData).TurnID = "another-turn"
			return events
		}},
		{"empty-turn", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			empty := ""
			events[3].Data.(*sdk.AssistantMessageData).TurnID = &empty
			events[4].Data.(*sdk.AssistantTurnEndData).TurnID = empty
			return events
		}},
		{"subagent-answer", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			agent := "child"
			events[3].AgentID = &agent
			return events
		}},
		{"error-shutdown", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[5].Data.(*sdk.SessionShutdownData).ShutdownType = "error"
			return events
		}},
		{"shutdown-before-turn-end", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[5].Timestamp = events[4].Timestamp.Add(-time.Second)
			return events
		}},
		{"later-execution", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			return append(events, sdk.SessionEvent{ID: "later", Timestamp: time.Now().UTC(),
				Data: &sdk.AssistantTurnStartData{TurnID: "uncompleted"}})
		}},
		{"duplicate-event", func(events []sdk.SessionEvent) []sdk.SessionEvent {
			events[3].ID = events[2].ID
			return events
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := savedTurn(test.change(history()), "saved-session", worktree); err == nil {
				t.Fatal("ambiguous or unfinished history was accepted")
			}
		})
	}
	original := history()
	result, err := savedTurn(original, "saved-session", worktree)
	if err != nil || result.Payload == "" || result.HistoryHash == "" || len(result.Events) != 1 ||
		result.InputHash != core.SHA256Hex([]byte("execute saved contract")) ||
		result.MessageID != "input-message" || result.TurnID != "review-turn" ||
		!result.ShutdownAt.Equal(original[5].Timestamp) {
		t.Fatal("completed main-agent result was not observed", result, err)
	}
	withoutShutdown := original[:5]
	if _, err := savedTurn(withoutShutdown, "saved-session", worktree); err == nil {
		t.Fatal("routine-shutdown requirement was silently removed")
	}
	stopped, err := savedQuestionTurn(withoutShutdown, "saved-session", worktree, true)
	if err != nil || !stopped.ShutdownAt.IsZero() || len(stopped.UsageTotals) != 0 ||
		stopped.Input != "execute saved contract" || !stopped.SubmittedAt.Equal(original[1].Timestamp) {
		t.Fatal("explicit stopped observation fabricated shutdown/usage or lost native input identity", err)
	}
	events := history()
	message, turn := "input-message", "review-turn"
	empty := events[3]
	empty.ID = "empty-reasoning-message"
	empty.Data = &sdk.AssistantMessageData{OriginatingMessageID: &message, TurnID: &turn}
	events = append(events[:3], append([]sdk.SessionEvent{empty}, events[3:]...)...)
	if observed, err := savedTurn(events, "saved-session", worktree); err != nil ||
		observed.Payload != result.Payload || len(observed.Events) != 2 {
		t.Fatal("empty native reasoning message hid the final answer", err)
	}
}

func TestSavedTurnRejectsLaterSubagentActivity(t *testing.T) {
	worktree, child := t.TempDir(), "child"
	for _, test := range []struct {
		name string
		data sdk.SessionEventData
	}{
		{"tool-start", &sdk.ToolExecutionStartData{}},
		{"tool-complete", &sdk.ToolExecutionCompleteData{}},
		{"turn-start", &sdk.AssistantTurnStartData{TurnID: "child-turn"}},
		{"turn-end", &sdk.AssistantTurnEndData{TurnID: "child-turn"}},
		{"message", &sdk.AssistantMessageData{Content: "child output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, at := range []int{2, 4, 5} {
				events := completedSavedHistory(worktree)
				childEvent := sdk.SessionEvent{
					ID: "child-activity", Timestamp: events[at-1].Timestamp, AgentID: &child, Data: test.data,
				}
				events = append(events[:at], append([]sdk.SessionEvent{childEvent}, events[at:]...)...)
				_, err := savedTurn(events, "saved-session", worktree)
				if err == nil {
					t.Fatal("child activity after the final answer was ignored", at)
				}
			}
		})
	}
}

func TestSavedTurnRejectsDeltaOutsideActiveTurn(t *testing.T) {
	worktree := t.TempDir()
	for _, at := range []int{2, 5} {
		for _, stopped := range []bool{false, true} {
			events := completedSavedHistory(worktree)
			delta := sdk.SessionEvent{
				ID: "delta", Timestamp: events[at-1].Timestamp,
				Data: &sdk.AssistantMessageDeltaData{DeltaContent: "later output"},
			}
			events = append(events[:at], append([]sdk.SessionEvent{delta}, events[at:]...)...)
			if stopped {
				events = events[:len(events)-1]
			}
			if _, err := savedQuestionTurn(events, "saved-session", worktree, stopped); err == nil {
				t.Fatal("out-of-turn delta accepted", at, stopped)
			}
		}
	}
	events := completedSavedHistory(worktree)
	delta := sdk.SessionEvent{
		ID: "delta", Timestamp: events[2].Timestamp,
		Data: &sdk.AssistantMessageDeltaData{DeltaContent: "active output"},
	}
	events = append(events[:3], append([]sdk.SessionEvent{delta}, events[3:]...)...)
	if _, err := savedTurn(events, "saved-session", worktree); err != nil {
		t.Fatal("active main-turn delta rejected", err)
	}
}

func TestSavedQuestionChecksActivityBeforeReceiptProjection(t *testing.T) {
	worktree := t.TempDir()
	for _, activity := range []sdk.SessionEvent{
		{Data: &sdk.PermissionCompletedData{Result: rpc.PermissionDeniedByPermissionRequestHook{}}},
		{Data: &sdk.SessionCompactionStartData{}},
		{Data: &sdk.SessionCompactionCompleteData{Success: true}},
		{Data: &sdk.ModelCallFailureData{}},
	} {
		events := completedSavedHistory(worktree)
		activity.ID, activity.Timestamp = "activity", events[2].Timestamp
		events = append(events[:3], append([]sdk.SessionEvent{activity}, events[3:]...)...)
		for _, stopped := range []bool{false, true} {
			history := events
			if stopped {
				history = history[:len(history)-1]
			}
			if _, err := savedQuestionTurn(history, "saved-session", worktree, stopped); err == nil {
				t.Fatalf("advisory activity %T disappeared before admission (stopped=%v)", activity.Data, stopped)
			}
		}
		// Generic review recovery retains its existing indexed receipt projection.
		result, err := savedTurn(events, "saved-session", worktree)
		if err != nil || len(result.Events) != 1 || result.Events[0].Payload != result.Payload {
			t.Fatalf("generic saved review receipt compatibility lost for %T: %+v, %v", activity.Data, result, err)
		}
	}
}

func completedSavedHistory(worktree string) []sdk.SessionEvent {
	message, turn := "input-message", "review-turn"
	now := time.Now().UTC()
	return []sdk.SessionEvent{
		{ID: "start", Timestamp: now, Data: &sdk.SessionStartData{
			SessionID: "saved-session", Context: &rpc.WorkingDirectoryContext{Cwd: worktree},
		}},
		{ID: "input", Timestamp: now, Data: &sdk.UserMessageData{MessageID: &message, Content: "execute saved contract"}},
		{ID: "turn-start", Timestamp: now, Data: &sdk.AssistantTurnStartData{TurnID: turn}},
		{ID: "answer", Timestamp: now, Data: &sdk.AssistantMessageData{
			Content:              `{"status":"SUCCEEDED","reviewVerdict":"REQUEST_CHANGES"}`,
			OriginatingMessageID: &message, TurnID: &turn,
		}},
		{ID: "end", Timestamp: now, Data: &sdk.AssistantTurnEndData{TurnID: turn}},
		{ID: "shutdown", Timestamp: now, Data: &sdk.SessionShutdownData{ShutdownType: "routine"}},
	}
}

func serveStopProtocol(conn net.Conn, history []sdk.SessionEvent) error {
	reader := bufio.NewReader(conn)
	var methods []string
	for {
		header, err := textproto.NewReader(reader).ReadMIMEHeader()
		if errors.Is(err, io.EOF) {
			// SDK shutdown detaches its registered session again after Adapter.Stop.
			expected := []string{"connect", "session.resume", "session.options.update", "session.abort", "session.detach", "session.detach"}
			if history != nil {
				expected = []string{"connect", "session.resume", "session.options.update", "session.getMessages", "session.detach", "session.detach"}
			}
			if !reflect.DeepEqual(methods, expected) {
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
		case "session.getMessages":
			if history == nil || request.Params["sessionId"] != "saved-session" {
				return fmt.Errorf("unbound saved-history request")
			}
			result["events"] = history
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
