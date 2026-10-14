package core

import (
	"strings"
	"testing"
)

func TestFileAccessPolicyValidation(t *testing.T) {
	for _, policy := range []FileAccessPolicy{
		{Level: FileAccessExact, Paths: []string{"src/a.go", "docs/guide.md"}},
		{Level: FileAccessCommitted},
		{Level: FileAccessAll},
	} {
		if err := policy.Validate(); err != nil {
			t.Fatalf("valid policy %+v rejected: %v", policy, err)
		}
	}
	for _, policy := range []FileAccessPolicy{
		{Level: FileAccessExact},
		{Level: FileAccessExact, Paths: []string{"src/a.go", "src/a.go"}},
		{Level: FileAccessExact, Paths: []string{"../secret"}},
		{Level: FileAccessExact, Paths: []string{".git/config"}},
		{Level: FileAccessExact, Paths: []string{"src/*.go"}},
		{Level: FileAccessCommitted, Paths: []string{"src/a.go"}},
		{Level: "unknown"},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
}

func TestNewTaskDefaultsAndDispatchBindsFileAccessPolicy(t *testing.T) {
	task := graphTask("file-access")
	task.FileAccess = nil
	graph := trackedGraph(t, task)
	saved, ok := graph.GraphTask(task.ID)
	if !ok || saved.FileAccess == nil || saved.FileAccess.Level != FileAccessCommitted {
		t.Fatalf("new Task did not default to committed file access: %+v", saved.FileAccess)
	}
	if err := graph.Transition(task.ID, Ready, "human", "ready", TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := NewGraphDispatch(graph, "dispatch", task.ID, task.Repo, RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.Instruction.FileAccess == nil || dispatch.Instruction.FileAccess.Level != FileAccessCommitted {
		t.Fatalf("Dispatch did not preserve the Task file-access policy: %+v", dispatch.Instruction.FileAccess)
	}
	rendered, err := RenderDispatchInstruction(dispatch)
	if err != nil || !strings.Contains(rendered, `"level":"committed"`) {
		t.Fatalf("rendered instruction omitted its file-access policy: %v\n%s", err, rendered)
	}
	saved.FileAccess.Level = FileAccessAll
	if dispatch.Instruction.FileAccess.Level != FileAccessCommitted {
		t.Fatal("changing the Task copy changed the issued Dispatch policy")
	}
}

func TestFileAccessChangeRequiresNewAuthorizationAndOmissionPreservesPolicy(t *testing.T) {
	graph := trackedGraph(t, graphTask("file-access-change"))
	task, _ := graph.GraphTask("file-access-change")
	task.Title = "Updated title"
	task.FileAccess = nil
	if err := graph.Track(task, "human", "edit without changing policy"); err != nil {
		t.Fatal(err)
	}
	task, _ = graph.GraphTask(task.ID)
	if task.FileAccess == nil || task.FileAccess.Level != FileAccessCommitted {
		t.Fatal("omitting fileAccess during an edit removed the saved policy")
	}

	task.FileAccess = &FileAccessPolicy{Level: FileAccessAll}
	if err := graph.Track(task, "human", "widen file access"); err == nil {
		t.Fatal("file-access change reused the previous authorization")
	}
	task.AuthorizationID = "decision:2"
	task.Decisions = append(task.Decisions, TaskDecision{
		ID: task.AuthorizationID, Source: "human:reapproval", Text: "approve the wider file access",
	})
	if err := graph.Track(task, "human", "reapprove file access"); err != nil {
		t.Fatal(err)
	}
}
