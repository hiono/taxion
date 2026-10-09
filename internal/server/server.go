// Package server exposes a read-only HTTP API over the CLI-owned ledger file.
//
// M2 scope: GET only, localhost only, no auth (auth is M5). The server reads
// the same JSON ledger file the CLI writes (default .taxion/ledger.json), so
// the CLI remains the single writer and this server never mutates state.
// Reads go through the core.Ledger shape, so a sqlite backend (LED-001,
// internal/store) can plug in later without changing the wire format.
//
// Fail-closed: if the ledger cannot be loaded the server answers 500 — it
// never serves an empty ledger as if that were the truth.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/hiono/taxion/internal/core"
)

// Server serves ledger reads from a JSON ledger file.
type Server struct {
	// StorePath is the CLI-owned ledger file. Read-only use.
	StorePath string
}

// Handler returns the read-only route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ledger", s.handleLedger)
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/dispatches", s.handleDispatches)
	mux.HandleFunc("/api/evidence", s.handleEvidence)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	return mux
}

func (s *Server) load(w http.ResponseWriter) (core.Ledger, bool) {
	l, err := core.LoadLedger(s.StorePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load ledger")
		return core.Ledger{}, false
	}
	return l, true
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
	if !guardGet(w, r) {
		return
	}
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

func (s *Server) handleDispatches(w http.ResponseWriter, r *http.Request) {
	if !guardGet(w, r) {
		return
	}
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

// guardGet rejects non-GET methods. Read-only by construction.
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
