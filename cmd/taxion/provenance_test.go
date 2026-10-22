package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/provenance"
)

func TestProvenanceCLIFailsBeforeStoreMutation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "taxion")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %s: %v", output, err)
	}
	request, _ := freshReadyFixture(t)
	file := freshManifest(t, request)
	directory, err := filepath.EvalSymlinks(filepath.Dir(storePath))
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotFiles(t, directory)
	run := func(args ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.Command(binary, append([]string{"--store", storePath}, args...)...)
		return cmd.CombinedOutput()
	}
	for _, args := range [][]string{
		{"task", "authorize", "--manifest", file},
		{"task", "create", "--title", "--help", "--repo", "synthetic/repository"},
		{"workflow", "start", "--file", file},
		{"serve"},
	} {
		output, err := run(args...)
		if err == nil || !bytes.Contains(output, []byte(`"code": "provenance-rejected"`)) ||
			!bytes.Contains(output, []byte(`"stateChanged": false`)) {
			t.Fatal("missing policy did not block mutation", args, string(output), err)
		}
		if after := snapshotFiles(t, directory); !equalFiles(before, after) {
			t.Fatal("rejected operation changed store")
		}
	}
	policyFile := filepath.Join(t.TempDir(), "policy.json")
	product := provenance.Policy{SchemaVersion: 1, Mode: "product", StoreDirectory: directory}
	write := func(value any) {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(policyFile, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(product)
	output, err := run("provenance", "configure", "--file", policyFile)
	if err == nil || !strings.Contains(string(output), "official release build") ||
		!equalFiles(before, snapshotFiles(t, directory)) {
		t.Fatal("local binary configured product store", string(output), err)
	}
	write(provenance.Policy{SchemaVersion: 1, Mode: "development", StoreDirectory: directory})
	if output, err := run("provenance", "configure", "--file", policyFile); err != nil {
		t.Fatal(string(output), err)
	}
	if output, err := run("provenance", "verify"); err != nil || !bytes.Contains(output, []byte(`"mode": "development"`)) {
		t.Fatal(string(output), err)
	}
	configured := snapshotFiles(t, directory)
	if _, err := run("provenance", "configure", "--file", policyFile); err == nil ||
		!equalFiles(configured, snapshotFiles(t, directory)) {
		t.Fatal("existing policy was overwritten")
	}
	if output, err := run("task", "authorize", "--manifest", file); err != nil ||
		!bytes.Contains(output, []byte(`"confirmationToken"`)) ||
		!equalFiles(configured, snapshotFiles(t, directory)) {
		t.Fatal("explicit isolated development preview failed or mutated state", string(output), err)
	}
}

func equalFiles(first, second map[string][]byte) bool {
	if len(first) != len(second) {
		return false
	}
	for path, data := range first {
		if !bytes.Equal(data, second[path]) {
			return false
		}
	}
	return true
}
