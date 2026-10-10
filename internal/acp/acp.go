// Package acp is a minimal ACP (Agent Client Protocol v1) agent spike.
//
// The client-provided cwd is the explicit repository identity so an editor
// session cannot silently target the server's working directory (REP-001).
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/runtime/local"
	"github.com/hiono/taxion/internal/store"
)

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

// Server is a single-connection ACP agent over stdio. Dispatch records
// ledger operations; it does not execute a worker.
type Server struct {
	storePath     string
	workspacePath string

	mu       sync.Mutex
	sessions map[string]*acpSession
	seq      int
}

// New returns a Server persisting its ledger to storePath.
func New(storePath string) *Server {
	return &Server{storePath: storePath, sessions: map[string]*acpSession{}}
}

func NewWithWorkspace(storePath, workspacePath string) *Server {
	return &Server{storePath: storePath, workspacePath: workspacePath, sessions: map[string]*acpSession{}}
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
		return // JSON-RPC notifications have no response ID.
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
			version = 1
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
		// session/cancel is an ACP notification, not a request.
	case "taxion/dispatch":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("taxion/dispatch: sessionId is required"))
			return
		}
		sess, err := s.lookup(p.SessionID)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		did, err := s.dispatch(p.SessionID, sess, out)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		s.respond(out, req.ID, map[string]any{"stopReason": "end_turn", "dispatchId": did}, nil)
	case "taxion/review":
		var p struct {
			SessionID string `json:"sessionId"`
			SHA       string `json:"sha"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("taxion/review: sessionId and sha are required"))
			return
		}
		sess, err := s.lookup(p.SessionID)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		if err := s.review(p.SessionID, sess, p.SHA, out); err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		s.respond(out, req.ID, map[string]any{"stopReason": "end_turn", "sha": strings.TrimSpace(p.SHA)}, nil)
	default:
		s.fail(out, req.ID, ErrMethodNotFound, fmt.Errorf("method not found: %s", req.Method))
	}
}

func (s *Server) load() (core.Ledger, error) {
	if s.workspacePath != "" {
		db, err := store.Open(s.workspacePath)
		if err != nil {
			return core.Ledger{}, err
		}
		defer db.Close()
		state, err := db.LoadWorkspace()
		return state.Ledger, err
	}
	l, err := core.LoadLedger(s.storePath)
	if errors.Is(err, os.ErrNotExist) {
		return core.Ledger{Tasks: []core.Task{}, Dispatches: []core.Dispatch{}, Evidence: []core.Evidence{}, Checkpoints: []core.Checkpoint{}}, nil
	}
	return l, err
}

func (s *Server) update(update func(*store.Workspace) error, legacy func(*core.Ledger) error) error {
	if s.workspacePath != "" {
		db, err := store.Open(s.workspacePath)
		if err != nil {
			return err
		}
		defer db.Close()
		return db.UpdateWorkspace(update)
	}
	l, err := s.load()
	if err != nil {
		return err
	}
	if err := legacy(&l); err != nil {
		return err
	}
	return l.Save(s.storePath)
}

func (s *Server) newSession(cwd string) (string, error) {
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
	task := core.Task{ID: tid, Title: "acp session " + sid, Repo: cwd, CreatedAt: time.Now().UTC()}
	if err := s.update(func(state *store.Workspace) error {
		state.Ledger.Tasks = append(state.Ledger.Tasks, task)
		return nil
	}, func(l *core.Ledger) error {
		l.Tasks = append(l.Tasks, task)
		return nil
	}); err != nil {
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

// Keep the deprecated prompt form for older clients, sharing dispatch/review
// helpers so its constraints do not diverge from the explicit methods.
func (s *Server) prompt(ctx context.Context, sid, text string, out *bufio.Writer) (string, error) {
	sess, err := s.lookup(sid)
	if err != nil {
		return "", err
	}
	if sha, ok := strings.CutPrefix(text, "review "); ok {
		if err := s.review(sid, sess, sha, out); err != nil {
			return "", err
		}
		return "end_turn", nil
	}
	if _, err := s.dispatch(sid, sess, out); err != nil {
		return "", err
	}
	return "end_turn", nil
}

func (s *Server) review(sid string, sess *acpSession, sha string, out *bufio.Writer) error {
	ev, err := core.BindEvidence(sess.taskID, strings.TrimSpace(sha))
	if err != nil {
		return err
	}
	if err := s.update(func(state *store.Workspace) error {
		state.Ledger.Evidence = append(state.Ledger.Evidence, ev)
		return nil
	}, func(l *core.Ledger) error {
		l.Evidence = append(l.Evidence, ev)
		return nil
	}); err != nil {
		return err
	}
	s.notify(out, sid, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": fmt.Sprintf("Evidence bound to %s for %s.", ev.SHA, sess.taskID)},
	})
	return nil
}

func (s *Server) dispatch(sid string, sess *acpSession, out *bufio.Writer) (string, error) {
	d := core.Dispatch{ID: fmt.Sprintf("d-%d", time.Now().UnixNano()),
		TaskID: sess.taskID, Repo: sess.cwd, Active: true, CreatedAt: time.Now().UTC()}
	var instruction *core.GraphDispatch
	var rendered string
	if err := s.update(func(state *store.Workspace) error {
		if err := core.GuardActiveDispatch(state.Ledger.Dispatches); err != nil {
			return err
		}
		task, found := findTask(state.Ledger, sess.taskID)
		if !found {
			return fmt.Errorf("unknown task: %s", sess.taskID)
		}
		var refs []string
		if state.HasGraph {
			refs, _ = state.Graph.TaskMemoryRefs(sess.taskID)
		}
		if err := memory.CheckDispatch(s.storePath, task, sess.cwd, refs...); err != nil {
			return err
		}
		if state.HasGraph {
			if err := state.Graph.CheckExecutable(task.ID, sess.cwd); err != nil {
				return err
			}
			graphDispatch, err := core.NewGraphDispatch(state.Graph, d.ID, task.ID, sess.cwd, core.RelationshipNone, "", "")
			if err != nil {
				return err
			}
			if err := state.Graph.IssueDispatch(graphDispatch, "acp", "dispatch issued"); err != nil {
				return err
			}
			rendered, err = core.RenderDispatchInstruction(graphDispatch)
			if err != nil {
				return err
			}
			instruction = &graphDispatch
		}
		state.Ledger.Dispatches = append(state.Ledger.Dispatches, d)
		return nil
	}, func(l *core.Ledger) error {
		if err := core.GuardActiveDispatch(l.Dispatches); err != nil {
			return err
		}
		task, found := findTask(*l, sess.taskID)
		if !found {
			return fmt.Errorf("unknown task: %s", sess.taskID)
		}
		if err := core.CheckGraphDispatch(s.storePath, task, sess.cwd); err != nil {
			return err
		}
		if err := memory.CheckDispatch(s.storePath, task, sess.cwd); err != nil {
			return err
		}
		l.Dispatches = append(l.Dispatches, d)
		return nil
	}); err != nil {
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
		"content":       map[string]any{"type": "text", "text": dispatchNotice(d, instruction, rendered, sess)},
	})
	return d.ID, nil
}

func dispatchNotice(d core.Dispatch, instruction *core.GraphDispatch, rendered string, sess *acpSession) string {
	if instruction == nil {
		return fmt.Sprintf("Dispatched %s for %s in %s.", d.ID, sess.taskID, sess.cwd)
	}
	return rendered
}

func findTask(ledger core.Ledger, id string) (core.Task, bool) {
	for _, task := range ledger.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return core.Task{}, false
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
