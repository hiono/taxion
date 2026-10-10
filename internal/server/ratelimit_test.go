package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func TestLimiterUnit(t *testing.T) {
	l := newLimiter(3, time.Minute, 5*time.Minute)
	now := time.Now()
	if !l.allow("1.2.3.4", now) {
		t.Fatal("fresh IP must be allowed")
	}
	l.fail("1.2.3.4", now)
	l.fail("1.2.3.4", now.Add(time.Second))
	if !l.allow("1.2.3.4", now.Add(2*time.Second)) {
		t.Fatal("below threshold must stay allowed")
	}
	l.fail("1.2.3.4", now.Add(3*time.Second))
	if l.allow("1.2.3.4", now.Add(4*time.Second)) {
		t.Fatal("at threshold the IP must lock out")
	}
	// Other IPs are unaffected.
	if !l.allow("5.6.7.8", now.Add(4*time.Second)) {
		t.Fatal("lockout must be per-IP")
	}
	// Lockout expires.
	if !l.allow("1.2.3.4", now.Add(6*time.Minute)) {
		t.Fatal("lockout must expire")
	}
	// Old failures slide out of the window.
	l2 := newLimiter(2, time.Minute, 5*time.Minute)
	l2.fail("9.9.9.9", now)
	l2.fail("9.9.9.9", now.Add(2*time.Minute))
	if !l2.allow("9.9.9.9", now.Add(2*time.Minute)) {
		t.Fatal("stale failures must not lock out")
	}
	// One success clears typos.
	l3 := newLimiter(2, time.Minute, 5*time.Minute)
	l3.fail("7.7.7.7", now)
	l3.reset("7.7.7.7")
	l3.fail("7.7.7.7", now.Add(time.Second))
	if !l3.allow("7.7.7.7", now.Add(2*time.Second)) {
		t.Fatal("reset must clear the counter")
	}
}

func TestKeyOfIgnoresForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ledger", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "1.1.1.1")
	if got := keyOf(req); got != "10.0.0.5" {
		t.Fatalf("key must be the connection host, got %q", got)
	}
}

// TestLockoutIntegration checks authorization: repeated bad tokens trigger 429, the
// audit log names the lockout, and one success resets the counter.
func TestLockoutIntegration(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.json")
	if err := (core.Ledger{}).Save(ledger); err != nil {
		t.Fatal(err)
	}
	audit := filepath.Join(dir, "audit.jsonl")
	srv := &Server{StorePath: ledger, Token: "secret", AuditPath: audit}
	srv.lim = newLimiter(3, time.Minute, 5*time.Minute)
	h := srv.Handler()

	bad := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/ledger", nil)
		req.RemoteAddr = "192.0.2.9:9999"
		req.Header.Set("Authorization", "Bearer wrong")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	good := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/ledger", nil)
		req.RemoteAddr = "192.0.2.9:9999"
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if code := bad().Code; code != http.StatusUnauthorized {
		t.Fatalf("1st bad = %d, want 401", code)
	}
	if code := bad().Code; code != http.StatusUnauthorized {
		t.Fatalf("2nd bad = %d, want 401", code)
	}
	// A success between failures resets the counter — no lockout.
	if code := good().Code; code != http.StatusOK {
		t.Fatalf("good = %d, want 200", code)
	}
	if code := bad().Code; code != http.StatusUnauthorized {
		t.Fatalf("post-success bad = %d, want 401 (counter reset)", code)
	}
	if code := bad().Code; code != http.StatusUnauthorized {
		t.Fatalf("bad = %d, want 401", code)
	}
	// The 3rd consecutive failure trips the lock but answers 401 itself;
	// the lockout applies from the next request on.
	if code := bad().Code; code != http.StatusUnauthorized {
		t.Fatalf("lock-tripping bad = %d, want 401", code)
	}
	if code := bad().Code; code != http.StatusTooManyRequests {
		t.Fatalf("post-lock bad = %d, want 429", code)
	}
	// Locked out: even the right token is refused while locked.
	if code := good().Code; code != http.StatusTooManyRequests {
		t.Fatalf("good-while-locked = %d, want 429", code)
	}

	data, err := os.ReadFile(audit)
	if err != nil {
		t.Fatal(err)
	}
	var sawLockout, sawPlain401 bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e["status"] == float64(429) {
			sawLockout = true
			if e["reason"] != "lockout" {
				t.Fatalf("429 line must carry reason=lockout, got %v", e)
			}
		}
		if e["status"] == float64(401) {
			sawPlain401 = true
			if _, ok := e["reason"]; ok {
				t.Fatalf("plain 401 must carry no reason, got %v", e)
			}
		}
	}
	if !sawLockout || !sawPlain401 {
		t.Fatalf("want 401 + 429 lines in audit, got:\n%s", data)
	}
}
