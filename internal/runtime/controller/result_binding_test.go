package controller

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestBoundCoderRejectsReportIdentifierWithoutContent(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "work/task")
	gitTest(t, root, "config", "user.name", "Taxion Test")
	gitTest(t, root, "config", "user.email", "taxion-test@example.com")
	gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "README.md")
	gitTest(t, root, "commit", "-m", "baseline")
	db, _, id := dispatchStoreWithEvidence(t, "work-report", root)
	defer db.Close()
	control, err := New(db, &fakeRuntime{role: "CODER", hash: "bound-report", worktree: root, bound: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	_, err = control.Finish(context.Background(), id, `{"status":"SUCCEEDED","evidence":["work-report","/outside/session/files/report.json"]}`)
	if err == nil || !strings.Contains(err.Error(), "requires workReport content") {
		t.Fatal("dangling file report was not rejected before accepting success", err)
	}
}

func TestBoundResultIdentityComesFromSession(t *testing.T) {
	dispatch := core.GraphDispatch{
		ID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		RuntimeSessionID: "session", ExecutionStartSHA: "observed-start",
		Instruction: core.DispatchInstruction{OutputMode: core.OutputModeWorktreeTreeSnapshot},
	}
	dispatch.Instruction.RequiredEvidence = []string{"work-report"}
	for _, role := range []string{"CODER", "REVIEWER", "ORCHESTRATOR"} {
		producer := dispatch
		producer.Recipient = role
		schema := BoundDispatchResultSchema(producer)
		properties := schema["properties"].(map[string]any)
		required := schema["required"].([]string)
		if !slices.Contains(required, "workReport") || properties["workReport"].(map[string]any)["minLength"] != 1 {
			t.Fatal("bound role can still submit a dangling work-report reference without content", role)
		}
		for _, field := range []string{"dispatchId", "taskId", "recipient", "repository", "startedSha", "outputSha"} {
			if _, ok := properties[field]; ok || slices.Contains(required, field) {
				t.Fatalf("model still transcribes host field %s", field)
			}
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

func TestDispatchResultSchemasRequireEveryDeclaredField(t *testing.T) {
	for _, role := range []string{"CODER", "REVIEWER"} {
		for _, evidence := range [][]string{nil, {"work-report"}} {
			dispatch := core.GraphDispatch{Recipient: role, Instruction: core.DispatchInstruction{RequiredEvidence: evidence}}
			for _, schema := range []map[string]any{
				DispatchResultSchema(dispatch, false), DispatchResultSchema(dispatch, true), BoundDispatchResultSchema(dispatch),
			} {
				properties := schema["properties"].(map[string]any)
				required := schema["required"].([]string)
				for field := range properties {
					if !slices.Contains(required, field) {
						t.Fatalf("provider strict schema rejects optional property %s for %s", field, role)
					}
				}
			}
		}
	}
}
