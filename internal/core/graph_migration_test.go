package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskGraphUnmarshalMigratesLegacyJobOwnershipToTask(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"jobs": [{
			"id": "job-legacy",
			"request": "preserve the legacy request",
			"goal": "migrate ownership without losing the Job",
			"status": "ACTIVE",
			"createdAt": "2026-11-13T08:00:00Z",
			"updatedAt": "2026-11-13T08:00:00Z",
			"rootTaskId": "task-legacy",
			"taskIds": ["task-legacy"]
		}],
		"tasks": [{
			"id": "task-legacy",
			"title": "Legacy task",
			"repo": "owner/repository",
			"type": "IMPLEMENTATION",
			"issue": "NOT_REQUIRED: migration fixture",
			"createdFrom": "test:legacy-job-migration",
			"status": "DRAFT"
		}]
	}`)

	var graph TaskGraph
	if err := json.Unmarshal(data, &graph); err != nil {
		t.Fatalf("migrate legacy Job ownership: %v", err)
	}
	task, ok := graph.GraphTask("task-legacy")
	if !ok || task.JobID != "job-legacy" {
		t.Fatalf("legacy Task ownership was not moved to Task.JobID: task=%+v found=%v", task, ok)
	}
	// The fixture omits legacy history; establish a current-format tracking event.
	if err := graph.Track(task, "migration-test", "record migrated Task"); err != nil {
		t.Fatalf("track migrated Task: %v", err)
	}
	if _, ok := graph.Job("job-legacy"); !ok {
		t.Fatal("legacy Job was lost during ownership migration")
	}
	if err := graph.Validate(); err != nil {
		t.Fatalf("migrated graph is invalid: %v", err)
	}

	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "\"taskIds\"") || strings.Contains(string(encoded), "\"rootTaskId\"") {
		t.Fatalf("legacy ownership fields leaked into the current model: %s", encoded)
	}
}

func TestTaskGraphUnmarshalRejectsConflictingLegacyJobOwnership(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"jobs": [{
			"id": "job-legacy",
			"request": "request",
			"goal": "goal",
			"status": "ACTIVE",
			"createdAt": "2026-11-13T08:00:00Z",
			"updatedAt": "2026-11-13T08:00:00Z",
			"taskIds": ["task-legacy"]
		}],
		"tasks": [{
			"id": "task-legacy",
			"jobId": "job-other",
			"title": "Legacy task",
			"repo": "owner/repository",
			"type": "IMPLEMENTATION",
			"issue": "NOT_REQUIRED: migration fixture",
			"createdFrom": "test:legacy-job-migration",
			"status": "DRAFT"
		}]
	}`)

	var graph TaskGraph
	if err := json.Unmarshal(data, &graph); err == nil {
		t.Fatal("conflicting legacy and canonical Job ownership must be rejected")
	}
}
