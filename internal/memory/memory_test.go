package memory

import (
	"errors"
	"path/filepath"
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
