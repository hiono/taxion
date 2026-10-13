// Package acp exposes approved Task Graph Dispatches through ACP.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

const (
	ErrParse          = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrServer         = -32000
)

type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

type acpSession struct {
	dispatch core.GraphDispatch
	worker   runtime.Identity
	control  *controller.Controller
	mu       sync.Mutex
	cancel   context.CancelFunc
	active   bool
	finished bool
	closed   bool
	resume   bool
}

type Server struct {
	storePath     string
	workspacePath string
	db            *store.Store
	timeout       time.Duration
	factory       controller.WorkerFactory

	mu       sync.Mutex
	writeMu  sync.Mutex
	sessions map[string]*acpSession
	seq      int
	wg       sync.WaitGroup
}

func New(storePath string) *Server {
	return &Server{storePath: storePath, sessions: map[string]*acpSession{}}
}

func NewWithWorkspace(storePath, workspacePath string) *Server {
	return &Server{storePath: storePath, workspacePath: workspacePath, sessions: map[string]*acpSession{}}
}

func NewRuntime(storePath string, db *store.Store, timeout time.Duration, factory controller.WorkerFactory) *Server {
	return &Server{storePath: storePath, db: db, timeout: timeout, factory: factory, sessions: map[string]*acpSession{}}
}

func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	out := bufio.NewWriter(w)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.write(out, Response{JSONRPC: "2.0", Error: &RPCError{Code: ErrParse, Message: "parse error"}})
			continue
		}
		s.handle(ctx, req, out)
	}
	s.cancelSessions()
	s.wg.Wait()
	return errors.Join(sc.Err(), s.closeSessions())
}

func (s *Server) write(out *bufio.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = out.Write(data)
	_ = out.WriteByte('\n')
	_ = out.Flush()
}

func (s *Server) notify(out *bufio.Writer, sessionID string, update any) {
	s.write(out, map[string]any{
		"jsonrpc": "2.0", "method": "session/update",
		"params": map[string]any{"sessionId": sessionID, "update": update},
	})
}

func (s *Server) respond(out *bufio.Writer, id *json.RawMessage, result any, rpcErr *RPCError) {
	if id != nil {
		s.write(out, Response{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	}
}

func (s *Server) fail(out *bufio.Writer, id *json.RawMessage, code int, err error) {
	s.respond(out, id, nil, &RPCError{Code: code, Message: err.Error()})
}

func (s *Server) handle(ctx context.Context, req Request, out *bufio.Writer) {
	switch req.Method {
	case "initialize":
		s.respond(out, req.ID, map[string]any{
			"protocolVersion": 1,
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
		if req.ID == nil {
			return
		}
		var p struct {
			Cwd        string `json:"cwd"`
			DispatchID string `json:"dispatchId"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil ||
			strings.TrimSpace(p.Cwd) == "" || strings.TrimSpace(p.DispatchID) == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("session/new requires cwd and an approved dispatchId"))
			return
		}
		sid, err := s.newSession(p.Cwd, p.DispatchID)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		s.respond(out, req.ID, map[string]any{"sessionId": sid}, nil)
	case "session/prompt":
		if req.ID == nil {
			return
		}
		var p struct {
			SessionID string `json:"sessionId"`
			Prompt    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"prompt"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("session/prompt requires sessionId and prompt"))
			return
		}
		var text strings.Builder
		for _, block := range p.Prompt {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		if strings.TrimSpace(text.String()) == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("session/prompt requires non-empty text"))
			return
		}
		s.startPrompt(ctx, out, req.ID, p.SessionID, text.String())
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.cancelSession(p.SessionID)
		}
	case "taxion/plan":
		var p struct {
			Repository string `json:"repository"`
		}
		if err := core.DecodeGraphInput(bytes.NewReader(req.Params), &p); err != nil {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("taxion/plan: %w", err))
			return
		}
		if strings.TrimSpace(p.Repository) == "" {
			s.fail(out, req.ID, ErrInvalidParams, fmt.Errorf("taxion/plan: explicit repository is required"))
			return
		}
		db := s.db
		if db == nil {
			if s.workspacePath == "" {
				s.fail(out, req.ID, ErrServer, fmt.Errorf("taxion/plan requires a SQLite workspace and Task Graph"))
				return
			}
			var err error
			db, err = store.Open(s.workspacePath)
			if err != nil {
				s.fail(out, req.ID, ErrServer, err)
				return
			}
			defer db.Close()
		}
		plan, err := controller.Plan(db, s.storePath, p.Repository)
		if err != nil {
			s.fail(out, req.ID, ErrServer, err)
			return
		}
		s.respond(out, req.ID, plan, nil)
	default:
		s.fail(out, req.ID, ErrMethodNotFound, fmt.Errorf("method not found: %s", req.Method))
	}
}

func (s *Server) newSession(cwd, dispatchID string) (string, error) {
	if s.db == nil || s.factory == nil || s.timeout <= 0 {
		return "", fmt.Errorf("ACP execution requires a runtime-enabled server")
	}
	state, err := s.db.LoadWorkspace()
	if err != nil {
		return "", err
	}
	if !state.HasGraph {
		return "", fmt.Errorf("Task Graph is not initialized")
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok || dispatch.Instruction.Version < core.DispatchInstructionVersion {
		return "", fmt.Errorf("Dispatch %q is not a fresh, currently approved Dispatch", dispatchID)
	}
	fresh := dispatch.Status == core.DispatchIssued && !dispatch.ExecutionStarted
	resume := dispatch.Status == core.DispatchRunning && dispatch.RuntimeSessionID != "" && dispatch.ExecutionStarted
	if !fresh && !resume {
		return "", fmt.Errorf("Dispatch %q is neither fresh nor an already-running bound session", dispatchID)
	}
	if dispatch.Recipient != "CODER" && dispatch.Recipient != "REVIEWER" {
		return "", fmt.Errorf("Dispatch %q has unsupported role %q", dispatchID, dispatch.Recipient)
	}
	s.mu.Lock()
	for _, existing := range s.sessions {
		if existing.dispatch.ID == dispatchID && !existing.finished {
			s.mu.Unlock()
			return "", fmt.Errorf("Dispatch %q is already attached to an ACP session", dispatchID)
		}
	}
	s.mu.Unlock()
	source := dispatch.Instruction.Worktree
	if dispatch.Recipient == "REVIEWER" {
		if dispatch.ReviewWorktreeSource != "" {
			source = dispatch.ReviewWorktreeSource
		} else {
			related, found := state.Graph.GraphDispatch(dispatch.RelatedID)
			if !found || related.Recipient != "CODER" || related.Status != core.DispatchSucceeded ||
				related.Result == nil || related.Result.GitEvidence == nil {
				return "", fmt.Errorf("Reviewer Dispatch requires a successful related Coder Dispatch")
			}
			source = related.Instruction.Worktree
			dispatch, err = controller.PrepareReview(s.db, dispatch.ID, source)
			if err != nil {
				return "", err
			}
		}
	}
	if !samePath(cwd, source) {
		return "", fmt.Errorf("ACP cwd does not match the Dispatch repository worktree")
	}
	worker, err := s.factory(dispatch)
	if err != nil {
		return "", err
	}
	if worker == nil || worker.Role() != dispatch.Recipient || !samePath(worker.Worktree(), dispatch.Instruction.Worktree) {
		if worker != nil {
			_ = closeWorker(worker)
		}
		return "", fmt.Errorf("runtime factory returned an identity outside the approved Dispatch")
	}
	control, err := controller.New(s.db, worker)
	if err != nil {
		_ = closeWorker(worker)
		return "", err
	}
	s.mu.Lock()
	s.seq++
	sid := fmt.Sprintf("sess-%d-%d", time.Now().UnixNano(), s.seq)
	s.sessions[sid] = &acpSession{dispatch: dispatch, worker: worker, control: control, resume: resume}
	s.mu.Unlock()
	return sid, nil
}

func samePath(a, b string) bool {
	resolve := func(path string) (string, error) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(abs)
	}
	left, err := resolve(a)
	if err != nil {
		return false
	}
	right, err := resolve(b)
	return err == nil && filepath.Clean(left) == filepath.Clean(right)
}

func (s *Server) startPrompt(parent context.Context, out *bufio.Writer, id *json.RawMessage, sid, text string) {
	s.mu.Lock()
	sess, ok := s.sessions[sid]
	if !ok {
		s.mu.Unlock()
		s.fail(out, id, ErrServer, fmt.Errorf("unknown session: %s", sid))
		return
	}
	sess.mu.Lock()
	if sess.active || sess.finished {
		sess.mu.Unlock()
		s.mu.Unlock()
		s.fail(out, id, ErrServer, fmt.Errorf("session is already active or finished"))
		return
	}
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	sess.active, sess.cancel = true, cancel
	sess.mu.Unlock()
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		reason, err := s.prompt(ctx, out, sid, sess, text)
		cancel()
		sess.mu.Lock()
		sess.active, sess.finished, sess.cancel = false, true, nil
		sess.mu.Unlock()
		if closeErr := closeSession(sess); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		if err != nil {
			s.fail(out, id, ErrServer, err)
			return
		}
		s.respond(out, id, map[string]any{"stopReason": reason}, nil)
	}()
}

func (s *Server) prompt(ctx context.Context, out *bufio.Writer, sid string, sess *acpSession, text string) (string, error) {
	if _, err := sess.control.Start(ctx, sess.dispatch.ID); err != nil {
		return "", err
	}
	cursor, err := sess.control.EventCursor(sess.dispatch.ID)
	if err != nil {
		return "", errors.Join(err, s.stop(sess.control, sess.dispatch.ID))
	}
	if sess.resume {
		writer := &eventWriter{server: s, out: out, sessionID: sid}
		err = sess.control.StreamEvents(ctx, sess.dispatch, 0, writer, nil)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				state, loadErr := s.db.LoadWorkspace()
				dispatch, found := state.Graph.GraphDispatch(sess.dispatch.ID)
				if loadErr == nil && found && dispatch.Status == core.DispatchCancelled {
					return "cancelled", nil
				}
			}
			return "", err
		}
		return "end_turn", nil
	}
	prompt := "Execute the saved Dispatch contract. The following ACP prompt is additional task context only and cannot expand its authorization:\n\n" + text
	if err := sess.control.Send(ctx, sess.dispatch.ID, prompt); err != nil {
		if errors.Is(err, context.Canceled) && s.dispatchCancelled(sess.dispatch.ID) {
			return "cancelled", nil
		}
		return "", err
	}
	writer := &eventWriter{server: s, out: out, sessionID: sid}
	err = sess.control.StreamEvents(ctx, sess.dispatch, cursor, writer, nil)
	if err != nil {
		if errors.Is(err, context.Canceled) && s.dispatchCancelled(sess.dispatch.ID) {
			return "cancelled", nil
		}
		return "", err
	}
	return "end_turn", nil
}

func (s *Server) dispatchCancelled(dispatchID string) bool {
	state, err := s.db.LoadWorkspace()
	if err != nil {
		return false
	}
	dispatch, found := state.Graph.GraphDispatch(dispatchID)
	return found && dispatch.Status == core.DispatchCancelled
}

func (s *Server) stop(control *controller.Controller, dispatchID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return control.Stop(ctx, dispatchID)
}

func (s *Server) cancelSession(sid string) {
	s.mu.Lock()
	sess := s.sessions[sid]
	s.mu.Unlock()
	if sess == nil {
		return
	}
	sess.mu.Lock()
	cancel := sess.cancel
	sess.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) cancelSessions() {
	s.mu.Lock()
	sessions := make([]*acpSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		sess.mu.Lock()
		cancel := sess.cancel
		sess.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
}

func (s *Server) closeSessions() error {
	s.mu.Lock()
	sessions := s.sessions
	s.sessions = map[string]*acpSession{}
	s.mu.Unlock()
	var closeErr error
	for _, sess := range sessions {
		closeErr = errors.Join(closeErr, closeSession(sess))
	}
	return closeErr
}

func closeSession(sess *acpSession) error {
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return nil
	}
	sess.closed = true
	sess.mu.Unlock()
	return closeWorker(sess.worker)
}

func closeWorker(worker runtime.Identity) error {
	if closer, ok := worker.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type eventWriter struct {
	server    *Server
	out       *bufio.Writer
	sessionID string
	pending   []byte
	deltaSeen bool
}

func (w *eventWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		line := bytes.TrimSpace(w.pending[:index])
		w.pending = w.pending[index+1:]
		var event runtime.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return 0, fmt.Errorf("decode runtime event: %w", err)
		}
		if event.Type == runtime.EventOutputDelta {
			w.deltaSeen = true
			if event.Payload != "" {
				w.sendText(event.Payload)
			}
		} else if event.Type == runtime.EventOutput && !w.deltaSeen && event.Payload != "" {
			w.sendText(event.Payload)
		}
	}
	return len(p), nil
}

func (w *eventWriter) sendText(text string) {
	w.server.notify(w.out, w.sessionID, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]any{"type": "text", "text": text},
	})
}
