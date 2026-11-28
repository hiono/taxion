package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func testGitWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "tracked.txt", ".gitignore")
	testGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "baseline")
	return dir
}

func TestWorkspaceDiffExcludesPreexistingUntrackedFiles(t *testing.T) {
	dir := testGitWorkspace(t)
	for _, file := range []string{"old.txt", "old.secret"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("private old content\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := captureWorkspaceBaseline(context.Background(), dir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	for file, content := range map[string]string{
		"tracked.txt": "changed tracked\n", "new.txt": "new tracked\n",
		"new-untracked.txt": "new untracked\n", "empty.txt": "", "new.secret": "ignored content\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, dir, "add", "old.txt", "new.txt")
	testGit(t, dir, "add", "-f", "old.secret")
	diff, err := baseline.diff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"changed tracked", "new tracked", "new untracked"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	for _, forbidden := range []string{"private old content", "ignored content"} {
		if strings.Contains(diff, forbidden) {
			t.Fatalf("diff included %q:\n%s", forbidden, diff)
		}
	}
	testGit(t, dir, "add", "tracked.txt")
	testGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "coder commit")
	diff, err = baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "changed tracked") || strings.Contains(diff, "private old content") {
		t.Fatalf("diff after commit = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffUsesWorkingSubdirectory(t *testing.T) {
	dir := testGitWorkspace(t)
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "old.txt"), []byte("private old content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	baseline, err := captureWorkspaceBaseline(context.Background(), subdir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	testGit(t, subdir, "add", "old.txt")
	if err := os.WriteFile(filepath.Join(subdir, "new.txt"), []byte("new content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "new content") || strings.Contains(diff, "private old content") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffDoesNotFollowNewSymlinks(t *testing.T) {
	dir := testGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "old.secret"), []byte("private old content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	baseline, err := captureWorkspaceBaseline(context.Background(), dir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	if err := os.Symlink("old.secret", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || strings.Contains(diff, "private old content") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffOutsideGitIsExplicitlyUnavailable(t *testing.T) {
	baseline, err := captureWorkspaceBaseline(context.Background(), t.TempDir())
	if err != nil || baseline != nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}

	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "actual file changes were not inspected") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffReportsMissingNewFile(t *testing.T) {
	dir := testGitWorkspace(t)
	_, err := workspaceGit(context.Background(), dir, true, "diff", "--no-index", "--", os.DevNull, "missing")
	if err == nil {
		t.Fatal("missing file was treated as a successful diff")
	}
}
