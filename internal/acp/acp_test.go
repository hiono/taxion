package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
)

type msg = map[string]any

func run(t *testing.T, srv *Server, lines ...string) []msg {
	t.Helper()
	in := strings.Join(lines, "\n") + "\n"
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}

	var msgs []msg
	trimmed := strings.TrimSpace(out.String())
	if trimmed == "" {
		return nil
	}
	for _, line := range strings.Split(trimmed, "\n") {
		var m msg
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad output line %q: %v", line, err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func req(id int, method string, params msg) string {
	p, _ := json.Marshal(params)
	return string(mustJSON(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(p),
	}))
}

func notif(method string, params msg) string {
	p, _ := json.Marshal(params)
	return string(mustJSON(map[string]any{
		"jsonrpc": "2.0", "method": method, "params": json.RawMessage(p),
	}))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func result(t *testing.T, m msg) msg {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", m)
	}
	return r
}

func TestFullFlow(t *testing.T) {
	srv := New(filepath.Join(t.TempDir(), "ledger.json"))
	msgs := run(t, srv,
		req(0, "initialize", msg{"protocolVersion": 1}),
		req(1, "session/new", msg{"cwd": "/repo/demo"}),
	)
	if v := result(t, msgs[0])["protocolVersion"]; v != float64(1) {
		t.Fatalf("expected protocolVersion 1, got %v", v)
	}
	sid := result(t, msgs[1])["sessionId"].(string)

	msgs = run(t, srv,
		req(2, "session/prompt", msg{"sessionId": sid, "prompt": []any{
			map[string]any{"type": "text", "text": "implement feature X"},
		}}),
	)
	if len(msgs) != 3 {
		t.Fatalf("expected 2 notifications + response, got %d: %v", len(msgs), msgs)
	}
	if msgs[2]["result"].(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("expected end_turn, got %v", msgs[2])
	}

	msgs = run(t, srv,
		req(3, "session/prompt", msg{"sessionId": sid, "prompt": []any{
			map[string]any{"type": "text", "text": "another task"},
		}}),
	)
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected DSP-001 error, got %v", msgs[0])
	}

	msgs = run(t, srv,
		req(4, "session/prompt", msg{"sessionId": sid, "prompt": []any{
			map[string]any{"type": "text", "text": "review deadbeef"},
		}}),
	)
	if len(msgs) != 2 || msgs[1]["result"].(map[string]any)["stopReason"] != "end_turn" {
		t.Fatalf("expected evidence receipt + end_turn, got %v", msgs)
	}
	msgs = run(t, srv,
		req(5, "session/prompt", msg{"sessionId": sid, "prompt": []any{
			map[string]any{"type": "text", "text": "review "},
		}}),
	)
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected SHA-001 error, got %v", msgs[0])
	}
}

func TestExplicitMethods(t *testing.T) {
	srv := New(filepath.Join(t.TempDir(), "ledger.json"))
	msgs := run(t, srv, req(1, "session/new", msg{"cwd": "/repo/demo"}))
	sid := result(t, msgs[0])["sessionId"].(string)

	msgs = run(t, srv, req(2, "taxion/dispatch", msg{"sessionId": sid}))
	if len(msgs) != 3 {
		t.Fatalf("expected 2 notifications + response, got %d: %v", len(msgs), msgs)
	}
	r := result(t, msgs[2])
	if r["stopReason"] != "end_turn" || r["dispatchId"] == "" {
		t.Fatalf("expected end_turn + dispatchId, got %v", r)
	}

	msgs = run(t, srv, req(3, "taxion/dispatch", msg{"sessionId": sid}))
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected DSP-001 error, got %v", msgs[0])
	}

	msgs = run(t, srv, req(4, "taxion/review", msg{"sessionId": sid, "sha": "cafef00d"}))
	if len(msgs) != 2 || result(t, msgs[1])["stopReason"] != "end_turn" {
		t.Fatalf("expected evidence receipt + end_turn, got %v", msgs)
	}
	msgs = run(t, srv, req(5, "taxion/review", msg{"sessionId": sid, "sha": ""}))
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected SHA-001 error, got %v", msgs[0])
	}

	msgs = run(t, srv, req(6, "taxion/dispatch", msg{"sessionId": "nope"}))
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected unknown-session error, got %v", msgs[0])
	}
	msgs = run(t, srv, req(7, "taxion/review", msg{"sessionId": "nope", "sha": "x"}))
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected unknown-session error, got %v", msgs[0])
	}
	msgs = run(t, srv, req(8, "taxion/dispatch", msg{}))
	if e, ok := msgs[0]["error"].(map[string]any); !ok || int(e["code"].(float64)) != ErrInvalidParams {
		t.Fatalf("expected invalid-params, got %v", msgs[0])
	}
}

func TestGates(t *testing.T) {
	srv := New(filepath.Join(t.TempDir(), "ledger.json"))
	msgs := run(t, srv,
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":99}}`,
		`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":""}}`,
		`{"jsonrpc":"2.0","id":2,"method":"frobnicate","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"nope","prompt":[]}}`,
	)
	if v := result(t, msgs[0])["protocolVersion"]; v != float64(1) {
		t.Fatalf("version negotiation must fall back to 1, got %v", v)
	}
	for i, want := range []int{ErrInvalidParams, ErrMethodNotFound, ErrServer} {
		e, ok := msgs[i+1]["error"].(map[string]any)
		if !ok || int(e["code"].(float64)) != want {
			t.Fatalf("msg %d: expected code %d, got %v", i+1, want, msgs[i+1])
		}
	}

	msgs = run(t, srv, req(1, "session/new", msg{"cwd": "/repo/demo"}))
	sid := result(t, msgs[0])["sessionId"].(string)
	msgs = run(t, srv, notif("session/cancel", msg{"sessionId": sid}))
	if len(msgs) != 0 {
		t.Fatalf("cancel notification must produce no output, got %v", msgs)
	}
	msgs = run(t, srv, req(2, "session/prompt", msg{"sessionId": sid, "prompt": []any{
		map[string]any{"type": "text", "text": "hello"},
	}}))
	if _, ok := msgs[0]["error"]; !ok {
		t.Fatalf("expected cancelled-session error, got %v", msgs[0])
	}
}

func TestDispatchMemoryPolicyOnExplicitAndPromptMethods(t *testing.T) {
	for _, method := range []string{"taxion/dispatch", "session/prompt"} {
		t.Run(method, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			srv := New(path)
			msgs := run(t, srv, req(1, "session/new", msg{"cwd": "/target"}))
			sid := result(t, msgs[0])["sessionId"].(string)
			s, err := memory.Open(memory.PathForLedger(path))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetPromoteAfter(1); err != nil {
				t.Fatal(err)
			}
			_, candidate, err := s.Warn("check target", memory.ScopeGlobal)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Approve(candidate, memory.CheckRepoEquals, "/other"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			params := msg{"sessionId": sid}
			if method == "session/prompt" {
				params["prompt"] = []any{msg{"type": "text", "text": "dispatch"}}
			}
			msgs = run(t, srv, req(2, method, params))
			if len(msgs) != 1 || !strings.Contains(string(mustJSON(msgs[0])), "MEM-001") {
				t.Fatalf("ACP policy denial: %+v", msgs)
			}
			l, err := core.LoadLedger(path)
			if err != nil || len(l.Dispatches) != 0 {
				t.Fatalf("ACP denial changed ledger: %+v %v", l, err)
			}
			// A corrupt policy must not turn into an empty/no-policy fallback.
			if err := os.WriteFile(memory.PathForLedger(path), []byte("corrupt"), 0o600); err != nil {
				t.Fatal(err)
			}
			msgs = run(t, srv, req(3, method, params))
			if len(msgs) != 1 || msgs[0]["error"] == nil {
				t.Fatalf("corrupt ACP policy must fail: %+v", msgs)
			}
		})
	}
}
