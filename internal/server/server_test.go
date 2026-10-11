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
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
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

func TestDispatchTaskGraphGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	task := core.Task{ID: "t", Title: "task", Repo: "owner/repo"}
	if err := (core.Ledger{Tasks: []core.Task{task}}).Save(path); err != nil {
		t.Fatal(err)
	}
	g := core.TaskGraph{Version: 1}
	gt := core.GraphTask{ID: task.ID, Title: task.Title, Repo: task.Repo, Type: "IMPLEMENTATION",
		Status: core.Draft, Issue: "Issue #47", CreatedFrom: "request"}
	if err := g.Track(gt, "human", "track"); err != nil {
		t.Fatal(err)
	}
	if err := g.Save(core.GraphPath(path)); err != nil {
		t.Fatal(err)
	}
	h := (&Server{StorePath: path}).Handler()
	rec := post(t, h, "/api/dispatches", `{"task":"t","repo":"owner/repo"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("graph gate: %d %s", rec.Code, rec.Body.String())
	}
	l, err := core.LoadLedger(path)
	if err != nil || len(l.Dispatches) != 0 {
		t.Fatal("rejected dispatch saved", err)
	}
	if err := os.WriteFile(core.GraphPath(path), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	rec = post(t, h, "/api/dispatches", `{"task":"t","repo":"owner/repo"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken graph: %d", rec.Code)
	}
}

func TestWorkspaceDispatchInstructionEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	workspace := filepath.Join(dir, "workspace.db")
	task := core.Task{ID: "t", Title: "task", Repo: "owner/repo", CreatedAt: time.Now().UTC()}
	graph := core.TaskGraph{Version: 1}
	graphTask := core.GraphTask{
		ID: task.ID, Title: task.Title, Repo: task.Repo, Type: "IMPLEMENTATION", Issue: "Issue #47",
		Status: core.Draft, Owner: "CODER", CreatedFrom: "request:1", Objective: "implement",
		Scope: []string{"src"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
		ValidationCommands: []string{"go test ./..."},
		ExitCondition:      "report", Worktree: "/work/tree", Branch: "feature", BaselineSHA: strings.Repeat("a", 40),
		AuthorizationID: "decision:1", Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "approved"}},
	}
	if err := graph.Track(graphTask, "human", "register"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{
		Ledger: core.Ledger{Tasks: []core.Task{task}, Dispatches: []core.Dispatch{}, Evidence: []core.Evidence{}, Checkpoints: []core.Checkpoint{}},
		Graph:  graph, HasGraph: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{StorePath: path, WorkspacePath: workspace}).Handler()
	ready := get(t, handler, "/api/tasks/plan?repo=owner/repo")
	var readyPlan controller.FollowUpPlan
	if err := json.Unmarshal(ready.Body.Bytes(), &readyPlan); err != nil {
		t.Fatal(err)
	}
	if ready.Code != http.StatusOK || len(readyPlan.Tasks) != 1 ||
		readyPlan.Tasks[0].Action != "DISPATCH" || len(readyPlan.Tasks[0].Gaps) != 0 ||
		readyPlan.Tasks[0].Task.AuthorizationID != graphTask.AuthorizationID ||
		readyPlan.Observation == nil || readyPlan.Observation.StartedAt.IsZero() ||
		readyPlan.Observation.CompletedAt.Before(readyPlan.Observation.StartedAt) ||
		!strings.HasPrefix(readyPlan.Observation.ContentHash, "sha256:") {
		t.Fatalf("authorized contract not available for follow-up: %d %+v", ready.Code, readyPlan)
	}
	created := post(t, handler, "/api/dispatches", `{"task":"t","repo":"owner/repo"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("dispatch: %d %s", created.Code, created.Body.String())
	}
	var dispatch core.Dispatch
	if err := json.Unmarshal(created.Body.Bytes(), &dispatch); err != nil {
		t.Fatal(err)
	}
	response := get(t, handler, "/api/dispatches/"+dispatch.ID+"/instruction")
	if response.Code != http.StatusOK {
		t.Fatalf("instruction: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Instruction core.DispatchInstruction `json:"instruction"`
		Hash        string                   `json:"instructionHash"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Instruction.DispatchID != dispatch.ID || body.Instruction.Worktree != "/work/tree" || body.Hash == "" {
		t.Fatalf("incomplete instruction response: %+v", body)
	}
	response = get(t, handler, "/api/tasks/plan?repo=owner/repo")
	var plan controller.FollowUpPlan
	if response.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Repository != task.Repo || len(plan.Tasks) != 1 || plan.Tasks[0].Task.ID != task.ID ||
		plan.Tasks[0].Action != "RECONCILE_DISPATCH" || plan.Tasks[0].Dispatches[0].ID != dispatch.ID ||
		plan.Observation == nil || plan.Observation.ContentHash == readyPlan.Observation.ContentHash {
		t.Fatalf("incomplete planning response: %+v", plan)
	}
	for _, test := range []struct {
		target string
		status int
	}{
		{"/api/tasks/plan", http.StatusBadRequest},
		{"/api/tasks/plan?repo=missing/repo", http.StatusConflict},
	} {
		if response := get(t, handler, test.target); response.Code != test.status {
			t.Fatalf("%s: %d %s", test.target, response.Code, response.Body.String())
		}
	}
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
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/ledger", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/ledger code = %d, want 405", m, rec.Code)
		}
	}
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
	got := get(t, h, "/api/tasks")
	var tasks []core.Task
	if err := json.Unmarshal(got.Body.Bytes(), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("task not persisted: %+v", tasks)
	}
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
	path, _ := seed(t)
	h := (&Server{StorePath: path}).Handler()
	if rec := post(t, h, "/api/dispatches", `{"task":"nope","repo":"r-a"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown task: code = %d, want 404", rec.Code)
	}
	if rec := post(t, h, "/api/dispatches", `{"task":"t-2"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing repo: code = %d, want 400", rec.Code)
	}
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
	if rec := post(t, h, "/api/reviews", `{"task":"t-1","repo":"r-a","sha":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty sha: code = %d, want 400", rec.Code)
	}
	if rec := post(t, h, "/api/reviews", `{"task":"t-1","sha":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing repo: code = %d, want 400", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/reviews", nil)
	grec := httptest.NewRecorder()
	h.ServeHTTP(grec, req)
	if grec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/reviews code = %d, want 405", grec.Code)
	}
}

func authed(t *testing.T, h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthGate(t *testing.T) {
	path, _ := seed(t)
	h := (&Server{StorePath: path, Token: "s3cret"}).Handler()
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/ledger", ""},
		{http.MethodGet, "/api/tasks", ""},
		{http.MethodPost, "/api/tasks", `{"title":"x","repo":"r-a"}`},
		{http.MethodPost, "/api/reviews", `{"task":"t-1","repo":"r-a","sha":"abc"}`},
	} {
		if rec := authed(t, h, tc.method, tc.target, "", tc.body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token = %d, want 401", tc.method, tc.target, rec.Code)
		}
		rec := authed(t, h, tc.method, tc.target, "wrong", tc.body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s wrong token = %d, want 401", tc.method, tc.target, rec.Code)
		}
		var e map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e["error"] != "unauthorized" {
			t.Fatalf("%s %s wrong-token body = %q, want identical 401", tc.method, tc.target, rec.Body.String())
		}
		if rec := authed(t, h, tc.method, tc.target, "s3cret", tc.body); rec.Code >= 400 && rec.Code != http.StatusConflict {
			t.Fatalf("%s %s correct token = %d, want success", tc.method, tc.target, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/ledger", nil)
	req.Header.Set("Authorization", "Basic c3M=")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("basic auth = %d, want 401", rec.Code)
	}
}

func TestNoTokenKeepsLegacyBehavior(t *testing.T) {
	path, _ := seed(t)
	h := (&Server{StorePath: path}).Handler()
	if rec := authed(t, h, http.MethodGet, "/api/ledger", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("no-token GET = %d, want 200", rec.Code)
	}
}

func TestDispatchMemoryDatabaseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l := core.Ledger{Tasks: []core.Task{{ID: "t", Title: "task", Repo: "repo"}}}
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memory.PathForLedger(path), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := post(t, (&Server{StorePath: path}).Handler(), "/api/dispatches", `{"task":"t","repo":"repo"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure must be 500, not policy denial/success: %d %s", rec.Code, rec.Body.String())
	}
	got, err := core.LoadLedger(path)
	if err != nil || len(got.Dispatches) != 0 {
		t.Fatalf("storage failure changed ledger: %+v %v", got, err)
	}
}

func TestDispatchMemoryPolicy(t *testing.T) {
	for _, kind := range []string{memory.CheckRepoEquals, memory.CheckTaskTitleMatches} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ledger.json")
			l := core.Ledger{Tasks: []core.Task{
				{ID: "good", Title: "fix: safe", Repo: "target"},
				{ID: "bad", Title: "unsafe", Repo: "target"},
			}}
			if err := l.Save(path); err != nil {
				t.Fatal(err)
			}
			s, err := memory.Open(memory.PathForLedger(path))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.SetPromoteAfter(1); err != nil {
				t.Fatal(err)
			}
			_, candidate, err := s.Warn("check dispatch", memory.ScopeProject)
			if err != nil {
				t.Fatal(err)
			}
			value := "target"
			if kind == memory.CheckTaskTitleMatches {
				value = "^fix:"
			}
			if _, err := s.Approve(candidate, kind, value); err != nil {
				t.Fatal(err)
			}
			h := (&Server{StorePath: path}).Handler()
			bad := `{"task":"bad","repo":"other"}`
			if rec := post(t, h, "/api/dispatches", bad); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "MEM-001") {
				t.Fatalf("policy denial: %d %s", rec.Code, rec.Body.String())
			}
			got, err := core.LoadLedger(path)
			if err != nil || len(got.Dispatches) != 0 {
				t.Fatalf("denial changed ledger: %+v %v", got, err)
			}
			if rec := post(t, h, "/api/dispatches", `{"task":"good","repo":"target"}`); rec.Code != http.StatusCreated {
				t.Fatalf("matching policy: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
