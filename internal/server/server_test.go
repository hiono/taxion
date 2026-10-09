package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/tasks", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s code = %d, want 405", m, rec.Code)
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
