package main

import (
	"bytes"
	"os"
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
	override := filepath.Join(t.TempDir(), "override.yaml")
	if err := os.WriteFile(override, []byte("version: 1\nruntime:\n  account: final-account\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--file", override, "--sources"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "runtime.account: invocation") {
		t.Fatal("environment overrode invocation file")
	}
	t.Setenv("TAXION_RUNTIME_ACCOUNT", "")
	output.Reset()
	if err := checkConfig([]string{"check", "--file", override}, &output); err == nil || output.Len() != 0 {
		t.Fatal("higher-priority invocation hid an invalid environment value")
	}
}
