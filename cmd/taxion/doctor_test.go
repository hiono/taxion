package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/runtime/copilot"
)

func doctorFixtureCLI(t *testing.T) string {
	t.Helper()
	cli := filepath.Join(t.TempDir(), "copilot")
	if err := os.WriteFile(cli, []byte(`#!/bin/sh
[ "$1" = --no-auto-update ] || exit 1
shift
[ "$1" = --version ] && { echo fixture; exit 0; }
[ "$2" = list ] && [ "$3" = --json ] || exit 2
if [ "$1" = mcp ]; then printf '{"mcpServers":{}}'; else printf '[]'; fi
`), 0700); err != nil {
		t.Fatal(err)
	}
	return cli
}

func TestDoctorWithoutWorkspaceOrModel(t *testing.T) {
	root, home, userConfig := t.TempDir(), t.TempDir(), t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", userConfig)
	t.Setenv("COPILOT_HOME", filepath.Join(home, "chosen-copilot"))
	path := filepath.Join(userConfig, "taxion/config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`version: 1
project: {id: fixture}
repository: {provider: github, host: github.com, path: owner/repo, account: user, credentialRef: gh:active}
runtime: {provider: copilot, account: user, credentialRef: copilot:logged-in, contractRef: fixture}
execution: {requester: human, worker: local, approvalRef: fixture}
`), 0600); err != nil {
		t.Fatal(err)
	}
	cli := doctorFixtureCLI(t)
	var output bytes.Buffer
	if err := doctorCmd([]string{"--cli", cli, "--json"}, &output); err != nil {
		t.Fatal(err)
	}
	var report copilot.DiscoveryReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Backend != "copilot" || report.Worktree != root || report.Mode != "native-cli-defaults" ||
		report.BaseDirectory != filepath.Join(home, "chosen-copilot") {
		t.Fatal("doctor did not use the selected execution context", report)
	}
	for _, path := range []string{".taxion", report.BaseDirectory} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("diagnostics created local state", path, err)
		}
	}
	output.Reset()
	if err := doctorCmd([]string{"--cli", cli}, &output); err != nil ||
		!bytes.Contains(output.Bytes(), []byte("検索先:")) || !bytes.Contains(output.Bytes(), []byte("missing")) {
		t.Fatal("human output hides empty search locations", output.String(), err)
	}
	oldStore := storePath
	storePath = filepath.Join(t.TempDir(), "ledger.json")
	defer func() { storePath = oldStore }()
	if err := doctorCmd([]string{"--cli", cli, "--dispatch", "missing"}, &output); err == nil {
		t.Fatal("missing saved Dispatch accepted")
	}
	if _, err := os.Stat(workspaceDBPath()); !os.IsNotExist(err) {
		t.Fatal("dispatch diagnostic created a database", err)
	}
}

func TestOpenCodeDoctorVerifiesAssetsWithoutLaunchingProvider(t *testing.T) {
	root, userConfig := t.TempDir(), t.TempDir()
	t.Chdir(root)
	t.Setenv("XDG_CONFIG_HOME", userConfig)
	marker := filepath.Join(root, "provider-was-started")
	executable := filepath.Join(root, "opencode")
	executableData := []byte("#!/bin/sh\ntouch " + marker + "\nexit 1\n")
	serverConfig := filepath.Join(root, "server.json")
	serverData := []byte(`{"model":"opencode/big-pickle"}`)
	for path, data := range map[string][]byte{executable: executableData, serverConfig: serverData} {
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(userConfig, "taxion/config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`version: 1
project: {id: fixture}
repository: {provider: github, host: github.com, path: owner/repo, account: user, credentialRef: gh:active}
runtime:
  provider: opencode
  account: opencode-managed
  credentialRef: opencode-managed
  contractRef: fixture
  acpCommand: %s
  acpCommandHash: sha256:%x
  acpConfig: %s
  acpConfigHash: sha256:%x
execution: {requester: human, worker: local, approvalRef: fixture}
`, executable, sha256.Sum256(executableData), serverConfig, sha256.Sum256(serverData))
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{false, true} {
		if changed {
			if err := os.WriteFile(executable, []byte("changed"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		var output bytes.Buffer
		err := doctorCmd([]string{"--json"}, &output)
		var report struct {
			Backend        string `json:"backend"`
			AssetsVerified bool   `json:"assetsVerified"`
		}
		if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if (err != nil) != changed || report.Backend != "opencode" || report.AssetsVerified == changed {
			t.Fatal("doctor hid an asset mismatch or used the wrong provider", report, err)
		}
		for _, path := range []string{marker, filepath.Join(root, ".taxion")} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("doctor launched a provider or created workspace state", path, err)
			}
		}
	}
}
