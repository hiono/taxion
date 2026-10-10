package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func candidate(t *testing.T, s *Store, scope string) int64 {
	t.Helper()
	var id int64
	for i := 0; i < PromoteAfter; i++ {
		_, lid, err := s.Warn("review the dispatch target", scope)
		if err != nil {
			t.Fatal(err)
		}
		id = lid
	}
	return id
}

func TestExistingCandidateMigration(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	db, err := sql.Open("sqlite", PathForLedger(ledger))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE lessons (
		id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
		body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
		status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL);
		CREATE TABLE warnings (
		id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL,
		scope TEXT NOT NULL DEFAULT 'project', count INTEGER NOT NULL DEFAULT 1,
		lesson_id INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
		CREATE UNIQUE INDEX idx_warnings_unique ON warnings(body,scope);`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO lessons(id,title,body,scope,status,created_at)
		VALUES(7,'existing candidate','old warning','project','proposed','2026-01-01T00:00:00Z');
		INSERT INTO warnings(body,scope,count,lesson_id,created_at,updated_at)
		VALUES('old warning','project',3,7,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(PathForLedger(ledger))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	candidate, err := s.Get(KindLesson, 7)
	if err != nil || len(candidate.SourceRefs) != 1 {
		t.Fatalf("legacy candidate source was not migrated: %+v, %v", candidate, err)
	}
	sourceID, err := strconv.ParseInt(strings.TrimPrefix(candidate.SourceRefs[0], "warning:"), 10, 64)
	if err != nil {
		t.Fatalf("invalid migrated source reference: %+v", candidate.SourceRefs)
	}
	source, err := s.WarningOccurrence(sourceID)
	if err != nil || source.OccurrenceCount != 3 || source.SourceRef != "legacy:aggregate-count-only" {
		t.Fatalf("legacy aggregate source was not preserved honestly: %+v, %v", source, err)
	}
	if n, id, err := s.Warn("old warning", ScopeProject); err != nil || n != 4 || id != 7 {
		t.Fatalf("migration lost existing count/candidate: %d %d %v", n, id, err)
	}
	if _, err := s.Approve(7, CheckRepoEquals, "target"); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatch(ledger, core.Task{}, "target"); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurablePromotionPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPromoteAfter(0); err == nil {
		t.Fatal("zero threshold must fail")
	}
	if err := s.SetPromoteAfter(-1); err == nil {
		t.Fatal("negative threshold must fail")
	}
	if err := s.SetPromoteAfter(2); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, id, err := s.Warn("w", ScopeProject); err != nil || n != 1 || id != 0 {
		t.Fatalf("before configured threshold: %d %d %v", n, id, err)
	}
	_, id, err := s.Warn("w", ScopeProject)
	if err != nil || id == 0 {
		t.Fatalf("configured threshold did not promote: %d %v", id, err)
	}
	if err := s.SetPromoteAfter(9); err != nil {
		t.Fatal(err)
	}
	if _, next, err := s.Warn("w", ScopeProject); err != nil || next != id {
		t.Fatalf("changing threshold replaced candidate: %d %v", next, err)
	}
	if err := s.SetPromoteAfter(1); err != nil {
		t.Fatal(err)
	}
	if _, next, err := s.Warn("new", ScopeGlobal); err != nil || next == 0 {
		t.Fatalf("threshold one must promote on first warning: %d %v", next, err)
	}
}

func TestApprovalAndDispatchChecks(t *testing.T) {
	for _, scope := range []string{ScopeProject, ScopeGlobal} {
		for _, kind := range []string{CheckRepoEquals, CheckTaskTitleMatches} {
			t.Run(scope+"/"+kind, func(t *testing.T) {
				ledger := filepath.Join(t.TempDir(), "ledger.json")
				s, err := Open(PathForLedger(ledger))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				id := candidate(t, s, scope)
				task := core.Task{Title: "fix: safe"}
				value := "target"
				if kind == CheckTaskTitleMatches {
					value = `^fix:`
				}
				if err := CheckDispatch(ledger, task, "other"); err != nil {
					t.Fatalf("unapproved candidate must not enforce: %v", err)
				}
				gid, err := s.Approve(id, kind, value)
				if err != nil {
					t.Fatal(err)
				}
				got, err := s.Get(KindGuardrail, gid)
				if err != nil || got.Scope != scope || got.Status != "approved" {
					t.Fatalf("approved guardrail: %+v %v", got, err)
				}
				lesson, err := s.Get(KindLesson, id)
				if err != nil || lesson.Status != "approved" {
					t.Fatalf("lesson approval: %+v %v", lesson, err)
				}
				if again, err := s.Approve(id, kind, value); err != nil || again != gid {
					t.Fatalf("repeat approval: %d %v", again, err)
				}
				if _, err := s.Approve(id, kind, "different"); err == nil {
					t.Fatal("changing an approved check must fail")
				}
				if err := CheckDispatch(ledger, task, "target"); err != nil {
					t.Fatalf("matching dispatch: %v", err)
				}
				task.Title = "unsafe"
				if err := CheckDispatch(ledger, task, "other"); !errors.Is(err, ErrGuardrailRejected) {
					t.Fatalf("mismatching dispatch must reject: %v", err)
				}
				hits, err := s.Search("dispatch", scope, KindGuardrail)
				if err != nil || len(hits) != 1 || hits[0].ID != gid {
					t.Fatalf("approved guardrail must be indexed: %+v %v", hits, err)
				}
			})
		}
	}
}

func TestApprovalValidationAndAtomicity(t *testing.T) {
	s := openTest(t)
	id := candidate(t, s, ScopeProject)
	for _, check := range []struct{ kind, value string }{
		{CheckRepoEquals, ""}, {CheckRepoEquals, " "},
		{CheckTaskTitleMatches, "["}, {"shell", "echo ok"},
	} {
		if _, err := s.Approve(id, check.kind, check.value); err == nil {
			t.Fatalf("invalid predicate accepted: %+v", check)
		}
	}
	ordinary, err := s.Put(KindLesson, Entry{Title: "not a warning candidate"})
	if err != nil {
		t.Fatal(err)
	}
	for _, notCandidate := range []int64{0, ordinary, 99999} {
		if _, err := s.Approve(notCandidate, CheckRepoEquals, "repo"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid candidate %d: %v", notCandidate, err)
		}
	}
	if _, err := s.Put(KindGuardrail, Entry{Title: "bypass", Status: "approved"}); err == nil {
		t.Fatal("Put must not bypass structured approval")
	}
	// Force a failure after the guardrail insert to prove it cannot escape
	// without its predicate and approved candidate.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_check BEFORE INSERT ON memory_guardrail_checks
		BEGIN SELECT RAISE(ABORT,'test failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(id, CheckRepoEquals, "repo"); err == nil {
		t.Fatal("failed predicate write must fail approval")
	}
	rails, err := s.List(KindGuardrail, "")
	if err != nil || len(rails) != 0 {
		t.Fatalf("approval leaked a guardrail: %+v %v", rails, err)
	}
	got, err := s.Get(KindLesson, id)
	if err != nil || got.Status != "proposed" {
		t.Fatalf("failed approval changed candidate: %+v %v", got, err)
	}
}

func TestWarningPromotionAtomicity(t *testing.T) {
	s := openTest(t)
	if err := s.SetPromoteAfter(1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_lesson BEFORE INSERT ON lessons
		BEGIN SELECT RAISE(ABORT,'test failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Warn("w", ScopeProject); err == nil {
		t.Fatal("failed candidate creation must fail warning")
	}
	var rows int
	if err := s.db.QueryRow(`SELECT count(*) FROM warnings`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("failed promotion committed count: %d %v", rows, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_lesson`); err != nil {
		t.Fatal(err)
	}
	if n, id, err := s.Warn("w", ScopeProject); err != nil || n != 1 || id == 0 {
		t.Fatalf("retry: count=%d id=%d err=%v", n, id, err)
	}
}

func TestAllApprovedChecksMustPass(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	s, err := Open(PathForLedger(ledger))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPromoteAfter(1); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct{ body, scope, kind, value string }{
		{"repo", ScopeProject, CheckRepoEquals, "target"},
		{"title", ScopeGlobal, CheckTaskTitleMatches, "^fix:"},
	} {
		_, id, err := s.Warn(check.body, check.scope)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Approve(id, check.kind, check.value); err != nil {
			t.Fatal(err)
		}
	}
	for _, dispatch := range []struct{ title, repo string }{
		{"fix: good", "other"}, {"wrong", "target"},
	} {
		if err := CheckDispatch(ledger, core.Task{Title: dispatch.title}, dispatch.repo); !errors.Is(err, ErrGuardrailRejected) {
			t.Fatalf("one matching predicate must not bypass the other: %+v %v", dispatch, err)
		}
	}
	if err := CheckDispatch(ledger, core.Task{Title: "fix: good"}, "target"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE memory_guardrail_checks SET check_value='[' WHERE check_kind='task-title-matches'`); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatch(ledger, core.Task{Title: "fix: good"}, "target"); err == nil || errors.Is(err, ErrGuardrailRejected) {
		t.Fatalf("malformed persisted regex must be a policy configuration error: %v", err)
	}
}

func TestMissingSettingsAndDanglingPolicyFail(t *testing.T) {
	s := openTest(t)
	if _, err := s.db.Exec(`DELETE FROM memory_settings`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPromoteAfter(2); err == nil {
		t.Fatal("missing settings must not report success")
	}
	if _, _, err := s.Warn("w", ScopeProject); err == nil {
		t.Fatal("missing settings must fail warning")
	}
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.Symlink("missing.db", PathForLedger(ledger)); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatch(ledger, core.Task{}, "repo"); err == nil {
		t.Fatal("existing broken policy link must not disable enforcement")
	}
}

func TestDispatchPolicyFailuresAndCompatibility(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	if err := CheckDispatch(ledger, core.Task{}, "repo"); err != nil {
		t.Fatalf("workspace without Memory must still work: %v", err)
	}

	if _, err := os.Stat(PathForLedger(ledger)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dispatch must not create absent Memory")
	}
	if err := os.WriteFile(PathForLedger(ledger), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatch(ledger, core.Task{}, "repo"); err == nil || errors.Is(err, ErrGuardrailRejected) {
		t.Fatalf("corrupt DB must fail as storage error: %v", err)
	}
	if err := os.Remove(PathForLedger(ledger)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(PathForLedger(ledger))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`INSERT INTO guardrails(title,status,created_at) VALUES('legacy','approved','')`); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatch(ledger, core.Task{}, "repo"); err == nil || !strings.Contains(err.Error(), "no structured check") {
		t.Fatalf("malformed approved policy must not be skipped: %v", err)
	}
}

func TestDispatchRejectsMissingOrStaleTaskMemoryReferences(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), "ledger.json")
	if err := CheckDispatch(ledger, core.Task{}, "repo", "decision:1"); err == nil {
		t.Fatal("reference without a Memory database was accepted")
	}
	s, err := Open(PathForLedger(ledger))
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Put(KindDecision, Entry{Title: "current decision"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprintf("decision:%d", id)
	if err := CheckDispatch(ledger, core.Task{}, "repo", ref); err != nil {
		t.Fatalf("valid Task Memory reference rejected: %v", err)
	}
	s, err = Open(PathForLedger(ledger))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(KindDecision, id, "human", "decision invalidated"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckDispatch(ledger, core.Task{}, "repo", ref); err == nil {
		t.Fatal("revoked Task Memory reference was accepted")
	}
}
