// Sanitizer redacts sensitive literals before memory leaves the machine.
//
// M7 self-protection: export (M7-2) applies RedactEntry to every row it
// prints, so tokens, emails, private keys, and host-local paths never reach
// shared output. This file is the library only; CLI application lives in
// M7-2. Stdlib only (regexp) — no new dependencies.
//
// Redaction is deterministic and idempotent: redacting twice yields the same
// text, so trial prints are stable. Anything not matching a rule passes
// through unchanged; unknown sensitivity classes fail open by design here
// (the export path, not this package, decides what may leave).
package memory

import (
	"regexp"
)

// Rule pairs a sensitivity class with the pattern that detects it.
type Rule struct {
	// Name is the class label used in the [redacted:name] marker.
	Name string
	// Re matches the sensitive literal. It must not match its own
	// replacement marker, so redaction stays idempotent.
	Re *regexp.Regexp
}

// DefaultRules is the built-in vocabulary table. Ordered most-specific
// first so a private-key block is not partially eaten by narrower rules.
var DefaultRules = []Rule{
	{Name: "private-key", Re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)},
	{Name: "token", Re: regexp.MustCompile(`\b(gho_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|ghp_[A-Za-z0-9]{20,}|glpat-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16}|xox[bap]-[A-Za-z0-9-]{10,})\b`)},
	{Name: "email", Re: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	{Name: "ipv4", Re: regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])\b`)},
	{Name: "home-path", Re: regexp.MustCompile(`(?:/home/|/Users/)[^/\s:;\"']+`)},
}

// Redact applies rules in order and returns the cleaned text.
func Redact(rules []Rule, text string) string {
	for _, r := range rules {
		text = r.Re.ReplaceAllString(text, "[redacted:"+r.Name+"]")
	}
	return text
}

// RedactEntry redacts an entry's free-text fields (Title, Body) with the
// default table. Identity and control fields (ID, Scope, Status, CreatedAt)
// pass through so trial prints stay joinable with the ledger.
func RedactEntry(e Entry) Entry {
	e.Title = Redact(DefaultRules, e.Title)
	e.Body = Redact(DefaultRules, e.Body)
	return e
}
