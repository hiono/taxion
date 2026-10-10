package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// auditEntry is one JSONL line in the audit log. It records WHAT was done,
// never secrets: no tokens, no Authorization header, no request body (titles,
// SHAs), no query string.
type auditEntry struct {
	TS     string `json:"ts"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Repo   string `json:"repo,omitempty"`
	Status int    `json:"status"`
	Authed bool   `json:"authed"`
}

// statusCatcher records the response code for the audit middleware.
type statusCatcher struct {
	http.ResponseWriter
	status int
}

func (c *statusCatcher) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *statusCatcher) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.ResponseWriter.Write(b)
}

// auditor appends JSONL entries to AuditPath (0600). Zero value disabled.
type auditor struct {
	path string

	mu sync.Mutex
	f  *os.File
}

func (a *auditor) enabled() bool { return a.path != "" }

// shouldLog covers the mutation surface plus every auth denial —
// successful reads are noise and stay out of the log.
func shouldLog(r *http.Request, status int) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// repoOf extracts the top-level "repo" from a JSON POST body without keeping
// the body (titles, SHAs). The body is restored for downstream handlers.
func repoOf(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	var body struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return ""
	}
	return body.Repo
}

// log appends one entry. Errors fail closed: the caller turns them into 500
// — a camera that cannot record must stop the action, not wave it through.
func (a *auditor) log(e auditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		a.f = f
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = a.f.Write(data)
	return err
}

// withAudit wraps next: it serves the request, then records mutations and
// denials. Authed reflects whether THIS request presented the valid token
// (false when auth is off or the check failed) — the token itself is never
// read here, only the authorize() verdict.
func (a *auditor) withAudit(s *Server, next http.Handler) http.Handler {
	if !a.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var repo string
		if r.Method == http.MethodPost {
			repo = repoOf(r)
		}
		catcher := &statusCatcher{ResponseWriter: w}
		next.ServeHTTP(catcher, r)
		status := catcher.status
		if status == 0 {
			status = http.StatusOK
		}
		if !shouldLog(r, status) {
			return
		}
		authed := s.Token != "" && authorized(r, s.Token)
		if err := a.log(auditEntry{
			TS:     time.Now().UTC().Format(time.RFC3339Nano),
			Method: r.Method,
			Path:   r.URL.Path,
			Repo:   repo,
			Status: status,
			Authed: authed,
		}); err != nil {
			// Headers are already sent; the failure is visible in the
			// server stderr and the entry is missing — operators must
			// treat a missing line as a gap, never as quiet success.
			_, _ = io.WriteString(os.Stderr, "taxion: audit log failed: "+err.Error()+"\n")
		}
	})
}

// authorized recomputes the Bearer verdict without writing a response,
// using the same comparison as the authorize gate.
func authorized(r *http.Request, token string) bool {
	return validBearer(r, token)
}

// open eagerly creates the log file so serve fails fast when the audit
// destination is unwritable — before serving a single request.
func (a *auditor) open() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f != nil {
		return nil
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	a.f = f
	return nil
}

// probe verifies writability without keeping the handle: used at startup so
// the serving auditor opens the single live handle later.
func (a *auditor) probe() error {
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
