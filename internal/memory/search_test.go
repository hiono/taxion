package memory

import (
	"path/filepath"
	"testing"
)

func seedSearch(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Put(KindLesson, Entry{
		Title: "トークンを履歴に残さない", Body: "シェル履歴に秘密を残さない運用", Scope: ScopeProject,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(KindGuardrail, Entry{
		Title: "loopback-by-default", Body: "M5までは外部公開しない", Scope: ScopeGlobal,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(KindDecision, Entry{
		Title: "FTS5 first", Body: "QMDはサイドカーに留める", Scope: ScopeProject,
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSearchFTSHits(t *testing.T) {
	s := seedSearch(t)
	for _, q := range []string{"トークン", "履歴に秘", "秘密を残さない"} {
		hits, err := s.Search(q, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].Kind != KindLesson {
			t.Fatalf("query %q: got %+v, want the lesson", q, hits)
		}
	}
}

func TestSearchShortQueryFallsToLIKE(t *testing.T) {
	s := seedSearch(t)
	hits, err := s.Search("秘密", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Kind != KindLesson {
		t.Fatalf("2-char query: got %+v, want the lesson via LIKE", hits)
	}
}

func TestSearchFilters(t *testing.T) {
	s := seedSearch(t)
	hits, err := s.Search("公開", ScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Kind != KindGuardrail {
		t.Fatalf("scope filter: got %+v", hits)
	}
	hits, err = s.Search("公開", ScopeProject, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("scope filter must exclude, got %+v", hits)
	}
	hits, err = s.Search("FTS5", "", KindDecision)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("kind filter: got %+v", hits)
	}
}

func TestSearchGuards(t *testing.T) {
	s := seedSearch(t)
	if _, err := s.Search("", "", ""); err == nil {
		t.Fatal("empty query must fail")
	}
	if _, err := s.Search("  ", "", ""); err == nil {
		t.Fatal("blank query must fail")
	}
	if _, err := s.Search("x", "", "nope"); err == nil {
		t.Fatal("unknown kind must fail")
	}
	if _, err := s.Search("x", "nope", ""); err == nil {
		t.Fatal("unknown scope must fail")
	}
	if hits, err := s.Search("存在しない語", "", ""); err != nil || len(hits) != 0 {
		t.Fatalf("no match: hits=%v err=%v", hits, err)
	}
	if _, err := s.Search(`"quoted" (paren) *star*`, "", ""); err != nil {
		t.Fatalf("special chars: %v", err)
	}
}
