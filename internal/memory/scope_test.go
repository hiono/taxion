package memory

import (
	"strings"
	"testing"
)

// Default writes must land in project: without an explicit --scope global,
// nothing may ever reach the global range.
func TestDefaultWriteNeverReachesGlobal(t *testing.T) {
	s := openTest(t)
	for _, kind := range []string{KindLesson, KindDecision, KindGuardrail, KindConcern} {
		if _, err := s.Put(kind, Entry{Title: kind + " default"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{KindLesson, KindDecision, KindGuardrail, KindConcern} {
		glob, err := s.List(kind, ScopeGlobal)
		if err != nil {
			t.Fatal(err)
		}
		if len(glob) != 0 {
			t.Fatalf("%s: default put leaked into global: %+v", kind, glob)
		}
		proj, err := s.List(kind, ScopeProject)
		if err != nil {
			t.Fatal(err)
		}
		if len(proj) != 1 {
			t.Fatalf("%s: default put should land in project, got %+v", kind, proj)
		}
	}
}

// Explicit global writes land in global only — and stay out of project lists.
func TestExplicitGlobalWriteStaysGlobal(t *testing.T) {
	s := openTest(t)
	if _, err := s.Put(KindLesson, Entry{Title: "g", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	proj, err := s.List(KindLesson, ScopeProject)
	if err != nil {
		t.Fatal(err)
	}
	if len(proj) != 0 {
		t.Fatalf("global write leaked into project: %+v", proj)
	}
}

// Warnings follow the same rule: default project, explicit global only.
func TestWarnScopeDefaults(t *testing.T) {
	s := openTest(t)
	if _, _, err := s.Warn("w1", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.WarningCount("w1", ScopeProject); got != 1 {
		t.Fatalf("default warn should count in project, got %d", got)
	}
	if got := s.WarningCount("w1", ScopeGlobal); got != 0 {
		t.Fatalf("default warn leaked into global, got %d", got)
	}
}

func TestScopeWriteNotice(t *testing.T) {
	if got := ScopeWriteNotice(ScopeProject); got != "" {
		t.Fatalf("project needs no notice, got %q", got)
	}
	if got := ScopeWriteNotice(""); got != "" {
		t.Fatalf("empty (defaults to project) needs no notice, got %q", got)
	}
	got := ScopeWriteNotice(ScopeGlobal)
	if !strings.Contains(got, "GLOBAL") || !strings.Contains(got, "--scope global") {
		t.Fatalf("global notice must name GLOBAL and the explicit flag, got %q", got)
	}
}
