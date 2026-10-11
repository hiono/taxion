package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func policyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
	return string(output)
}

func TestConfigRequiresUntrackedAndIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	policyGit(t, root, "init", "-q")
	path := filepath.Join(root, ".taxion", "config.yaml")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(validConfig), 0600); err != nil {
		t.Fatal(err)
	}
	files := Files{Project: path}
	if _, err := ReadFiles(t.TempDir(), root, files); err == nil || !strings.Contains(err.Error(), "ignore rule") {
		t.Fatal("untracked but unignored configuration accepted", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.taxion/config.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(t.TempDir(), root, files); err != nil {
		t.Fatal("ignored untracked configuration rejected", err)
	}
	// Ambient Git state must not redirect the gate to a different repository.
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	if err := CheckGitPolicy(path); err != nil {
		t.Fatal("ambient Git variables changed the inspected worktree", err)
	}
	t.Setenv("GIT_WORK_TREE", root)
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	policyGit(t, root, "add", "-f", ".taxion/config.yaml")
	if _, err := ReadFiles(t.TempDir(), root, files); err == nil || !strings.Contains(err.Error(), "Git-tracked") {
		t.Fatal("tracked configuration accepted despite ignore rule", err)
	}
}

func TestConfigGitPolicyExcludeSymlinksAndWorktrees(t *testing.T) {
	root := t.TempDir()
	policyGit(t, root, "init", "-q")
	policyGit(t, root, "config", "user.name", "Test")
	policyGit(t, root, "config", "user.email", "test@example.com")
	path := filepath.Join(root, "selected.yaml")
	if err := os.WriteFile(path, []byte(validConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("/selected.yaml\n/link.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckGitPolicy(path); err != nil {
		t.Fatal(".git/info/exclude not honored", err)
	}
	external := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(path, external); err != nil {
		t.Fatal(err)
	}
	policyGit(t, root, "add", "-f", "selected.yaml")
	if err := CheckGitPolicy(external); err == nil {
		t.Fatal("external symlink hid tracked target")
	}
	policyGit(t, root, "commit", "-qm", "test baseline")
	linked := filepath.Join(t.TempDir(), "worktree")
	policyGit(t, root, "worktree", "add", "--detach", "-q", linked)
	if err := CheckGitPolicy(filepath.Join(linked, "selected.yaml")); err == nil {
		t.Fatal("linked worktree .git file bypassed tracking check")
	}
	target := filepath.Join(t.TempDir(), "private.yaml")
	if err := os.WriteFile(target, []byte(validConfig), 0600); err != nil {
		t.Fatal(err)
	}
	localLink := filepath.Join(root, "link.yaml")
	if err := os.Symlink(target, localLink); err != nil {
		t.Fatal(err)
	}
	if err := CheckGitPolicy(localLink); err != nil {
		t.Fatal("ignored link to non-worktree config rejected", err)
	}
	policyGit(t, root, "add", "-f", "link.yaml")
	if err := CheckGitPolicy(localLink); err == nil {
		t.Fatal("tracked link to external config accepted")
	}
	// A dangling default is a read failure, not an absent optional file.
	if err := os.Mkdir(filepath.Join(root, ".taxion"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, ".taxion", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(t.TempDir(), root, Files{}); err == nil {
		t.Fatal("dangling default link silently skipped")
	}
}
