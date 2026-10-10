package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEntries(t *testing.T, path string, n int) {
	t.Helper()
	a := &auditor{path: path}
	for i := 0; i < n; i++ {
		if err := a.log(auditEntry{Method: "POST", Path: "/api/tasks", Status: 201, Authed: true}); err != nil {
			t.Fatal(err)
		}
	}
}

func readRawLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestChainLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeEntries(t, path, 3)
	lines := readRawLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if _, ok := first["prev"]; ok {
		t.Fatal("genesis line must omit prev")
	}
	for i := 1; i < 3; i++ {
		var e struct {
			Prev string `json:"prev"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &e); err != nil {
			t.Fatal(err)
		}
		if e.Prev != chainHash([]byte(lines[i-1]+"\n")) {
			t.Fatalf("line %d prev does not link line %d", i+1, i)
		}
	}
	checked, legacy, bad, err := Verify(path)
	if err != nil || bad != 0 || checked != 2 || legacy != 1 {
		t.Fatalf("verify = checked=%d legacy=%d bad=%d err=%v", checked, legacy, bad, err)
	}
}

func TestChainContinuesAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeEntries(t, path, 2)
	// A fresh auditor models a restart without retaining the previous hash.
	writeEntries(t, path, 1)
	lines := readRawLines(t, path)
	var e struct {
		Prev string `json:"prev"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Prev != chainHash([]byte(lines[1]+"\n")) {
		t.Fatal("restarted auditor must continue the chain")
	}
	if _, _, bad, err := Verify(path); err != nil || bad != 0 {
		t.Fatalf("verify after restart: bad=%d err=%v", bad, err)
	}
}

func TestChainDetectsTamper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	writeEntries(t, path, 3)
	lines := readRawLines(t, path)
	// Keep stored hashes unchanged so the next line, not the edited line,
	// is where the predecessor mismatch becomes detectable.
	tampered := strings.Replace(lines[1], "201", "200", 1)
	if tampered == lines[1] {
		t.Fatal("test setup: nothing to tamper")
	}
	lines[1] = tampered
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, bad, err := Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 3 {
		t.Fatalf("tamper must break at line 3, got bad=%d", bad)
	}
}

func TestLegacyPrefixCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	// Start with an old-format line to exercise upgrade compatibility.
	if err := os.WriteFile(path, []byte("{\"ts\":\"t\",\"method\":\"POST\",\"path\":\"/x\",\"status\":201,\"authed\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEntries(t, path, 2)
	checked, legacy, bad, err := Verify(path)
	if err != nil || bad != 0 || legacy != 1 || checked != 2 {
		t.Fatalf("legacy prefix must verify: checked=%d legacy=%d bad=%d err=%v", checked, legacy, bad, err)
	}
	lines := readRawLines(t, path)
	legacyLine := "{\"ts\":\"t\",\"method\":\"POST\",\"path\":\"/y\",\"status\":201,\"authed\":true}"
	lines = append(lines, legacyLine)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, bad, err = Verify(path)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 4 {
		t.Fatalf("post-chain legacy line must break at line 4, got bad=%d", bad)
	}
}
