package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// auditEntry is one JSONL line in the audit log. It records WHAT was done
// and WHO claimed to do it, never secrets: no tokens, no Authorization
// header, no request body (titles, SHAs), no query string. Who is a
// self-asserted operator label (X-Operator), not an identity proof — under a
// single shared token it distinguishes callers by claim, not by credential.
type auditEntry struct {
	TS     string `json:"ts"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Repo   string `json:"repo,omitempty"`
	Who    string `json:"who,omitempty"`
	Status int    `json:"status"`
	Authed bool   `json:"authed"`
	// Reason names WHY a denial happened (M8-E): "lockout" for 429s.
	// Successes and ordinary 401s carry no reason.
	Reason string `json:"reason,omitempty"`
	// Prev links this line to the previous one (M8-G hash chain): hex
	// SHA-256 of the exact previous line bytes. Omitted on genesis.
	Prev string `json:"prev,omitempty"`
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
// M8-G: it also maintains the hash chain — last is the hash of the most
// recently written line (nil = genesis so far), guarded by the same mutex.
type auditor struct {
	path string

	mu   sync.Mutex
	f    *os.File
	last []byte
}

func (a *auditor) enabled() bool { return a.path != "" }

// shouldLog covers the mutation surface plus every auth denial (401/403)
// and rate-limit lockout (429) — successful reads are noise and stay out.
func shouldLog(r *http.Request, status int) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return status == http.StatusUnauthorized || status == http.StatusForbidden ||
		status == http.StatusTooManyRequests
}

// whoOf extracts the self-asserted operator label. It is scrubbed to a
// safe alphabet and capped: a log line must stay one clean JSON line, and
// a hostile label must not smuggle control characters or megabytes in.
func whoOf(r *http.Request) string {
	const maxWho = 64
	w := strings.TrimSpace(r.Header.Get("X-Operator"))
	if w == "" {
		return ""
	}
	var b strings.Builder
	for _, c := range w {
		if c < 0x20 || c == 0x7f || c > 0x7e {
			continue
		}
		if b.Len() >= maxWho {
			break
		}
		b.WriteRune(c)
	}
	return b.String()
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
// M8-G: the entry links onto the previous line before writing.
func (a *auditor) log(e auditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		a.f = f
		if err := a.initChainLocked(); err != nil {
			return err
		}
	}
	if a.last != nil {
		e.Prev = hex.EncodeToString(a.last)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err = a.f.Write(data); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	a.last = sum[:]
	return nil
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
		reason := ""
		if status == http.StatusTooManyRequests {
			// Only the authorize lockout path produces 429, so the
			// mapping is exact — no other handler may use 429.
			reason = "lockout"
		}
		if err := a.log(auditEntry{
			TS:     time.Now().UTC().Format(time.RFC3339Nano),
			Method: r.Method,
			Path:   r.URL.Path,
			Repo:   repo,
			Who:    whoOf(r),
			Status: status,
			Authed: authed,
			Reason: reason,
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

// initChainLocked continues the hash chain from the existing tail.
// Call with mu held, after a.f is open. Missing/empty file = genesis.
func (a *auditor) initChainLocked() error {
	if a.last != nil {
		return nil
	}
	h, err := tailHash(a.path)
	if err != nil {
		return err
	}
	if h == "" {
		return nil
	}
	raw, err := hex.DecodeString(h)
	if err != nil {
		return err
	}
	a.last = raw
	return nil
}

// open eagerly creates the log file so serve fails fast when the audit
// destination is unwritable — before serving a single request. It also
// continues the hash chain from the existing tail (no second genesis).
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
	return a.initChainLocked()
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
