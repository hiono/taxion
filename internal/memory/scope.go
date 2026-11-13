package memory

import "fmt"

// ScopeWriteNotice returns a global-write notice, or "" for other scopes.
// Explicit --scope global is the opt-in; a notice instead of a prompt keeps
// scripted writes non-interactive.
func ScopeWriteNotice(scope string) string {
	if scope == ScopeGlobal {
		return fmt.Sprintf("taxion: GLOBAL scope write (explicit --scope %s)", ScopeGlobal)
	}
	return ""
}
