package cli

import (
	"bytes"
	"context"
	"fmt"
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
	cmd.Env = workspaceGitEnvironment()
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

func testCaptureWorkspaceBaseline(t *testing.T, dir string) (*workspaceBaseline, error) {
	t.Helper()
	baseline, err := captureWorkspaceBaseline(context.Background(), dir)
	if baseline != nil {
		t.Cleanup(func() { _ = baseline.close() })
	}
	return baseline, err
}

func TestWorkspaceDiffExcludesPreexistingUntrackedFiles(t *testing.T) {
	dir := testGitWorkspace(t)
	for _, file := range []string{"old.txt", "old.secret"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("private old content\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
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
	baseline, err := testCaptureWorkspaceBaseline(t, subdir)
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
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
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
	baseline, err := testCaptureWorkspaceBaseline(t, t.TempDir())
	if err != nil || baseline != nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}

	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "actual file changes were not inspected") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffHandlesLargeWorkspace(t *testing.T) {
	dir := testGitWorkspace(t)
	data := bytes.Repeat([]byte("x"), 4*1024*1024)
	for i := 0; i < 17; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%d", i)), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(baseline.snapshot)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("snapshot permissions=%v err=%v", info, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("small change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "+small change") {
		t.Fatalf("diff=%q err=%v", diff, err)
	}
}

func TestWorkspaceDiffReportsMissingNewFile(t *testing.T) {
	dir := testGitWorkspace(t)
	_, err := workspaceGit(context.Background(), dir, true, "diff", "--no-index", "--", os.DevNull, "missing")
	if err == nil {
		t.Fatal("missing file was treated as a successful diff")
	}
}

func TestWorkspaceDiffLimitsGitOutput(t *testing.T) {
	dir := testGitWorkspace(t)
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte(strings.Repeat("x", maxWorkspaceDiffBytes)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := baseline.diff(context.Background()); err == nil {
		t.Fatal("oversized tracked diff was accepted")
	}
}

func TestWorkspaceDiffIgnoresGitRepositoryEnvironment(t *testing.T) {
	dir, other := testGitWorkspace(t), testGitWorkspace(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("target repository\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "target repository") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffOmitsSubmoduleContents(t *testing.T) {
	dir, source := testGitWorkspace(t), testGitWorkspace(t)
	testGit(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", source, "module")
	testGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "add submodule")
	testGit(t, dir, "config", "diff.submodule", "diff")
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	module := filepath.Join(dir, "module")
	if err := os.WriteFile(filepath.Join(module, "tracked.txt"), []byte("submodule content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, module, "add", "tracked.txt")
	testGit(t, module, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "change")
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "Subproject commit") || strings.Contains(diff, "+submodule content") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffComparesPreRunContents(t *testing.T) {
	dir := testGitWorkspace(t)
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.txt", "unrelated preexisting line\nold tracked\n")
	write("edited.txt", "unchanged private line\nold untracked\n")
	write("deleted.txt", "deleted untracked\n")
	write("staged.txt", "preexisting staged\n")
	write("unchanged.txt", "unchanged staged transition\n")
	testGit(t, dir, "add", "staged.txt")
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	write("tracked.txt", "unrelated preexisting line\nnew tracked\n")
	write("edited.txt", "unchanged private line\nnew untracked\n")
	write("staged.txt", "changed staged\n")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "edited.txt", "unchanged.txt")
	testGit(t, dir, "rm", "--cached", "-fq", "staged.txt")
	diff, err := baseline.diff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-old tracked", "+new tracked", "-old untracked", "+new untracked", "-deleted untracked", "-preexisting staged", "+changed staged"} {
		if !strings.Contains(diff, want) {
			t.Errorf("missing %q in diff:\n%s", want, diff)
		}
	}
	for _, forbidden := range []string{"unrelated preexisting line", "unchanged private line", "unchanged staged transition"} {
		if strings.Contains(diff, forbidden) {
			t.Errorf("diff includes %q:\n%s", forbidden, diff)
		}
	}
}

func TestWorkspaceDiffTrackedSubdirectoryAndSymlinkParent(t *testing.T) {
	dir := testGitWorkspace(t)
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested", "file.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "sub/nested/file.txt")
	baseline, err := testCaptureWorkspaceBaseline(t, sub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(sub, "nested", "file.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(sub, "nested")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(sub, "nested")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("outside private data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "-before") || strings.Contains(diff, "outside private data") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffBinaryAndCleanFilters(t *testing.T) {
	dir := testGitWorkspace(t)
	marker := filepath.Join(dir, "filter-ran")
	testGit(t, dir, "config", "filter.test.clean", "touch "+marker)
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.bin filter=test\n"), 0600); err != nil {
		t.Fatal(err)
	}

	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.bin"), []byte(strings.Repeat("x", 40000)+"private\x00binary"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "Binary file") || strings.Contains(diff, "private") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("clean filter ran or marker stat failed: %v", err)
	}
}

func TestWorkspaceDiffIncludesIndexOnlyChanges(t *testing.T) {
	dir := testGitWorkspace(t)
	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("private context\nbaseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("private context\nstaged change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "tracked.txt")
	if err := os.WriteFile(path, []byte("private context\nbaseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "+staged change") || strings.Contains(diff, "private context") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffStagedUsesPreRunIndex(t *testing.T) {
	dir := testGitWorkspace(t)
	path := filepath.Join(dir, "tracked.txt")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("private staged secret\nold staged\n")
	testGit(t, dir, "add", "tracked.txt")
	write("old staged\n")
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	write("private staged secret\nnew staged\n")
	testGit(t, dir, "add", "tracked.txt")
	write("old staged\n")
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "-old staged") || !strings.Contains(diff, "+new staged") || strings.Contains(diff, "private staged secret") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffStagingPreexistingUntracked(t *testing.T) {
	dir := testGitWorkspace(t)
	path := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(path, []byte("private preexisting\n"), 0600); err != nil {
		t.Fatal(err)
	}
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "old.txt")
	diff, err := baseline.diff(context.Background())
	if err != nil || strings.Contains(diff, "private preexisting") || !strings.Contains(diff, "Index entry added") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
	if err := os.WriteFile(path, []byte("private preexisting\nnew staged line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "old.txt")
	diff, err = baseline.diff(context.Background())
	if err != nil || strings.Contains(diff, "private preexisting") || !strings.Contains(diff, "+new staged line") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffIndexOnlyRemovalAndMode(t *testing.T) {
	dir := testGitWorkspace(t)
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "update-index", "--chmod=+x", "tracked.txt")
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "old mode 100644\nnew mode 100755") || strings.Contains(diff, "original") {
		t.Fatalf("mode diff = %q, error = %v", diff, err)
	}
	testGit(t, dir, "rm", "--cached", "-fq", "tracked.txt")
	diff, err = baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "Index entry removed: \"tracked.txt\"") || strings.Contains(diff, "original") {
		t.Fatalf("removal diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffIndexRemovalBecomingIgnored(t *testing.T) {
	dir := testGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "tracked.secret"), []byte("private ignored content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "-f", "tracked.secret")
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "rm", "--cached", "-q", "tracked.secret")
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "Index entry removed: \"tracked.secret\"") || strings.Contains(diff, "private ignored content") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceDiffUnbornRepository(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init", "-q")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.txt", "private existing\nold tracked\n")
	write("untracked.txt", "old untracked\n")
	testGit(t, dir, "add", "tracked.txt")
	baseline, err := testCaptureWorkspaceBaseline(t, dir)
	if err != nil || baseline == nil {
		t.Fatalf("baseline = %#v, error = %v", baseline, err)
	}
	write("tracked.txt", "private existing\nnew tracked\n")
	write("untracked.txt", "new untracked\n")
	testGit(t, dir, "add", "tracked.txt")
	diff, err := baseline.diff(context.Background())
	if err != nil || !strings.Contains(diff, "+new tracked") || !strings.Contains(diff, "+new untracked") || strings.Contains(diff, "private existing") {
		t.Fatalf("diff = %q, error = %v", diff, err)
	}
}

func TestWorkspaceGitDisallowsTransports(t *testing.T) {
	dir := testGitWorkspace(t)
	if _, err := workspaceGit(context.Background(), dir, false, "-c", "protocol.file.allow=always", "ls-remote", dir); err == nil {
		t.Fatal("snapshot command allowed Git file transport")
	}
}
