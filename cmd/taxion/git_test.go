package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitCheckIndex(t *testing.T) {
	t.Chdir(t.TempDir())
	var output bytes.Buffer
	if err := checkGitIndex([]string{"check-index"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("nonrepository check succeeded")
	}

	git := func(args ...string) {
		t.Helper()
		result, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, result, err)
		}
	}
	git("init", "-q")
	if err := checkGitIndex([]string{"check-index"}, &output); err != nil ||
		!strings.Contains(output.String(), "Secret content scanning has not been performed") {
		t.Fatal("empty index check failed or overstated protection", err)
	}
	if err := os.WriteFile(".env", []byte("fake-private-payload"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".env")
	output.Reset()
	err := checkGitIndex([]string{"check-index"}, &output)
	if err == nil || output.Len() != 0 || strings.Contains(err.Error(), "fake-private-payload") {
		t.Fatal("unsafe index accepted or private data disclosed", err)
	}
	for _, args := range [][]string{nil, {"show"}, {"check-index", "--file", "index"}, {"check-index", "extra"}} {
		output.Reset()
		if err := checkGitIndex(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid invocation succeeded")
		}
	}
}

func TestGitHookExplicitPathOnlyLifecycle(t *testing.T) {
	t.Chdir(t.TempDir())
	if output, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("init test repository: %s: %v", output, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"hook", "status"}, "not installed"},
		{[]string{"hook", "install", "--paths-only"}, "prohibited paths only"},
		{[]string{"hook", "status"}, "Secret content scanning is not configured"},
		{[]string{"hook", "uninstall"}, "not installed"},
	} {
		var output bytes.Buffer
		if err := checkGitIndex(tc.args, &output); err != nil || !strings.Contains(output.String(), tc.want) {
			t.Fatal("incorrect hook lifecycle output", tc.args, err)
		}
	}
	for _, args := range [][]string{
		{"hook"}, {"hook", "install"}, {"hook", "install", "--scanner"},
		{"hook", "status", "extra"}, {"hook", "uninstall", "--force"}, {"hook", "replace"},
	} {
		var output bytes.Buffer
		if err := checkGitIndex(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid hook invocation succeeded", args, err)
		}
	}
}

func TestGitInstalledHookWithPersistentCLI(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "taxion binary's path")
	if output, err := exec.Command("go", "build", "-o", executable, ".").CombinedOutput(); err != nil {
		t.Fatalf("build hook executable: %s: %v", output, err)
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return string(output)
	}
	run := func(args ...string) {
		t.Helper()
		command := exec.Command(executable, args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("taxion %v: %s: %v", args, output, err)
		}
	}
	git("init", "-q")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	run("git", "hook", "install", "--paths-only")
	run("git", "hook", "status")
	git("commit", "--allow-empty", "-qm", "safe baseline")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("fake-private-payload"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".env")
	head := git("rev-parse", "HEAD")
	index := git("ls-files", "--stage")
	command := exec.Command("git", "-C", root, "commit", "-qm", "must reject")
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "prohibited local paths") ||
		strings.Contains(string(output), "fake-private-payload") {
		t.Fatal("installed CLI hook failed to reject safe fake secret path", string(output), err)
	}
	if head != git("rev-parse", "HEAD") || index != git("ls-files", "--stage") {
		t.Fatal("rejected commit changed HEAD or index")
	}
	run("git", "hook", "uninstall")
	run("git", "hook", "status")
}

func TestGitCheckIndexSecretMode(t *testing.T) {
	t.Chdir(t.TempDir())
	if output, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("init test repository: %s: %v", output, err)
	}
	scanner := filepath.Join(t.TempDir(), "scanner")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf '1.9.0\\n'; exit 0; fi\ncat >/dev/null\nprintf '[]'\n"
	if err := os.WriteFile(scanner, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := checkGitIndex([]string{"check-index", "--secrets", "--scanner", scanner}, &output); err != nil ||
		!strings.Contains(output.String(), "does not guarantee") {
		t.Fatal("secret mode failed or overstated coverage", err)
	}
	for _, args := range [][]string{
		{"check-index", "--scanner", scanner},
		{"check-index", "--secrets", "--scanner", filepath.Join(t.TempDir(), "missing")},
		{"check-index", "--secrets", "extra"},
	} {
		output.Reset()
		if err := checkGitIndex(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid secret invocation succeeded", args, err)
		}
	}
}
