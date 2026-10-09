package core

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"
)

// TestWireKeysFrozen pins the exact JSON key sets of the frozen ledger wire
// format (docs/05-ledger-schema.md). Any key addition/removal/rename fails
// here by design: change the schema doc + ADR first, then this test.
func TestWireKeysFrozen(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		value any
		want  []string
	}{
		"ledger": {
			Ledger{},
			[]string{"checkpoints", "dispatches", "evidence", "tasks"},
		},
		"task": {
			Task{CreatedAt: now},
			[]string{"createdAt", "id", "repo", "title"},
		},
		"dispatch": {
			Dispatch{CreatedAt: now},
			[]string{"active", "createdAt", "id", "repo", "taskId"},
		},
		"evidence": {
			Evidence{CreatedAt: now},
			[]string{"createdAt", "sha", "taskId"},
		},
		"checkpoint": {
			Checkpoint{},
			[]string{"done", "name"},
		},
	}
	for name, tc := range cases {
		data, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		gotKeys := slices.Sorted(maps.Keys(got))
		if !slices.Equal(gotKeys, tc.want) {
			t.Errorf("%s: keys = %v, want %v (see docs/05-ledger-schema.md)", name, gotKeys, tc.want)
		}
	}
}
