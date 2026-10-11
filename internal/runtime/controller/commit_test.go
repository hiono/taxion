package controller

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestControllerCommitRequiresRunningBoundCoderAndPreservesGraph(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "work/task")
	gitTest(t, root, "config", "user.name", "Test")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "README.md")
	gitTest(t, root, "commit", "-qm", "baseline")
	db, path, id := dispatchStore(t, root)
	defer db.Close()
	worker := &fakeRuntime{role: "CODER", hash: "config-v1", worktree: root}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	scanner := filepath.Join(t.TempDir(), "scanner")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf '1.9.0\\n'; exit 0; fi\ncat >/dev/null\nprintf '[]'\n"
	if err := os.WriteFile(scanner, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	message := "feat(app): implement approved change\n\nintent(app): satisfy the authorized criterion\n"
	if _, err := control.Commit(context.Background(), id, message, scanner); err == nil {
		t.Fatal("unbound Dispatch committed")
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Commit(context.Background(), id, message, scanner); err == nil {
		t.Fatal("issued Dispatch committed before execution")
	}
	if err := control.Send(context.Background(), id, "execute"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "internal"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "source.txt"), []byte("approved content"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "internal/source.txt")
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, rejected := range []*fakeRuntime{
		{role: "REVIEWER", hash: "config-v1", worktree: root},
		{role: "CODER", hash: "other-config", worktree: root},
		{role: "CODER", hash: "config-v1", worktree: t.TempDir()},
	} {
		other, err := New(db, rejected)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Commit(context.Background(), id, message, scanner); err == nil {
			t.Fatal("unmatched runtime committed")
		}
	}
	head := gitTest(t, root, "rev-parse", "HEAD")
	if _, err := control.Commit(context.Background(), id, message, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing scanner committed")
	}
	if head != gitTest(t, root, "rev-parse", "HEAD") {
		t.Fatal("rejected scanner advanced HEAD")
	}
	sha, err := control.Commit(context.Background(), id, message, scanner)
	if err != nil || sha != gitTest(t, root, "rev-parse", "HEAD") {
		t.Fatal("bound Coder commit failed", err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("commit altered Task or Dispatch acceptance state", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	restored, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(after, restored) || sha != gitTest(t, root, "rev-parse", "HEAD") {
		t.Fatal("managed Git commit or runtime binding did not persist", err)
	}
	control, err = New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.Transition("task-1", core.Blocked, "human", "pause work", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Commit(context.Background(), id, message, scanner); err == nil {
		t.Fatal("blocked Task committed")
	}
	if err := control.Stop(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Commit(context.Background(), id, message, scanner); err == nil {
		t.Fatal("cancelled Dispatch committed")
	}
}
