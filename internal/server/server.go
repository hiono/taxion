// Package server exposes the Taxion HTTP API over the CLI-owned ledger file.
//
// M5 scope: reads (GET) plus the CLI mutation surface (POST), plus optional
// Bearer-token auth. The server is the sole writer besides the CLI; both go
// through the core.Ledger shape and the same guards (REP-001, DSP-001,
// SHA-001). No ledger keys are added (M2-3 wire freeze); POST request bodies
// are versioned separately in docs/05.
//
// Auth: when Token is set, every route requires
// `Authorization: Bearer <token>` (constant-time compare, 401 otherwise).
// When Token is empty, the server keeps the pre-M5 localhost behavior.
// Tokens travel via flag/env only — never in the ledger, logs, or URLs.
//
// Fail-closed: unreadable ledger answers 500; guard violations answer
// 400/404/409 with a JSON error — never a silent success.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// Server serves the Taxion API from a JSON ledger file.
type Server struct {
	// StorePath is the CLI-owned ledger file.
	StorePath string
	// Token enables Bearer auth when non-empty. Empty keeps pre-M5 behavior.
	Token string
	// AuditPath enables the append-only audit log (M5-2, docs/07). Empty
	// disables it. The log records mutations and auth denials only —
	// never tokens, bodies, or query strings.
	AuditPath string

	// lim is the brute-force limiter (M8-E). Built in Handler when a
	// token is configured; tests may inject a short-window one.
	lim *limiter
}

// CheckAudit fails fast when the audit destination is unwritable. Call it
// before ListenAndServe: a camera that cannot record must stop startup,
// not wave traffic through.
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

// validBearer is the single Bearer comparison behind both the authorize
// gate and the audit verdict — one comparison, no oracle drift.
func validBearer(r *http.Request, token string) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// authorize enforces the Bearer token when one is configured. Missing and
// wrong credentials get the identical 401 — no oracle for attackers.
// Repeated failures from one IP earn a 429 lockout (M8-E).
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
	l, err := core.LoadLedger(s.StorePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load ledger")
		return core.Ledger{}, false
	}
	return l, true
}

func (s *Server) save(w http.ResponseWriter, l core.Ledger) bool {
	if err := l.Save(s.StorePath); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot save ledger")
		return false
	}
	return true
}

// decode limits and parses a JSON request body.
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

// createTask mirrors `taxion task create`: title and an explicit repo
// (REP-001) are required.
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
	l, ok := s.load(w)
	if !ok {
		return
	}
	t := core.Task{ID: newID("t"), Title: body.Title, Repo: body.Repo, CreatedAt: time.Now().UTC()}
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

// createDispatch mirrors `taxion dispatch`: the task must exist, the repo is
// explicit (REP-001), and at most one dispatch may be active (DSP-001 → 409).
func (s *Server) createDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task string `json:"task"`
		Repo string `json:"repo"`
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
	l, ok := s.load(w)
	if !ok {
		return
	}
	found := false
	for _, t := range l.Tasks {
		if t.ID == body.Task {
			found = true
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
	d := core.Dispatch{ID: newID("d"), TaskID: body.Task, Repo: body.Repo, Active: true, CreatedAt: time.Now().UTC()}
	l.Dispatches = append(l.Dispatches, d)
	if !s.save(w, l) {
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, d)
}

// handleReviews creates evidence bindings (POST only). It mirrors
// `taxion review`: repo is explicit (REP-001) and the sha is required and
// kept exact (SHA-001).
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
	ev, err := core.BindEvidence(body.Task, body.SHA) // SHA-001
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
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

// guardGet rejects non-GET methods on read-only routes.
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
