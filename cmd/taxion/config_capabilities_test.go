package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rejectingConfigWriter struct{}

func (rejectingConfigWriter) Write([]byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestConfigCapabilitiesShowImplementationNotExternalAccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	userDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(userDir, "taxion", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	input := `version: 1
project: {id: private-project}
repository: {provider: PROVIDER, host: HOST, path: owner/repo, account: private-scm-account, credentialRef: private-scm-reference}
runtime: {provider: copilot, account: private-ai-account, credentialRef: private-ai-reference, contractRef: private-contract}
execution: {requester: private-requester, worker: private-worker, approvalRef: private-approval}
`
	for _, test := range []struct {
		provider, host, adapter string
	}{
		{"github", "github.com", "GITHUB_COM_GH_API"},
		{"github", "GITHUB.COM", "GITHUB_COM_GH_API"},
		{"github", "github.example", "UNIMPLEMENTED"},
		{"gitlab", "gitlab.com", "UNIMPLEMENTED"},
		{"bitbucket", "bitbucket.org", "UNIMPLEMENTED"},
	} {
		t.Run(test.provider+":"+test.host, func(t *testing.T) {
			text := strings.NewReplacer("PROVIDER", test.provider, "HOST", test.host).Replace(input)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			var ordinary, output bytes.Buffer
			if err := checkConfig([]string{"check"}, &ordinary); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(ordinary.String(), "repository.access.adapter:") {
				t.Fatal("default output changed")
			}
			if err := checkConfig([]string{"check", "--sources", "--capabilities"}, &output); err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{
				"repository.access.adapter: " + test.adapter,
				"runtime.adapter: COPILOT_SDK",
				"runtime.contract.verification: UNIMPLEMENTED",
				"runtime.contract.billing: UNIMPLEMENTED",
				"Implementation coverage only", "have not been verified", "repository.provider: user",
			} {
				if !strings.Contains(output.String(), expected) {
					t.Fatal("missing implementation or unverified boundary", expected, output.String())
				}
			}
			if strings.Contains(output.String(), "private-") {
				t.Fatal("capability output exposed selected identifiers or references")
			}
			if err := checkConfig([]string{"check", "--capabilities"}, rejectingConfigWriter{}); err == nil {
				t.Fatal("failed capability output was suppressed")
			}
		})
	}
}
