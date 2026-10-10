package memory

import (
	"path/filepath"
	"testing"
)

func TestWarnCountsAndPromotesOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 1; i < PromoteAfter; i++ {
		n, lid, err := s.Warn("never deploy on friday", ScopeProject)
		if err != nil {
			t.Fatal(err)
		}
		if n != i || lid != 0 {
			t.Fatalf("warn %d: count=%d lid=%d, want no promotion yet", i, n, lid)
		}
	}
	n, lid, err := s.Warn("never deploy on friday", ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if n != PromoteAfter || lid == 0 {
		t.Fatalf("threshold warn: count=%d lid=%d, want promotion", n, lid)
	}
	got, err := s.Get(KindLesson, lid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "proposed" || got.Scope != ScopeProject || got.Body != "never deploy on friday" {
		t.Fatalf("promoted lesson wrong: %+v", got)
	}

	// Fourth repeat increments the count without creating another candidate.
	n2, lid2, err := s.Warn("never deploy on friday", ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != PromoteAfter+1 || lid2 != lid {
		t.Fatalf("repeat: count=%d lid=%d, want %d and same lesson", n2, lid2, PromoteAfter+1)
	}
	lessons, err := s.List(KindLesson, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) != 1 {
		t.Fatalf("want exactly 1 promoted lesson, got %d", len(lessons))
	}
}

func TestWarnScopesAreSeparate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for i := 0; i < PromoteAfter; i++ {
		if _, _, err := s.Warn("same words", ScopeGlobal); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.WarningCount("same words", ScopeProject); n != 0 {
		t.Fatalf("project count = %d, want 0 (scopes separate)", n)
	}
	if _, _, err := s.Warn("same words", ScopeProject); err != nil {
		t.Fatal(err)
	}
	if n := s.WarningCount("same words", ScopeProject); n != 1 {
		t.Fatalf("project count = %d, want 1", n)
	}
}

func TestWarnGuards(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, _, err := s.Warn("", ScopeProject); err == nil {
		t.Fatal("empty warning body must fail")
	}
	if _, _, err := s.Warn("x", "nope"); err == nil {
		t.Fatal("unknown scope must fail")
	}
	// Empty scope defaults to project and counts there.
	if n, _, err := s.Warn("defaulted", ""); err != nil || n != 1 {
		t.Fatalf("empty scope: n=%d err=%v, want default project", n, err)
	}
	if n := s.WarningCount("defaulted", ScopeProject); n != 1 {
		t.Fatalf("defaulted count = %d, want 1", n)
	}
}
