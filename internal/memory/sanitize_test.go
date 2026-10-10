package memory

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	// Build synthetic shapes at runtime so repository hygiene can keep scanning
	// every test file for actual credentials without a fixture allowlist.
	key := "-----BEGIN " + "RSA PRIVATE KEY-----\nABC\n-----END RSA PRIVATE KEY-----"
	gitlab := "glpat-" + strings.Repeat("a", 20)
	in := "key " + key + " token gho_abc123 and " + gitlab + " and AKIAIOSFODNN7EXAMPLE " +
		"mail hiono@example.com ip 192.168.1.10 path /home/onohr/work id 7"
	out := Redact(DefaultRules, in)
	for _, secret := range []string{
		"ABC", "gho_abc123", gitlab, "AKIAIOSFODNN7EXAMPLE",
		"hiono@example.com", "192.168.1.10", "/home/onohr/work",
	} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret leaked: %q in %q", secret, out)
		}
	}
	for _, marker := range []string{
		"[redacted:key]", "[redacted:token]", "[redacted:email]",
		"[redacted:ip]", "[redacted:path]",
	} {
		if !strings.Contains(out, marker) {
			t.Fatalf("missing marker %q in %q", marker, out)
		}
	}
	if !strings.Contains(out, "id 7") {
		t.Fatalf("clean text damaged: %q", out)
	}
}

func TestRedactIdempotent(t *testing.T) {
	once := Redact(DefaultRules, "mail hiono@example.com ip 10.0.0.1")
	twice := Redact(DefaultRules, once)
	if once != twice {
		t.Fatalf("not idempotent:\n%s\n%s", once, twice)
	}
	if Redact(DefaultRules, "[redacted:email] [redacted:ip] [redacted:path] [redacted:token] [redacted:key]") !=
		"[redacted:email] [redacted:ip] [redacted:path] [redacted:token] [redacted:key]" {
		t.Fatal("rules match their own markers")
	}
}

func TestRedactCleanPassthrough(t *testing.T) {
	in := "Lesson: always confirm the repo before dispatch. No secrets here."
	if got := Redact(DefaultRules, in); got != in {
		t.Fatalf("clean text changed:\n%s\n%s", in, got)
	}
}

func TestRedactEntry(t *testing.T) {
	e := RedactEntry(Entry{
		ID: 3, Title: "contact hiono@example.com", Body: "key at /home/onohr/.ssh/id",
		Scope: ScopeGlobal, Status: "proposed", SourceRefs: []string{"owner hiono@example.com"},
	})
	if strings.Contains(e.Title, "hiono@example.com") || strings.Contains(e.Body, "/home/onohr/.ssh/id") ||
		strings.Contains(e.SourceRefs[0], "hiono@example.com") {
		t.Fatalf("entry secrets leaked: %+v", e)
	}
	if e.ID != 3 || e.Scope != ScopeGlobal || e.Status != "proposed" {
		t.Fatalf("identity fields must pass through: %+v", e)
	}
}
