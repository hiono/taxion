package memory

import (
	"fmt"
	"io"
)

// ExportedEntry is one exported row with its kind attached.
type ExportedEntry struct {
	Kind  string
	Entry Entry
}

// ExportKinds is the stable export order.
var ExportKinds = []string{KindLesson, KindDecision, KindGuardrail, KindConcern}

// Export collects rows across tables, filtered by scope ("" = all scopes,
// otherwise project|global — anything else fails closed) and redacted when
// sanitize is true.
func Export(s *Store, scope string, sanitize bool) ([]ExportedEntry, error) {
	if scope != "" {
		if err := checkScope(scope); err != nil {
			return nil, err
		}
	}
	var out []ExportedEntry
	for _, kind := range ExportKinds {
		rows, err := s.List(kind, scope)
		if err != nil {
			return nil, err
		}
		for _, e := range rows {
			if sanitize {
				e = RedactEntry(e)
			}
			out = append(out, ExportedEntry{Kind: kind, Entry: e})
		}
	}
	return out, nil
}

// PrintExport labels the preview so sanitized and original output cannot
// be mistaken for each other when reviewed before sharing.
func PrintExport(w io.Writer, entries []ExportedEntry, sanitize bool) {
	if sanitize {
		fmt.Fprintln(w, "taxion memory export — SANITIZED preview (no data is transmitted)")
	} else {
		fmt.Fprintln(w, "taxion memory export — UNSANITIZED preview (no data is transmitted)")
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "taxion: no memory entries to export")
		return
	}
	for _, ee := range entries {
		fmt.Fprintf(w, "[%s %d v%d] %s (%s, %s)\n%s\nsources: %v\n",
			ee.Kind, ee.Entry.ID, ee.Entry.Version, ee.Entry.Title, ee.Entry.Scope, ee.Entry.Status,
			ee.Entry.Body, ee.Entry.SourceRefs)
	}
}
