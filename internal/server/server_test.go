package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func seed(t *testing.T) (string, core.Ledger) {
	t.Helper()
	l := core.Ledger{
		Tasks: []core.Task{
			{ID: "t-1", Title: "one", Repo: "r-a", CreatedAt: time.Now().UTC()},
			{ID: "t-2", Title: "two", Repo: "r-b", CreatedAt: time.Now().UTC()},
		},
		Dispatches: []core.Dispatch{
			{ID: "d-1", TaskID: "t-1", Repo: "r-a", Active: true, CreatedAt: time.Now().UTC()},
		},
		Evidence: []core.Evidence{
			{SHA: "abc123", TaskID: "t-1", CreatedAt: time.Now().UTC()},
		},
	}
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	return path, l
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func post(t *testing.T, h http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLedgerRoundTrip(t *testing.T) {
	path, want := seed(t)
	rec := get(t, (&Server{StorePath: path}).Handler(), "/api/ledger")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var got core.Ledger
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != len(want.Tasks) || len(got.Dispatches) != 1 || len(got.Evidence) != 1 {
		t.Fatalf("ledger mismatch: %+v", got)
	}
	if got.Tasks[0].ID != "t-1" || got.Dispatches[0].TaskID != "t-1" || got.Evidence[0].SHA != "abc123" {
		t.Fatalf("content mismatch: %+v", got)
	}
}

func TestRepoFilter(t *testing.T) {
	path, _ := seed(t)
	h := (&Server{StorePath: path}).Handler()
	rec := get(t, h, "/api/tasks?repo=r-a")
	var tasks []core.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t-1" {
		t.Fatalf("tasks filter mismatch: %+v", tasks)
	}
	rec = get(t, h, "/api/dispatches?repo=r-b")
	var ds []core.Dispatch
	if err := json.Unmarshal(rec.Body.Bytes(), &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Fatalf("dispatches filter mismatch: %+v", ds)
	}
}

func TestEvidenceTaskFilter(t *testing.T) {
	path, _ := seed(t)
	h := (&Server{StorePath: path}).Handler()
	rec := get(t, h, "/api/evidence?task=t-1")
	var ev []core.Evidence
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 || ev[0].SHA != "abc123" {
		t.Fatalf("evidence filter mismatch: %+v", ev)
	}
}

func TestReadOnlyAndNotFound(t *testing.T) {
	path, _ := seed(t)
	h := (&Server{StorePath: path}).Handler()
	// GET-only routes reject every mutation with 405.
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/ledger", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/ledger code = %d, want 405", m, rec.Code)
		}
	}
	// Write routes reject non-GET/POST with 405; empty POST is 400 (bad body).
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/tasks", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/tasks code = %d, want 405", m, rec.Code)
		}
	}
	rec := get(t, h, "/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route code = %d, want 404", rec.Code)
	}
}

func TestMissingLedgerFailsClosed(t *testing.T) {
	h := (&Server{StorePath: filepath.Join(t.TempDir(), "absent.json")}).Handler()
	rec := get(t, h, "/api/ledger")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500 (never serve empty as truth)", rec.Code)
	}
}

func TestCreateTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := (core.Ledger{}).Save(path); err != nil {
		t.Fatal(err)
	}
	h := (&Server{StorePath: path}).Handler()
	rec := post(t, h, "/api/tasks", `{"title":"via api","repo":"r-a"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created core.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "via api" || created.Repo != "r-a" || created.ID == "" {
		t.Fatalf("created task mismatch: %+v", created)
	}
	// Persisted: visible via GET.
	got := get(t, h, "/api/tasks")
	var tasks []core.Task
	if err := json.Unmarshal(got.Body.Bytes(), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("task not persisted: %+v", tasks)
	}
	// Guards: missing title and missing/invalid repo are 400.
	for _, body := range []string{`{"repo":"r-a"}`, `{"title":"x"}`, `{"title":"x","repo":""}`} {
		if rec := post(t, h, "/api/tasks", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: code = %d, want 400", body, rec.Code)
		}
	}
	if rec := post(t, h, "/api/tasks", `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: code = %d, want 400", rec.Code)
	}
}

func TestCreateDispatchGuards(t *testing.T) {
	path, _ := seed(t) // t-1/r-a, t-2/r-b, one active dispatch on t-1
	h := (&Server{StorePath: path}).Handler()
	// Unknown task → 404.
	if rec := post(t, h, "/api/dispatches", `{"task":"nope","repo":"r-a"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: code = %d, want 404", rec.Code)
	}
	// Missing repo → 400 (REP-001).
	if rec := post(t, h, "/api/dispatches", `{"task":"t-2"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing repo: code = %d, want 400", rec.Code)
	}
	// Active dispatch exists → 409 (DSP-001).
	if rec := post(t, h, "/api/dispatches", `{"task":"t-2","repo":"r-b"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second dispatch: code = %d, want 409", rec.Code)
	}
}

func TestCreateReview(t *testing.T) {
	path, _ := seed(t)
	h := (&Server{StorePath: path}).Handler()
	rec := post(t, h, "/api/reviews", `{"task":"t-1","repo":"r-a","sha":"beef01"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var ev core.Evidence
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.SHA != "beef01" || ev.TaskID != "t-1" {
		t.Fatalf("evidence mismatch: %+v", ev)
	}
	// SHA-001: empty sha is 400; missing repo is 400.
	if rec := post(t, h, "/api/reviews", `{"task":"t-1","repo":"r-a","sha":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty sha: code = %d, want 400", rec.Code)
	}
	if rec := post(t, h, "/api/reviews", `{"task":"t-1","sha":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing repo: code = %d, want 400", rec.Code)
	}
	// Reviews route is POST only.
	req := httptest.NewRequest(http.MethodGet, "/api/reviews", nil)
	grec := httptest.NewRecorder()
	h.ServeHTTP(grec, req)
	if grec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/reviews code = %d, want 405", grec.Code)
	}
}
