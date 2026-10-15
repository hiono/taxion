package controller

import (
	"slices"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestBoundResultIdentityComesFromSession(t *testing.T) {
	dispatch := core.GraphDispatch{
		ID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		RuntimeSessionID: "session", ExecutionStartSHA: "observed-start",
		Instruction: core.DispatchInstruction{OutputMode: core.OutputModeWorktreeTreeSnapshot},
	}
	schema := BoundDispatchResultSchema(dispatch)
	properties := schema["properties"].(map[string]any)
	required := schema["required"].([]string)
	for _, field := range []string{"dispatchId", "taskId", "recipient", "repository", "startedSha", "outputSha"} {
		if _, ok := properties[field]; ok || slices.Contains(required, field) {
			t.Fatalf("model still transcribes host field %s", field)
		}
	}
	result := core.DispatchResult{Status: core.DispatchFailed, Blockers: []string{"actual blocker"}}
	if err := bindResultIdentity(dispatch, &result); err != nil ||
		result.DispatchID != dispatch.ID || result.TaskID != dispatch.TaskID || result.Recipient != dispatch.Recipient ||
		result.Repository != dispatch.Repository || result.StartedSHA != dispatch.ExecutionStartSHA ||
		result.OutputSHA != dispatch.ExecutionStartSHA || result.Blockers[0] != "actual blocker" {
		t.Fatal("host completion lost binding or actual result", result, err)
	}
	for _, conflict := range []core.DispatchResult{
		{DispatchID: "other"}, {TaskID: "other"}, {Recipient: "REVIEWER"}, {Repository: "other/repo"},
		{StartedSHA: "guessed"}, {OutputSHA: "guessed"},
	} {
		if err := bindResultIdentity(dispatch, &conflict); err == nil {
			t.Fatal("conflicting model metadata accepted", conflict)
		}
	}
	dispatch.RuntimeSessionID = ""
	if err := bindResultIdentity(dispatch, &core.DispatchResult{}); err == nil {
		t.Fatal("unbound result accepted")
	}
}
