package memory

import (
	"regexp"
	"slices"
)

// Rule is one named redaction pattern.
type Rule struct {
	Name string
	Re   *regexp.Regexp
}

// DefaultRules defines the default patterns, not a complete secret detector.
// Key blocks come first so their contents are not separately replaced and
// the closing delimiter remains matchable. Markers avoid pattern syntax so
// a second redaction pass leaves them unchanged.
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

// Redact applies rules in order. Unmatched text passes through unchanged;
// callers must review the result rather than treating it as safe to share.
func Redact(rules []Rule, text string) string {
	out := text
	for _, r := range rules {
		out = r.Re.ReplaceAllString(out, "[redacted:"+r.Name+"]")
	}
	return out
}

// RedactEntry cleans the free-text fields of an entry (Title, Body, SourceRefs) with
// the DefaultRules. Identity fields (ID, Scope, Status, CreatedAt) pass
// through untouched. Custom rule sets go through Redact directly.
func RedactEntry(e Entry) Entry {
	e.Title = Redact(DefaultRules, e.Title)
	e.Body = Redact(DefaultRules, e.Body)
	e.SourceRefs = slices.Clone(e.SourceRefs)
	for i := range e.SourceRefs {
		e.SourceRefs[i] = Redact(DefaultRules, e.SourceRefs[i])
	}
	return e
}
