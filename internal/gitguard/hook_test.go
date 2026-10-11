package gitguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookInstallStatusAndRemoval(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	executable := filepath.Join(t.TempDir(), "taxion tool's binary")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	for _, tc := range []struct {
		operation string
		installed bool
	}{
		{"status", false}, {"install", true}, {"install", true},
		{"status", true}, {"uninstall", false}, {"uninstall", false}, {"install", true},
	} {
		installed, err := ManageHook(context.Background(), root, executable, tc.operation)
		if err != nil || installed != tc.installed {
			t.Fatal("incorrect hook lifecycle", tc.operation, installed, err)
		}
	}
	content, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "'\"'\"'") {
		t.Fatal("executable path not shell quoted", err)
	}
	gitTest(t, root, "config", "user.name", "Test")
	gitTest(t, root, "config", "user.email", "test@example.com")
	gitTest(t, root, "commit", "--allow-empty", "-qm", "quoted executable")
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if _, err := ManageHook(context.Background(), root, executable, "status"); err == nil {
		t.Fatal("missing executable reported as installed protection")
	}
	head := gitTest(t, root, "rev-parse", "HEAD")
	command := exec.Command("git", "-C", root, "commit", "--allow-empty", "-qm", "must stop")
	if _, err := command.CombinedOutput(); err == nil {
		t.Fatal("hook permitted commit without executable")
	}
	if head != gitTest(t, root, "rev-parse", "HEAD") {
		t.Fatal("failed hook advanced HEAD")
	}
	if _, err := ManageHook(context.Background(), root, executable, "uninstall"); err != nil {
		t.Fatal("missing executable prevented safe hook removal", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("hook not removed", err)
	}
}

func TestHookPreservesExistingCustomEditedAndSymlinkFiles(t *testing.T) {
	for _, kind := range []string{"custom", "edited", "symlink", "nonexecutable"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			gitTest(t, root, "init", "-q")
			path := filepath.Join(root, ".git", "hooks", "pre-commit")
			script, err := hookScript(os.Args[0])
			if err != nil {
				t.Fatal(err)
			}
			mode := os.FileMode(0700)
			switch kind {
			case "custom":
				script = []byte("#!/bin/sh\nexit 0\n")
			case "edited":
				script = append(script, []byte("# human edit\n")...)
			case "nonexecutable":
				mode = 0600
			case "symlink":
				if err := os.Symlink("missing-hook", path); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "symlink" {
				if err := os.WriteFile(path, script, mode); err != nil {
					t.Fatal(err)
				}
			}
			for _, operation := range []string{"install", "status", "uninstall"} {
				if _, err := ManageHook(context.Background(), root, os.Args[0], operation); err == nil {
					t.Fatal("existing hook accepted for management", kind, operation)
				}
			}
			if kind == "symlink" {
				target, err := os.Readlink(path)
				if err != nil || target != "missing-hook" {
					t.Fatal("symlink changed", err)
				}
			} else {
				content, err := os.ReadFile(path)
				if err != nil || string(content) != string(script) {
					t.Fatal("existing hook changed", err)
				}
			}
		})
	}
}

func TestHookEffectivePathAndLinkedWorktree(t *testing.T) {
	for _, kind := range []string{"default", "relative", "absolute", "linked"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			gitTest(t, root, "init", "-q")
			path := filepath.Join(root, ".git", "hooks", "pre-commit")
			switch kind {
			case "relative":
				gitTest(t, root, "config", "core.hooksPath", "custom/hooks")
				path = filepath.Join(root, "custom", "hooks", "pre-commit")
			case "absolute":
				hooks := t.TempDir()
				gitTest(t, root, "config", "core.hooksPath", hooks)
				path = filepath.Join(hooks, "pre-commit")
			case "linked":
				gitTest(t, root, "config", "user.name", "Test")
				gitTest(t, root, "config", "user.email", "test@example.com")
				gitTest(t, root, "commit", "--allow-empty", "-qm", "baseline")
				linked := filepath.Join(t.TempDir(), "linked")
				gitTest(t, root, "worktree", "add", "--detach", "-q", linked)
				root = linked
			}
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0700); err != nil {
				t.Fatal(err)
			}
			if installed, err := ManageHook(context.Background(), nested, os.Args[0], "install"); err != nil || !installed {
				t.Fatal("hook installation failed", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("hook not installed at effective Git location", err)
			}
			if _, err := ManageHook(context.Background(), nested, os.Args[0], "uninstall"); err != nil {
				t.Fatal("effective hook removal failed", err)
			}
		})
	}
}

func TestHookRejectsInvalidInputs(t *testing.T) {
	root := t.TempDir()
	if _, err := ManageHook(context.Background(), root, os.Args[0], "install"); err == nil {
		t.Fatal("nonrepository accepted")
	}
	gitTest(t, root, "init", "-q")
	for _, executable := range []string{"relative", "/control\npath", filepath.Join(t.TempDir(), "missing")} {
		if _, err := ManageHook(context.Background(), root, executable, "install"); err == nil {
			t.Fatal("invalid executable accepted")
		}
	}
	if _, err := ManageHook(context.Background(), root, os.Args[0], "replace"); err == nil {
		t.Fatal("unknown operation accepted")
	}
	gitTest(t, root, "config", "core.hooksPath", "/dev/null")
	if _, err := ManageHook(context.Background(), root, os.Args[0], "install"); err == nil {
		t.Fatal("disabled hook path accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ManageHook(ctx, root, os.Args[0], "status"); err == nil {
		t.Fatal("cancelled path inspection accepted")
	}
}

func TestContentHookLifecycleAndMissingScanner(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	scanner := fakeScanner(t, "clean")
	for _, operation := range []string{"install", "install", "status"} {
		selected := scanner
		if operation == "status" {
			selected = ""
		}
		installed, secrets, err := ManageConfiguredHook(context.Background(), root, os.Args[0], operation, selected)
		if err != nil || !installed || !secrets {
			t.Fatal("full hook lifecycle failed", operation, err)
		}
	}
	if err := os.Remove(scanner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], "status", ""); err == nil {
		t.Fatal("missing stored scanner reported healthy")
	}
	if installed, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], "uninstall", ""); err != nil || installed {
		t.Fatal("scanner missing prevented exact hook removal", err)
	}
	if _, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], "install", scanner); err == nil {
		t.Fatal("missing scanner installation accepted")
	}
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing scanner created a hook", err)
	}
	scanner = fakeScanner(t, "clean")
	if err := os.WriteFile(scanner, []byte("#!/bin/sh\nprintf '2.0.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], "install", scanner); err == nil {
		t.Fatal("wrong scanner version installed")
	}
}

func TestHookModesRequireExplicitRemovalToSwitch(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	scanner := fakeScanner(t, "clean")
	if _, err := ManageHook(context.Background(), root, os.Args[0], "install"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], "install", scanner); err == nil {
		t.Fatal("path-only hook silently upgraded")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("existing path-only hook changed", err)
	}
	if _, err := ManageHook(context.Background(), root, os.Args[0], "uninstall"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], "install", scanner); err != nil {
		t.Fatal(err)
	}
	if _, err := ManageHook(context.Background(), root, os.Args[0], "install"); err == nil {
		t.Fatal("content hook silently downgraded")
	}
}

func TestFullHookScannerReferenceDoesNotAuthorizeEditedScript(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	scanner := fakeScanner(t, "clean")
	script, err := secretHookScript(os.Args[0], scanner)
	if err != nil {
		t.Fatal(err)
	}
	script = append(script, []byte("# additional custom behavior\n")...)
	path := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"status", "uninstall"} {
		if _, _, err := ManageConfiguredHook(context.Background(), root, os.Args[0], operation, ""); err == nil {
			t.Fatal("metadata accepted edited script", operation)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(script) {
		t.Fatal("edited script changed", err)
	}
}
