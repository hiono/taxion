// Package memory — sanitizer (M7-1).
//
// Redact is a deterministic vocab-table redactor for export paths (M7-2):
// private keys, API tokens, emails, IP addresses, and home-directory paths
// become [redacted:<class>] markers. Stdlib regexp only — no external deps.
//
// The rules must never match their own markers (idempotence): markers carry
// no @, no dotted quads, no slashes, and no token prefixes.
//
// Anything not matching a rule passes through unchanged; unknown sensitivity
// classes fail open by design here (the export path, not this package,
// decides what may leave the machine).
package memory

import "regexp"

// Rule is one named redaction pattern.
type Rule struct {
	Name string
	Re   *regexp.Regexp
}

// DefaultRules is the export-safe vocab table. Order matters only for
// overlapping spans (key blocks first, they are the longest).
var DefaultRules = []Rule{
	{"key", regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"token", regexp.MustCompile(`\b(gho|ghp|ghs|github_pat)_[A-Za-z0-9_]+`)},
	{"token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"token", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]+\b`)},
	{"email", regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)},
	{"ip", regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)},
	{"path", regexp.MustCompile(`/(home|Users)/[^\s"']+`)},
}

// Redact applies every rule in order and returns the cleaned text.
func Redact(rules []Rule, text string) string {
	out := text
	for _, r := range rules {
		out = r.Re.ReplaceAllString(out, "[redacted:"+r.Name+"]")
	}
	return out
}

// RedactEntry cleans the free-text fields of an entry (Title, Body) with
// the DefaultRules. Identity fields (ID, Scope, Status, CreatedAt) pass
// through untouched. Custom rule sets go through Redact directly.
func RedactEntry(e Entry) Entry {
	e.Title = Redact(DefaultRules, e.Title)
	e.Body = Redact(DefaultRules, e.Body)
	return e
}
