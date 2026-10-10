package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func seedAudit(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := (core.Ledger{}).Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func doReq(h http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestAuditLogsMutation(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{StorePath: seedAudit(t), AuditPath: filepath.Join(dir, "audit.jsonl")}
	h := srv.Handler()
	rec := doReq(h, http.MethodPost, "/api/tasks", `{"title":"secret-title","repo":"demo"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d", rec.Code)
	}
	lines := readLines(t, srv.AuditPath)
	if len(lines) != 1 {
		t.Fatalf("want 1 audit line, got %d", len(lines))
	}
	e := lines[0]
	if e["method"] != "POST" || e["path"] != "/api/tasks" || e["repo"] != "demo" {
		t.Fatalf("fields mismatch: %v", e)
	}
	if e["status"] != float64(201) || e["authed"] != false {
		t.Fatalf("outcome mismatch: %v", e)
	}
	if _, ok := e["ts"].(string); !ok {
		t.Fatalf("missing ts: %v", e)
	}
	raw, _ := os.ReadFile(srv.AuditPath)
	if strings.Contains(string(raw), "secret-title") {
		t.Fatal("audit log must never contain request bodies")
	}
}

func TestAuditLogsDenialAndMarksAuthed(t *testing.T) {
	dir := t.TempDir()
	srv := &Server{StorePath: seedAudit(t), Token: "tok", AuditPath: filepath.Join(dir, "audit.jsonl")}
	h := srv.Handler()
	// Wrong token on a read: denial, authed false.
	rec := doReq(h, http.MethodGet, "/api/tasks", "", "wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
	// Valid token mutation: authed true.
	rec = doReq(h, http.MethodPost, "/api/tasks", `{"title":"x","repo":"demo"}`, "tok")
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d", rec.Code)
	}
	lines := readLines(t, srv.AuditPath)
	if len(lines) != 2 {
		t.Fatalf("want 2 audit lines, got %v", lines)
	}
	if lines[0]["status"] != float64(401) || lines[0]["authed"] != false {
		t.Fatalf("denial mismatch: %v", lines[0])
	}
	if lines[1]["status"] != float64(201) || lines[1]["authed"] != true {
		t.Fatalf("authed mutation mismatch: %v", lines[1])
	}
	raw, _ := os.ReadFile(srv.AuditPath)
	if strings.Contains(string(raw), "tok") {
		t.Fatal("audit log must never contain the token")
	}
}

func TestAuditSkipsSuccessfulReads(t *testing.T) {
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.jsonl")
	srv := &Server{StorePath: seedAudit(t), AuditPath: audit}
	rec := doReq(srv.Handler(), http.MethodGet, "/api/tasks", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if _, err := os.Stat(audit); !os.IsNotExist(err) {
		t.Fatal("successful reads must not create audit output")
	}
}

func TestAuditDisabled(t *testing.T) {
	srv := &Server{StorePath: seedAudit(t)}
	rec := doReq(srv.Handler(), http.MethodPost, "/api/tasks", `{"title":"x","repo":"demo"}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d", rec.Code)
	}
	if err := srv.CheckAudit(); err != nil {
		t.Fatalf("disabled audit must probe clean: %v", err)
	}
}

func TestCheckAuditFailsFast(t *testing.T) {
	srv := &Server{StorePath: seedAudit(t), AuditPath: filepath.Join(t.TempDir(), "no-such-dir", "audit.jsonl")}
	if err := srv.CheckAudit(); err == nil {
		t.Fatal("unwritable audit destination must fail fast")
	}
}
