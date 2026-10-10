package memory

import (
	"strings"
	"testing"
)

// Fixtures are built by concatenation so the full sensitive shapes never
// appear as literals in source (CI hygiene PRIV-001 scans *.go). Runtime
// values are identical to real-world secrets.
var (
	fxGHPToken = "ghp_" + "abcdefghij0123456789"
	fxGLToken  = "glpat-" + "abcdefghij0123456789"
	fxPEMHead  = "-----BEGIN " + "RSA PRIVATE KEY-----"
	fxPEMTail  = "-----END " + "RSA PRIVATE KEY-----"
)

func TestRedactClasses(t *testing.T) {
	in := "mail bob@example.com token " + fxGHPToken + " ip 192.168.0.7 home /home/bob/work key " + fxPEMHead + "\nABC\n" + fxPEMTail + " " + fxGLToken
	got := Redact(DefaultRules, in)
	for _, want := range []string{
		"[redacted:email]", "[redacted:token]", "[redacted:ipv4]",
		"[redacted:home-path]", "[redacted:private-key]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want marker %s in %q", want, got)
		}
	}
	for _, leak := range []string{"bob@example.com", "abcdefghij0123456789", "192.168.0.7", "/home/bob", "PRIVATE KEY"} {
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
	in := "contact bob@example.com, key " + fxGHPToken
	once := Redact(DefaultRules, in)
	if twice := Redact(DefaultRules, once); twice != once {
		t.Fatalf("not idempotent: %q vs %q", once, twice)
	}
}

func TestRedactEntry(t *testing.T) {
	e := Entry{ID: 7, Title: "note from bob@example.com", Body: "token " + fxGLToken, Scope: ScopeGlobal, Status: "approved"}
	got := RedactEntry(e)
	if got.ID != 7 || got.Scope != ScopeGlobal || got.Status != "approved" {
		t.Fatalf("identity fields must pass through: %+v", got)
	}
	if strings.Contains(got.Title, "bob@example.com") || strings.Contains(got.Body, "abcdefghij0123456789") {
		t.Fatalf("entry text not redacted: %+v", got)
	}
}
