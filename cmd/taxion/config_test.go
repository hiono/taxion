package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCheckIsExplicitAndDoesNotClaimAuthentication(t *testing.T) {
	input := `version: 1
project: {id: project-a}
repository:
  provider: github
  host: github.com
  path: owner/application
  account: developer-a
  credentialRef: scm-credential
runtime:
  provider: copilot
  account: developer-b
  credentialRef: ai-credential
  contractRef: company-contract
execution:
  requester: developer-a
  worker: local-worker
  approvalRef: decision-1
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := checkConfig([]string{"check", "--file", path}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "have not been verified") ||
		strings.Contains(output.String(), "ai-credential") {
		t.Fatal("check claimed external authorization or printed credential references")
	}
	for _, args := range [][]string{
		nil, {"show"}, {"check"}, {"check", "--file", path, "extra"},
		{"check", "--file", path + ".missing"},
	} {
		output.Reset()
		if err := checkConfig(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid invocation produced a successful configuration check")
		}
	}
	if err := os.WriteFile(path, []byte(input+"token: private-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := checkConfig([]string{"check", "--file", path}, &output); err == nil ||
		strings.Contains(err.Error(), "private-secret") || output.Len() != 0 {
		t.Fatal("invalid input succeeded or leaked its value")
	}
}
