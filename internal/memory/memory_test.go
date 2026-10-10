package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRoundTripAllKinds(t *testing.T) {
	s := openTest(t)
	for _, kind := range []string{KindLesson, KindDecision, KindGuardrail, KindConcern} {
		id, err := s.Put(kind, Entry{Title: kind + " one", Body: "body", Scope: ScopeProject})
		if err != nil {
			t.Fatalf("%s put: %v", kind, err)
		}
		got, err := s.Get(kind, id)
		if err != nil {
			t.Fatalf("%s get: %v", kind, err)
		}
		if got.Title != kind+" one" || got.Body != "body" ||
			got.Scope != ScopeProject || got.Status != "proposed" {
			t.Fatalf("%s mismatch: %+v", kind, got)
		}
		if got.CreatedAt.IsZero() {
			t.Fatalf("%s: zero CreatedAt", kind)
		}
	}
}

func TestListScopeFilter(t *testing.T) {
	s := openTest(t)
	if _, err := s.Put(KindLesson, Entry{Title: "proj", Scope: ScopeProject}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(KindLesson, Entry{Title: "glob", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(KindLesson, "")
	if err != nil || len(all) != 2 || all[0].Title != "proj" || all[1].Title != "glob" {
		t.Fatalf("unfiltered list = %+v, err=%v", all, err)
	}
	proj, err := s.List(KindLesson, ScopeProject)
	if err != nil || len(proj) != 1 || proj[0].Title != "proj" {
		t.Fatalf("project list = %+v, err=%v", proj, err)
	}
}

func TestGuards(t *testing.T) {
	s := openTest(t)
	if _, err := s.Put("nope", Entry{Title: "x"}); err == nil {
		t.Fatal("unknown kind must fail")
	}

	if _, err := s.Put(KindLesson, Entry{Title: ""}); err == nil {
		t.Fatal("empty title must fail")
	}
	if _, err := s.Put(KindLesson, Entry{Title: "x", Scope: "nope"}); err == nil {
		t.Fatal("unknown scope must fail")
	}
	if _, err := s.Get(KindLesson, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id must be ErrNotFound, got %v", err)
	}
	if _, err := s.List("nope", ""); err == nil {
		t.Fatal("list unknown kind must fail")
	}
	if _, err := s.List(KindLesson, "nope"); err == nil {
		t.Fatal("list unknown scope must fail")
	}
}

func TestSourceReferencesRevisionsRevocationAndHistory(t *testing.T) {
	s := openTest(t)
	id, err := s.Put(KindDecision, Entry{
		Title: "Use SQLite", Body: "Persist the decision", Scope: ScopeProject,
		SourceRefs: []string{"task:T-1", "dispatch:D-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(KindDecision, id)
	if err != nil || !slices.Equal(original.SourceRefs, []string{"task:T-1", "dispatch:D-1"}) ||
		original.Version != 1 || original.UpdatedAt.IsZero() {
		t.Fatalf("entry provenance/version not persisted: %+v, %v", original, err)
	}
	newID, err := s.Revise(KindDecision, id, Entry{
		Title: "Use SQLite workspace state", Body: "Preserve the rationale",
	}, "human", "clarified storage boundary")
	if err != nil {
		t.Fatal(err)
	}
	revised, err := s.Get(KindDecision, newID)
	if err != nil || revised.Version != 2 || revised.SupersedesID != id ||
		!slices.Equal(revised.SourceRefs, original.SourceRefs) || revised.Status != "proposed" {
		t.Fatalf("revision metadata not persisted: %+v, %v", revised, err)
	}
	old, err := s.Get(KindDecision, id)
	if err != nil || old.Status != "superseded" {
		t.Fatalf("old version was not retained as superseded: %+v, %v", old, err)
	}
	if _, err := s.ResolveReference(fmt.Sprintf("%s:%d", KindDecision, id)); err == nil {
		t.Fatal("superseded reference remained active")
	}
	if _, err := s.ResolveReference(fmt.Sprintf("%s:%d", KindDecision, newID)); err != nil {
		t.Fatalf("current reference rejected: %v", err)
	}
	events, err := s.Events(KindDecision, id)
	if err != nil || len(events) != 2 || events[1].Action != "superseded" ||
		events[1].Actor != "human" || events[1].Reason != "clarified storage boundary" {
		t.Fatalf("revision history not recorded: %+v, %v", events, err)
	}
	if err := s.Revoke(KindDecision, newID, "human", "decision no longer applies"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveReference(fmt.Sprintf("%s:%d", KindDecision, newID)); err == nil {
		t.Fatal("revoked reference remained active")
	}
}

func TestWarningCandidateRetainsOccurrenceSourcesAndRevocationDisablesGuardrail(t *testing.T) {
	s := openTest(t)
	if err := s.SetPromoteAfter(2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WarnWithSource("check owner before deploy", ScopeProject, "dispatch:D-1"); err != nil {
		t.Fatal(err)
	}
	_, lessonID, err := s.WarnWithSource("check owner before deploy", ScopeProject, "task:T-1")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.Get(KindLesson, lessonID)
	if err != nil || !slices.Contains(candidate.SourceRefs, "dispatch:D-1") ||
		!slices.Contains(candidate.SourceRefs, "task:T-1") ||
		!slices.Contains(candidate.SourceRefs, "warning:1") ||
		!slices.Contains(candidate.SourceRefs, "warning:2") || len(candidate.SourceRefs) != 4 {
		t.Fatalf("candidate lost warning provenance: %+v, %v", candidate, err)
	}
	source, err := s.WarningOccurrence(1)
	if err != nil || source.Body != "check owner before deploy" || source.SourceRef != "dispatch:D-1" ||
		source.OccurrenceCount != 1 {
		t.Fatalf("warning source reference cannot be resolved: %+v, %v", source, err)
	}
	guardrailID, err := s.Approve(lessonID, CheckRepoEquals, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	checks, err := s.ApprovedChecks()
	if err != nil || len(checks) != 1 || checks[0].ID != guardrailID {
		t.Fatalf("approval not active: %+v, %v", checks, err)
	}
	if err := s.Revoke(KindLesson, lessonID, "human", "underlying warning was incorrect"); err != nil {
		t.Fatal(err)
	}
	guardrail, err := s.Get(KindGuardrail, guardrailID)
	if err != nil || guardrail.Status != "revoked" {
		t.Fatalf("source revocation did not disable guardrail: %+v, %v", guardrail, err)
	}
	checks, err = s.ApprovedChecks()
	if err != nil || len(checks) != 0 {
		t.Fatalf("revoked guardrail still enforced: %+v, %v", checks, err)
	}
}

func TestOpenMigratesExistingMemoryDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE lessons (
		id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
		body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
		status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL);
		INSERT INTO lessons(title,body,scope,status,created_at)
		VALUES('legacy lesson','old data','project','proposed','2026-01-01T00:00:00Z')`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(KindLesson, 1)
	if err != nil || got.Version != 1 || got.UpdatedAt.IsZero() || len(got.SourceRefs) != 0 {
		t.Fatalf("legacy entry migration failed: %+v, %v", got, err)
	}
}

func TestEntryWithMissingTimestampFailsClosed(t *testing.T) {
	s := openTest(t)
	id, err := s.Put(KindDecision, Entry{Title: "timestamp must be present"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE decisions SET created_at='' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(KindDecision, id); err == nil {
		t.Fatal("entry with a missing creation timestamp was accepted")
	}
}

func TestRevisingApprovedLessonRevokesItsGuardrail(t *testing.T) {
	s := openTest(t)
	if err := s.SetPromoteAfter(1); err != nil {
		t.Fatal(err)
	}
	_, lessonID, err := s.Warn("rule based on old evidence", ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	guardrailID, err := s.Approve(lessonID, CheckRepoEquals, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	newID, err := s.Revise(KindLesson, lessonID, Entry{
		Title: "Corrected rule", Body: "Updated after review",
	}, "human", "original evidence was incomplete")
	if err != nil {
		t.Fatal(err)
	}
	oldGuardrail, err := s.Get(KindGuardrail, guardrailID)
	if err != nil || oldGuardrail.Status != "revoked" {
		t.Fatalf("revision left old policy active: %+v, %v", oldGuardrail, err)
	}
	if _, err := s.ResolveReference(fmt.Sprintf("%s:%d", KindLesson, lessonID)); err == nil {
		t.Fatal("superseded lesson reference remained valid")
	}
	if _, err := s.ResolveReference(fmt.Sprintf("%s:%d", KindLesson, newID)); err != nil {
		t.Fatalf("revised lesson reference rejected: %v", err)
	}
	checks, err := s.ApprovedChecks()
	if err != nil || len(checks) != 0 {
		t.Fatalf("revised source still enforces old guardrail: %+v, %v", checks, err)
	}
}
