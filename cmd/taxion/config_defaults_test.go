package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckDiscoversDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(userDir, "taxion", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	input := `version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: approval}
`
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(".taxion", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".taxion/config.yaml", []byte("version: 1\nruntime:\n  account: project-ai\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := checkConfig([]string{"check", "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "runtime.account: project") ||
		!strings.Contains(output.String(), "runtime.contractRef: user") {
		t.Fatal("default provenance not reported")
	}
	t.Setenv("TAXION_RUNTIME_ACCOUNT", "selected-account")
	output.Reset()
	if err := checkConfig([]string{"check", "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "runtime.account: environment") ||
		strings.Contains(output.String(), "selected-account") {
		t.Fatal("environment precedence or safe provenance output incorrect")
	}
	command := exec.Command("git", "init", "-q")
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("init test worktree: %s: %v", result, err)
	}
	output.Reset()
	if err := checkConfig([]string{"check"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("CLI accepted unignored project configuration")
	}
	if err := os.WriteFile(".gitignore", []byte("/.taxion/config.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig([]string{"check"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("CLI accepted a file-only ignore rule")
	}
	if err := os.WriteFile(".gitignore", []byte("/.taxion/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkConfig([]string{"check"}, &output); err != nil {
		t.Fatal("CLI rejected ignored project configuration", err)
	}
	command = exec.Command("git", "add", "-f", ".taxion/config.yaml")
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("track test config: %s: %v", result, err)
	}
	output.Reset()
	if err := checkConfig([]string{"check"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("environment override bypassed tracked directory gate")
	}
}

func TestConfigCheckRejectsInvalidEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.Mkdir(".taxion", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".taxion/config.yaml", []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAXION_RUNTIME_ACCOUNT", "")
	var output bytes.Buffer
	if err := checkConfig([]string{"check"}, &output); err == nil ||
		!strings.Contains(err.Error(), "TAXION_RUNTIME_ACCOUNT") || output.Len() != 0 {
		t.Fatal("invalid environment value accepted", err)
	}
}

func TestConfigCheckRejectsRemovedFileOptions(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, option := range []string{"--file", "--user-file", "--project-file", "-file", "-user-file", "-project-file"} {
		for _, args := range [][]string{{"check", option, "private-path"}, {"check", option + "=private-path"}} {
			var output bytes.Buffer
			err := checkConfig(args, &output)
			if err == nil || !strings.Contains(err.Error(), "removed") ||
				!strings.Contains(err.Error(), "Global") || !strings.Contains(err.Error(), "TAXION_*") ||
				strings.Contains(err.Error(), "private-path") || output.Len() != 0 {
				t.Fatal("removed option lacked safe migration guidance", args, err)
			}
		}
	}
	var output bytes.Buffer
	if err := checkConfig([]string{"check"}, &output); err == nil || output.Len() != 0 {
		t.Fatal("missing fixed files accepted")
	}
}
