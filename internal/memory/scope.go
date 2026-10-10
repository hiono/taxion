// Scope enforcement for memory writes (M7-3, issue #34).
//
// Rule: project/global mixing is prevented by construction. Writes default
// to project; global is reachable only through an explicit --scope global,
// and every global write prints a confirmation notice (non-interactive —
// Taxion never blocks on a prompt, the explicit flag IS the confirmation).
// Reads (List/Search/Export) validate non-empty scopes via checkScope.
package memory

import "fmt"

// ScopeWriteNotice returns the confirmation line a writer must print for a
// global write, or "" when no notice is needed (project scope). Keeping the
// wording in one place lets CLI paths share — and tests pin — the exact
// explicitness record.
func ScopeWriteNotice(scope string) string {
	if scope == ScopeGlobal {
		return fmt.Sprintf("taxion: GLOBAL scope write (explicit --scope %s)", ScopeGlobal)
	}
	return ""
}
