package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func dumpMemory(t *testing.T, s *Store, table string) [][]any {
	t.Helper()
	rows, err := s.db.Query("SELECT * FROM " + quoteIdentifier(table) + " ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCompleteMemoryMigrationPreservesHistoryAndApproval(t *testing.T) {
	s := openTest(t)
	if _, err := s.db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPromoteAfter(2); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := s.WarnWithSource("repository permission warning", ScopeGlobal, "task:T-1"); err != nil {
			t.Fatal(err)
		}
	}
	candidates, err := s.List(KindLesson, ScopeGlobal)
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	if _, err := s.Approve(candidates[0].ID, CheckRepoEquals, "owner/repo"); err != nil {
		t.Fatal(err)
	}
	original, err := s.Put(KindDecision, Entry{Title: "original", Body: "owner@example.org", SourceRefs: []string{"issue:58"}})
	if err != nil {
		t.Fatal(err)
	}
	revised, err := s.Revise(KindDecision, original, Entry{Title: "revised", Body: "current"}, "human", "update")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(KindDecision, revised, "human", "expired"); err != nil {
		t.Fatal(err)
	}
	concern, err := s.Put(KindConcern, Entry{Title: "concern", Scope: ScopeProject, SourceRefs: []string{"task:T-1"}})
	if err != nil {
		t.Fatal(err)
	}
	left, right := "lesson:"+strconv.FormatInt(candidates[0].ID, 10), "concern:"+strconv.FormatInt(concern, 10)
	conflict, err := s.AddConflict(left, right, "human", "review contradiction")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveConflict(conflict, "human", "reviewed first disagreement"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddConflict(left, right, "human", "new disagreement"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	snapshot := filepath.Join(root, "snapshot.db")
	report, err := s.Snapshot(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) == 0 {
		t.Fatal("sensitive source omitted from review")
	}
	encoded, err := json.Marshal(report)
	if err != nil || strings.Contains(string(encoded), "owner@example.org") {
		t.Fatal("review leaked source data", err)
	}
	target := filepath.Join(root, "restored.db")
	approval := MigrationApproval{Hash: report.Hash, Destination: target, Actor: "human", Source: "human:exact-local-migration"}
	if _, err := RestoreSnapshot(t.Context(), snapshot, approval); err == nil {
		t.Fatal("sensitive transfer lacked explicit approval")
	}
	approval.ApproveSensitive = true
	if _, err := RestoreSnapshot(t.Context(), snapshot, approval); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, table := range []string{"lessons", "decisions", "guardrails", "concerns", "memory_entry_events", "warnings",
		"warning_events", "memory_conflicts", "memory_settings", "memory_guardrail_checks", "sqlite_sequence"} {
		if !reflect.DeepEqual(dumpMemory(t, s, table), dumpMemory(t, restored, table)) {
			t.Fatalf("migration lost %s", table)
		}
	}
	checks, err := restored.ApprovedChecks()
	if err != nil || len(checks) != 1 || checks[0].Scope != ScopeGlobal {
		t.Fatal("global approval lost", checks, err)
	}
	found, err := restored.Search("permission", "", KindLesson)
	if err != nil || len(found) != 1 {
		t.Fatal("search index lost", found, err)
	}
	for _, path := range []string{snapshot, target} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private permissions lost", err)
		}
	}
	if _, err := RestoreSnapshot(t.Context(), snapshot, approval); err == nil {
		t.Fatal("existing destination replaced")
	}
	approval.Hash = "sha256:" + strings.Repeat("0", 64)
	approval.Destination = filepath.Join(root, "tampered.db")
	if _, err := RestoreSnapshot(t.Context(), snapshot, approval); err == nil {
		t.Fatal("wrong exact approval accepted")
	}
	if _, err := os.Stat(approval.Destination); !os.IsNotExist(err) {
		t.Fatal("failed restore persisted a destination")
	}
	if _, err := s.Snapshot(t.Context(), snapshot); err == nil {
		t.Fatal("existing snapshot replaced")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Snapshot(ctx, filepath.Join(root, "cancelled.db")); err == nil {
		t.Fatal("cancelled snapshot succeeded")
	}
}

func TestRedactEntryLeavesSourceUnchanged(t *testing.T) {
	entry := Entry{Title: "source", SourceRefs: []string{"owner@example.org"}}
	before := slices.Clone(entry.SourceRefs)
	redacted := RedactEntry(entry)
	if !slices.Equal(entry.SourceRefs, before) || redacted.SourceRefs[0] == entry.SourceRefs[0] {
		t.Fatal("redaction mutates source provenance")
	}
}
