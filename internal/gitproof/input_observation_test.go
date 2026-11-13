package gitproof

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestObserveInputChecksAuthorityBeforeCapturingBlobs(t *testing.T) {
	root := t.TempDir()
	gitCommand(t, root, "init", "-b", "work/input")
	gitCommand(t, root, "config", "user.name", "Taxion Test")
	gitCommand(t, root, "config", "user.email", "test@example.com")
	gitCommand(t, root, "remote", "add", "origin", "https://github.com/owner/repo.git")
	baseline := commitFile(t, root, "README.md", "baseline")
	task := core.GraphTask{
		Repo: "owner/repo", Worktree: root, Branch: "work/input",
		BaselineSHA: baseline, TargetSHA: baseline, OutputMode: core.OutputModeWorktreeTreeSnapshot,
		Scope: []string{"docs"}, OutOfScope: []string{"private"},
		FileAccess: &core.FileAccessPolicy{Level: core.FileAccessAll},
	}
	selection := []byte(`{"repository":{"host":"github.com","path":"owner/repo"}}`)
	if err := os.Mkdir(filepath.Join(root, "private"), 0700); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private", "data.txt")
	if err := os.WriteFile(private, []byte("synthetic forbidden content"), 0600); err != nil {
		t.Fatal(err)
	}
	blob := gitCommand(t, root, "hash-object", "--no-filters", "private/data.txt")
	task.Scope = append(task.Scope, "private")
	if _, err := ObserveInput(task, "human:supplied-input", selection); err == nil {
		t.Fatal("forbidden path was accepted by expanded observation scope")
	}
	if err := exec.Command("git", "-C", root, "cat-file", "-e", blob).Run(); err == nil {
		t.Fatal("forbidden file was captured before checking permission")
	}
	if err := os.Remove(private); err != nil {
		t.Fatal(err)
	}
	empty, err := ObserveInput(task, "human:unchanged-input", selection)
	if err != nil || len(empty.ChangedFiles) != 0 || empty.CommitSHA != baseline {
		t.Fatal("unchanged supplied input was rejected", err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "input.txt"), []byte("supplied"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "config", "filter.synthetic.clean", "false")
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "attributes"), []byte("docs/* filter=synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveInput(task, "human:filter-input", selection); err == nil || !strings.Contains(err.Error(), "filters") {
		t.Fatal("read-only observation could invoke a configured executable filter", err)
	}
	marker := filepath.Join(t.TempDir(), "filter-called")
	gitCommand(t, root, "config", "filter.synthetic.clean", "touch "+marker)
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "attributes"), []byte("README.md filter=synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("changed tracked input"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveInput(task, "human:tracked-filter", selection); err == nil {
		t.Fatal("tracked clean filter was allowed before path discovery")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("path discovery executed a tracked-file filter", err)
	}
}
