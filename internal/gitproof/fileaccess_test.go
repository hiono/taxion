package gitproof

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestTrackedPathsAtAndCommittedChangePolicy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "init", "-b", "work/task")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "taxion-test@example.com")
	baseline := commitFile(t, root, "src/app.go", "package app")
	output := commitFile(t, root, "src/new.go", "package app")

	paths, err := TrackedPathsAt(root, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"src/app.go"}) {
		t.Fatalf("tracked snapshot at baseline = %v", paths)
	}
	dispatch := core.GraphDispatch{
		Recipient: "CODER",
		Instruction: core.DispatchInstruction{
			Worktree: root, BaselineSHA: baseline, AuthorizedActions: []string{"src"},
			FileAccess: &core.FileAccessPolicy{Level: core.FileAccessCommitted},
		},
	}
	if err := CheckChangedPaths(dispatch, []string{"src/app.go"}); err != nil {
		t.Fatalf("baseline tracked file was rejected: %v", err)
	}
	if err := CheckChangedPaths(dispatch, []string{"src/new.go"}); err == nil {
		t.Fatal("file added after Dispatch start was accepted")
	}
	dispatch.Instruction.FileAccess = &core.FileAccessPolicy{
		Level: core.FileAccessExact, Paths: []string{"src/new.go"},
	}
	if err := CheckChangedPaths(dispatch, []string{"src/new.go"}); err != nil {
		t.Fatalf("exactly approved new file was rejected: %v", err)
	}
	dispatch.Instruction.FileAccess = &core.FileAccessPolicy{Level: core.FileAccessAll}
	if err := CheckChangedPaths(dispatch, []string{"src/new.go"}); err != nil {
		t.Fatalf("worktree-wide file access rejected an in-scope file: %v", err)
	}
	if output == baseline {
		t.Fatal("fixture did not create a later commit")
	}
}
