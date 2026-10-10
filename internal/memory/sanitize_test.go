package memory

import (
	"strings"
	"testing"
)

func TestRedactClasses(t *testing.T) {
	in := "mail bob@example.com token ghp_abcdefghij0123456789 ip 192.168.0.7 home /home/bob/work key -----BEGIN RSA PRIVATE KEY-----\nABC\n-----END RSA PRIVATE KEY----- glpat-abcdefghij0123456789"
	got := Redact(DefaultRules, in)
	for _, want := range []string{
		"[redacted:email]", "[redacted:token]", "[redacted:ipv4]",
		"[redacted:home-path]", "[redacted:private-key]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want marker %s in %q", want, got)
		}
	}
	for _, leak := range []string{"bob@example.com", "ghp_abcdefghij0123456789", "192.168.0.7", "/home/bob", "PRIVATE KEY", "glpat-abcdefghij0123456789"} {
		if strings.Contains(got, leak) {
			t.Errorf("sensitive literal leaked in %q", got)
		}
	}
}

func TestRedactCleanTextUntouched(t *testing.T) {
	in := "Lesson: never serve beyond loopback without a token (see ADR-0016)."
	if got := Redact(DefaultRules, in); got != in {
		t.Fatalf("clean text changed: %q", got)
	}
}

func TestRedactIdempotent(t *testing.T) {
	in := "contact bob@example.com, key ghp_abcdefghij0123456789"
	once := Redact(DefaultRules, in)
	if twice := Redact(DefaultRules, once); twice != once {
		t.Fatalf("not idempotent: %q vs %q", once, twice)
	}
}

func TestRedactEntry(t *testing.T) {
	e := Entry{ID: 7, Title: "note from bob@example.com", Body: "token glpat-abcdefghij0123456789", Scope: ScopeGlobal, Status: "approved"}
	got := RedactEntry(e)
	if got.ID != 7 || got.Scope != ScopeGlobal || got.Status != "approved" {
		t.Fatalf("identity fields must pass through: %+v", got)
	}
	if strings.Contains(got.Title, "bob@example.com") || strings.Contains(got.Body, "glpat-abcdefghij0123456789") {
		t.Fatalf("entry text not redacted: %+v", got)
	}
}
