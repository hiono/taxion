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

// Retain operation metadata, not credentials or free-text request bodies.
// X-Operator is self-asserted: a shared token cannot prove caller identity.
type auditEntry struct {
	TS     string `json:"ts"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Repo   string `json:"repo,omitempty"`
	Who    string `json:"who,omitempty"`
	Status int    `json:"status"`
	Authed bool   `json:"authed"`
	Reason string `json:"reason,omitempty"`
	Prev   string `json:"prev,omitempty"`
}

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

type auditor struct {
	path string

	mu sync.Mutex
	f  *os.File
	// Protect last and the append together so each line hashes its actual
	// predecessor rather than a concurrently superseded one.
	last []byte
}

func (a *auditor) enabled() bool { return a.path != "" }

func shouldLog(r *http.Request, status int) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return status == http.StatusUnauthorized || status == http.StatusForbidden ||
		status == http.StatusTooManyRequests
}

// Restrict and cap caller-controlled labels to keep them printable and
// prevent unbounded identity fields in audit records.
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

// Restore the consumed body so audit extraction cannot change what the
// downstream handler validates.
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
			// This mapping relies on lockout being the only 429 source;
			// adding other 429 responses requires a distinct audit reason.
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
			// The response is already sent, so changing its status cannot
			// report this failure. stderr must expose the audit gap.
			_, _ = io.WriteString(os.Stderr, "taxion: audit log failed: "+err.Error()+"\n")
		}
	})
}

func authorized(r *http.Request, token string) bool {
	return validBearer(r, token)
}

// Continue from the existing tail so restarting does not create an unlinked
// genesis inside one file. Requires mu and an open a.f.
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

// Close the startup probe so the serving auditor owns the live handle and
// initializes its chain from the tail when it actually opens the log.
func (a *auditor) probe() error {
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
