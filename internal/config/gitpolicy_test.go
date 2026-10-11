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
	if _, err := ReadFiles(t.TempDir(), root); err == nil || !strings.Contains(err.Error(), "ignore rule") {
		t.Fatal("untracked but unignored configuration accepted", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.taxion/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(t.TempDir(), root); err != nil {
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
	if _, err := ReadFiles(t.TempDir(), root); err == nil || !strings.Contains(err.Error(), "Git-tracked") {
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
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.taxion/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(t.TempDir(), root); err == nil {
		t.Fatal("dangling default link silently skipped")
	}
}

func TestProjectDirectoryAndDotenvPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, ignore, localFile, wantError string
		track, remove                      bool
	}{
		{name: "missing directory still requires ignore", wantError: "ignore rule"},
		{name: "file only ignore", ignore: "/.taxion/config.yaml\n", localFile: ".taxion/config.yaml", wantError: "ignore rule"},
		{name: "whole directory ignored", ignore: "/.taxion/\n", localFile: ".taxion/cache/data"},
		{name: "negated directory", ignore: "/.taxion/\n!/.taxion/\n/.taxion/*\n!/.taxion/shared/\n", localFile: ".taxion/shared/example", wantError: "ignore rule"},
		{name: "tracked sibling", ignore: "/.taxion/\n", localFile: ".taxion/cache/data", track: true, wantError: "Git-tracked"},
		{name: "tracked absent sibling", ignore: "/.taxion/\n", localFile: ".taxion/cache/data", track: true, remove: true, wantError: "Git-tracked"},
		{name: "dotenv requires ignore", ignore: "/.taxion/\n", localFile: ".env", wantError: "ignore rule"},
		{name: "dotenv ignored", ignore: "/.taxion/\n/.env\n", localFile: ".env"},
		{name: "dotenv tracked", ignore: "/.taxion/\n/.env\n", localFile: ".env", track: true, wantError: "Git-tracked"},
		{name: "dotenv tracked but absent", ignore: "/.taxion/\n", localFile: ".env", track: true, remove: true, wantError: "Git-tracked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, userDir := t.TempDir(), t.TempDir()
			policyGit(t, root, "init", "-q")
			if err := os.Mkdir(filepath.Join(userDir, "taxion"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(userDir, "taxion", "config.yaml"), []byte(validConfig), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(tc.ignore), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.localFile != "" {
				path := filepath.Join(root, tc.localFile)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("local data"), 0600); err != nil {
					t.Fatal(err)
				}
				if tc.track {
					policyGit(t, root, "add", "-f", tc.localFile)
				}
				if tc.remove {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := policyGit(t, root, "ls-files", "--stage")
			_, err := ReadFiles(userDir, root)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatal("incorrect local path policy result", err)
			}
			if before != policyGit(t, root, "ls-files", "--stage") {
				t.Fatal("configuration check changed the Git index")
			}
		})
	}
}

func TestProjectLocalSymlinkPolicy(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	policyGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.taxion\n/.env\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "config.yaml"), []byte(validConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, ".taxion")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(t.TempDir(), root); err != nil {
		t.Fatal("ignored directory link to non-worktree rejected", err)
	}
	// The entire resolved directory, not just config.yaml, must remain local.
	policyGit(t, external, "init", "-q")
	if err := os.WriteFile(filepath.Join(external, "state"), []byte("local state"), 0600); err != nil {
		t.Fatal(err)
	}
	policyGit(t, external, "add", "state")
	if _, err := ReadFiles(t.TempDir(), root); err == nil {
		t.Fatal("directory symlink concealed tracked descendants")
	}
	if err := os.Remove(filepath.Join(root, ".taxion")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".taxion"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".taxion", "config.yaml"), []byte(validConfig), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "environment.txt")
	if err := os.WriteFile(target, []byte("local env data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(t.TempDir(), root); err == nil || !strings.Contains(err.Error(), "ignore rule") {
		t.Fatal("dotenv symlink concealed unignored target", err)
	}
}

func TestFixedGlobalConfigurationInsideWorktree(t *testing.T) {
	userDir, projectDir := t.TempDir(), t.TempDir()
	policyGit(t, userDir, "init", "-q")
	path := filepath.Join(userDir, "taxion", "config.yaml")
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(validConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(userDir, projectDir); err == nil || !strings.Contains(err.Error(), "ignore rule") {
		t.Fatal("unignored Global configuration accepted", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, ".git", "info", "exclude"), []byte("/taxion/config.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFiles(userDir, projectDir); err != nil {
		t.Fatal("ignored Global configuration rejected", err)
	}
	policyGit(t, userDir, "add", "-f", "taxion/config.yaml")
	if _, err := ReadFiles(userDir, projectDir); err == nil || !strings.Contains(err.Error(), "Git-tracked") {
		t.Fatal("tracked Global configuration accepted", err)
	}
}
