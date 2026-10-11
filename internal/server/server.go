// Package server exposes the HTTP API over the CLI-owned ledger.
// Shared core guards keep HTTP and CLI operations subject to the same
// constraints; endpoint contracts belong in docs/05-ledger-schema.md.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

// Server serves the Taxion API from a JSON ledger file.
type Server struct {
	StorePath     string
	WorkspacePath string
	// Token gates requests when non-empty; an empty value leaves them unauthenticated.
	Token string
	// AuditPath enables auditing; empty disables it.
	AuditPath string

	lim *limiter
}

type apiError struct {
	status  int
	message string
}

func (e apiError) Error() string { return e.message }

func findTask(ledger core.Ledger, id string) (core.Task, bool) {
	for _, task := range ledger.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return core.Task{}, false
}

// CheckAudit checks startup writability so serving does not begin with a
// known unavailable audit destination. It does not guarantee later writes.
func (s *Server) CheckAudit() error {
	if s.AuditPath == "" {
		return nil
	}
	return (&auditor{path: s.AuditPath}).probe()
}

// Handler returns the route table behind the auth gate, with audit
// recording mutations and denials when AuditPath is set.
func (s *Server) Handler() http.Handler {
	if s.Token != "" && s.lim == nil {
		s.lim = newLimiter(rateMaxFails, rateWindow, rateLockout)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ledger", s.handleLedger)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/dispatches", s.handleDispatches)
	mux.HandleFunc("GET /api/dispatches/{id}/instruction", s.handleDispatchInstruction)
	mux.HandleFunc("/api/evidence", s.handleEvidence)
	mux.HandleFunc("/api/reviews", s.handleReviews)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})
	h = (&auditor{path: s.AuditPath}).withAudit(s, h)
	return h
}

func (s *Server) handleDispatchInstruction(w http.ResponseWriter, r *http.Request) {
	if s.WorkspacePath == "" {
		writeErr(w, http.StatusNotFound, "dispatch instruction not available")
		return
	}
	db, err := store.Open(s.WorkspacePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load dispatch instruction")
		return
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load dispatch instruction")
		return
	}
	dispatch, ok := state.Graph.GraphDispatch(r.PathValue("id"))
	if !state.HasGraph || !ok {
		writeErr(w, http.StatusNotFound, "dispatch instruction not found")
		return
	}
	writeJSON(w, map[string]any{
		"instruction":     dispatch.Instruction,
		"instructionHash": dispatch.InstructionHash,
	})
}

// Share the comparison with audit reporting so authentication and
// the recorded verdict cannot apply different token checks.
func validBearer(r *http.Request, token string) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// Missing and incorrect credentials receive the same response to
// avoid distinguishing authentication failure cases.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	ip := keyOf(r)
	if !s.lim.allow(ip, time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "locked out: too many failed logins")
		return false
	}
	if !validBearer(r, s.Token) {
		s.lim.fail(ip, time.Now())
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	s.lim.reset(ip)
	return true
}

func (s *Server) load(w http.ResponseWriter) (core.Ledger, bool) {
	if s.WorkspacePath != "" {
		db, err := store.Open(s.WorkspacePath)
		if err == nil {
			defer db.Close()
			state, loadErr := db.LoadWorkspace()
			if loadErr == nil {
				return state.Ledger, true
			}
			err = loadErr
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot load ledger")
			return core.Ledger{}, false
		}
	}
	l, err := core.LoadLedger(s.StorePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load ledger")
		return core.Ledger{}, false
	}
	return l, true
}

func (s *Server) save(w http.ResponseWriter, l core.Ledger) bool {
	if s.WorkspacePath != "" {
		db, err := store.Open(s.WorkspacePath)
		if err == nil {
			defer db.Close()
			err = db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Ledger = l
				return nil
			})
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot save ledger")
			return false
		}
		return true
	}
	if err := l.Save(s.StorePath); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot save ledger")
		return false
	}
	return true
}

func (s *Server) updateWorkspace(update func(*store.Workspace) error) error {
	db, err := store.Open(s.WorkspacePath)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.UpdateWorkspace(update)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request) {
	if !guardGet(w, r) {
		return
	}
	l, ok := s.load(w)
	if !ok {
		return
	}
	writeJSON(w, l)
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTasks(w, r)
	case http.MethodPost:
		s.createTask(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	l, ok := s.load(w)
	if !ok {
		return
	}
	repo := r.URL.Query().Get("repo")
	tasks := make([]core.Task, 0)
	for _, t := range l.Tasks {
		if repo == "" || t.Repo == repo {
			tasks = append(tasks, t)
		}
	}
	writeJSON(w, tasks)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
		Repo  string `json:"repo"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Title == "" {
		writeErr(w, http.StatusBadRequest, "title is required")
		return
	}
	if err := core.CheckRepo(body.Repo); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t := core.Task{ID: newID("t"), Title: body.Title, Repo: body.Repo, CreatedAt: time.Now().UTC()}
	if s.WorkspacePath != "" {
		if err := s.updateWorkspace(func(state *store.Workspace) error {
			state.Ledger.Tasks = append(state.Ledger.Tasks, t)
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot save ledger")
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, t)
		return
	}
	l, ok := s.load(w)
	if !ok {
		return
	}
	l.Tasks = append(l.Tasks, t)
	if !s.save(w, l) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t)
}

func (s *Server) handleDispatches(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listDispatches(w, r)
	case http.MethodPost:
		s.createDispatch(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST only")
	}
}

func (s *Server) listDispatches(w http.ResponseWriter, r *http.Request) {
	l, ok := s.load(w)
	if !ok {
		return
	}
	repo := r.URL.Query().Get("repo")
	ds := make([]core.Dispatch, 0)
	for _, d := range l.Dispatches {
		if repo == "" || d.Repo == repo {
			ds = append(ds, d)
		}
	}
	writeJSON(w, ds)
}

func (s *Server) createDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task               string `json:"task"`
		Repo               string `json:"repo"`
		Relationship       string `json:"relationship"`
		RelatedDispatchID  string `json:"relatedDispatchId"`
		RelationshipReason string `json:"relationshipReason"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Task == "" {
		writeErr(w, http.StatusBadRequest, "task is required")
		return
	}
	if err := core.CheckRepo(body.Repo); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.WorkspacePath != "" {
		d := core.Dispatch{ID: newID("d"), TaskID: body.Task, Repo: body.Repo, Active: true, CreatedAt: time.Now().UTC()}
		err := s.updateWorkspace(func(state *store.Workspace) error {
			selected, found := findTask(state.Ledger, body.Task)
			if !found {
				return apiError{status: http.StatusNotFound, message: fmt.Sprintf("unknown task %q", body.Task)}
			}
			var refs []string
			if state.HasGraph {
				refs, _ = state.Graph.TaskMemoryRefs(body.Task)
			}
			if err := memory.CheckDispatch(s.StorePath, selected, body.Repo, refs...); err != nil {
				return apiError{status: http.StatusConflict, message: err.Error()}
			}
			if selected.Repo != body.Repo {
				return apiError{status: http.StatusConflict, message: "dispatch: repository does not match task"}
			}
			if err := core.GuardActiveDispatch(state.Ledger.Dispatches); err != nil {
				return apiError{status: http.StatusConflict, message: err.Error()}
			}
			if state.HasGraph {
				if err := state.Graph.CheckExecutable(body.Task, body.Repo); err != nil {
					return apiError{status: http.StatusConflict, message: err.Error()}
				}
				relationship := core.DispatchRelationship(body.Relationship)
				if relationship == "" {
					relationship = core.RelationshipNone
				}
				memoryContext, err := memory.ResolveDispatchContext(s.StorePath, refs)
				if err != nil {
					return apiError{status: http.StatusConflict, message: err.Error()}
				}
				graphDispatch, err := core.NewGraphDispatch(state.Graph, d.ID, body.Task, body.Repo,
					relationship, body.RelatedDispatchID, body.RelationshipReason, memoryContext)
				if err != nil {
					return apiError{status: http.StatusConflict, message: err.Error()}
				}
				if err := state.Graph.IssueDispatch(graphDispatch, "http", "dispatch issued"); err != nil {
					return apiError{status: http.StatusConflict, message: err.Error()}
				}
			}
			state.Ledger.Dispatches = append(state.Ledger.Dispatches, d)
			return nil
		})
		if err != nil {
			var response apiError
			if errors.As(err, &response) {
				writeErr(w, response.status, response.message)
			} else {
				writeErr(w, http.StatusInternalServerError, "cannot save ledger")
			}
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, d)
		return
	}
	if body.Relationship != "" || body.RelatedDispatchID != "" || body.RelationshipReason != "" {
		writeErr(w, http.StatusConflict, "dispatch relationships require an initialized Task Graph")
		return
	}
	l, ok := s.load(w)
	if !ok {
		return
	}
	found := false
	var selected core.Task
	for _, t := range l.Tasks {
		if t.ID == body.Task {
			found = true
			selected = t
			break
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("unknown task %q", body.Task))
		return
	}
	if err := core.GuardActiveDispatch(l.Dispatches); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err := core.CheckGraphDispatch(s.StorePath, selected, body.Repo); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, core.ErrGraphRejected) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	if err := memory.CheckDispatch(s.StorePath, selected, body.Repo); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, memory.ErrGuardrailRejected) {
			status = http.StatusConflict
		}
		writeErr(w, status, err.Error())
		return
	}
	d := core.Dispatch{ID: newID("d"), TaskID: body.Task, Repo: body.Repo, Active: true, CreatedAt: time.Now().UTC()}
	l.Dispatches = append(l.Dispatches, d)
	if !s.save(w, l) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, d)
}

func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Task string `json:"task"`
		Repo string `json:"repo"`
		SHA  string `json:"sha"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Task == "" {
		writeErr(w, http.StatusBadRequest, "task is required")
		return
	}
	if err := core.CheckRepo(body.Repo); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ev, err := core.BindEvidence(body.Task, body.SHA)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.WorkspacePath != "" {
		if err := s.updateWorkspace(func(state *store.Workspace) error {
			state.Ledger.Evidence = append(state.Ledger.Evidence, ev)
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "cannot save ledger")
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, ev)
		return
	}
	l, ok := s.load(w)
	if !ok {
		return
	}
	l.Evidence = append(l.Evidence, ev)
	if !s.save(w, l) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, ev)
}

func (s *Server) handleEvidence(w http.ResponseWriter, r *http.Request) {
	if !guardGet(w, r) {
		return
	}
	l, ok := s.load(w)
	if !ok {
		return
	}
	task := r.URL.Query().Get("task")
	ev := make([]core.Evidence, 0)
	for _, e := range l.Evidence {
		if task == "" || e.TaskID == task {
			ev = append(ev, e)
		}
	}
	writeJSON(w, ev)
}

func guardGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "read-only: GET only")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
