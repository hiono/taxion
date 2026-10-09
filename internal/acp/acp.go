// Package acp is a minimal ACP (Agent Client Protocol v1) agent spike.
//
// It exposes Taxion dispatch/review over newline-delimited JSON-RPC 2.0 on
// stdio: initialize, session/new, session/prompt, session/cancel. No core
// change — all governance (DSP-001, SHA-001, CHK-002, REP-001) is enforced
// by internal/core and internal/runtime/local. Stdlib only.
//
// Prompt convention (spike): plain text dispatches the session task to the
// coder adapter; text starting with "review <sha>" binds evidence (SHA-001).
// The client-provided cwd acts as the explicit repo identity (REP-001).
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/local"
)

// JSON-RPC error codes.
const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrServer         = -32000
)

// Request is one newline-delimited JSON-RPC message.
type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Response is a JSON-RPC response (never sent for notifications).
type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

type acpSession struct {
	taskID     string
	cwd        string
	adapter    *local.Adapter
	adapterSID string
	cancelled  bool
}

// Server is a single-connection ACP agent over stdio.
type Server struct {
	storePath string

	mu       sync.Mutex
	sessions map[string]*acpSession
	seq      int
}

// New returns a Server persisting its ledger to storePath.
func New(storePath string) *Server {
	return &Server{storePath: storePath, sessions: map[string]*acpSession{}}
}

// Serve reads newline-delimited JSON-RPC from r and writes responses and
// session/update notifications to w until EOF.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	out := bufio.NewWriter(w)
	defer out.Flush()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.write(out, Response{JSONRPC: "2.0", Error: &RPCError{Code: ErrParse, Message: "parse error"}})
			continue
		}
		s.handle(ctx, req, out)
	}
	return sc.Err()
}

func (s *Server) write(out *bufio.Writer, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(v)
	out.Write(data)
	out.WriteByte('\n')
	out.Flush()
}

func (s *Server) notify(out *bufio.Writer, sessionID string, update any) {
	s.write(out, map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  map[string]any{"sessionId": sessionID, "update": update},
	})
}

func (s *Server) respond(out *bufio.Writer, id *json.RawMessage, result any, rpcErr *RPCError) {
	if id == nil {
		return // notification: no response
	}
	s.write(out, Response{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
}

func (s *Server) fail(out *bufio.Writer, id *json.RawMessage, code int, err error) {
	s.respond(out, id, nil, &RPCError{Code: code, Message: err.Error()})
}

func (s *Server) handle(ctx context.Context, req Request, out *bufio.Writer) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion int `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := 1
		if p.ProtocolVersion < 1 {
			version = 1 // our latest supported
		}
		s.respond(out, req.ID, map[string]any{
			"protocolVersion": version,
			"agentCapabilities": map[string]any{
				"loadSession": false,
				"promptCapabilities": map[string]any{
					"image": false, "audio": false, "embeddedContext": false,
				},
			},
			"agentInfo":   map[string]any{"name": "taxion", "title": "Taxion", "version": "0.1.0"},
			"authMethods": []any{},
		}, nil)
	case "session/new":
		var p struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Cwd == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("session/new: non-empty cwd is required (REP-001)"))
			return
		}
		sid, err := s.newSession(p.Cwd)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		s.respond(out, req.ID, map[string]any{"sessionId": sid}, nil)
	case "session/prompt":
		var p struct {
			SessionID string `json:"sessionId"`
			Prompt    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"prompt"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("session/prompt: sessionId and prompt are required"))
			return
		}
		var text string
		for _, b := range p.Prompt {
			if b.Type == "text" {
				text = b.Text
				break
			}
		}
		reason, err := s.prompt(ctx, p.SessionID, text, out)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		s.respond(out, req.ID, map[string]any{"stopReason": reason}, nil)
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.cancel(ctx, p.SessionID)
		// notification: no response
	default:
		s.fail(out, req.ID, ErrMethodNotFound, fmt.Errorf("method not found: %s", req.Method))
	}
}

func (s *Server) load() (core.Ledger, error) {
	l, err := core.LoadLedger(s.storePath)
	if err != nil {
		return core.Ledger{}, nil // spike: missing store starts empty
	}
	return l, nil
}

func (s *Server) newSession(cwd string) (string, error) {
	l, err := s.load()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.seq++
	sid := fmt.Sprintf("sess-%d-%d", time.Now().UnixNano(), s.seq)
	tid := fmt.Sprintf("t-%d-%d", time.Now().UnixNano(), s.seq)
	s.mu.Unlock()

	adapter := local.New("coder", "plan", "execute", "verify")
	adapterSID, err := adapter.Start(context.Background(), tid)
	if err != nil {
		return "", err
	}
	l.Tasks = append(l.Tasks, core.Task{ID: tid, Title: "acp session " + sid, Repo: cwd, CreatedAt: time.Now().UTC()})
	if err := l.Save(s.storePath); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[sid] = &acpSession{taskID: tid, cwd: cwd, adapter: adapter, adapterSID: adapterSID}
	s.mu.Unlock()
	return sid, nil
}

func (s *Server) lookup(sid string) (*acpSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sid]
	if !ok {
		return nil, fmt.Errorf("unknown session: %s", sid)
	}
	if sess.cancelled {
		return nil, fmt.Errorf("session cancelled: %s", sid)
	}
	return sess, nil
}

// prompt implements the spike convention: "review <sha>" binds evidence,
// anything else dispatches the session task.
func (s *Server) prompt(ctx context.Context, sid, text string, out *bufio.Writer) (string, error) {
	sess, err := s.lookup(sid)
	if err != nil {
		return "", err
	}
	l, err := s.load()
	if err != nil {
		return "", err
	}
	if sha, ok := strings.CutPrefix(text, "review "); ok {
		ev, err := core.BindEvidence(sess.taskID, strings.TrimSpace(sha)) // SHA-001
		if err != nil {
			return "", err
		}
		l.Evidence = append(l.Evidence, ev)
		if err := l.Save(s.storePath); err != nil {
			return "", err
		}
		s.notify(out, sid, map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": fmt.Sprintf("Evidence bound to %s for %s.", ev.SHA, sess.taskID)},
		})
		return "end_turn", nil
	}
	if err := core.GuardActiveDispatch(l.Dispatches); err != nil { // DSP-001
		return "", err
	}
	d := core.Dispatch{ID: fmt.Sprintf("d-%d", time.Now().UnixNano()),
		TaskID: sess.taskID, Repo: sess.cwd, Active: true, CreatedAt: time.Now().UTC()}
	l.Dispatches = append(l.Dispatches, d)
	if err := l.Save(s.storePath); err != nil {
		return "", err
	}
	entries := []map[string]any{}
	for _, step := range []string{"plan", "execute", "verify"} {
		entries = append(entries, map[string]any{
			"content": step, "priority": "medium", "status": "pending",
		})
	}
	s.notify(out, sid, map[string]any{"sessionUpdate": "plan", "entries": entries})
	s.notify(out, sid, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": fmt.Sprintf("Dispatched %s for %s in %s.", d.ID, sess.taskID, sess.cwd)},
	})
	return "end_turn", nil
}

func (s *Server) cancel(ctx context.Context, sid string) {
	s.mu.Lock()
	sess, ok := s.sessions[sid]
	if ok {
		sess.cancelled = true
	}
	s.mu.Unlock()
	if ok {
		sess.adapter.Stop(ctx, sess.adapterSID)
	}
}
