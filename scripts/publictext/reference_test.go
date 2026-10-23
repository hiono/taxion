package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/publictext"
)

func TestGitHubReferenceStatesAndTransitionTimes(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	target := "https://github.com/example/tool/pull/1"
	for _, tc := range []struct {
		kind    string
		extra   map[string]any
		state   string
		stateAt time.Time
	}{
		{"pull", map[string]any{"state": "open"}, "IN_PROGRESS", at.Add(-time.Hour)},
		{"pull", map[string]any{"merged_at": at.Add(-time.Minute)}, "COMPLETED", at.Add(-time.Minute)},
		{"pull", map[string]any{"state": "closed", "closed_at": at}, "FAILED", at},
		{"run", map[string]any{"status": "in_progress", "conclusion": nil}, "IN_PROGRESS", at.Add(-time.Hour)},
		{"run", map[string]any{"status": "completed", "conclusion": "success", "updated_at": at}, "COMPLETED", at},
		{"run", map[string]any{"status": "completed", "conclusion": "failure", "updated_at": at}, "FAILED", at},
		{"release", map[string]any{"draft": true}, "NOT_PUBLISHED", at.Add(-time.Hour)},
		{"release", map[string]any{"draft": false, "published_at": at}, "PUBLISHED", at},
		{"review", map[string]any{"state": "APPROVED", "submitted_at": at}, "APPROVED", at},
		{"comment", map[string]any{"updated_at": at}, "EXISTS", at},
	} {
		fields := map[string]any{"html_url": target, "created_at": at.Add(-time.Hour)}
		for key, value := range tc.extra {
			fields[key] = value
		}
		data, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := referenceMetadata(target, tc.kind, data)
		if err != nil || ref.State != tc.state || !ref.StateAt.Equal(tc.stateAt) {
			t.Fatalf("%s state timestamp not preserved: %#v: %v", tc.kind, ref, err)
		}
		// The object existed earlier, but its later result must not be backdated.
		if tc.state == "COMPLETED" || tc.state == "PUBLISHED" || tc.state == "APPROVED" {
			resolve := func(context.Context, string) (publictext.Reference, error) { return ref, nil }
			if publictext.VerifyReference(t.Context(), target, tc.state, tc.stateAt.Add(-time.Second), resolve) == nil {
				t.Fatal("later result treated as known at original creation")
			}
		}
	}
	for _, target := range []string{"http://github.com/example/tool/issues/1", "https://evil.example/x",
		"https://github.com@example.org/x", "https://github.com/example/tool/issues/1#future-123",
		"https://github.com/example/tool/issues/1?endpoint=other", "https://github.com/example/tool/pull/not-a-number"} {
		if _, err := resolveGitHub(t.Context(), target); err == nil {
			t.Fatal("unsupported URL accepted without authoritative metadata")
		}
	}
}

func TestProgressCLIAndContextFailureProduceNoOutput(t *testing.T) {
	var output, warnings bytes.Buffer
	if err := run([]string{"-progress", "NOT_RUN"}, &output, &warnings); err != nil || output.String() != "この検証は実行していません。\n" {
		t.Fatal("not-run stage misrepresented", err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "context.json")
	if err := os.WriteFile(file, []byte(`{"at":"2026-01-01T00:00:00Z","claims":[]} {}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-progress", "COMPLETED"},
		{"-progress", "NOT_RUN", "-reference", "https://github.com/example/tool/pull/1"},
		{"-progress", "PLANNED", "-at", "invalid"},
		{"-context", file, "nonexistent.md"},
		{"-context", file, "-repository"},
	} {
		output.Reset()
		if err := run(args, &output, &warnings); err == nil || strings.TrimSpace(output.String()) != "" {
			t.Fatal("invalid temporal context produced public output", args, err)
		}
	}
}
