package memory

import (
	"bytes"
	"strings"
	"testing"
)

func seedExport(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir() + "/mem.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Put(KindLesson, Entry{Title: "clean lesson", Body: "nothing sensitive", Scope: ScopeProject}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(KindDecision, Entry{Title: "deploy key", Body: "contact ops@example.com", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExportSanitizedByDefault(t *testing.T) {
	s := seedExport(t)
	entries, err := Export(s, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	var buf bytes.Buffer
	PrintExport(&buf, entries, true)
	out := buf.String()
	if strings.Contains(out, "ops@example.com") {
		t.Fatalf("sanitized export leaked raw email:\n%s", out)
	}
	if !strings.Contains(out, "[redacted:email]") {
		t.Fatalf("sanitized export lost the marker:\n%s", out)
	}
	if !strings.Contains(out, "SANITIZED") || !strings.Contains(out, "nothing is sent anywhere") {
		t.Fatalf("trial print must say sanitized + unsent:\n%s", out)
	}
}

func TestExportRawOptOut(t *testing.T) {
	s := seedExport(t)
	entries, err := Export(s, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	PrintExport(&buf, entries, false)
	out := buf.String()
	if !strings.Contains(out, "ops@example.com") {
		t.Fatalf("raw export should keep the text:\n%s", out)
	}
	if !strings.Contains(out, "UNSANITIZED") {
		t.Fatalf("raw export must say UNSANITIZED:\n%s", out)
	}
}

func TestExportScopeFilter(t *testing.T) {
	s := seedExport(t)
	entries, err := Export(s, ScopeProject, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Entry.Scope != ScopeProject {
		t.Fatalf("project filter mismatch: %+v", entries)
	}
}

func TestExportGuards(t *testing.T) {
	s := seedExport(t)
	if _, err := Export(s, "everywhere", true); err == nil {
		t.Fatal("unknown scope must fail")
	}
	var buf bytes.Buffer
	PrintExport(&buf, nil, true)
	if !strings.Contains(buf.String(), "no memory entries to export") {
		t.Fatalf("empty export message missing: %q", buf.String())
	}
}
